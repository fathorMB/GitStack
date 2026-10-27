#!/bin/bash
# Ciclo della CI sulla VM (GIT-291, v2, passo e12 di e2e.ps1).
#
# Una persona (il proprietario del repo) avvia con `gs workflow run` un workflow che gira come
# pod nel runner integrato: costruisce un'immagine OCI (un solo strato, fatto con tar), la prova
# e ne scrive il digest nel log. Poi, dal terminale: `gs run watch --exit-status` (uscita 0),
# un secondo run che fallisce a posta (`--exit-status` non zero, `gs run view --log-failed`
# col log del solo job fallito), la pubblicazione dell'immagine nel registry con il token della
# persona (docker non serve: API OCI con curl), `gs package list` che la mostra e `gs release`
# su un tag di prova v0.0.0-test.N con il file dell'immagine come asset.
#
# Limite dichiarato (da chiudere con il giro su homehub): il push dal pod col token del job
# (permissions: packages: write) non è provato qui; lo coprono TestRegistryAccess_SoloIlRepoDelJob
# e TestTokenDelJob. Il push di questo passo usa il token della persona.
#
# Variabili (le passa e2e.ps1): GS_VM_IP, GS_CA_SHA256, GS_USER_TOKEN, GS_USER_NAME, GS_REPO_NAME.
# Ogni passo stampa "STEP ok <nome>"; al primo errore esce con codice != 0
# dopo aver stampato "STEP FAIL <nome>: <motivo>".
set -u

W="$(mktemp -d)"
trap 'rm -rf "$W"' EXIT
export HOME="$W/home"
mkdir -p "$HOME" "$W/bin"
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=E2E GIT_AUTHOR_EMAIL=e2e@example.com GIT_COMMITTER_NAME=E2E GIT_COMMITTER_EMAIL=e2e@example.com
BASE="https://${GS_VM_IP}"

fail() { echo "STEP FAIL $1: $2"; exit 1; }
ok() { echo "STEP ok $1"; }

# --- installazione di gs da /downloads (impronta della CA verificata) -------
curl -fsSLk -o "$W/install-gs.sh" "$BASE/install-gs.sh" || fail install "download di install-gs.sh"
out="$(GS_BASE_URL="$BASE" GS_INSTALL_DIR="$W/bin" GS_CA_SHA256="$GS_CA_SHA256" sh "$W/install-gs.sh" 2>&1)" || fail install "install-gs.sh: $out"
[ -x "$W/bin/gs" ] || fail install "gs non installato"
CA=""
for cert in "$HOME"/.config/gitstack/ca-*.crt; do [ -f "$cert" ] && CA="$cert" && break; done
[ -n "$CA" ] || fail install "CA non salvata dall'installer"
ok install

export PATH="$W/bin:$PATH"
export SSL_CERT_FILE="$CA" GIT_SSL_CAINFO="$CA"
export GS_HOST="${GS_VM_IP}"
export GS_NO_KEYRING=1
GS="$W/bin/gs"
REPO="${GS_USER_NAME}/${GS_REPO_NAME}"
as_person() { GS_TOKEN="$GS_USER_TOKEN" "$GS" "$@"; }
fail_run() {
  local step="$1" id="$2" reason="$3" state job
  echo "STEP FAIL $step: $reason"
  state="$(as_person run view "$id" -R "$REPO" --json id,status,conclusion,jobs 2>&1)" || true
  echo "===== run $id: stato dei job (max 5000 righe) ====="
  printf '%s\n' "$state" | head -n 5000
  echo "===== run $id: log di tutti i job (max 5000 righe per job) ====="
  while read -r job; do
    [ -n "$job" ] || continue
    echo "===== job $job ====="
    curl -fsSL --cacert "$CA" -H "Authorization: Bearer $GS_USER_TOKEN" "$BASE/api/v1/repos/$REPO/actions/jobs/$job/logs" 2>&1 | head -n 5000 || true
  done < <(printf '%s' "$state" | python3 -c 'import json,sys; d=json.load(sys.stdin); [print(j["id"]) for j in d.get("jobs", [])]')
  exit 1
}
# Il credential helper di `gs auth setup-git` legge GS_TOKEN dall'ambiente: ogni git che parla
# col server passa da as_git (GIT-433), senza esportare il token per tutto lo script.
as_git() { GS_TOKEN="$GS_USER_TOKEN" git "$@"; }
GS_TOKEN="$GS_USER_TOKEN" "$GS" auth setup-git >/dev/null 2>&1 || fail setup_git "gs auth setup-git: exit $?"

out="$(as_person repo create "$GS_REPO_NAME" --json fullName 2>&1)" || fail repo_create "$out"
case "$out" in *"$REPO"*) ;; *) fail repo_create "atteso $REPO in: $out" ;; esac
ok repo_create

# --- il workflow: build e test dell'immagine in un pod, con un input -------------------------
as_person repo clone "$REPO" "$W/repo" >/dev/null 2>&1 || fail clone "clone"
cd "$W/repo" || exit 1
mkdir -p .gitstack/workflows
cat >.gitstack/workflows/image.yml <<'EOF'
name: image
on:
  workflow_dispatch:
    inputs:
      versione:
        required: true
      rompi:
        required: false
        default: "no"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: build dello strato
        run: |
          set -eu
          mkdir -p rootfs/app
          echo "versione ${{ github.event.inputs.versione }}" > rootfs/app/VERSION
          tar -C rootfs -cf layer.tar .
          echo "IMAGE-LAYER-DIGEST sha256:$(sha256sum layer.tar | cut -d' ' -f1)"
  test:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - name: prova
        run: |
          set -eu
          if [ "${{ github.event.inputs.rompi }}" = si ]; then echo "E2E-ERRORE-VOLUTO: la prova fallisce a posta"; exit 1; fi
          echo "IMAGE-TEST-OK"
EOF
echo "# ciclo CI" >README.md
git add -A
git commit -q -m "Workflow image" || fail clone "commit"
out="$(as_git push -q origin HEAD:main 2>&1)" || fail workflow_push "$out"
ok workflow_push

wl="$(as_person workflow list -R "$REPO" 2>&1)" || fail workflow_list "$wl"
case "$wl" in *image.yml*) ;; *) fail workflow_list "image.yml assente: $wl" ;; esac
ok workflow_list

# Senza l'input obbligatorio il run non parte.
as_person workflow run image.yml -R "$REPO" --ref main >/dev/null 2>&1 && fail workflow_input "il run è partito senza l'input obbligatorio"
ok workflow_input

# --- run riuscito ----------------------------------------------------------------------------
rid="$(as_person workflow run image.yml -R "$REPO" --ref main -f versione=0.0.1 --json id --jq .id 2>&1)" || fail run_ok_start "$rid"
case "$rid" in '' | *[!0-9a-f-]*) fail run_ok_start "id non valido: $rid" ;; esac
w="$(as_person run watch "$rid" -R "$REPO" --exit-status --interval 3s 2>&1)" || fail_run run_watch_ok "$rid" "uscita non zero: $w"
case "$w" in *completed*success*) ;; *) fail_run run_watch_ok "$rid" "esito inatteso: $w" ;; esac
ok run_watch_ok

# --- run che fallisce: watch --exit-status non zero, view --log-failed col log del job -------
bid="$(as_person workflow run image.yml -R "$REPO" --ref main -f versione=0.0.2 -f rompi=si --json id --jq .id 2>&1)" || fail_run run_bad_start "$rid" "$bid"
if as_person run watch "$bid" -R "$REPO" --exit-status --interval 3s >"$W/watch.out" 2>&1; then fail_run run_watch_ko "$bid" "watch --exit-status è uscito 0 su un run fallito"; fi
grep -q failure "$W/watch.out" || fail_run run_watch_ko "$bid" "esito 'failure' assente: $(cat "$W/watch.out")"
ok run_watch_ko
lf="$(as_person run view "$bid" -R "$REPO" --log-failed 2>&1)" || fail_run run_log_failed "$bid" "$lf"
case "$lf" in *"E2E-ERRORE-VOLUTO"*) ;; *) fail_run run_log_failed "$bid" "il log del job fallito manca: $lf" ;; esac
case "$lf" in *"IMAGE-LAYER-DIGEST"*) fail_run run_log_failed "$bid" "log-failed stampa anche il job riuscito" ;; esac
ok run_log_failed

# --- immagine nel registry (token della persona), poi la release ------------------------------
echo "versione 0.0.1" >"$W/VERSION"
mkdir -p "$W/rootfs/app" && cp "$W/VERSION" "$W/rootfs/app/VERSION"
tar -C "$W/rootfs" -cf "$W/layer.tar" . || fail_run image_push "$bid" "tar"
sha() { sha256sum "$1" | cut -d' ' -f1; }
reg() { curl -fsSL --cacert "$CA" -u "token:${GS_USER_TOKEN}" "$@"; }
IMG="${REPO}/app"
upload_blob() { # file
  local d loc
  d="sha256:$(sha "$1")"
  loc="$(curl -fsSL --cacert "$CA" -u "token:${GS_USER_TOKEN}" -X POST -D - -o /dev/null "$BASE/v2/${IMG}/blobs/uploads/" | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')"
  [ -n "$loc" ] || return 1
  case "$loc" in http*) ;; *) loc="${BASE}${loc}" ;; esac
  case "$loc" in *\?*) loc="${loc}&digest=${d}" ;; *) loc="${loc}?digest=${d}" ;; esac
  reg -X PUT -H 'Content-Type: application/octet-stream' --data-binary "@$1" "$loc" >/dev/null
}
printf '{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":["sha256:%s"]}}' "$(sha "$W/layer.tar")" >"$W/config.json"
upload_blob "$W/layer.tar" || fail_run image_push "$bid" "upload dello strato"
upload_blob "$W/config.json" || fail_run image_push "$bid" "upload della config"
printf '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:%s","size":%s},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:%s","size":%s}]}' \
  "$(sha "$W/config.json")" "$(wc -c <"$W/config.json")" "$(sha "$W/layer.tar")" "$(wc -c <"$W/layer.tar")" >"$W/manifest.json"
reg -X PUT -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' --data-binary "@$W/manifest.json" "$BASE/v2/${IMG}/manifests/v0.0.0-test.1" >/dev/null || fail_run image_push "$bid" "push del manifest"
ok image_push

pk="$(as_person package list -R "$REPO" 2>&1)" || fail_run package_list "$bid" "$pk"
case "$pk" in *v0.0.0-test.1*) ;; *) fail_run package_list "$bid" "tag assente: $pk" ;; esac
ok package_list

cd "$W/repo" || exit 1
git tag v0.0.0-test.1 || fail_run release "$bid" "git tag"
out="$(as_git push -q origin v0.0.0-test.1 2>&1)" || fail_run release "$bid" "push del tag: $out"
relid="$(as_person release create -R "$REPO" --tag v0.0.0-test.1 --name "Prova 1" --generate-notes --prerelease 2>&1)" || fail_run release "$bid" "release create: $relid"
as_person release upload "$relid" "$W/layer.tar" -R "$REPO" >/dev/null 2>&1 || fail_run release "$bid" "release upload"
as_person release list -R "$REPO" 2>&1 | grep -q v0.0.0-test.1 || fail_run release "$bid" "release list senza il tag"
ok release

echo "GS-CI-CYCLE-DONE"
