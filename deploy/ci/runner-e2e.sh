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
#   3. una build rootless (BuildKit senza demone) nel namespace gitstack-ci-build e un pod
#      privilegiato nello stesso namespace, rifiutato dalla ValidatingAdmissionPolicy.
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

if [ "${fail}" -ne 0 ]; then echo "ESITO: KO" >&2; exit 1; fi
echo "ESITO: ok"
