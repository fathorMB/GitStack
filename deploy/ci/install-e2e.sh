#!/usr/bin/env bash
# deploy/ci/install-e2e.sh — passi di .github/workflows/install.yml (GIT-203,
# regole N2 e V1): installazione, backup, aggiornamento e ripristino provati
# su un runner Ubuntu vero. Il workflow li chiama uno alla volta, questo
# script contiene la logica, così si legge, si prova con shellcheck e si può
# rilanciare a mano su una VM.
#
# Sottocomandi:
#   previous-commit            Ultimo commit di main con CI verde, antenato
#                              stretto di $TARGET_SHA, con release sha-<commit>
#                              (immagini e binario pubblicati) e con
#                              deploy/k3s-version (aggiornamento di k3s, N10).
#   older-k3s                  Una patch di k3s precedente a quella dichiarata
#                              in deploy/k3s-version (esiste su GitHub).
#   wait-healthy [secondi]     Attende https://$E2E_HOST/api/healthz = ok, il rollout di
#                              tutti i Deployment/StatefulSet, nessun pod in terminazione e
#                              /downloads/ca.crt = 200 per 3 volte di fila.
#   seed                       Admin (cambio password obbligatorio), utente,
#                              token, repo con un commit spinto via HTTPS e
#                              una issue. Stato in $E2E_STATE_DIR.
#   mutate                     Dati scritti DOPO il backup: un secondo commit
#                              e una seconda issue, che il restore deve togliere.
#   verify-seed [--push]       Dopo il restore (o l'upgrade): admin con la
#                              password cambiata, HEAD e issue del seed, nessun
#                              dato di `mutate`, CA invariata, clone; con --push
#                              anche un push nuovo (il suo HEAD diventa il riferimento).
#   verify-after-mutate        Solo prima del restore: i dati di `mutate` ci sono.
#   verify-k3s <versione>      k3s in esecuzione = versione, da `k3s --version`,
#                              da `kubectl version` e da `gitstack status --json`.
#   latest-backup              Stampa il percorso dell'archivio più recente.
#   diagnostics <cartella>     Log del cluster e dei servizi (non fallisce mai).
#
# Variabili: E2E_HOST (IP o nome con cui l'istanza è raggiungibile),
# E2E_STATE_DIR (default /var/tmp/gitstack-ci), TARGET_SHA, GITHUB_REPOSITORY,
# GH_TOKEN (previous-commit). Tutto quello che è segreto resta in file 0600
# dentro E2E_STATE_DIR e non viene mai stampato.
set -euo pipefail

STATE_DIR="${E2E_STATE_DIR:-/var/tmp/gitstack-ci}"
HOST="${E2E_HOST:-}"
NEW_ADMIN_PASSWORD='Ci-admin-password-lunga-42'
USER_PASSWORD='Ci-utente-password-lunga-42'
KUBECTL=(sudo k3s kubectl)

log() { printf '==> %s\n' "$*"; }
fail() { printf 'ERRORE: %s\n' "$*" >&2; exit 1; }
need() { [ -n "${!1:-}" ] || fail "manca la variabile $1"; }

base_url() { need E2E_HOST; printf 'https://%s' "${HOST}"; }

# --- previous-commit -----------------------------------------------------

previous_commit() {
  need TARGET_SHA
  need GITHUB_REPOSITORY
  local c assets
  while read -r c; do
    [ -n "${c}" ] || continue
    [ "${c}" != "${TARGET_SHA}" ] || continue
    git cat-file -e "${c}^{commit}" 2>/dev/null || continue
    git merge-base --is-ancestor "${c}" "${TARGET_SHA}" || continue
    git cat-file -e "${c}:deploy/k3s-version" 2>/dev/null || continue
    assets="$(gh release view "sha-${c}" --repo "${GITHUB_REPOSITORY}" --json assets -q '.assets[].name' 2>/dev/null || true)"
    printf '%s\n' "${assets}" | grep -qx 'gitstack-linux-amd64' || continue
    printf '%s\n' "${c}"
    return 0
  done < <(gh run list --repo "${GITHUB_REPOSITORY}" --workflow ci.yml --branch main --event push \
    --status success --limit 60 --json headSha -q '.[].headSha')
  fail "nessun commit precedente di main con CI verde, release sha-<commit> e deploy/k3s-version: indicalo con l'ingresso previous_commit"
}

# --- older-k3s -----------------------------------------------------------

older_k3s() {
  local declared re='^v([0-9]+)\.([0-9]+)\.([0-9]+)\+k3s([0-9]+)$'
  declared="$(tr -d '[:space:]' < deploy/k3s-version)"
  [[ "${declared}" =~ ${re} ]] || fail "deploy/k3s-version non riconosciuto: '${declared}'"
  local maj="${BASH_REMATCH[1]}" min="${BASH_REMATCH[2]}" pat="${BASH_REMATCH[3]}" rev="${BASH_REMATCH[4]}"
  local p r tag
  for ((p = pat; p >= 0; p--)); do
    for ((r = (p == pat ? rev - 1 : 3); r >= 1; r--)); do
      tag="v${maj}.${min}.${p}+k3s${r}"
      if gh api "repos/k3s-io/k3s/releases/tags/${tag/+/%2B}" --silent 2>/dev/null; then
        printf '%s\n' "${tag}"
        return 0
      fi
    done
  done
  fail "nessuna patch di k3s precedente a ${declared} nella linea ${maj}.${min}: indicala con l'ingresso k3s_from"
}

# --- HTTP ----------------------------------------------------------------

CODE=""
BODY=""
# api METODO PERCORSO JSON [opzioni curl...]: il codice HTTP resta in $CODE e il
# corpo in $BODY (non in una subshell: si chiama senza $(...)). Il corpo non va
# stampato quando contiene credenziali.
api() {
  local method="$1" path="$2" data="$3"
  shift 3
  local out args=(-sS --cacert "${STATE_DIR}/ca.crt" -X "${method}" -H 'Content-Type: application/json' -w '%{http_code}')
  [ -z "${data}" ] || args+=(-d "${data}")
  out="$(mktemp)"
  CODE="$(curl "${args[@]}" "$@" -o "${out}" "$(base_url)${path}" || true)"
  BODY="$(cat "${out}")"
  rm -f "${out}"
}

expect() { # <atteso> <ottenuto> <descrizione>
  [ "$1" = "$2" ] || fail "$3: HTTP $2 (atteso $1)"
}

wait_healthy() {
  need E2E_HOST
  local timeout="${1:-600}" deadline body
  deadline=$((SECONDS + timeout))
  while [ "${SECONDS}" -lt "${deadline}" ]; do
    body="$(curl -sk -m 10 "$(base_url)/api/healthz" || true)"
    if printf '%s' "${body}" | grep -q '"status" *: *"ok"'; then
      log "healthz ok"
      break
    fi
    sleep 5
  done
  printf '%s' "${body}" | grep -q '"status" *: *"ok"' || fail "/api/healthz non ok dopo ${timeout}s"
  # Dopo un restore o un upgrade i pod (web in particolare) si riavviano: il
  # gateway può essere sano mentre /downloads/ca.crt dà 502.
  local remaining kind res
  for kind in deployment statefulset; do
    remaining=$((deadline - SECONDS))
    [ "${remaining}" -ge 10 ] || remaining=10
    # Nessuna risorsa: rollout status senza nomi non fallisce con -l.
    "${KUBECTL[@]}" -n default rollout status "${kind}" -l app.kubernetes.io/instance --timeout="${remaining}s"       || fail "rollout dei ${kind} non finito entro ${timeout}s"
  done
  # Il rollout può essere "finito" mentre un vecchio pod è ancora in
  # terminazione ma nell'endpoint di Traefik (502/504 sul client, GIT-248):
  # si esce solo senza pod in terminazione e con 3 risposte 200 di fila.
  local code="" ok=0 terminating
  while [ "${SECONDS}" -lt "${deadline}" ]; do
    terminating="$(terminating_pods)"
    if [ -n "${terminating}" ]; then
      log "pod in terminazione: ${terminating//$'
'/ }"
      ok=0
      sleep 5
      continue
    fi
    code="$(curl -sk -m 10 -o /dev/null -w '%{http_code}' "$(base_url)/downloads/ca.crt" || true)"
    if [ "${code}" = "200" ]; then
      ok=$((ok + 1))
      if [ "${ok}" -ge 3 ]; then
        log "rollout completo, nessun pod in terminazione e /downloads/ca.crt 200 (${ok} volte di fila)"
        return 0
      fi
      sleep 2
      continue
    fi
    ok=0
    sleep 5
  done
  res="${code:-nessuna risposta}"
  fail "/downloads/ca.crt: HTTP ${res} (o pod in terminazione) dopo ${timeout}s"
}

# Nomi dei pod dei rilasci GitStack con deletionTimestamp (uno per riga).
terminating_pods() {
  "${KUBECTL[@]}" -n default get pods -l app.kubernetes.io/instance -o json 2>/dev/null     | jq -r '.items[] | select(.metadata.deletionTimestamp != null) | .metadata.name' 2>/dev/null || true
}

# download_ca <file> [secondi]: scarica /downloads/ca.crt e riprova per un tempo
# limitato (502/504 mentre un pod si ferma); riesce solo con HTTP 200 e un
# certificato leggibile. Su errore dice l'ultimo codice HTTP.
download_ca() {
  local out="$1" limit="${2:-90}" end code="" tries=0
  end=$((SECONDS + limit))
  while :; do
    tries=$((tries + 1))
    code="$(curl -sk -m 15 -o "${out}" -w '%{http_code}' "$(base_url)/downloads/ca.crt" || true)"
    if [ "${code}" = "200" ] && openssl x509 -in "${out}" -noout 2>/dev/null; then
      return 0
    fi
    [ "${SECONDS}" -lt "${end}" ] || break
    sleep 3
  done
  fail "download di /downloads/ca.crt: ultimo HTTP ${code:-nessuna risposta} dopo ${tries} tentativi in ${limit}s"
}

fetch_ca() {
  mkdir -p "${STATE_DIR}"
  chmod 700 "${STATE_DIR}"
  # Il primo scaricamento è senza verifica: la CA si confronta subito con
  # l'impronta che dà la macchina (gitstack-tls fingerprint).
  download_ca "${STATE_DIR}/ca.crt"
  local got want
  got="$(openssl x509 -in "${STATE_DIR}/ca.crt" -noout -fingerprint -sha256 | cut -d= -f2 | tr -d ':' | tr 'A-F' 'a-f')"
  want="$(sudo gitstack-tls fingerprint | tr -d ': \n' | tr 'A-F' 'a-f')"
  [ "${got}" = "${want}" ] || fail "l'impronta di ca.crt (${got}) non è quella della macchina (${want})"
  printf '%s\n' "${got}" > "${STATE_DIR}/ca.sha256"
}

git_env() {
  export GIT_TERMINAL_PROMPT=0 GCM_INTERACTIVE=never
  export GIT_AUTHOR_NAME=CI GIT_AUTHOR_EMAIL=ci@example.com GIT_COMMITTER_NAME=CI GIT_COMMITTER_EMAIL=ci@example.com
}

# git col solo la CA interna e senza credential helper.
cigit() { git -c credential.helper= -c core.autocrlf=false -c "http.sslCAInfo=${STATE_DIR}/ca.crt" "$@"; }

# --- seed ----------------------------------------------------------------

login() { # <utente> <password> <cookiejar> -> $CODE
  api POST /api/v1/auth/login "$(jq -n --arg u "$1" --arg p "$2" '{username:$u,password:$p}')" -c "$3"
}

seed() {
  need E2E_HOST
  git_env
  fetch_ca
  local jar="${STATE_DIR}/admin.jar" initial suffix user repo token expires
  # Password iniziale dal Secret: mai stampata.
  initial="$("${KUBECTL[@]}" -n default get secret gitstack-identity-admin -o jsonpath='{.data.password}' | base64 -d)"
  [ -n "${initial}" ] || fail "password iniziale dell'admin non leggibile"

  login admin "${initial}" "${jar}"
  expect 200 "${CODE}" "login admin con la password del Secret"
  printf '%s' "${BODY}" | jq -e '.mustChangePassword == true' >/dev/null || fail "il primo login non chiede il cambio password"
  api GET /api/v1/users '' -b "${jar}"
  expect 403 "${CODE}" "chiamata prima del cambio password"
  printf '%s' "${BODY}" | grep -q password_change_required || fail "403 senza password_change_required"
  api PUT /api/v1/users/admin/password "$(jq -n --arg c "${initial}" --arg n "${NEW_ADMIN_PASSWORD}" '{currentPassword:$c,newPassword:$n}')" -b "${jar}"
  expect 204 "${CODE}" "cambio password admin"
  login admin "${NEW_ADMIN_PASSWORD}" "${jar}"
  expect 200 "${CODE}" "login admin con la password cambiata"
  printf '%s' "${BODY}" | jq -e '.mustChangePassword == false' >/dev/null || fail "dopo il cambio mustChangePassword resta true"
  api GET /api/v1/users '' -b "${jar}"
  expect 200 "${CODE}" "chiamata autenticata dell'admin"

  suffix="$(date +%H%M%S)"
  user="ci${suffix}"
  repo="dati-${suffix}"
  api POST /api/v1/users "$(jq -n --arg u "${user}" --arg p "${USER_PASSWORD}" '{username:$u,email:($u+"@example.com"),password:$p}')" -b "${jar}"
  expect 201 "${CODE}" "creazione utente di prova"
  login "${user}" "${USER_PASSWORD}" "${STATE_DIR}/user.jar"
  expect 200 "${CODE}" "login utente di prova"
  expires="$(date -u -d '+6 hours' +%Y-%m-%dT%H:%M:%SZ)"
  api POST /api/v1/user/tokens "$(jq -n --arg e "${expires}" '{name:"ci",scopes:["read:resource","write:resource"],expiresAt:$e}')" -b "${STATE_DIR}/user.jar"
  expect 201 "${CODE}" "creazione token"
  token="$(printf '%s' "${BODY}" | jq -r .token)"
  if [ -z "${token}" ] || [ "${token}" = null ]; then fail "la risposta di creazione token non ha 'token'"; fi
  api POST /api/v1/repos "$(jq -n --arg o "${user}" --arg n "${repo}" '{owner:$o,name:$n,visibility:"private"}')" -H "Authorization: Bearer ${token}"
  expect 201 "${CODE}" "creazione repo"

  # push HTTPS con il token
  local w="${STATE_DIR}/w-seed" url
  url="https://${user}:${token}@${HOST}/${user}/${repo}.git"
  rm -rf "${w}"
  git init -q -b main "${w}"
  printf 'seed %s\n' "${suffix}" > "${w}/seed.txt"
  cigit -C "${w}" add -A
  cigit -C "${w}" commit -q -m 'seed: primo commit'
  cigit -C "${w}" push -q "${url}" main
  api POST "/api/v1/repos/${user}/${repo}/issues" "$(jq -n '{title:"Issue del seed",body:"creata prima del backup"}')" -H "Authorization: Bearer ${token}"
  expect 201 "${CODE}" "creazione issue del seed"

  umask 077
  {
    printf 'CI_USER=%s\nCI_REPO=%s\nCI_TOKEN=%s\n' "${user}" "${repo}" "${token}"
    printf 'HEAD_SEED=%s\n' "$(cigit -C "${w}" rev-parse HEAD)"
    printf 'ISSUE_SEED=%s\n' "$(printf '%s' "${BODY}" | jq -r .number)"
  } > "${STATE_DIR}/seed.env"
  log "seed: utente ${user}, repo ${repo}, HEAD $(cigit -C "${w}" rev-parse --short HEAD)"
}

load_seed() {
  [ -f "${STATE_DIR}/seed.env" ] || fail "seed non eseguito (manca ${STATE_DIR}/seed.env)"
  # shellcheck disable=SC1091
  . "${STATE_DIR}/seed.env"
}

remote_head() { # <utente> <repo> <token> -> sha di main
  cigit ls-remote "https://$1:$3@${HOST}/$1/$2.git" refs/heads/main | cut -f1
}

mutate() {
  need E2E_HOST
  git_env
  load_seed
  local w="${STATE_DIR}/w-mutate"
  rm -rf "${w}"
  cigit clone -q "https://${CI_USER}:${CI_TOKEN}@${HOST}/${CI_USER}/${CI_REPO}.git" "${w}"
  printf 'dopo il backup\n' > "${w}/dopo-backup.txt"
  cigit -C "${w}" add -A
  cigit -C "${w}" commit -q -m 'mutate: commit dopo il backup'
  cigit -C "${w}" push -q origin main
  api POST "/api/v1/repos/${CI_USER}/${CI_REPO}/issues" "$(jq -n '{title:"Issue dopo il backup",body:"il restore la deve togliere"}')" -H "Authorization: Bearer ${CI_TOKEN}"
  expect 201 "${CODE}" "creazione issue dopo il backup"
  {
    printf 'HEAD_MUTATED=%s\n' "$(cigit -C "${w}" rev-parse HEAD)"
    printf 'ISSUE_MUTATED=%s\n' "$(printf '%s' "${BODY}" | jq -r .number)"
  } >> "${STATE_DIR}/seed.env"
}

verify_after_mutate() {
  need E2E_HOST
  git_env
  load_seed
  local head
  head="$(remote_head "${CI_USER}" "${CI_REPO}" "${CI_TOKEN}")"
  [ "${head}" = "${HEAD_MUTATED}" ] || fail "prima del restore HEAD è ${head}, atteso ${HEAD_MUTATED}"
  api GET "/api/v1/repos/${CI_USER}/${CI_REPO}/issues/${ISSUE_MUTATED}" '' -H "Authorization: Bearer ${CI_TOKEN}"
  expect 200 "${CODE}" "issue dopo il backup, prima del restore"
}

verify_seed() {
  local push=0
  [ "${1:-}" != --push ] || push=1
  need E2E_HOST
  git_env
  load_seed
  local head fp
  # La CA è quella di prima (il restore la riporta com'era).
  download_ca "${STATE_DIR}/ca-verify.crt"
  fp="$(openssl x509 -in "${STATE_DIR}/ca-verify.crt" -noout -fingerprint -sha256 | cut -d= -f2 | tr -d ':' | tr 'A-F' 'a-f')"
  [ "${fp}" = "$(cat "${STATE_DIR}/ca.sha256")" ] || fail "la CA è cambiata (${fp})"
  login admin "${NEW_ADMIN_PASSWORD}" "${STATE_DIR}/admin.jar"
  expect 200 "${CODE}" "login admin con la password cambiata"
  printf '%s' "${BODY}" | jq -e '.mustChangePassword == false' >/dev/null || fail "l'admin chiede di nuovo il cambio password"
  login "${CI_USER}" "${USER_PASSWORD}" "${STATE_DIR}/user.jar"
  expect 200 "${CODE}" "login utente di prova"
  head="$(remote_head "${CI_USER}" "${CI_REPO}" "${CI_TOKEN}")"
  [ "${head}" = "${HEAD_SEED}" ] || fail "HEAD di main è ${head}, atteso ${HEAD_SEED}"
  api GET "/api/v1/repos/${CI_USER}/${CI_REPO}/issues/${ISSUE_SEED}" '' -H "Authorization: Bearer ${CI_TOKEN}"
  expect 200 "${CODE}" "issue del seed"
  printf '%s' "${BODY}" | jq -e '.title == "Issue del seed"' >/dev/null || fail "titolo dell'issue del seed diverso"
  if [ -n "${ISSUE_MUTATED:-}" ]; then
    api GET "/api/v1/repos/${CI_USER}/${CI_REPO}/issues/${ISSUE_MUTATED}" '' -H "Authorization: Bearer ${CI_TOKEN}"
    expect 404 "${CODE}" "issue creata dopo il backup (deve sparire con il restore)"
  fi
  # clone pulito e push nuovo
  local c="${STATE_DIR}/w-verify"
  rm -rf "${c}"
  cigit clone -q "https://${CI_USER}:${CI_TOKEN}@${HOST}/${CI_USER}/${CI_REPO}.git" "${c}"
  [ "$(cigit -C "${c}" rev-parse HEAD)" = "${HEAD_SEED}" ] || fail "il clone non è al commit del seed"
  [ -f "${c}/seed.txt" ] || fail "seed.txt manca nel clone"
  [ ! -e "${c}/dopo-backup.txt" ] || fail "dopo-backup.txt presente: il restore non ha tolto il commit successivo"
  if [ "${push}" = 1 ]; then
    # Nome e contenuto unici per chiamata: nello scenario upgrade verify-seed --push
    # gira due volte in processi diversi (SECONDS riparte da 0) e il backup
    # contiene già il file del primo giro: un file identico dà "nothing to commit".
    local uniq
    uniq="$(date +%s%N)-$$-$(mktemp -u XXXXXX)"
    printf 'dopo il verify %s\n' "${uniq}" > "${c}/verify-${uniq}.txt"
    cigit -C "${c}" add -A
    cigit -C "${c}" commit -q -m 'verify: push dopo il ripristino'
    cigit -C "${c}" push -q origin main
    # il nuovo HEAD diventa quello di riferimento per le verifiche successive;
    # i dati di `mutate` si azzerano
    grep -vE '^(HEAD_SEED|HEAD_MUTATED|ISSUE_MUTATED)=' "${STATE_DIR}/seed.env" > "${STATE_DIR}/seed.env.new"
    printf 'HEAD_SEED=%s\n' "$(cigit -C "${c}" rev-parse HEAD)" >> "${STATE_DIR}/seed.env.new"
    mv "${STATE_DIR}/seed.env.new" "${STATE_DIR}/seed.env"
  fi
  log "verifica: dati del seed intatti, dati successivi tolti, clone e push funzionano"
}

# --- k3s -----------------------------------------------------------------

verify_k3s() {
  local want="${1:?versione attesa}" got status
  got="$(k3s --version | head -n1 | grep -o 'v[0-9][^ ]*')"
  [ "${got}" = "${want}" ] || fail "k3s --version: ${got}, atteso ${want}"
  got="$("${KUBECTL[@]}" version -o json | jq -r .serverVersion.gitVersion)"
  [ "${got}" = "${want}" ] || fail "kubectl version (server): ${got}, atteso ${want}"
  status="$(sudo gitstack status --json)"
  got="$(printf '%s' "${status}" | jq -r .report.k3s_version)"
  [ "${got}" = "${want}" ] || fail "gitstack status --json k3s_version: ${got}, atteso ${want}"
  log "k3s ${want}: kubectl version e gitstack status --json concordano"
}

latest_backup() {
  local f
  f="$(sudo find /var/backups/gitstack -maxdepth 1 -type f -name 'gitstack-backup-*' -not -name '*.sha256' -printf '%T@ %p\n' | sort -rn | head -n1 | cut -d' ' -f2-)"
  [ -n "${f}" ] || fail "nessun archivio in /var/backups/gitstack"
  printf '%s\n' "${f}"
}

diagnostics() {
  local d="${1:?cartella}"
  mkdir -p "${d}"
  {
    "${KUBECTL[@]}" get all -A -o wide
    "${KUBECTL[@]}" get events -A --sort-by=.lastTimestamp
    "${KUBECTL[@]}" describe pods -A
  } > "${d}/kubectl.txt" 2>&1 || true
  { sudo journalctl -u k3s --no-pager -n 400; } > "${d}/k3s.log" 2>&1 || true
  { sudo gitstack status; } > "${d}/gitstack-status.txt" 2>&1 || true
  { sudo ls -la /var/backups/gitstack; } > "${d}/backups.txt" 2>&1 || true
  df -h > "${d}/df.txt" 2>&1 || true
  free -m > "${d}/free.txt" 2>&1 || true
  local p
  for p in gateway identity core git web; do
    "${KUBECTL[@]}" -n default logs "deployment/gitstack-${p}" --all-containers --tail=300 > "${d}/log-${p}.txt" 2>&1 || true
  done
  return 0
}

main() {
  local cmd="${1:-}"
  [ -n "${cmd}" ] || fail "uso: install-e2e.sh <sottocomando> (vedi l'intestazione)"
  shift
  case "${cmd}" in
    previous-commit) previous_commit ;;
    older-k3s) older_k3s ;;
    wait-healthy) wait_healthy "$@" ;;
    seed) seed ;;
    mutate) mutate ;;
    verify-seed) verify_seed "$@" ;;
    verify-after-mutate) verify_after_mutate ;;
    verify-k3s) verify_k3s "$@" ;;
    latest-backup) latest_backup ;;
    diagnostics) diagnostics "$@" ;;
    *) fail "sottocomando sconosciuto: ${cmd}" ;;
  esac
}

main "$@"
