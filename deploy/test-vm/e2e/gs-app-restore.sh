#!/bin/bash
# App nel ciclo backup -> restore (GIT-388, criterio 2 di GIT-351, H4), sulla VM.
# Lo lancia e2e.ps1 (lib/phases.ps1) con due comandi:
#
#   push     Pubblica nel registry di GitStack un'immagine di prova piccola con tre
#            tag (v1, v2, v3), ognuna col suo manifest: busybox come rootfs, un file
#            /TAG diverso per tag, Entrypoint `sleep`. v1 e v2 le usera' l'app, v3 no.
#            Variabili: GS_VM_IP, GS_CA (file della CA), GS_USER_TOKEN, GS_IMAGE
#            (<owner>/<repo>/<nome>). Stampa "STEP ok push" o "STEP FAIL push: ...".
#   inspect  Dopo il restore, guarda il Deployment "app" nel namespace dell'app.
#            Variabili: GS_APP_NS, GS_VAR_NAME, GS_VAR_VALUE, GS_SECRET_NAME,
#            GS_SECRET_VALUE. Stampa una riga per chiave, mai il valore del segreto:
#              deploy=yes|no
#              image=<riferimento>
#              var=match|mismatch|missing
#              secret=match|mismatch|missing   (dal pod, altrimenti dal Secret app-secrets)
#
# Un solo trap EXIT in testa; niente `set -e` (i controlli negativi si scrivono con if).
set -u

W="$(mktemp -d)"
trap 'rm -rf "$W"; if [ -n "${MNT:-}" ]; then sudo -n k3s ctr -n k8s.io images unmount "$MNT" >/dev/null 2>&1; fi' EXIT
MNT=""

KUBECONFIG_PATH="${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"
kc() { sudo -n env "KUBECONFIG=${KUBECONFIG_PATH}" k3s kubectl "$@"; }
fail() { echo "STEP FAIL $1: $2"; exit 1; }
sha() { sha256sum "$1" | cut -d' ' -f1; }

# --- rootfs: l'immagine busybox che il chart ha gia' scaricato, oppure un busybox statico --------
build_rootfs() { # cartella
  local root="$1" m
  mkdir -p "$root"
  m="$W/mnt"
  mkdir -p "$m"
  if sudo -n k3s ctr -n k8s.io images mount docker.io/library/busybox:1.36 "$m" >/dev/null 2>&1; then
    MNT="$m"
    sudo -n tar -C "$m" -cf "$W/base.tar" . 2>/dev/null || return 1
    sudo -n k3s ctr -n k8s.io images unmount "$m" >/dev/null 2>&1
    MNT=""
    sudo -n chown "$(id -u):$(id -g)" "$W/base.tar"
    tar -C "$root" -xf "$W/base.tar" 2>/dev/null || return 1
    if [ -e "$root/bin/sleep" ] || [ -L "$root/bin/sleep" ]; then return 0; fi
  fi
  local bb=""
  for c in /usr/lib/initramfs-tools/bin/busybox "$(command -v busybox 2>/dev/null)"; do
    if [ -n "$c" ] && [ -x "$c" ]; then bb="$c"; break; fi
  done
  [ -n "$bb" ] || return 1
  mkdir -p "$root/bin"
  cp "$bb" "$root/bin/busybox" || return 1
  for a in sleep printenv sh; do ln -sf busybox "$root/bin/$a"; done
  return 0
}

upload_blob() { # file
  local d loc
  d="sha256:$(sha "$1")"
  loc="$(curl -fsSL --cacert "$GS_CA" -u "token:${GS_USER_TOKEN}" -X POST -D - -o /dev/null "$BASE/v2/${GS_IMAGE}/blobs/uploads/" | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')"
  [ -n "$loc" ] || return 1
  case "$loc" in http*) ;; *) loc="${BASE}${loc}" ;; esac
  case "$loc" in *\?*) loc="${loc}&digest=${d}" ;; *) loc="${loc}?digest=${d}" ;; esac
  curl -fsSL --cacert "$GS_CA" -u "token:${GS_USER_TOKEN}" -X PUT -H 'Content-Type: application/octet-stream' --data-binary "@$1" "$loc" >/dev/null
}

push() {
  BASE="https://${GS_VM_IP}"
  local root="$W/rootfs" tag
  build_rootfs "$root" || fail push "rootfs di busybox non disponibile (ne' dall'immagine del chart ne' statico)"
  for tag in v1 v2 v3; do
    echo "e2e-$tag" >"$root/TAG"
    tar -C "$root" -cf "$W/layer-$tag.tar" . || fail push "tar di $tag"
    printf '{"architecture":"amd64","os":"linux","config":{"Entrypoint":["/bin/sleep","86400"]},"rootfs":{"type":"layers","diff_ids":["sha256:%s"]}}' "$(sha "$W/layer-$tag.tar")" >"$W/config-$tag.json"
    upload_blob "$W/layer-$tag.tar" || fail push "upload dello strato $tag"
    upload_blob "$W/config-$tag.json" || fail push "upload della config $tag"
    printf '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:%s","size":%s},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:%s","size":%s}]}' \
      "$(sha "$W/config-$tag.json")" "$(wc -c <"$W/config-$tag.json")" "$(sha "$W/layer-$tag.tar")" "$(wc -c <"$W/layer-$tag.tar")" >"$W/manifest-$tag.json"
    curl -fsSL --cacert "$GS_CA" -u "token:${GS_USER_TOKEN}" -X PUT -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' --data-binary "@$W/manifest-$tag.json" "$BASE/v2/${GS_IMAGE}/manifests/$tag" >/dev/null || fail push "manifest di $tag"
  done
  echo "STEP ok push"
}

# printenv nel container "app": il pod puo' essere ancora in avvio dopo il restore, si riprova.
pod_env() { # nome
  local v
  for _ in 1 2 3 4 5 6 7 8 9 10 11 12; do
    if v="$(kc exec -n "$GS_APP_NS" deploy/app -- printenv "$1" 2>/dev/null)"; then printf '%s' "$v"; return 0; fi
    sleep 5
  done
  return 1
}

compare() { # chiave valore-atteso valore-letto (vuoto = assente)
  if [ -z "$3" ]; then echo "$1=missing"; elif [ "$3" = "$2" ]; then echo "$1=match"; else echo "$1=mismatch"; fi
}

inspect() {
  local img v s
  if ! kc get deploy app -n "$GS_APP_NS" >/dev/null 2>&1; then
    echo "deploy=no"; echo "image="; echo "var=missing"; echo "secret=missing"
    return 0
  fi
  echo "deploy=yes"
  img="$(kc get deploy app -n "$GS_APP_NS" -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null)"
  echo "image=${img}"
  v="$(pod_env "$GS_VAR_NAME")" || v=""
  compare var "$GS_VAR_VALUE" "$v"
  s="$(pod_env "$GS_SECRET_NAME")" || s=""
  if [ -z "$s" ]; then
    # Secret app-secrets (GIT-373) se il segreto non e' nell'env del pod.
    s="$(kc get secret app-secrets -n "$GS_APP_NS" -o "jsonpath={.data.${GS_SECRET_NAME}}" 2>/dev/null | base64 -d 2>/dev/null)" || s=""
  fi
  compare secret "$GS_SECRET_VALUE" "$s"
  # shellcheck disable=SC2016  # l'espansione di $(cat ...) avviene nel pod, non sull'host
  if kc exec -n "$GS_APP_NS" deploy/app -- sh -c 'test -f /data/gitstack-e2e.txt && test "$(cat /data/gitstack-e2e.txt; printf x)" = gitstack-e2e-volume-423x' 2>/dev/null; then
    echo 'volume=match'
  else
    echo 'volume=mismatch'
  fi
}

write_volume() {
  for _ in 1 2 3 4 5 6 7 8 9 10 11 12; do
    # shellcheck disable=SC2016  # l'espansione di $(cat ...) avviene nel pod, non sull'host
    if kc exec -n "$GS_APP_NS" deploy/app -- sh -c 'printf %s gitstack-e2e-volume-423 > /data/gitstack-e2e.txt && test "$(cat /data/gitstack-e2e.txt)" = gitstack-e2e-volume-423' 2>/dev/null; then
      echo 'STEP ok volume'; return 0
    fi
    sleep 5
  done
  fail volume 'scrittura o rilettura fallita'
}

case "${1:-}" in
  push) push ;;
  inspect) inspect ;;
  write-volume) write_volume ;;
  *) echo "uso: $0 push|inspect" >&2; exit 2 ;;
esac
