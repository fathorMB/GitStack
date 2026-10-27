#!/usr/bin/env bash
# deploy/ci/runner-e2e.sh - prova end-to-end del runner integrato (GIT-317, ADR 0001 §6)
# su un cluster con il chart gia' installato (ci.enabled=true), nel job `chart` di ci.yml.
#
# Fa, nell'ordine (una riga ok/ko per controllo, niente elenchi lunghi):
#   1. un push su admin/ci-e2e con un workflow che usa `uses: actions/checkout`: il job gira
#      in un pod usa e getta, il run in core risulta completed/success;
#   2. lo stesso con il mirror delle action configurato nel chart (ci.runner.actionMirror):
#      un mirror finto in-cluster serve actions/checkout, e il log del job contiene il
#      marcatore del mirror, che nessuna action di github.com stampa;
#   2b. la cache dei job (actions/cache: salva nel primo run, ripristina nel secondo) e il 501
#      di upload-artifact@v4 (GIT-291);
#   3. una build rootless (BuildKit senza demone) nel namespace gitstack-ci-build e un pod
#      privilegiato nello stesso namespace, rifiutato dalla ValidatingAdmissionPolicy;
#   4. (GIT-371) un workflow con `runs-on: gitstack-build` che costruisce un'immagine con BuildKit
#      rootless e la pubblica nel registry del repo col token del job; poi, dal namespace di build,
#      le prove di rete (ci-gateway e registry si', core su altre porte, postgres, nats, identity,
#      git, API Kubernetes e IP privati no) e gli altri rifiuti della policy (hostPort, capability).
#
# Richiede kubectl, helm, git, curl, jq. La password di admin viene cambiata per la durata
# della prova e rimessa quella iniziale all'uscita (come lo smoke del repo con maiuscole).
# Variabili: RELEASE (gitstack), BASE_URL (http://localhost:8080), CHART_DIR (deploy/gitstack),
# RUNNER_IMAGE (gitstack-ci-runner:test), RUN_TIMEOUT (secondi, 420).
set -euo pipefail

RELEASE="${RELEASE:-gitstack}"
BASE_URL="${BASE_URL:-http://localhost:8080}"
CHART_DIR="${CHART_DIR:-deploy/gitstack}"
RUNNER_IMAGE="${RUNNER_IMAGE:-gitstack-ci-runner:test}"
RUN_TIMEOUT="${RUN_TIMEOUT:-420}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
MIRROR_NS=ci-mirror
BUILD_NS=gitstack-ci-build
OWNER="admin"
REPO="${E2E_REPO:-ci-e2e}"

work="$(mktemp -d)"
jar="${work}/jar"
api=""
initial_pw=""
tmp_pw=""
tok=""
fail=0

ok() { echo "ok: $*"; }
ko() { echo "KO: $*" >&2; fail=1; }

# Il pod di un job sparisce appena finisce: i suoi log si seguono finche' vive.
watch_pods() {
  local seen=" " p
  while true; do
    for p in $(kubectl -n gitstack-ci-jobs get pods -o name 2>/dev/null | sed 's#^pod/##'); do
      case "${seen}" in *" ${p} "*) continue ;; esac
      seen="${seen}${p} "
      (kubectl -n gitstack-ci-jobs logs -f "${p}" --all-containers --prefix >"${work}/pod-${p}.log" 2>&1 || true) &
    done
    sleep 1
  done
}

diagnostics() {
  echo "--- diagnostica ---" >&2
  for f in "${work}"/pod-*.log; do [ -e "${f}" ] && { echo "--- ${f##*/} ---" >&2; tail -n 60 "${f}" >&2; }; done
  kubectl -n gitstack-ci-jobs get pods -o wide >&2 || true
  kubectl logs deployment/"${RELEASE}-core" --tail=60 >&2 || true
  kubectl -n "${MIRROR_NS}" logs pod/action-mirror --tail=40 >&2 || true
}

change_password() { # corrente, nuova
  local code
  code=$(jq -n --arg c "$1" --arg n "$2" '{currentPassword: $c, newPassword: $n}' \
    | curl -s -o "${work}/pw.body" -w '%{http_code}' -b "${jar}" -c "${jar}" -X PUT \
      -H 'content-type: application/json' -d @- "${api}/users/${OWNER}/password" || true)
  [ "${code}" = 204 ] || { echo "cambio password: status ${code}, corpo '$(head -c 200 "${work}/pw.body")'" >&2; return 1; }
}

cleanup() {
  local rc=$?
  kill "${watcher}" 2>/dev/null || true
  [ -z "${bwatcher:-}" ] || kill "${bwatcher}" 2>/dev/null || true
  if [ -n "${tmp_pw}" ] && [ -n "${initial_pw}" ]; then
    change_password "${tmp_pw}" "${initial_pw}" || echo "attenzione: password iniziale non ripristinata" >&2
  fi
  if [ "${rc}" -ne 0 ] || [ "${fail}" -ne 0 ]; then diagnostics; fi
  rm -rf "${work}"
}
trap cleanup EXIT
watch_pods &
watcher=$!

prefix=$(sed -n "s/^export const API_BASE_URL = '\([^']*\)';.*$/\1/p" "${REPO_ROOT}/web/src/lib/http.ts")
[ -n "${prefix}" ] || { echo "API_BASE_URL non trovato in web/src/lib/http.ts" >&2; exit 1; }
api="${BASE_URL}${prefix}"

# --- sessione, token e repo -------------------------------------------------------------
initial_pw=$(kubectl get secret "${RELEASE}-identity-admin" -o jsonpath='{.data.password}' | base64 -d)
tmp_pw="e2e-$(head -c 48 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)"
code=$(jq -n --arg p "${initial_pw}" '{username: "admin", password: $p}' \
  | curl -s -o "${work}/login.body" -w '%{http_code}' -c "${jar}" -X POST -H 'content-type: application/json' -d @- "${api}/auth/login" || true)
[ "${code}" = 200 ] || { echo "login admin: status ${code}" >&2; exit 1; }
change_password "${initial_pw}" "${tmp_pw}"

exp=$(date -u -d '+2 days' +%Y-%m-%dT%H:%M:%SZ)
code=$(jq -n --arg e "${exp}" --arg n "e2e-${REPO}" '{name: $n, scopes: ["read:resource", "write:resource"], expiresAt: $e}' \
  | curl -s -o "${work}/token.body" -w '%{http_code}' -b "${jar}" -X POST -H 'content-type: application/json' -d @- "${api}/user/tokens" || true)
[ "${code}" = 201 ] || { echo "creazione token: status ${code}, corpo '$(head -c 200 "${work}/token.body")'" >&2; exit 1; }
tok=$(jq -r .token "${work}/token.body")
code=$(curl -s -o "${work}/repo.body" -w '%{http_code}' -H "Authorization: Bearer ${tok}" -X POST -H 'content-type: application/json' \
  -d "{\"owner\":\"${OWNER}\",\"name\":\"${REPO}\"}" "${api}/repos" || true)
[ "${code}" = 201 ] || { echo "creazione repo: status ${code}, corpo '$(head -c 200 "${work}/repo.body")'" >&2; exit 1; }
ok "repo ${OWNER}/${REPO} creato"

# --- repo git locale e push ---------------------------------------------------------------
src="${work}/src"
mkdir -p "${src}/.gitstack/workflows"
git -C "${src}" init -q -b main
git -C "${src}" config user.name e2e
git -C "${src}" config user.email e2e@example.invalid
auth="Authorization: Basic $(printf 'admin:%s' "${tok}" | base64 -w0)"
push() { # messaggio
  git -C "${src}" add -A
  git -C "${src}" commit -q -m "$1"
  git -C "${src}" -c credential.helper= -c "http.extraHeader=${auth}" push -q "${BASE_URL}/${OWNER}/${REPO}.git" HEAD:refs/heads/main
  git -C "${src}" rev-parse HEAD
}

wait_run() { # sha -> scrive in ${work}/run.json l'esecuzione conclusa
  local sha="$1" i st
  for ((i = 0; i < RUN_TIMEOUT / 3; i++)); do
    curl -s -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/actions/runs" >"${work}/runs.json" || true
    jq -e --arg s "${sha}" '[.items[]? | select(.sha == $s)] | length > 0' "${work}/runs.json" >/dev/null 2>&1 \
      && jq --arg s "${sha}" '[.items[] | select(.sha == $s)][0]' "${work}/runs.json" >"${work}/run.json"
    if [ -s "${work}/run.json" ]; then
      st=$(jq -r .status "${work}/run.json")
      [ "${st}" = completed ] || [ "${st}" = cancelled ] && return 0
    fi
    sleep 3
  done
  return 1
}

check_run() { # etichetta sha
  local label="$1" sha="$2" concl jobid
  rm -f "${work}/run.json"
  if ! wait_run "${sha}"; then ko "${label}: nessun run concluso per ${sha:0:8} entro ${RUN_TIMEOUT}s"; return 1; fi
  concl=$(jq -r '.status + "/" + (.conclusion // "-")' "${work}/run.json")
  jobid=$(jq -r '.jobs[0].id' "${work}/run.json")
  curl -s -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/actions/jobs/${jobid}/logs" >"${work}/job.log" || true
  if [ "${concl}" = completed/success ]; then ok "${label}: run ${concl}"; else
    ko "${label}: run ${concl}"; jq -c . "${work}/run.json" >&2; tail -n 40 "${work}/job.log" >&2; return 1; fi
}

# --- 1. action pubbliche da github.com -------------------------------------------------------
cat >"${src}/.gitstack/workflows/e2e.yml" <<'EOF'
name: e2e
on: [push]
jobs:
  pubbliche:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/github-script@v7
        with:
          script: core.info('E2E-GITHUB-SCRIPT')
      - run: echo "E2E-PASSO-DOPO-CHECKOUT"
EOF
echo "ciao" >"${src}/README.md"
sha1=$(push "primo workflow")
if check_run "action pubbliche" "${sha1}"; then
  if grep -q 'E2E-PASSO-DOPO-CHECKOUT' "${work}/job.log"; then ok "action pubbliche: il passo dopo uses: e' stato eseguito"; else ko "action pubbliche: passo finale assente dal log"; fi
  if grep -q 'E2E-GITHUB-SCRIPT' "${work}/job.log"; then ok "action pubbliche: actions/github-script scaricata da github.com ed eseguita"; else ko "action pubbliche: l'output di actions/github-script manca dal log"; fi
  if grep -q 'MIRROR-FINTO' "${work}/job.log"; then ko "action pubbliche: il marcatore del mirror compare senza mirror"; else ok "action pubbliche: nessun marcatore del mirror"; fi
fi

# --- 1b. cache dei job e artefatti v4 (GIT-330, GIT-291) --------------------------------------
# actions/cache salva nel primo run («Cache saved with key») e ripristina nel secondo («Cache
# restored from key»); upload-artifact@v4 riceve il 501 col messaggio documentato. I run partono
# con workflow_dispatch: quelli per push di e2e.yml non si confondono con questi. I marcatori E2E-CACHE-*
# si cercano dopo «| »: act ristampa anche il testo dello script, che li contiene entrambi.
cat >"${src}/.gitstack/workflows/cache.yml" <<'EOF'
name: cache
on: workflow_dispatch
jobs:
  cache:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/cache@v4
        with:
          path: e2e-cache
          key: e2e-cache-v1
      - run: |
          mkdir -p e2e-cache
          if [ -f e2e-cache/dato ]; then echo "E2E-CACHE-PRESENTE"; else echo ciao >e2e-cache/dato; echo "E2E-CACHE-NUOVA"; fi
  artefatto:
    runs-on: ubuntu-latest
    steps:
      - run: echo contenuto >file.txt
      - uses: actions/upload-artifact@v4
        with:
          name: file
          path: file.txt
EOF
push "workflow della cache" >/dev/null

dispatch_cache() { # n -> scrive ${work}/cache-<n>.json e i log dei due job
  local n="$1" id i st
  code=$(curl -s -o "${work}/disp.json" -w '%{http_code}' -H "Authorization: Bearer ${tok}" -X POST -H 'content-type: application/json' \
    -d '{"ref":"main"}' "${api}/repos/${OWNER}/${REPO}/workflows/cache.yml/dispatches" || true)
  [ "${code}" = 202 ] || { ko "cache run ${n}: avvio del workflow, status ${code}, corpo '$(head -c 200 "${work}/disp.json")'"; return 1; }
  id=$(jq -r .id "${work}/disp.json")
  for ((i = 0; i < RUN_TIMEOUT / 3; i++)); do
    curl -s -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/actions/runs/${id}" >"${work}/cache-${n}.json" || true
    st=$(jq -r '.status // ""' "${work}/cache-${n}.json" 2>/dev/null || true)
    [ "${st}" = completed ] || [ "${st}" = cancelled ] && break
    sleep 3
  done
  [ "${st}" = completed ] || { ko "cache run ${n}: ${id} non concluso entro ${RUN_TIMEOUT}s (stato '${st}')"; return 1; }
  local job jid
  for job in cache artefatto; do
    jid=$(jq -r --arg j "${job}" '[.jobs[] | select(.name == $j)][0].id' "${work}/cache-${n}.json")
    curl -s -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/actions/jobs/${jid}/logs" >"${work}/cache-${n}-${job}.log" || true
  done
  echo "run cache ${n}: ${id}"
}
job_concl() { jq -r --arg j "$2" '[.jobs[] | select(.name == $j)][0].conclusion // "-"' "${work}/cache-$1.json"; }

if dispatch_cache 1; then
  if [ "$(job_concl 1 cache)" = success ]; then ok "cache run 1: job cache success"; else ko "cache run 1: job cache $(job_concl 1 cache)"; tail -n 30 "${work}/cache-1-cache.log" >&2; fi
  if grep -q 'Cache saved with key: e2e-cache-v1' "${work}/cache-1-cache.log"; then ok "cache run 1: «$(grep -o 'Cache saved with key: e2e-cache-v1' "${work}/cache-1-cache.log" | head -n1)»"; else ko "cache run 1: manca «Cache saved with key»"; tail -n 30 "${work}/cache-1-cache.log" >&2; fi
  if grep -q '| E2E-CACHE-NUOVA' "${work}/cache-1-cache.log"; then ok "cache run 1: cache vuota all'avvio"; else ko "cache run 1: la cache non era vuota"; fi
fi
if dispatch_cache 2; then
  if [ "$(job_concl 2 cache)" = success ]; then ok "cache run 2: job cache success"; else ko "cache run 2: job cache $(job_concl 2 cache)"; tail -n 30 "${work}/cache-2-cache.log" >&2; fi
  if grep -q 'Cache restored from key: e2e-cache-v1' "${work}/cache-2-cache.log"; then ok "cache run 2: «$(grep -o 'Cache restored from key: e2e-cache-v1' "${work}/cache-2-cache.log" | head -n1)»"; else ko "cache run 2: manca «Cache restored from key»"; tail -n 30 "${work}/cache-2-cache.log" >&2; fi
  if grep -q '| E2E-CACHE-PRESENTE' "${work}/cache-2-cache.log"; then ok "cache run 2: il contenuto salvato nel primo run è tornato"; else ko "cache run 2: il file della cache manca"; fi
  if [ "$(job_concl 2 artefatto)" = failure ] && grep -q 'GitStack non supporta actions/upload-artifact@v4' "${work}/cache-2-artefatto.log"; then
    ok "artefatti v4: il passo fallisce col messaggio documentato (501)"
  else
    ko "artefatti v4: atteso failure con «GitStack non supporta actions/upload-artifact@v4», trovato $(job_concl 2 artefatto)"; tail -n 20 "${work}/cache-2-artefatto.log" >&2
  fi
fi

# --- 1c. input di workflow_dispatch, github.sha e GITHUB_SHA (GIT-439) --------------------------
# Lo schema di image.yml del ciclo CI dell'e2e sulla VM (gs-ci-cycle.sh): un input obbligatorio,
# uno con default, due job con needs. Il job build stampa l'input e lo sha (contesto e ambiente), il
# job test fallisce se rompi=si. Il primo run deve avere gli input e lo sha del commit, il secondo
# (rompi=si) deve fallire nel job test. I marcatori si cercano dopo «| »: act ristampa anche lo script.
cat >"${src}/.gitstack/workflows/image.yml" <<'EOF'
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
      - name: build
        run: |
          set -eu
          echo "E2E-INPUT-VERSIONE=${{ github.event.inputs.versione }}"
          echo "E2E-INPUT-ROMPI=${{ github.event.inputs.rompi }}"
          echo "E2E-SHA-CTX=${{ github.sha }}"
          echo "E2E-SHA-ENV=${GITHUB_SHA}"
          echo "E2E-REF-ENV=${GITHUB_REF}"
  test:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - name: prova
        run: |
          set -eu
          if [ "${{ github.event.inputs.rompi }}" = si ]; then echo "E2E-ROMPI-VOLUTO"; exit 1; fi
          echo "E2E-IMAGE-TEST-OK"
EOF
shaI=$(push "workflow image")

dispatch_image() { # n inputs-json -> scrive ${work}/image-<n>.json e i log dei job build e test
  local n="$1" id i st job jid
  code=$(curl -s -o "${work}/disp.json" -w '%{http_code}' -H "Authorization: Bearer ${tok}" -X POST -H 'content-type: application/json' \
    -d "{\"ref\":\"main\",\"inputs\":$2}" "${api}/repos/${OWNER}/${REPO}/workflows/image.yml/dispatches" || true)
  [ "${code}" = 202 ] || { ko "image run ${n}: avvio del workflow, status ${code}, corpo '$(head -c 200 "${work}/disp.json")'"; return 1; }
  id=$(jq -r .id "${work}/disp.json")
  for ((i = 0; i < RUN_TIMEOUT / 3; i++)); do
    curl -s -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/actions/runs/${id}" >"${work}/image-${n}.json" || true
    st=$(jq -r '.status // ""' "${work}/image-${n}.json" 2>/dev/null || true)
    [ "${st}" = completed ] || [ "${st}" = cancelled ] && break
    sleep 3
  done
  [ "${st}" = completed ] || { ko "image run ${n}: ${id} non concluso entro ${RUN_TIMEOUT}s (stato '${st}')"; return 1; }
  for job in build test; do
    jid=$(jq -r --arg j "${job}" '[.jobs[] | select(.name == $j)][0].id' "${work}/image-${n}.json")
    curl -s -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/actions/jobs/${jid}/logs" >"${work}/image-${n}-${job}.log" || true
  done
}
image_concl() { jq -r --arg j "$2" '[.jobs[] | select(.name == $j)][0].conclusion // "-"' "${work}/image-$1.json"; }
image_has() { grep -qF -- "| $2" "${work}/image-$1-$3.log"; } # n marcatore job

if dispatch_image 1 '{"versione":"0.0.1"}'; then
  if [ "$(jq -r .conclusion "${work}/image-1.json")" = success ]; then ok "image run 1: run success (input obbligatorio dato, due job con needs)"; else
    ko "image run 1: run $(jq -r .conclusion "${work}/image-1.json")"; tail -n 30 "${work}/image-1-build.log" >&2; fi
  if image_has 1 'E2E-INPUT-VERSIONE=0.0.1' build; then ok "image run 1: github.event.inputs.versione arriva al passo"; else ko "image run 1: input versione assente dal log"; tail -n 30 "${work}/image-1-build.log" >&2; fi
  if image_has 1 'E2E-INPUT-ROMPI=no' build; then ok "image run 1: l'input con default (rompi=no) arriva al passo"; else ko "image run 1: input rompi assente dal log"; fi
  if image_has 1 "E2E-SHA-CTX=${shaI}" build; then ok "image run 1: github.sha è lo sha del commit"; else ko "image run 1: github.sha diverso da ${shaI}"; grep -F 'E2E-SHA' "${work}/image-1-build.log" >&2 || true; fi
  if image_has 1 "E2E-SHA-ENV=${shaI}" build; then ok "image run 1: GITHUB_SHA è lo sha del commit"; else ko "image run 1: GITHUB_SHA diverso da ${shaI}"; fi
  if image_has 1 'E2E-REF-ENV=refs/heads/main' build; then ok "image run 1: GITHUB_REF è refs/heads/main"; else ko "image run 1: GITHUB_REF inatteso"; fi
  if image_has 1 'E2E-IMAGE-TEST-OK' test; then ok "image run 1: il job test (needs: build) è partito"; else ko "image run 1: il job test non ha stampato E2E-IMAGE-TEST-OK"; fi
fi
if dispatch_image 2 '{"versione":"0.0.2","rompi":"si"}'; then
  if [ "$(jq -r .conclusion "${work}/image-2.json")" = failure ] && [ "$(image_concl 2 build)" = success ] && [ "$(image_concl 2 test)" = failure ]; then
    ok "image run 2: con rompi=si il run fallisce nel job test"
  else
    ko "image run 2: atteso run failure con build success e test failure, trovato $(jq -r .conclusion "${work}/image-2.json") / $(image_concl 2 build) / $(image_concl 2 test)"
  fi
  if image_has 2 'E2E-ROMPI-VOLUTO' test; then ok "image run 2: il log del job test ha il marcatore del fallimento voluto"; else ko "image run 2: marcatore E2E-ROMPI-VOLUTO assente"; tail -n 20 "${work}/image-2-test.log" >&2; fi
fi

# --- 2. mirror delle action ----------------------------------------------------------------
kubectl create namespace "${MIRROR_NS}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "${MIRROR_NS}" create configmap action-mirror --from-file=server.js="${HERE}/action-mirror/server.js" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "${MIRROR_NS}" delete pod action-mirror --ignore-not-found --wait=true >/dev/null
kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: action-mirror
  namespace: ${MIRROR_NS}
  labels: {app: action-mirror}
spec:
  containers:
    - name: mirror
      image: ${RUNNER_IMAGE}
      imagePullPolicy: IfNotPresent
      env: [{name: HOME, value: /tmp}]
      command:
        - sh
        - -c
        - |
          set -eu
          # Un repo bare per action, con un'action composita che stampa un marcatore.
          for spec in github-script:v7:SCRIPT; do
            name=\${spec%%:*}; rest=\${spec#*:}; tag=\${rest%%:*}; mark=\${rest#*:}
            bare=/tmp/mirror/actions/\$name.git
            git init -q --bare -b main "\$bare"
            w=\$(mktemp -d); cd "\$w"; git init -q -b main
            printf 'name: %s (mirror finto)\nruns:\n  using: composite\n  steps:\n    - run: echo MIRROR-FINTO-%s\n      shell: bash\n' "\$name" "\$mark" > action.yml
            git add action.yml
            git -c user.name=m -c user.email=m@example.invalid commit -q -m mirror
            git tag "\$tag"
            git push -q "\$bare" main "\$tag"
          done
          cd /tmp
          exec node /srv/server.js
      volumeMounts: [{name: srv, mountPath: /srv}]
      readinessProbe: {tcpSocket: {port: 8080}, periodSeconds: 2}
  volumes: [{name: srv, configMap: {name: action-mirror}}]
---
apiVersion: v1
kind: Service
metadata: {name: action-mirror, namespace: ${MIRROR_NS}}
spec:
  selector: {app: action-mirror}
  ports: [{port: 8080, targetPort: 8080}]
EOF
kubectl -n "${MIRROR_NS}" wait --for=condition=Ready pod/action-mirror --timeout=120s >/dev/null
ok "mirror finto in ${MIRROR_NS}"

mirror_url="http://action-mirror.${MIRROR_NS}.svc.cluster.local:8080"
helm upgrade "${RELEASE}" "${CHART_DIR}" --reuse-values \
  --set "ci.runner.actionMirror=${mirror_url}" \
  --set "ci.runner.actionMirrorEgress[0].to[0].namespaceSelector.matchLabels.kubernetes\\.io/metadata\\.name=${MIRROR_NS}" \
  --set "ci.runner.actionMirrorEgress[0].to[0].podSelector.matchLabels.app=action-mirror" \
  --set "ci.runner.actionMirrorEgress[0].ports[0].protocol=TCP" \
  --set "ci.runner.actionMirrorEgress[0].ports[0].port=8080" \
  --wait --timeout 3m >/dev/null
kubectl rollout restart "deployment/${RELEASE}-core" >/dev/null
kubectl rollout status "deployment/${RELEASE}-core" --timeout=180s >/dev/null
ok "chart aggiornato con ci.runner.actionMirror=${mirror_url}"

echo "# secondo commit" >>"${src}/README.md"
sha2=$(push "workflow col mirror")
if check_run "mirror" "${sha2}"; then
  if grep -q 'MIRROR-FINTO-SCRIPT' "${work}/job.log"; then ok "mirror: il log contiene il marcatore dell'action del mirror"; else ko "mirror: marcatori assenti dal log"; tail -n 30 "${work}/job.log" >&2; fi
  if grep -q 'E2E-GITHUB-SCRIPT' "${work}/job.log"; then ko "mirror: l'action vera di github.com ha girato"; else ok "mirror: nessuna action di github.com ha girato"; fi
  hits=$(kubectl -n "${MIRROR_NS}" logs pod/action-mirror)
  if grep -q "MIRROR-HIT GET /actions/github-script/info/refs" <<<"${hits}"; then ok "mirror: il mirror ha ricevuto il clone di actions/github-script"; else ko "mirror: nessuna richiesta per actions/github-script nel log del mirror"; fi
fi

# --- 3. build rootless e pod privilegiato nel namespace di build ---------------------------
helm upgrade "${RELEASE}" "${CHART_DIR}" --reuse-values --set ci.build.enabled=true --wait --timeout 3m >/dev/null
for _ in $(seq 1 30); do # la policy di ammissione diventa attiva qualche istante dopo la creazione
  kubectl get validatingadmissionpolicybinding "${BUILD_NS}-rootless-only" >/dev/null 2>&1 && break
  sleep 1
done
sleep 5
build_image=$(helm get values "${RELEASE}" -a -o json | jq -r .ci.build.image)
kubectl -n "${BUILD_NS}" delete pod ci-build ci-privileged ci-ephemeral-target --ignore-not-found --wait=true >/dev/null
kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ci-build
  namespace: ${BUILD_NS}
  labels: {gitstack.io/ci-job: "true"}
spec:
  restartPolicy: Never
  activeDeadlineSeconds: 300
  automountServiceAccountToken: false
  enableServiceLinks: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 1000
    runAsGroup: 1000
    fsGroup: 1000
    seccompProfile: {type: Unconfined}
  containers:
    - name: build
      image: ${build_image}
      env:
        - {name: BUILDKITD_FLAGS, value: --oci-worker-no-process-sandbox}
        - {name: HOME, value: /home/user}
        - {name: XDG_RUNTIME_DIR, value: /tmp/run}
      command:
        - sh
        - -c
        - |
          set -eu
          mkdir -p /tmp/run /home/user/.local/tmp /home/user/.local/share/buildkit /tmp/ctx
          printf 'FROM scratch\nCOPY hello /hello\n' > /tmp/ctx/Dockerfile
          echo ciao > /tmp/ctx/hello
          buildctl-daemonless.sh build --frontend dockerfile.v0 --local context=/tmp/ctx --local dockerfile=/tmp/ctx --output type=oci,dest=/tmp/out.tar
          test -s /tmp/out.tar
          echo "BUILD-ROOTLESS-OK \$(wc -c </tmp/out.tar) byte"
      securityContext:
        allowPrivilegeEscalation: true
        capabilities: {drop: [ALL], add: [SETUID, SETGID]}
        seccompProfile: {type: Unconfined}
        appArmorProfile: {type: Unconfined}
      volumeMounts:
        - {name: local, mountPath: /home/user/.local}
        - {name: tmp, mountPath: /tmp}
  volumes:
    - {name: local, emptyDir: {}}
    - {name: tmp, emptyDir: {}}
EOF
if kubectl -n "${BUILD_NS}" wait --for=jsonpath='{.status.phase}'=Succeeded pod/ci-build --timeout=240s >/dev/null 2>&1 \
   && kubectl -n "${BUILD_NS}" logs ci-build | grep -q BUILD-ROOTLESS-OK; then
  ok "build rootless: BuildKit senza demone, completata in ${BUILD_NS}"
else
  ko "build rootless non riuscita"; kubectl -n "${BUILD_NS}" logs ci-build --tail=30 >&2 || true
fi
if kubectl -n "${BUILD_NS}" get pod ci-build -o json | jq -e '[.spec.volumes[]? | select(.hostPath)] | length == 0' >/dev/null; then
  ok "build rootless: nessun volume hostPath (nessun socket del runtime)"
else
  ko "build rootless: volume hostPath presente"
fi

# Stesso pod senza altre violazioni, ma privileged: lo rifiuta la policy, per quel motivo.
if kubectl apply -f - >/dev/null 2>"${work}/priv.err" <<EOF
apiVersion: v1
kind: Pod
metadata: {name: ci-privileged, namespace: ${BUILD_NS}, labels: {gitstack.io/ci-job: "true"}}
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext: {runAsNonRoot: true, runAsUser: 1000}
  containers:
    - name: p
      image: ${build_image}
      command: [sleep, "60"]
      securityContext: {privileged: true, allowPrivilegeEscalation: true, capabilities: {drop: [ALL]}}
EOF
then
  ko "pod privilegiato accettato nel namespace di build"
elif grep -q 'non possono essere privileged' "${work}/priv.err"; then
  ok "pod privilegiato rifiutato dalla policy: $(grep -o 'non possono essere privileged' "${work}/priv.err")"
else
  ko "pod privilegiato rifiutato per un altro motivo: $(head -c 300 "${work}/priv.err")"
fi

# Sonda ephemeral container (criterio GIT-333): un pod bersaglio conforme alla policy, poi un
# tentativo di ephemeral container privilegiata sul subresource pods/ephemeralcontainers.
kubectl apply -f - >/dev/null 2>"${work}/eph-target.err" <<EOF || true
apiVersion: v1
kind: Pod
metadata:
  name: ci-ephemeral-target
  namespace: ${BUILD_NS}
  labels: {gitstack.io/ci-job: "true"}
spec:
  restartPolicy: Never
  activeDeadlineSeconds: 300
  automountServiceAccountToken: false
  enableServiceLinks: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 1000
    runAsGroup: 1000
    fsGroup: 1000
    seccompProfile: {type: Unconfined}
  containers:
    - name: target
      image: ${build_image}
      command: [sleep, "120"]
      securityContext:
        allowPrivilegeEscalation: true
        capabilities: {drop: [ALL], add: [SETUID, SETGID]}
        seccompProfile: {type: Unconfined}
        appArmorProfile: {type: Unconfined}
      volumeMounts:
        - {name: tmp, mountPath: /tmp}
  volumes:
    - {name: tmp, emptyDir: {}}
EOF
if kubectl -n "${BUILD_NS}" wait --for=condition=Ready pod/ci-ephemeral-target --timeout=120s >/dev/null 2>&1; then
  if kubectl -n "${BUILD_NS}" patch pod ci-ephemeral-target --subresource=ephemeralcontainers --type=strategic -p '{"spec":{"ephemeralContainers":[{"name":"dbg","image":"'"${build_image}"'","command":["sleep","30"],"securityContext":{"privileged":true}}]}}' >/dev/null 2>"${work}/eph.err"; then
    ko "ephemeral container privilegiata accettata nel namespace di build"
  elif grep -q 'non accettano ephemeral container' "${work}/eph.err"; then
    ok "ephemeral container rifiutata dalla policy: $(grep -o 'non accettano ephemeral container' "${work}/eph.err")"
  else
    ko "ephemeral container rifiutata per un altro motivo: $(head -c 300 "${work}/eph.err")"
  fi
else
  ko "ephemeral: pod bersaglio non pronto"
fi
kubectl -n "${BUILD_NS}" delete pod ci-ephemeral-target --ignore-not-found --wait=false >/dev/null

# --- 4. workflow `runs-on: gitstack-build` (GIT-371, ADR 0001 §6, CI2) -------------------------
# Un workflow vero chiede la label documentata: il dispatcher crea il pod nel namespace di build
# (gate + BuildKit rootless come sidecar + runner), il job costruisce l'immagine e la pubblica nel
# registry del repo col token del job, dal gateway nel cluster. Poi le prove negative di rete,
# dal namespace di build, e altre due violazioni della policy di ammissione.
build_pods_json="${work}/buildpod.json"
watch_build_pods() {
  local seen=" " p j
  while true; do
    for p in $(kubectl -n "${BUILD_NS}" get pods -l 'gitstack.io/ci-job=true' -o name 2>/dev/null | sed 's#^pod/##'); do
      case "${p}" in ci-????????????????????????????????) ;; *) continue ;; esac
      j=$(kubectl -n "${BUILD_NS}" get pod "${p}" -o json 2>/dev/null || true)
      [ -n "${j}" ] && printf '%s' "${j}" >"${build_pods_json}"
      case "${seen}" in *" ${p} "*) continue ;; esac
      seen="${seen}${p} "
      (kubectl -n "${BUILD_NS}" logs -f "${p}" --all-containers --prefix >"${work}/buildpod-${p}.log" 2>&1 || true) &
    done
    sleep 1
  done
}
watch_build_pods &
bwatcher=$!

cat >"${src}/.gitstack/workflows/build.yml" <<'EOF'
name: build
on: workflow_dispatch
permissions:
  contents: read
  packages: write
jobs:
  image:
    runs-on: gitstack-build
    steps:
      # Come su GitHub, il codice nel workspace c'e' solo dopo actions/checkout: senza, il
      # Dockerfile non esiste (GIT-393). Il runner integrato lo salta e copia il workspace.
      - uses: actions/checkout@v4
      - run: gitstack-build --tag e2e-1 .
EOF
printf 'FROM scratch\nCOPY hello /hello\n' >"${src}/Dockerfile"
echo "ciao dal job di build" >"${src}/hello"
push "workflow gitstack-build" >/dev/null
# Il token del job pubblica solo se il repo gli concede packages: write (di default e' read, CI4).
code=$(curl -s -o "${work}/perm.body" -w '%{http_code}' -H "Authorization: Bearer ${tok}" -X PUT -H 'content-type: application/json'   -d '{"contents":"read","packages":"write","actions":"read"}' "${api}/repos/${OWNER}/${REPO}/actions/permissions" || true)
if [ "${code}" = 200 ]; then ok "build: il repo concede packages: write al token del job"; else ko "build: permessi del token del job, status ${code}, corpo '$(head -c 200 "${work}/perm.body")'"; fi

code=$(curl -s -o "${work}/bdisp.json" -w '%{http_code}' -H "Authorization: Bearer ${tok}" -X POST -H 'content-type: application/json' \
  -d '{"ref":"main"}' "${api}/repos/${OWNER}/${REPO}/workflows/build.yml/dispatches" || true)
if [ "${code}" != 202 ]; then
  ko "build: avvio del workflow, status ${code}, corpo '$(head -c 200 "${work}/bdisp.json")'"
else
  bid=$(jq -r .id "${work}/bdisp.json")
  bst=""
  for ((i = 0; i < RUN_TIMEOUT / 3; i++)); do
    curl -s -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/actions/runs/${bid}" >"${work}/build-run.json" || true
    bst=$(jq -r '.status // ""' "${work}/build-run.json" 2>/dev/null || true)
    [ "${bst}" = completed ] || [ "${bst}" = cancelled ] && break
    sleep 3
  done
  bjob=$(jq -r '.jobs[0].id // ""' "${work}/build-run.json" 2>/dev/null || true)
  [ -z "${bjob}" ] || curl -s -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/actions/jobs/${bjob}/logs" >"${work}/build-job.log" || true
  bconcl=$(jq -r '.jobs[0].conclusion // "-"' "${work}/build-run.json" 2>/dev/null || echo "-")
  if [ "${bst}" = completed ] && [ "${bconcl}" = success ]; then ok "build: run ${bid} completed/success"; else
    ko "build: run ${bid} ${bst}/${bconcl}"; tail -n 40 "${work}/build-job.log" >&2 || true; fi
  if grep -q 'gitstack-build: pubblicata' "${work}/build-job.log" 2>/dev/null; then ok "build: il log del job conferma la pubblicazione"; else ko "build: nel log manca «gitstack-build: pubblicata»"; fi

  # Il pod del job: nel namespace di build, con gate e sidecar BuildKit, senza hostPath, non nel namespace dei job.
  if [ -s "${build_pods_json}" ]; then
    pod_name=$(jq -r .metadata.name "${build_pods_json}")
    expected="ci-$(printf '%s' "${bjob}" | tr -d '-')"
    if [ "${pod_name}" = "${expected}" ]; then ok "build: il pod del job (${pod_name:0:11}...) e' stato creato in ${BUILD_NS}"; else ko "build: pod '${pod_name}' diverso dal pod del job '${expected}'"; fi
    if jq -e '([.spec.initContainers[].name] == ["gate", "buildkitd"]) and ([.spec.containers[].name] == ["job"])' "${build_pods_json}" >/dev/null; then
      ok "build: gate fail-closed prima del sidecar buildkitd, poi il runner"; else ko "build: container del pod inattesi: $(jq -c '[.spec.initContainers[].name, .spec.containers[].name]' "${build_pods_json}")"; fi
    if jq -e '[.spec.volumes[]? | select(.hostPath)] | length == 0' "${build_pods_json}" >/dev/null; then ok "build: il pod del job non ha volumi hostPath"; else ko "build: il pod del job ha un hostPath"; fi
    if jq -e '.spec.automountServiceAccountToken == false' "${build_pods_json}" >/dev/null; then ok "build: il pod del job non monta il token del ServiceAccount"; else ko "build: token del ServiceAccount montato"; fi
    if kubectl -n gitstack-ci-jobs get pod "${expected}" >/dev/null 2>&1; then ko "build: il job e' comparso anche nel namespace dei job"; else ok "build: nessun pod del job in gitstack-ci-jobs"; fi
  else
    ko "build: nessun pod visto in ${BUILD_NS} durante il run"
  fi

  # L'immagine e' nel registry del repo, pubblicata dal token del job (pushedBy.kind = job).
  code=$(curl -s -o "${work}/pkgs.json" -w '%{http_code}' -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/packages" || true)
  if [ "${code}" = 200 ] && jq -e '[.. | objects | select((.tags? // []) | index("e2e-1")) | select(.pushedBy.kind == "job")] | length >= 1' "${work}/pkgs.json" >/dev/null 2>&1; then
    ok "build: l'immagine ${OWNER}/${REPO}:e2e-1 e' nel registry, pubblicata col token del job"
  else
    ko "build: immagine e2e-1 non trovata fra i pacchetti del repo (status ${code}): $(head -c 300 "${work}/pkgs.json")"
  fi
fi

# Prove negative dal namespace di build: un pod conforme alla policy di ammissione e con l'etichetta dei
# job (come quelli veri) prova la NetworkPolicy ci-build-egress su indirizzi IP, non su nomi: un nome che
# non si risolve farebbe passare ogni «irraggiungibile». Le prime due righe (ci-gateway, gateway) provano il metodo. k3s applica le
# NetworkPolicy a un pod dopo l'avvio: come fa il gate dei pod veri, la sonda aspetta che l'API Kubernetes sia chiusa prima di controllare.
svc() { kubectl get svc "${RELEASE}-$1" -o jsonpath='{.spec.clusterIP}'; }
core_ip=$(svc core); gw_ip=$(svc gateway); id_ip=$(svc identity); git_ip=$(svc git); nats_ip=$(svc nats); pg_ip=$(svc postgres)
kube_ip=$(kubectl get svc kubernetes -o jsonpath='{.spec.clusterIP}')
probe_cmd="w=0; while nc -z -w2 ${kube_ip} 443; do w=\$((w + 1)); if [ \$w -ge 45 ]; then echo 'KO: la NetworkPolicy non e stata applicata al pod entro 90s'; echo fine-prove; exit 0; fi; sleep 2; done; echo \"attesa: NetworkPolicy applicata al pod dopo \$((w * 2))s (le regole arrivano dopo l avvio: per questo i pod veri hanno il gate)\"; check() { if nc -z -w3 \"\$2\" \"\$3\"; then r=raggiungibile; else r=bloccato; fi; if [ \"\$r\" = \"\$4\" ]; then echo \"ok: \$1 \$r\"; else echo \"KO: \$1 \$r (atteso \$4)\"; fi; }; \
check 'ci-gateway di core' ${core_ip} 8081 raggiungibile; \
check 'gateway del registry' ${gw_ip} 8080 raggiungibile; \
check 'core su un altra porta (8080)' ${core_ip} 8080 bloccato; \
check 'postgres' ${pg_ip} 5432 bloccato; \
check 'nats' ${nats_ip} 4222 bloccato; \
check 'identity' ${id_ip} 8080 bloccato; \
check 'git' ${git_ip} 8080 bloccato; \
check 'API Kubernetes' ${kube_ip} 443 bloccato; \
check 'IP della LAN 192.168.255.254' 192.168.255.254 80 bloccato; \
check 'IP privato 10.255.255.254' 10.255.255.254 80 bloccato; \
echo fine-prove"
probe_b64=$(printf '%s' "${probe_cmd}" | base64 -w0)
kubectl -n "${BUILD_NS}" delete pod ci-build-probe --ignore-not-found --wait=true >/dev/null
kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ci-build-probe
  namespace: ${BUILD_NS}
  labels: {gitstack.io/ci-job: "true"}
spec:
  restartPolicy: Never
  activeDeadlineSeconds: 120
  automountServiceAccountToken: false
  enableServiceLinks: false
  securityContext: {runAsNonRoot: true, runAsUser: 1000, seccompProfile: {type: RuntimeDefault}}
  containers:
    - name: probe
      image: docker.io/busybox:1.36
      env: [{name: PROBE_B64, value: "${probe_b64}"}]
      command: [sh, -c, 'eval "\$(echo \$PROBE_B64 | base64 -d)"']
      resources:
        requests: {cpu: 50m, memory: 32Mi, ephemeral-storage: 64Mi}
        limits: {cpu: 200m, memory: 64Mi, ephemeral-storage: 64Mi}
      securityContext: {allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}
EOF
pphase=""
for _ in $(seq 1 60); do
  pphase=$(kubectl -n "${BUILD_NS}" get pod ci-build-probe -o jsonpath='{.status.phase}' 2>/dev/null || true)
  [ "${pphase}" = Succeeded ] || [ "${pphase}" = Failed ] && break
  sleep 2
done
kubectl -n "${BUILD_NS}" logs ci-build-probe >"${work}/probe.log" 2>&1 || true
if [ "${pphase}" = Succeeded ] && grep -q '^fine-prove' "${work}/probe.log"; then
  nprobe=0
  while IFS= read -r line; do
    case "${line}" in
      ok:*) ok "build rete: ${line#ok: }"; nprobe=$((nprobe + 1)) ;;
      KO:*) ko "build rete: ${line#KO: }"; nprobe=$((nprobe + 1)) ;;
    esac
  done <"${work}/probe.log"
  [ "${nprobe}" -eq 10 ] || ko "build rete: ${nprobe} controlli stampati, attesi 10"
else
  ko "build rete: pod di prova in fase '${pphase}'"; cat "${work}/probe.log" >&2 || true
fi
kubectl -n "${BUILD_NS}" delete pod ci-build-probe --ignore-not-found --wait=false >/dev/null

# La policy di ammissione rifiuta ancora un hostPort e una capability oltre SETUID/SETGID.
for variant in hostport capability; do
  case "${variant}" in
    hostport) extra='ports: [{containerPort: 8080, hostPort: 8089}]'; sc='{allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}'; want='non aprono hostPort' ;;
    capability) extra=''; sc='{allowPrivilegeEscalation: false, capabilities: {drop: [ALL], add: [NET_ADMIN]}}'; want='possono aggiungere solo SETUID e SETGID' ;;
  esac
  if kubectl apply -f - >/dev/null 2>"${work}/${variant}.err" <<EOF
apiVersion: v1
kind: Pod
metadata: {name: ci-${variant}, namespace: ${BUILD_NS}, labels: {gitstack.io/ci-job: "true"}}
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext: {runAsNonRoot: true, runAsUser: 1000}
  containers:
    - name: p
      image: ${build_image}
      command: [sleep, "60"]
      ${extra}
      securityContext: ${sc}
EOF
  then
    ko "pod con ${variant} accettato nel namespace di build"
    kubectl -n "${BUILD_NS}" delete pod "ci-${variant}" --ignore-not-found --wait=false >/dev/null
  elif grep -q "${want}" "${work}/${variant}.err"; then
    ok "policy di ammissione: pod con ${variant} rifiutato («${want}»)"
  else
    ko "pod con ${variant} rifiutato per un altro motivo: $(head -c 300 "${work}/${variant}.err")"
  fi
done

if [ "${fail}" -ne 0 ]; then echo "ESITO: KO" >&2; exit 1; fi
echo "ESITO: ok"
