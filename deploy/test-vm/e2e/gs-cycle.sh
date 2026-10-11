#!/bin/bash
# Ciclo di gs sulla VM (GIT-173, M-07/L, passo e8 di e2e.ps1).
#
# Scarica gs da /downloads con install-gs.sh (impronta della CA verificata,
# checksum SHA-256 di SHA256SUMS), poi fa il ciclo di un agente con GS_HOST e
# GS_TOKEN: repo create, clone, issue create, commit con `fixes #n` e push,
# issue chiusa, notifiche, `gs api` su un endpoint admin.
#
# Variabili (le passa e2e.ps1):
#   GS_VM_IP           indirizzo della VM (ingress HTTPS)
#   GS_CA_SHA256       impronta SHA-256 della CA interna
#   GS_USER_TOKEN      token read+write:resource dell'utente di prova
#   GS_ADMIN_TOKEN     token read+write:user dell'admin
#   GS_USER_NAME       utente di prova (proprietario del repo)
#   GS_REPO_NAME       nome del repo da creare
#   GS_AGENT_NAME      utente agente a cui l'admin crea un token con gs api
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

# --- 1. installazione da /downloads ---------------------------------------
# Il primo scaricamento di install-gs.sh e' con -k: e' lo script stesso a
# verificare l'impronta della CA (GS_CA_SHA256) prima di scaricare il binario.
curl -fsSLk -o "$W/install-gs.sh" "$BASE/install-gs.sh" || fail install "download di install-gs.sh"
out="$(GS_BASE_URL="$BASE" GS_INSTALL_DIR="$W/bin" GS_CA_SHA256="$GS_CA_SHA256" sh "$W/install-gs.sh" 2>&1)" || fail install "install-gs.sh: $out"
[ -x "$W/bin/gs" ] || fail install "gs non installato"
ok install

# --- 2. checksum -------------------------------------------------------------
CA="$(ls "$HOME"/.config/gitstack/ca-*.crt 2>/dev/null | head -n 1)"
[ -n "$CA" ] || fail checksum "CA non salvata dall'installer"
os="$(uname -s | tr 'A-Z' 'a-z')"
case "$(uname -m)" in x86_64 | amd64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; *) arch="$(uname -m)" ;; esac
curl -fsSL --cacert "$CA" -o "$W/SHA256SUMS" "$BASE/downloads/SHA256SUMS" || fail checksum "download di SHA256SUMS"
want="$(awk -v f="gs_${os}_${arch}" '$2 == f { print $1 }' "$W/SHA256SUMS")"
got="$(sha256sum "$W/bin/gs" | awk '{ print $1 }')"
[ -n "$want" ] && [ "$want" = "$got" ] || fail checksum "atteso '$want', calcolato '$got'"
# un binario alterato non deve passare lo stesso controllo
cp "$W/bin/gs" "$W/gs-alterato" && printf 'x' >> "$W/gs-alterato"
[ "$(sha256sum "$W/gs-alterato" | awk '{ print $1 }')" != "$want" ] || fail checksum "il binario alterato ha lo stesso checksum"
ok checksum

# --- 3. ciclo con GS_HOST / GS_TOKEN -----------------------------------------
export PATH="$W/bin:$PATH"
export SSL_CERT_FILE="$CA"
export GS_HOST="${GS_VM_IP}"
export GS_NO_KEYRING=1
GS="$W/bin/gs"
ADMIN_TOKEN="$GS_ADMIN_TOKEN"
export GS_TOKEN="$GS_USER_TOKEN"
REPO="${GS_USER_NAME}/${GS_REPO_NAME}"

"$GS" version >/dev/null 2>&1 || fail version "gs version: exit $?"
ok version

out="$("$GS" auth status 2>&1)" || fail auth_status "$out"
case "$out" in *"$GS_USER_NAME"*) ;; *) fail auth_status "manca l'utente in: $out" ;; esac
case "$out" in *"$GS_TOKEN"*) fail auth_status "il token e' stato stampato" ;; esac
ok auth_status

out="$("$GS" repo create "$GS_REPO_NAME" --json fullName,visibility 2>&1)" || fail repo_create "$out"
case "$out" in *"$REPO"*private*) ;; *) fail repo_create "atteso $REPO privato in: $out" ;; esac
ok repo_create

"$GS" auth setup-git >/dev/null 2>&1 || fail setup_git "gs auth setup-git: exit $?"
cd "$W" || exit 1
out="$("$GS" repo clone "$REPO" "$W/work" 2>&1)" || fail clone "$out"
[ -d "$W/work/.git" ] || fail clone "manca .git"
ok clone

num="$("$GS" issue create -R "$REPO" -t "Manca il README" -b "da aggiungere" --json number --jq .number 2>&1)" || fail issue_create "$num"
case "$num" in '' | *[!0-9]*) fail issue_create "numero non valido: $num" ;; esac
ok issue_create

cd "$W/work" || exit 1
echo "# ciclo gs" > README.md
git add README.md && git commit -q -m "Aggiunge il README

fixes #$num" || fail push "commit"
out="$(git push -q origin HEAD:main 2>&1)" || fail push "$out"
ok push

# core chiude l'issue quando consuma l'evento git.push (asincrono: polling).
state=""
for _ in 1 2 3 4 5 6 7 8 9 10 11 12; do
  state="$("$GS" issue view "$num" -R "$REPO" --json state --jq .state 2>/dev/null)"
  [ "$state" = closed ] && break
  sleep 5
done
[ "$state" = closed ] || fail issue_chiusa "stato '$state' dopo il push con fixes #$num"
ok issue_chiusa

# Le notifiche si leggono con gs (la lista puo' essere vuota: non si notificano
# le proprie azioni, ma il comando deve rispondere e leggerle tutte).
"$GS" notification list --json id >/dev/null 2>&1 || fail notification "notification list: exit $?"
"$GS" notification read --all >/dev/null 2>&1 || fail notification "notification read --all: exit $?"
left="$("$GS" notification list --json id --jq length 2>&1)" || fail notification "$left"
[ "$left" = 0 ] || fail notification "restano $left notifiche non lette"
ok notification

exp="$(date -u -d '+2 hours' +%Y-%m-%dT%H:%M:%SZ)"
out="$(GS_TOKEN="$ADMIN_TOKEN" "$GS" api -X POST "/users/${GS_AGENT_NAME}/tokens" -f name=gs-e2e -F 'scopes[]=read:resource' -f "expiresAt=$exp" --jq .token 2>&1)" || fail gs_api_admin "$out"
case "$out" in gst_*) ;; *) fail gs_api_admin "token dell'agente non valido: $out" ;; esac
"$GS" api -X POST "/users/${GS_AGENT_NAME}/tokens" -f name=no -F 'scopes[]=read:resource' -f "expiresAt=$exp" >/dev/null 2>&1 && fail gs_api_admin "un utente non admin ha creato un token per un altro"
ok gs_api_admin
echo "GS-CYCLE-DONE"
