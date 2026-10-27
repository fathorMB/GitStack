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
#   write-volume   Scrive il file di prova nel volume /data dell'app (GS_APP_NS).
#   push-web, probe-anon, probe-auth, diag   Ciclo dell'app (GIT-355, H1/H6/H7/H12):
#            push-web pubblica l'immagine e2eweb (v1, v2: busybox httpd sulla 8080 che serve
#            "e2e-web-<tag>"; GS_VM_IP, GS_CA, GS_USER_TOKEN, GS_IMAGE); probe-anon chiede la
#            pagina senza credenziale e vuole 401; probe-auth la chiede con GS_PROBE_TOKEN e vuole
#            il corpo GS_EXPECT (GS_APP_HOST, GS_CA); diag stampa i pod del namespace GS_APP_NS.
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

push() { publish push sleep v1 v2 v3; }
push_web() { publish push-web web v1 v2; }

# publish <nome-del-passo> <sleep|web> <tag>...
# sleep: Entrypoint `sleep`, per i controlli del restore. web: busybox httpd sulla 8080 che serve
# /www/index.html = "e2e-web-<tag>" (passo del ciclo dell'app, GIT-355).
publish() {
  local step="$1" kind="$2" tag
  shift 2
  BASE="https://${GS_VM_IP}"
  local root="$W/rootfs" entry='["/bin/sleep","86400"]'
  build_rootfs "$root" || fail "$step" "rootfs di busybox non disponibile (ne' dall'immagine del chart ne' statico)"
  if [ "$kind" = web ]; then
    entry='["/bin/busybox","httpd","-f","-p","8080","-h","/www"]'
    if [ ! -e "$root/bin/busybox" ]; then fail "$step" "manca /bin/busybox nel rootfs: httpd non disponibile"; fi
    mkdir -p "$root/www"
  fi
  for tag in "$@"; do
    echo "e2e-$tag" >"$root/TAG"
    if [ "$kind" = web ]; then printf 'e2e-web-%s\n' "$tag" >"$root/www/index.html"; fi
    tar -C "$root" -cf "$W/layer-$tag.tar" . || fail "$step" "tar di $tag"
    printf '{"architecture":"amd64","os":"linux","config":{"Entrypoint":%s},"rootfs":{"type":"layers","diff_ids":["sha256:%s"]}}' "$entry" "$(sha "$W/layer-$tag.tar")" >"$W/config-$tag.json"
    upload_blob "$W/layer-$tag.tar" || fail "$step" "upload dello strato $tag"
    upload_blob "$W/config-$tag.json" || fail "$step" "upload della config $tag"
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
    # shellcheck disable=SC2016 # $1 e' il nome passato alla shell nel pod
    if v="$(kc exec -n "$GS_APP_NS" deploy/app -- sh -c 'printenv "$1" || true' sh "$1" 2>/dev/null)"; then printf '%s' "$v"; return 0; fi
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
    echo "deploy=no"; echo "image="; echo "var=exec-failed"; echo "secret=missing"; echo "volume=exec-failed"
    return 0
  fi
  echo "deploy=yes"
  img="$(kc get deploy app -n "$GS_APP_NS" -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null)"
  echo "image=${img}"
  if v="$(pod_env "$GS_VAR_NAME")"; then compare var "$GS_VAR_VALUE" "$v"; else echo 'var=exec-failed'; fi
  s="$(pod_env "$GS_SECRET_NAME")" || s=""
  if [ -z "$s" ]; then
    # Secret app-secrets (GIT-373) se il segreto non e' nell'env del pod.
    s="$(kc get secret app-secrets -n "$GS_APP_NS" -o "jsonpath={.data.${GS_SECRET_NAME}}" 2>/dev/null | base64 -d 2>/dev/null)" || s=""
  fi
  compare secret "$GS_SECRET_VALUE" "$s"
  # shellcheck disable=SC2016  # l'espansione di $(cat ...) avviene nel pod, non sull'host
  if v="$(kc exec -n "$GS_APP_NS" deploy/app -- sh -c 'if test -f /data/gitstack-e2e.txt && test "$(cat /data/gitstack-e2e.txt; printf x)" = gitstack-e2e-volume-423x; then echo match; else echo mismatch; fi' 2>/dev/null)"; then
    echo "volume=$v"
  else
    echo 'volume=exec-failed'
  fi
}

diagnose() {
  local dir="$1" pod container
  mkdir -p "$dir"
  kc -n "$GS_APP_NS" get pods -o wide >"$dir/pods.txt" 2>&1
  kc -n "$GS_APP_NS" describe deploy app >"$dir/deployment.txt" 2>&1
  kc -n "$GS_APP_NS" describe pods >"$dir/describe-pods.txt" 2>&1
  kc -n "$GS_APP_NS" get events --sort-by=.lastTimestamp >"$dir/events.txt" 2>&1
  kc -n "$GS_APP_NS" get pvc -o wide >"$dir/pvc.txt" 2>&1
  kc get pv -o wide >"$dir/pv.txt" 2>&1
  : >"$dir/logs.txt"
  while read -r pod; do
    [ -n "$pod" ] || continue
    for container in $(kc -n "$GS_APP_NS" get "$pod" -o jsonpath='{.spec.initContainers[*].name} {.spec.containers[*].name}'); do
      for previous in current previous; do
        printf '\n=== %s %s %s ===\n' "$pod" "$container" "$previous" >>"$dir/logs.txt"
        if [ "$previous" = previous ]; then
          kc -n "$GS_APP_NS" logs "$pod" -c "$container" --previous >>"$dir/logs.txt" 2>&1 || true
        else
          kc -n "$GS_APP_NS" logs "$pod" -c "$container" >>"$dir/logs.txt" 2>&1 || true
        fi
      done
    done
  done < <(kc -n "$GS_APP_NS" get pods -o name 2>>"$dir/logs.txt")
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

# --- Ciclo dell'app (GIT-355): login davanti all'app, deploy, rollback -----------------------
# Il nome DNS dell'app (apps.e2e.test) non esiste: si risolve a mano verso Traefik della VM.

app_curl() { # file-corpo intestazione... ; stampa il codice HTTP
  local out="$1"
  shift
  curl -sS --max-time 15 --cacert "$GS_CA" --resolve "${GS_APP_HOST}:443:127.0.0.1" -o "$out" -w '%{http_code}' "$@" "https://${GS_APP_HOST}/" 2>>"$W/curl.err"
}

# probe-anon: senza credenziale il forward-auth del gateway risponde 401 (H6), mai il corpo dell'app.
probe_anon() {
  local code
  code="$(app_curl "$W/anon.out" -H 'Accept: application/json')" || code="000"
  if [ "$code" != 401 ]; then
    fail login-anon "senza credenziale HTTP $code (atteso 401): $(head -c 200 "$W/anon.out" 2>/dev/null | tr '\n' ' ') $(tr '\n' ' ' <"$W/curl.err" 2>/dev/null)"
  fi
  if grep -q 'e2e-web-' "$W/anon.out" 2>/dev/null; then fail login-anon "senza credenziale il corpo dell'app e' arrivato lo stesso"; fi
  echo "STEP ok login-anon"
}

# probe-auth: con il token GitStack (Authorization: Bearer gst_..., read sul repo) l'app risponde con il
# corpo atteso GS_EXPECT. Si riprova fino a 90 s: dopo un deploy il nuovo pod e il cambio di Service
# non sono istantanei.
probe_auth() {
  local code="000" i
  for i in $(seq 1 18); do
    code="$(app_curl "$W/auth.out" -H "Authorization: Bearer ${GS_PROBE_TOKEN}")" || code="000"
    if [ "$code" = 200 ] && grep -q "^${GS_EXPECT}\$" "$W/auth.out" 2>/dev/null; then
      echo "STEP ok serve-${GS_EXPECT}"
      return 0
    fi
    sleep 5
  done
  fail "serve-${GS_EXPECT}" "con il token HTTP $code, corpo '$(head -c 200 "$W/auth.out" 2>/dev/null | tr '\n' ' ')' (atteso '${GS_EXPECT}') $(tr '\n' ' ' <"$W/curl.err" 2>/dev/null)"
}

# diag: i pod del namespace dell'app (stato, eventi, log), per la diagnostica del passo.
diag() {
  echo "== pod di ${GS_APP_NS}"
  kc get pods -n "$GS_APP_NS" -o wide 2>&1
  echo "== eventi di ${GS_APP_NS}"
  kc get events -n "$GS_APP_NS" --sort-by=.lastTimestamp 2>&1 | tail -n 30
  echo "== describe dei pod"
  kc describe pods -n "$GS_APP_NS" 2>&1 | tail -n 120
  echo "== log dei pod"
  kc get pods -n "$GS_APP_NS" -o name 2>/dev/null | while read -r p; do
    echo "-- $p"
    kc logs -n "$GS_APP_NS" "$p" --all-containers --tail=50 2>&1
  done
  return 0
}

case "${1:-}" in
  push) push ;;
  push-web) push_web ;;
  inspect) inspect ;;
  diagnose) diagnose "$2" ;;
  write-volume) write_volume ;;
  probe-anon) probe_anon ;;
  probe-auth) probe_auth ;;
  diag) diag ;;
  *) echo "uso: $0 push|push-web|inspect|write-volume|probe-anon|probe-auth|diag" >&2; exit 2 ;;
esac
