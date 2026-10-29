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
#   ci-config [posti]          La CI è accesa: core con GITSTACK_CORE_CI_ENABLED, namespace
#                              dei job, posti (se indicati) e PriorityClass.
#   ci-off                     Dopo un rollback: core senza CI.
#   ci-run                     Repo con un workflow di prova, push, run concluso con
#                              successo in un pod usa e getta e marcatore nel log.
#   verify-ci                  Dopo restore o upgrade: run e log del job ancora presenti.
#   verify-apps-domain <dominio>|unset [install.log warn|nowarn]
#                              Dominio delle app (H2, GIT-346): con un dominio, config.yaml
#                              e `gitstack status` lo riportano, il certificato del server ha
#                              il SAN *.<dominio>, si verifica con la CA interna per
#                              <app>-<repo>-<owner>.<dominio> e Traefik lo serve sulla porta 443
#                              (SNI <app>-<repo>-<owner>.<dominio>). Con install.log e warn|nowarn controlla
#                              che l'avviso del dominio predefinito ci sia (warn) o manchi (nowarn). Con `unset` (v2 aggiornata): nessun apps_domain
#                              in config.yaml e status lo dice. Una riga ok/ko per controllo.
#   verify-apps-hosting on|off Core con/senza GITSTACK_CORE_APPS_ENABLED, `gitstack status`, config.yaml e RBAC
#                              (auth can-i del ServiceAccount di core). Una riga ok/ko per controllo.
#   apps-off-api               Hosting spento: POST app = 503 apps_unavailable, nessun namespace, nessuna app.
#   apps-run <dominio>         Hosting acceso: un'app `login` creata da API passa dal forward-auth VERO del gateway:
#                              senza credenziali redirect/401, con un token API valido (read:resource e read sul repo)
#                              200 dall'app, con un token falso mai 200.
#   apps-public <dominio>      App `public` (GIT-457): senza login 200 e intestazioni X-GitStack-* del client tolte;
#                              public → login e interruttore d'istanza spento riportano la rotta interna;
#                              nessun Secret TLS nel namespace dell'app (ADR 0002).
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
      log "pod in terminazione: $(printf '%s' "${terminating}" | tr '[:space:]' ' ')"
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

# --- CI integrata (GIT-285) ----------------------------------------------

CI_JOBS_NS=gitstack-ci-jobs
CI_MARKER=CI-INSTALL-PROVA-OK

# ci-config: la CI è accesa nel cluster (core la usa, namespace dei job, posti).
# Una riga ok per controllo; esce al primo controllo che manca.
ci_config() {
  local want="${1:-}" env cap
  env="$("${KUBECTL[@]}" -n default get deployment gitstack-core -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="GITSTACK_CORE_CI_ENABLED")].value}')"
  [ "${env}" = true ] || fail "core non ha GITSTACK_CORE_CI_ENABLED=true (trovato '${env}'): la CI non è accesa"
  "${KUBECTL[@]}" get namespace "${CI_JOBS_NS}" >/dev/null || fail "manca il namespace dei job ${CI_JOBS_NS}"
  cap="$("${KUBECTL[@]}" -n "${CI_JOBS_NS}" get resourcequota ci-jobs -o jsonpath='{.spec.hard.pods}')"
  [ -z "${want}" ] || [ "${cap}" = "${want}" ] || fail "posti dei job: ${cap}, attesi ${want}"
  "${KUBECTL[@]}" get priorityclass gitstack-ci-job >/dev/null || fail "manca la PriorityClass gitstack-ci-job"
  log "CI accesa: core con CI, namespace ${CI_JOBS_NS}, ${cap} posti, PriorityClass gitstack-ci-job"
}

# ci-off: dopo il rollback la CI non c'è (revisione di partenza, senza CI).
ci_off() {
  local env
  env="$("${KUBECTL[@]}" -n default get deployment gitstack-core -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="GITSTACK_CORE_CI_ENABLED")].value}')"
  [ -z "${env}" ] || fail "dopo il rollback core ha ancora GITSTACK_CORE_CI_ENABLED='${env}'"
  log "CI spenta come prima dell'aggiornamento fallito"
}

# ci-run: un repo con un workflow di prova, un push, il run concluso con
# successo in un pod usa e getta e il marcatore nel log del job.
ci_run() {
  need E2E_HOST
  git_env
  load_seed
  local repo w sha deadline run="" concl jobid
  repo="ci-$(date +%H%M%S)"
  api POST /api/v1/repos "$(jq -n --arg o "${CI_USER}" --arg n "${repo}" '{owner:$o,name:$n,visibility:"private"}')" -H "Authorization: Bearer ${CI_TOKEN}"
  expect 201 "${CODE}" "creazione del repo del workflow di prova"
  w="${STATE_DIR}/w-ci"
  rm -rf "${w}"
  git init -q -b main "${w}"
  mkdir -p "${w}/.gitstack/workflows"
  cat > "${w}/.gitstack/workflows/prova.yml" <<'EOF'
name: prova
on: [push]
jobs:
  prova:
    runs-on: ubuntu-latest
    steps:
      - run: echo "CI-INSTALL-PROVA-OK"
EOF
  cigit -C "${w}" add -A
  cigit -C "${w}" commit -q -m 'ci: workflow di prova'
  cigit -C "${w}" push -q "https://${CI_USER}:${CI_TOKEN}@${HOST}/${CI_USER}/${repo}.git" main
  sha="$(cigit -C "${w}" rev-parse HEAD)"
  deadline=$((SECONDS + 600))
  while [ "${SECONDS}" -lt "${deadline}" ]; do
    api GET "/api/v1/repos/${CI_USER}/${repo}/actions/runs" '' -H "Authorization: Bearer ${CI_TOKEN}"
    if [ "${CODE}" = 200 ]; then
      run="$(printf '%s' "${BODY}" | jq -c --arg s "${sha}" '[.items[]? | select(.sha == $s)][0] // empty')"
      if [ -n "${run}" ] && [ "$(printf '%s' "${run}" | jq -r .status)" = completed ]; then break; fi
    fi
    run=""
    sleep 5
  done
  [ -n "${run}" ] || fail "nessun run concluso per ${sha:0:8} entro 600s (CI accesa ma il job non gira)"
  concl="$(printf '%s' "${run}" | jq -r '.conclusion // "-"')"
  [ "${concl}" = success ] || fail "run ${sha:0:8}: conclusione ${concl}, attesa success"
  jobid="$(printf '%s' "${run}" | jq -r '.jobs[0].id')"
  api GET "/api/v1/repos/${CI_USER}/${repo}/actions/jobs/${jobid}/logs" '' -H "Authorization: Bearer ${CI_TOKEN}"
  expect 200 "${CODE}" "log del job"
  printf '%s' "${BODY}" | grep -q "${CI_MARKER}" || fail "il log del job non contiene ${CI_MARKER}"
  grep -vE '^(CI_REPO_RUN|CI_RUN_SHA|CI_JOB_ID)=' "${STATE_DIR}/seed.env" > "${STATE_DIR}/seed.env.new" || true
  {
    printf 'CI_REPO_RUN=%s\nCI_RUN_SHA=%s\nCI_JOB_ID=%s\n' "${repo}" "${sha}" "${jobid}"
  } >> "${STATE_DIR}/seed.env.new"
  mv "${STATE_DIR}/seed.env.new" "${STATE_DIR}/seed.env"
  log "ci-run: run ${sha:0:8} success nel repo ${repo}, marcatore nel log"
}

# verify-ci: dopo restore o upgrade il run e il suo log (nel backup, CI5) ci sono ancora.
verify_ci() {
  need E2E_HOST
  git_env
  load_seed
  [ -n "${CI_REPO_RUN:-}" ] || fail "ci-run non eseguito (manca CI_REPO_RUN in seed.env)"
  local run
  api GET "/api/v1/repos/${CI_USER}/${CI_REPO_RUN}/actions/runs" '' -H "Authorization: Bearer ${CI_TOKEN}"
  expect 200 "${CODE}" "elenco dei run dopo il ripristino"
  run="$(printf '%s' "${BODY}" | jq -c --arg s "${CI_RUN_SHA}" '[.items[]? | select(.sha == $s)][0] // empty')"
  [ -n "${run}" ] || fail "il run ${CI_RUN_SHA:0:8} non c'è più"
  [ "$(printf '%s' "${run}" | jq -r '.conclusion // "-"')" = success ] || fail "il run ${CI_RUN_SHA:0:8} non è più success"
  api GET "/api/v1/repos/${CI_USER}/${CI_REPO_RUN}/actions/jobs/${CI_JOB_ID}/logs" '' -H "Authorization: Bearer ${CI_TOKEN}"
  expect 200 "${CODE}" "log del job dopo il ripristino"
  printf '%s' "${BODY}" | grep -q "${CI_MARKER}" || fail "il log del job ripristinato non contiene ${CI_MARKER}"
  log "verify-ci: run e log del job presenti dopo il ripristino"
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

# Una riga per controllo: ok/ko; l'esito finale è il codice di uscita.
APPS_KO=0
apps_check() { # <descrizione> <comando...>
  local d="$1"
  shift
  if "$@"; then printf 'ok  %s
' "${d}"; else printf 'ko  %s
' "${d}"; APPS_KO=$((APPS_KO + 1)); fi
}

verify_apps_domain() {
  local want="${1:-}" log="${2:-}" warn="${3:-}"
  [ -n "${want}" ] || fail "uso: verify-apps-domain <dominio>|unset [install.log]"
  local cfg=/etc/gitstack/config.yaml tls=/etc/gitstack/tls
  if [ "${want}" = "unset" ]; then
    apps_check "config.yaml senza apps_domain (installazione v2 aggiornata)" bash -c "! sudo grep -q '^apps_domain:' ${cfg}"
    apps_check "gitstack status: dominio delle app non impostato" bash -c "sudo gitstack status | grep -q 'Dominio delle app: non impostato'"
    [ "${APPS_KO}" -eq 0 ] || fail "verify-apps-domain unset: ${APPS_KO} controlli ko"
    return 0
  fi
  local app="web-sito-acme.${want}"
  apps_check "config.yaml: apps_domain: ${want}" bash -c "sudo grep -qx 'apps_domain: ${want}' ${cfg}"
  apps_check "gitstack status: Dominio delle app: ${want}" bash -c "sudo gitstack status | grep -q 'Dominio delle app: ${want}'"
  apps_check "server.crt: SAN DNS:*.${want}" bash -c "sudo openssl x509 -in ${tls}/server.crt -noout -ext subjectAltName | grep -q 'DNS:\*\.${want}'"
  apps_check "server.crt verificato con la CA interna per ${app}" sudo openssl verify -CAfile "${tls}/ca.crt" -verify_hostname "${app}" "${tls}/server.crt"
  apps_check "un livello solo: x.y.${want} non coperto" bash -c "! sudo openssl verify -CAfile ${tls}/ca.crt -verify_hostname x.y.${want} ${tls}/server.crt >/dev/null 2>&1"
  # Quello che serve Traefik, con la CA scaricata dall'istanza (fetch_ca la tiene in STATE_DIR).
  [ -f "${STATE_DIR}/ca.crt" ] || fetch_ca
  apps_check "porta 443 (SNI ${app}): certificato valido per la CA e per il nome" bash -c "openssl s_client -connect '${HOST}:443' -servername '${app}' -CAfile '${STATE_DIR}/ca.crt' -verify_hostname '${app}' -verify_return_error </dev/null 2>&1 | grep -Eq 'Verification: OK|Verify return code: 0 \(ok\)'"
  case "${warn}" in
    warn) apps_check "install.log: avviso del dominio predefinito presente" grep -q -- '--apps-domain non indicato' "${log}" ;;
    nowarn) apps_check "install.log: nessun avviso, il dominio era esplicito" bash -c "! grep -q -- '--apps-domain non indicato' '${log}'" ;;
    '') ;;
    *) fail "verify-apps-domain: terzo argomento warn|nowarn" ;;
  esac
  [ "${APPS_KO}" -eq 0 ] || fail "verify-apps-domain ${want}: ${APPS_KO} controlli ko"
  printf 'ok  verify-apps-domain %s
' "${want}"
}

# --- Hosting delle app (GIT-375, ADR 0002) -------------------------------------

core_env() { # <NOME> -> valore dell'env di core (vuoto se manca)
  "${KUBECTL[@]}" -n default get deployment gitstack-core -o jsonpath="{.spec.template.spec.containers[0].env[?(@.name==\"$1\")].value}"
}
gateway_env_name() { # <NOME> -> NOME se il gateway ha quella variabile (anche da Secret), vuoto se manca
  "${KUBECTL[@]}" -n default get deployment gitstack-gateway -o jsonpath="{.spec.template.spec.containers[0].env[?(@.name==\"$1\")].name}"
}

can_i() { # <serviceaccount> <verbo> <risorsa> [opzioni...]: yes|no
  local sa="$1"
  shift
  "${KUBECTL[@]}" auth can-i "$@" --as "${sa}" 2>/dev/null || true
}

no_workload_binding() {
  ! "${KUBECTL[@]}" get clusterrolebinding -o jsonpath='{range .items[*]}{.roleRef.name}{"
"}{end}' | grep -qx gitstack-app-workload
}

# Con l'hosting spento devono mancare TUTTI i ClusterRole delle app: uno per uno (con
# `get A B` basta che ne manchi uno perché il comando fallisca).
no_apps_clusterroles() {
  local r
  for r in gitstack-apps-controller gitstack-app-workload; do
    if "${KUBECTL[@]}" get clusterrole "${r}" >/dev/null 2>&1; then
      printf 'ClusterRole %s presente
' "${r}" >&2
      return 1
    fi
  done
  return 0
}

# Prove negative con impersonazione: la ValidatingAdmissionPolicy deve rifiutare, al ServiceAccount
# di core, ciò che il ClusterRole concede su tutto il cluster ma l'ADR 0002 non ammette.
denied() { # <descrizione> <comando...>: ok se il comando FALLISCE (rifiutato)
  local d="$1" out
  shift
  if out="$("$@" 2>&1)"; then
    printf 'KO  %s: il comando è riuscito
' "${d}" >&2
    return 1
  fi
  case "${out}" in
    *gitstack-apps-controller-scope*|*"core può"*|*"il RoleBinding delle app"*) return 0 ;;
    *) printf 'KO  %s: rifiutato ma non dalla policy: %s
' "${d}" "${out}" >&2; return 1 ;;
  esac
}
deny_rolebinding_kube_system() { # <sa>
  denied "rolebinding in kube-system" "${KUBECTL[@]}" create rolebinding x -n kube-system     --clusterrole=gitstack-app-workload "--serviceaccount=default:${1#system:serviceaccount:default:}" "--as=$1"
}
# Il controller aggiorna i namespace con un UPDATE (PUT), non con patch: la prova fa lo stesso,
# con `replace` di un namespace letto da admin e modificato con jq.
ns_update_as() { # <sa> <namespace> <label> <valore>
  "${KUBECTL[@]}" get namespace "$2" -o json | jq --arg k "$3" --arg v "$4" '.metadata.labels[$k] = $v' |
    "${KUBECTL[@]}" replace -f - "--as=$1"
}
deny_namespace_label() { # <sa>
  denied "label su un namespace non gitstack-app-*" ns_update_as "$1" kube-system pod-security.kubernetes.io/enforce privileged
}
deny_namespace_managed_label() { # <sa>
  denied "managed-app=true su un namespace di sistema" ns_update_as "$1" kube-system gitstack.io/managed-app true
}
# I namespace di sistema hanno già la loro protezione: si prova su uno creato apposta.
deny_namespace_delete() { # <sa>
  local rc=0
  "${KUBECTL[@]}" create namespace e2e-not-an-app >/dev/null 2>&1 || true
  denied "delete di un namespace non gitstack-app-*" "${KUBECTL[@]}" delete namespace e2e-not-an-app --dry-run=server "--as=$1" || rc=1
  "${KUBECTL[@]}" delete namespace e2e-not-an-app --wait=false >/dev/null 2>&1 || true
  return "${rc}"
}

# verify-apps-hosting on|off: una riga ok/ko per controllo; l'esito è il codice di uscita.
verify_apps_hosting() {
  local want="${1:-}" sa
  APPS_KO=0
  case "${want}" in on | off) ;; *) fail "uso: verify-apps-hosting on|off" ;; esac
  sa="system:serviceaccount:default:$("${KUBECTL[@]}" -n default get deployment gitstack-core -o jsonpath='{.spec.template.spec.serviceAccountName}')"
  if [ "${want}" = on ]; then
    apps_check "core: GITSTACK_CORE_APPS_ENABLED=true" test "$(core_env GITSTACK_CORE_APPS_ENABLED)" = true
    apps_check "core: GITSTACK_CORE_APPS_DOMAIN impostato" test -n "$(core_env GITSTACK_CORE_APPS_DOMAIN)"
    apps_check "core: GITSTACK_CORE_APPS_AUTH_URL verso il gateway" test "$(core_env GITSTACK_CORE_APPS_AUTH_URL | grep -c '/_appauth/verify$')" = 1
    apps_check "gateway: chiavi di firma del login (GITSTACK_GATEWAY_APPAUTH_SIGNING_KEYS)" test -n "$(gateway_env_name GITSTACK_GATEWAY_APPAUTH_SIGNING_KEYS)"
    apps_check "gateway: dominio delle app (GITSTACK_GATEWAY_APPS_DOMAIN)" test -n "$(gateway_env_name GITSTACK_GATEWAY_APPS_DOMAIN)"
    apps_check "gateway: indirizzo pubblico (GITSTACK_GATEWAY_PUBLIC_URL)" test -n "$(gateway_env_name GITSTACK_GATEWAY_PUBLIC_URL)"
    apps_check "gitstack status: Hosting delle app: acceso" bash -c "sudo gitstack status | grep -q 'Hosting delle app: acceso'"
    apps_check "config.yaml: apps_enabled: true" bash -c "sudo grep -qx 'apps_enabled: true' /etc/gitstack/config.yaml"
    apps_check "RBAC: il ServiceAccount di core crea namespace" test "$(can_i "${sa}" create namespaces)" = yes
    apps_check "RBAC: ... ma non legge i Secret del cluster" test "$(can_i "${sa}" get secrets -n kube-system)" = no
    apps_check "RBAC: ... né crea Deployment fuori dalle app" test "$(can_i "${sa}" create deployments -n kube-system)" = no
    apps_check "RBAC: ... né crea ClusterRoleBinding" test "$(can_i "${sa}" create clusterrolebindings)" = no
    apps_check "RBAC: nessun ClusterRoleBinding verso il ruolo modello" no_workload_binding
    apps_check "policy: la ValidatingAdmissionPolicy del controller esiste" "${KUBECTL[@]}" get validatingadmissionpolicybinding gitstack-apps-controller-scope
    apps_check "policy: core non lega il ruolo modello in kube-system" deny_rolebinding_kube_system "${sa}"
    apps_check "policy: core non cambia le label di kube-system" deny_namespace_label "${sa}"
    apps_check "policy: core non marca kube-system managed-app" deny_namespace_managed_label "${sa}"
    apps_check "policy: core non elimina namespace non suoi" deny_namespace_delete "${sa}"
  else
    apps_check "core: senza GITSTACK_CORE_APPS_ENABLED" test -z "$(core_env GITSTACK_CORE_APPS_ENABLED)"
    apps_check "gitstack status: Hosting delle app: spento" bash -c "sudo gitstack status | grep -q 'Hosting delle app: spento'"
    apps_check "RBAC: nessun ClusterRole delle app" no_apps_clusterroles
  fi
  [ "${APPS_KO}" -eq 0 ] || fail "verify-apps-hosting ${want}: ${APPS_KO} controlli ko"
  printf 'ok  verify-apps-hosting %s\n' "${want}"
}

# apps-off-api: con l'hosting spento l'API risponde 503 apps_unavailable e nel cluster non nasce nulla.
apps_off_api() {
  need E2E_HOST
  load_seed
  local before after
  before="$("${KUBECTL[@]}" get namespaces -l gitstack.io/managed-app=true -o name | wc -l)"
  api POST "/api/v1/repos/${CI_USER}/${CI_REPO}/apps" '{"name":"web","image":"docker.io/library/nginx:1.27-alpine","port":80}' -H "Authorization: Bearer ${CI_TOKEN}"
  expect 503 "${CODE}" "creazione app con l'hosting spento"
  printf '%s' "${BODY}" | jq -e '.code == "apps_unavailable"' >/dev/null || fail "503 senza il codice apps_unavailable"
  sleep 5
  after="$("${KUBECTL[@]}" get namespaces -l gitstack.io/managed-app=true -o name | wc -l)"
  [ "${before}" = "${after}" ] || fail "con l'hosting spento sono comparsi namespace delle app (${before} -> ${after})"
  api GET "/api/v1/repos/${CI_USER}/${CI_REPO}/apps" '' -H "Authorization: Bearer ${CI_TOKEN}"
  expect 200 "${CODE}" "elenco app"
  printf '%s' "${BODY}" | jq -e '.items | length == 0' >/dev/null || fail "con l'hosting spento è stata creata un'app nel modello"
  printf 'ok  apps-off-api: 503 apps_unavailable, nessun namespace, nessuna app nel modello\n'
}

# app_namespace <host> <secondi>: stampa il namespace dell'IngressRoute il cui match contiene l'host,
# aspettandolo fino alla scadenza; esce 1 (senza stampare) se non compare. Nessuna lettura di
# items[0] su una lista che può essere vuota: il filtro jq dà una stringa vuota.
app_namespace() {
  local h="${1:?host}" limit="${2:?secondi}" end n=""
  end=$((SECONDS + limit))
  while :; do
    n="$("${KUBECTL[@]}" get ingressroute -A -o json 2>/dev/null | jq -r --arg h "${h}" '[.items[]? | select((.spec.routes[0].match // "") | contains($h)) | .metadata.namespace][0] // empty' 2>/dev/null || true)"
    if [ -n "${n}" ]; then
      printf '%s' "${n}"
      return 0
    fi
    [ "${SECONDS}" -lt "${end}" ] || return 1
    sleep 3
  done
}

# apps_diagnostics <namespace>: in caso di errore di apps-run raccoglie lo stato dell'app, i log del
# container (anche --previous, se è ripartito) e le righe appauth del gateway. Mai fatale.
apps_diagnostics() {
  local ns="${1:-default}" p
  "${KUBECTL[@]}" -n "${ns}" get all,ingressroute,middleware -o wide || true
  "${KUBECTL[@]}" -n "${ns}" describe pods | tail -60 || true
  printf '== NetworkPolicy di %s ==\n' "${ns}" >&2
  "${KUBECTL[@]}" -n "${ns}" get networkpolicy -o yaml || true
  local pods
  pods="$("${KUBECTL[@]}" -n "${ns}" get pods -o name 2>/dev/null || true)"
  while IFS= read -r p; do
    [ -n "${p}" ] || continue
    printf '== log di %s (corrente) ==
' "${p}" >&2
    "${KUBECTL[@]}" -n "${ns}" logs "${p}" --all-containers --tail=60 || true
    printf '== log di %s (--previous) ==
' "${p}" >&2
    "${KUBECTL[@]}" -n "${ns}" logs "${p}" --all-containers --previous --tail=60 || true
  done <<<"${pods}"
  printf '== gateway: righe appauth ==
' >&2
  "${KUBECTL[@]}" -n default logs deployment/gitstack-gateway --tail=500 | grep -i 'appauth' | tail -40 || true
  printf '== gateway: ultime righe ==
' >&2
  "${KUBECTL[@]}" -n default logs deployment/gitstack-gateway --tail=20 || true
}

# apps_report <host> <file intestazioni> <corpo> <namespace> <messaggio> (GIT-483): in caso di errore stampa
# la risposta ricevuta (intestazioni e primi byte del corpo), lo stato dell'app e le righe appauth del
# gateway, poi esce. Una risposta 200 che non è dell'app (p. es. la web UI di GitStack, servita
# dall'Ingress senza host finché Traefik non ha caricato la rotta dell'app) si riconosce da qui.
apps_report() {
  local host="${1}" hdr="${2}" rbody="${3}" ns="${4}" msg="${5}"
  printf 'Risposta ricevuta da %s:\n' "${host}" >&2
  if [ -s "${hdr}" ]; then head -n 30 "${hdr}" >&2 || true; fi
  printf '%s\n' "${rbody}" | head -c 1500 >&2 || true
  printf '\n' >&2
  apps_diagnostics "${ns:-default}"
  fail "${msg}"
}

# apps-run <dominio>: un'app di prova creata da API risponde al suo indirizzo automatico (HTTPS con la
# CA interna, risoluzione forzata verso l'host: il DNS wildcard non c'è sul runner).
apps_run() {
  need E2E_HOST
  load_seed
  local domain="${1:?dominio delle app}" host deadline code="" ns loc body
  local resolve="${HOST}"
  api POST "/api/v1/repos/${CI_USER}/${CI_REPO}/apps" '{"name":"web","image":"docker.io/nginxinc/nginx-unprivileged:1.27-alpine","port":8080,"healthCheck":{"path":"/","intervalSeconds":5}}' -H "Authorization: Bearer ${CI_TOKEN}"
  expect 202 "${CODE}" "creazione app"
  host="$(printf '%s' "${BODY}" | jq -r '.app.address // empty' | sed -E 's#^https://##')"
  [ -n "${host}" ] || fail "l'app creata non ha un indirizzo automatico: $(printf '%s' "${BODY}" | head -c 300)"
  case "${host}" in *".${domain}") ;; *) fail "indirizzo ${host} fuori dal dominio ${domain}" ;; esac
  # Il controller crea il namespace in modo asincrono dopo il 202: lo si aspetta (dall'IngressRoute
  # dell'app, come apps_public), con un errore chiaro se non arriva.
  ns="$(app_namespace "${host}" 180)" || {
    "${KUBECTL[@]}" get namespaces --show-labels || true
    "${KUBECTL[@]}" get ingressroute -A -o wide || true
    fail "apps-run: il namespace dell'app non è comparso entro 180s (IngressRoute con ${host})"
  }
  # Con un token API valido (accesso `login`: read sul repo) il forward-auth del gateway ammette e
  # l'app risponde. Il DNS wildcard non c'è sul runner: la risoluzione è forzata verso l'host.
  # GIT-483: un 200 da solo non basta. Finché Traefik non ha caricato la rotta dell'app (o il pod non è
  # pronto) il nome dell'app cade nell'Ingress senza host della web UI di GitStack, che risponde 200 a
  # chiunque: si aspetta la pagina vera dell'app (nginx di prova), non solo il codice.
  deadline=$((SECONDS + 420))
  code=""
  body=""
  while [ "${SECONDS}" -lt "${deadline}" ]; do
    code="$(curl -sS -m 10 --cacert "${STATE_DIR}/ca.crt" --resolve "${host}:443:${resolve}" -H "Authorization: Bearer ${CI_TOKEN}" -o "${STATE_DIR}/apps-auth.body" -D "${STATE_DIR}/apps-auth.hdr" -w '%{http_code}' "https://${host}/" 2>/dev/null || true)"
    body="$(head -c 4000 "${STATE_DIR}/apps-auth.body" 2>/dev/null || true)"
    if [ "${code}" = 200 ] && printf '%s' "${body}" | grep -q 'Welcome to nginx'; then break; fi
    code="${code:-000}"
    sleep 5
  done
  if [ "${code}" != 200 ] || ! printf '%s' "${body}" | grep -q 'Welcome to nginx'; then
    apps_report "${host}" "${STATE_DIR}/apps-auth.hdr" "${body}" "${ns}" "l'app non risponde con la sua pagina e un token valido su https://${host}/ (ultimo codice '${code}')"
  fi
  printf 'ok  apps-run: con un token valido https://%s/ risponde 200 con la pagina dell'"'"'app (namespace %s)\n' "${host}" "${ns}"
  # Senza credenziali: redirect al login (browser) o 401 (client API), mai 200.
  code="$(curl -sS -m 10 --cacert "${STATE_DIR}/ca.crt" --resolve "${host}:443:${resolve}" -o "${STATE_DIR}/apps-noauth.body" -D "${STATE_DIR}/apps-noauth.hdr" -w '%{http_code}' "https://${host}/" 2>/dev/null || true)"
  loc="$(grep -i '^location:' "${STATE_DIR}/apps-noauth.hdr" | head -n1 | tr -d '\r' || true)"
  case "${code}" in
    301 | 302 | 303 | 307 | 308 | 401) printf 'ok  apps-run: senza credenziali %s %s\n' "${code}" "${loc}" ;;
    *)
      body="$(head -c 4000 "${STATE_DIR}/apps-noauth.body" 2>/dev/null || true)"
      apps_report "${host}" "${STATE_DIR}/apps-noauth.hdr" "${body}" "${ns}" "senza credenziali l'app doveva dare un redirect al login o 401, trovato '${code}'"
      ;;
  esac
  # Un token falso non passa.
  code="$(curl -sS -m 10 --cacert "${STATE_DIR}/ca.crt" --resolve "${host}:443:${resolve}" -H "Authorization: Bearer gst_falso" -o /dev/null -w '%{http_code}' "https://${host}/" 2>/dev/null || true)"
  if [ "${code}" = 200 ]; then fail "un token falso ha passato il forward-auth (200)"; fi
  printf 'ok  apps-run: con un token falso %s (non 200)\n' "${code}"
}

# apps-public <dominio> (GIT-457, H1/H6/ADR 0002): un'app `public` è raggiungibile senza login, le
# intestazioni X-GitStack-* mandate dal client non arrivano all'app, public → login e l'interruttore
# d'istanza spento riportano la rotta interna, nessun Secret TLS nel namespace dell'app.
apps_public() {
  need E2E_HOST
  load_seed
  local domain="${1:?dominio delle app}" host ns="" deadline code="" body="" orig_on
  local resolve="${HOST}" jar="${STATE_DIR}/admin-public.jar" settings

  # Interruttore d'istanza: l'admin (cookie di sessione) legge i valori e li rimanda interi.
  login admin "${NEW_ADMIN_PASSWORD}" "${jar}"
  expect 200 "${CODE}" "login admin"
  api GET /api/v1/admin/apps/settings '' -b "${jar}"
  expect 200 "${CODE}" "lettura impostazioni delle app"
  settings="${BODY}"
  orig_on="$(printf '%s' "${settings}" | jq -r '.publicAppsEnabled')"
  set_public_switch() { # <true|false>
    api PUT /api/v1/admin/apps/settings "$(printf '%s' "${settings}" | jq --argjson on "$1" '{publicAppsEnabled:$on,signedImagesOnly,restricted,maxPerApp,logRetention}')" -b "${jar}"
    expect 200 "${CODE}" "interruttore delle app pubbliche = $1"
  }
  set_mode() { # <login|public>
    api PUT "/api/v1/repos/${CI_USER}/${CI_REPO}/apps/pub/access" "$(jq -n --arg m "$1" '{mode:$m}')" -H "Authorization: Bearer ${CI_TOKEN}"
    expect 200 "${CODE}" "accesso dell'app pub = $1"
  }
  # noauth <intestazione...>: codice HTTP senza credenziali; il corpo resta in $body.
  noauth() {
    local out h args=()
    for h in "$@"; do args+=(-H "${h}"); done
    out="$(mktemp)"
    code="$(curl -sS -m 10 --cacert "${STATE_DIR}/ca.crt" --resolve "${host}:443:${resolve}" "${args[@]}" -o "${out}" -D "${STATE_DIR}/apps-public.hdr" -w '%{http_code}' "https://${host}/" 2>/dev/null || true)"
    body="$(cat "${out}")"
    rm -f "${out}"
  }

  api POST "/api/v1/repos/${CI_USER}/${CI_REPO}/apps" '{"name":"pub","image":"docker.io/traefik/whoami:v1.10","port":80,"healthCheck":{"path":"/","intervalSeconds":5}}' -H "Authorization: Bearer ${CI_TOKEN}"
  expect 202 "${CODE}" "creazione app pub"
  host="$(printf '%s' "${BODY}" | jq -r '.app.address // empty' | sed -E 's#^https://##')"
  [ -n "${host}" ] || fail "l'app pub non ha un indirizzo automatico: $(printf '%s' "${BODY}" | head -c 300)"
  case "${host}" in *".${domain}") ;; *) fail "indirizzo ${host} fuori dal dominio ${domain}" ;; esac

  set_public_switch true
  set_mode public

  # a. senza credenziali, con intestazioni falsificate: 200 e nessuna arriva all'app.
  # GIT-483: come in apps-run, un 200 da solo non basta (l'Ingress senza host della web UI risponde 200
  # finché Traefik non ha caricato la rotta dell'app): si aspetta l'elenco di whoami (riga `Hostname:`).
  ns="$(app_namespace "${host}" 180 || true)"
  deadline=$((SECONDS + 420))
  while [ "${SECONDS}" -lt "${deadline}" ]; do
    noauth 'X-GitStack-User: intruso' 'X-GitStack-Signature: falsa' 'X-GitStack-Email: x@y'
    if [ "${code}" = 200 ] && printf '%s' "${body}" | grep -q '^Hostname:'; then break; fi
    sleep 5
  done
  if [ "${code}" != 200 ] || ! printf '%s' "${body}" | grep -q '^Hostname:'; then
    "${KUBECTL[@]}" get ingressroute,middleware -A -o wide || true
    "${KUBECTL[@]}" -n default logs deployment/gitstack-core --tail=40 || true
    apps_report "${host}" "${STATE_DIR}/apps-public.hdr" "${body}" "${ns}" "la risposta di ${host} senza login non è quella di whoami (codice '${code}')"
  fi
  if printf '%s' "${body}" | grep -qi '^X-Gitstack-User: intruso'; then fail "X-GitStack-User del client è arrivata all'app"; fi
  if printf '%s' "${body}" | grep -qi '^X-Gitstack-Signature: falsa'; then fail "X-GitStack-Signature del client è arrivata all'app"; fi
  if printf '%s' "${body}" | grep -qi '^X-Gitstack-Email: x@y'; then fail "X-GitStack-Email del client è arrivata all'app"; fi
  printf 'ok  apps-public: senza login 200, intestazioni X-GitStack-* del client tolte\n'

  ns="$(app_namespace "${host}" 120)" || fail "namespace dell'app pub non trovato (IngressRoute con ${host})"

  # d. public → login: la rotta interna torna (30x o 401 senza credenziali).
  set_mode login
  deadline=$((SECONDS + 180))
  while [ "${SECONDS}" -lt "${deadline}" ]; do
    noauth
    case "${code}" in 301 | 302 | 303 | 307 | 308 | 401) break ;; esac
    sleep 5
  done
  case "${code}" in
    301 | 302 | 303 | 307 | 308 | 401) ;;
    *) fail "dopo public → login senza credenziali atteso 30x o 401, trovato '${code}'" ;;
  esac
  printf 'ok  apps-public: public → login riporta la rotta interna\n'

  # e. interruttore spento con l'app ancora public: l'app non è più esposta.
  set_mode public
  deadline=$((SECONDS + 180))
  while [ "${SECONDS}" -lt "${deadline}" ]; do
    noauth
    [ "${code}" = 200 ] && break
    sleep 3
  done
  [ "${code}" = 200 ] || fail "l'app public non è tornata raggiungibile prima di spegnere l'interruttore ('${code}')"
  set_public_switch false
  deadline=$((SECONDS + 180))
  while [ "${SECONDS}" -lt "${deadline}" ]; do
    if "${KUBECTL[@]}" -n "${ns}" get ingressroute app >/dev/null 2>&1; then
      sleep 3
    else
      break
    fi
  done
  if "${KUBECTL[@]}" -n "${ns}" get ingressroute app >/dev/null 2>&1; then fail "interruttore spento: l'IngressRoute di un'app public esiste ancora"; fi
  noauth
  if [ "${code}" = 200 ]; then fail "interruttore spento: l'app risponde ancora 200 senza login"; fi
  printf 'ok  apps-public: interruttore spento, app non esposta\n'

  # f. ADR 0002: nessun Secret TLS nel namespace dell'app.
  if "${KUBECTL[@]}" get secrets -A --field-selector type=kubernetes.io/tls -o jsonpath='{range .items[*]}{.metadata.namespace}{"\n"}{end}' | grep -qx "${ns}"; then
    fail "Secret TLS nel namespace dell'app ${ns} (ADR 0002: i certificati restano nel namespace di Traefik)"
  fi
  printf 'ok  apps-public: nessun Secret TLS nel namespace dell'"'"'app (ADR 0002)\n'

  set_public_switch "${orig_on}"
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
  # GIT-479: i Deployment com'erano dopo un rollback (serviceAccount, ReplicaSet) e la storia di helm.
  { "${KUBECTL[@]}" -n default get deployments,replicasets,serviceaccounts -o yaml; } > "${d}/deployments.yaml" 2>&1 || true
  { sudo helm history gitstack -n default --kubeconfig /etc/rancher/k3s/k3s.yaml; } > "${d}/helm-history.txt" 2>&1 || true
  # GIT-482: ServiceAccount di ogni Deployment (campo e campo deprecato) e manifest della revisione in uso.
  { "${KUBECTL[@]}" -n default get deployments -o custom-columns='NOME:.metadata.name,SA:.spec.template.spec.serviceAccountName,SA_DEPRECATO:.spec.template.spec.serviceAccount,GEN:.metadata.generation'; sudo helm get manifest gitstack -n default --kubeconfig /etc/rancher/k3s/k3s.yaml; } > "${d}/deployments-sa.txt" 2>&1 || true
  { sudo journalctl -u k3s --no-pager -n 400; } > "${d}/k3s.log" 2>&1 || true
  { sudo gitstack status; } > "${d}/gitstack-status.txt" 2>&1 || true
  { sudo ls -la /var/backups/gitstack; } > "${d}/backups.txt" 2>&1 || true
  df -h > "${d}/df.txt" 2>&1 || true
  free -m > "${d}/free.txt" 2>&1 || true
  local p
  "${KUBECTL[@]}" -n "${CI_JOBS_NS}" get pods,resourcequota -o wide > "${d}/ci-jobs.txt" 2>&1 || true
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
    ci-config) ci_config "$@" ;;
    ci-off) ci_off ;;
    ci-run) ci_run ;;
    verify-ci) verify_ci ;;
    verify-apps-domain) verify_apps_domain "$@" ;;
    verify-apps-hosting) verify_apps_hosting "$@" ;;
    apps-off-api) apps_off_api ;;
    apps-run) apps_run "$@" ;;
    apps-public) apps_public "$@" ;;
    latest-backup) latest_backup ;;
    diagnostics) diagnostics "$@" ;;
    *) fail "sottocomando sconosciuto: ${cmd}" ;;
  esac
}

main "$@"
