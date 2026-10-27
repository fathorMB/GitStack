#!/usr/bin/env bash
# deploy/ci/autodeploy-e2e.sh - prova end-to-end del deploy automatico da branch e delle firme
# (GIT-349, H7/H10), sul cluster k3d del job `chart` di ci.yml, dopo runner-e2e.sh (GIT-317).
#
# Fa, nell'ordine (una riga per controllo, `OK: ...` o `KO: ...` + uscita 1):
#   a. accende le app (apps.enabled) e allinea il registry: l'host dell'immagine dell'app e'
#      quello che vedono i job (GITSTACK_CORE_CI_REGISTRY_ADDR, un nome di Service del cluster),
#      quindi core lo usa come URL pubblico e i nodi lo raggiungono con un hosts.toml di containerd;
#   b. crea un repo con un workflow `on: push` su `runs-on: gitstack-build` che costruisce
#      un'immagine (BuildKit rootless, come in runner-e2e.sh) e la pubblica col token del job: il
#      registry la firma con la chiave dell'istanza. Il primo run pubblica sha-<commit>, poi si
#      crea l'app con followBranch=main su <registry>/<owner>/<repo>/web:sha-<commit>;
#   c. un secondo push su main: il workflow pubblica un tag nuovo e, senza altro, core mette in
#      produzione una versione nuova: trigger=branch_push, image.verified=true, stato serving;
#   c2. (GIT-377) l'app passa alla regola sui tag v*-test.*; un terzo push pubblica tre tag e solo
#      v9.9-test.1 crea una versione: trigger=tag_push, serving, commitSha; branch + regola: 422;
#   d. caso negativo: un tag pubblicato con il token di un utente (nessuna firma della CI, nessuna
#      notifica del job) NON crea una versione nuova entro NEG_WAIT secondi, e i pacchetti lo
#      mostrano «unverified».
#
# Richiede kubectl, helm, git, curl, jq, docker (per l'hosts.toml dei nodi k3d). La password di
# admin viene cambiata per la durata della prova; all'uscita si rimettono la password iniziale e
# i valori del chart (helm rollback). Variabili: RELEASE (gitstack), BASE_URL
# (http://localhost:8080), K3D_NODES_FILTER (k3d-gitstack-ci), RUN_TIMEOUT (secondi, 420),
# SERVE_TIMEOUT (secondi, 300), NEG_WAIT (secondi, 40).
set -euo pipefail

RELEASE="${RELEASE:-gitstack}"
BASE_URL="${BASE_URL:-http://localhost:8080}"
K3D_NODES_FILTER="${K3D_NODES_FILTER:-k3d-gitstack-ci}"
RUN_TIMEOUT="${RUN_TIMEOUT:-420}"
SERVE_TIMEOUT="${SERVE_TIMEOUT:-300}"
NEG_WAIT="${NEG_WAIT:-40}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
OWNER="admin"
REPO="autodeploy-e2e"
APP="web"
APPS_DOMAIN="apps.gitstack.test"

work="$(mktemp -d)"
jar="${work}/jar"
api=""
initial_pw=""
tmp_pw=""
tok=""
helm_rev=""
nodes=""
reg_addr=""

change_password() { # corrente, nuova
  local code
  code=$(jq -n --arg c "$1" --arg n "$2" '{currentPassword: $c, newPassword: $n}' \
    | curl -s -o "${work}/pw.body" -w '%{http_code}' -b "${jar}" -c "${jar}" -X PUT \
      -H 'content-type: application/json' -d @- "${api}/users/${OWNER}/password" || true)
  [ "${code}" = 204 ] || { echo "cambio password: status ${code}, corpo '$(head -c 200 "${work}/pw.body")'" >&2; return 1; }
}

cleanup() {
  local rc=$?
  if [ "${rc}" -ne 0 ]; then
    echo "--- diagnostica ---" >&2
    kubectl logs deployment/"${RELEASE}-core" --tail=100 >&2 || true
    kubectl get pods -A -o wide >&2 || true
  fi
  if [ -n "${tok}" ]; then
    curl -s -o /dev/null -H "Authorization: Bearer ${tok}" -X DELETE "${api}/repos/${OWNER}/${REPO}/apps/${APP}" || true
  fi
  if [ -n "${tmp_pw}" ] && [ -n "${initial_pw}" ]; then
    change_password "${tmp_pw}" "${initial_pw}" || echo "attenzione: password iniziale non ripristinata" >&2
  fi
  if [ -n "${helm_rev}" ]; then
    helm rollback "${RELEASE}" "${helm_rev}" --wait --timeout 3m >/dev/null 2>&1 || echo "attenzione: valori del chart non ripristinati" >&2
  fi
  rm -rf "${work}"
}
trap cleanup EXIT

prefix=$(sed -n "s/^export const API_BASE_URL = '\([^']*\)';.*$/\1/p" "${REPO_ROOT}/web/src/lib/http.ts")
if [ -z "${prefix}" ]; then echo "KO: API_BASE_URL non trovato in web/src/lib/http.ts" >&2; exit 1; fi
api="${BASE_URL}${prefix}"

# --- a. app accese, registry raggiungibile dai nodi ---------------------------------------------
helm_rev=$(helm history "${RELEASE}" -o json | jq -r 'map(.revision) | max')
reg_addr=$(kubectl get deploy "${RELEASE}-core" -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="GITSTACK_CORE_CI_REGISTRY_ADDR")].value}')
if [ -z "${reg_addr}" ]; then echo "KO: GITSTACK_CORE_CI_REGISTRY_ADDR assente da core (ci.build.enabled non attivo?)" >&2; exit 1; fi
gw_ip=$(kubectl get svc "${RELEASE}-gateway" -o jsonpath='{.spec.clusterIP}')
gw_port=$(kubectl get svc "${RELEASE}-gateway" -o jsonpath='{.spec.ports[0].port}')

helm upgrade "${RELEASE}" "${REPO_ROOT}/deploy/gitstack" --reuse-values \
  --set apps.enabled=true --set "apps.domain=${APPS_DOMAIN}" \
  --set "core.env.publicUrl=http://${reg_addr}" \
  --wait --timeout 4m >/dev/null
kubectl rollout status "deployment/${RELEASE}-core" --timeout=180s >/dev/null
if ! kubectl get deploy "${RELEASE}-core" -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' | grep -q GITSTACK_CORE_APPS_ENABLED; then
  echo "KO: core non ha GITSTACK_CORE_APPS_ENABLED dopo l'upgrade" >&2; exit 1
fi
echo "OK: app accese (apps.domain=${APPS_DOMAIN}), registry ${reg_addr} come URL pubblico di core"

# I nodi risolvono i nomi del cluster col DNS dell'host, non col CoreDNS: containerd riceve un
# hosts.toml che manda l'host del registry al ClusterIP del gateway (HTTP).
nodes=$(docker ps --filter "name=${K3D_NODES_FILTER}" --format '{{.Names}}' | grep -v 'serverlb\|tools' || true)
if [ -z "${nodes}" ]; then echo "KO: nessun nodo k3d con nome ${K3D_NODES_FILTER}" >&2; exit 1; fi
while IFS= read -r node; do
  d="/var/lib/rancher/k3s/agent/etc/containerd/certs.d/${reg_addr}"
  docker exec "${node}" sh -c "mkdir -p '${d}' && printf 'server = \"http://%s\"\n[host.\"http://%s:%s\"]\n  capabilities = [\"pull\", \"resolve\"]\n' '${reg_addr}' '${gw_ip}' '${gw_port}' > '${d}/hosts.toml'"
done <<<"${nodes}"
echo "OK: hosts.toml di containerd per ${reg_addr} -> ${gw_ip}:${gw_port} su $(echo "${nodes}" | wc -l) nodo/i"

# --- sessione, token, repo ----------------------------------------------------------------------
initial_pw=$(kubectl get secret "${RELEASE}-identity-admin" -o jsonpath='{.data.password}' | base64 -d)
tmp_pw="e2e-$(head -c 48 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)"
code=$(jq -n --arg p "${initial_pw}" '{username: "admin", password: $p}' \
  | curl -s -o "${work}/login.body" -w '%{http_code}' -c "${jar}" -X POST -H 'content-type: application/json' -d @- "${api}/auth/login" || true)
if [ "${code}" != 200 ]; then echo "KO: login admin: status ${code}" >&2; exit 1; fi
change_password "${initial_pw}" "${tmp_pw}"
exp=$(date -u -d '+2 days' +%Y-%m-%dT%H:%M:%SZ)
code=$(jq -n --arg e "${exp}" '{name: "e2e-autodeploy", scopes: ["read:resource", "write:resource", "read:registry", "write:registry"], expiresAt: $e}' \
  | curl -s -o "${work}/token.body" -w '%{http_code}' -b "${jar}" -X POST -H 'content-type: application/json' -d @- "${api}/user/tokens" || true)
if [ "${code}" != 201 ]; then echo "KO: creazione token: status ${code}, corpo '$(head -c 200 "${work}/token.body")'" >&2; exit 1; fi
tok=$(jq -r .token "${work}/token.body")
code=$(curl -s -o "${work}/repo.body" -w '%{http_code}' -H "Authorization: Bearer ${tok}" -X POST -H 'content-type: application/json' \
  -d "{\"owner\":\"${OWNER}\",\"name\":\"${REPO}\"}" "${api}/repos" || true)
if [ "${code}" != 201 ]; then echo "KO: creazione repo: status ${code}, corpo '$(head -c 200 "${work}/repo.body")'" >&2; exit 1; fi
code=$(curl -s -o "${work}/perm.body" -w '%{http_code}' -H "Authorization: Bearer ${tok}" -X PUT -H 'content-type: application/json' \
  -d '{"contents":"read","packages":"write","actions":"read"}' "${api}/repos/${OWNER}/${REPO}/actions/permissions" || true)
if [ "${code}" != 200 ]; then echo "KO: permessi del token del job, status ${code}, corpo '$(head -c 200 "${work}/perm.body")'" >&2; exit 1; fi
echo "OK: repo ${OWNER}/${REPO} creato, il token del job puo' pubblicare"

# --- b. workflow di build e prima immagine ------------------------------------------------------
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
cat >"${src}/.gitstack/workflows/image.yml" <<'EOF'
name: image
on: [push]
permissions:
  contents: read
  packages: write
jobs:
  image:
    runs-on: gitstack-build
    steps:
      - uses: actions/checkout@v4
      - run: gitstack-build --name web --tag "sha-$(echo "$GITHUB_SHA" | cut -c1-7)" .
EOF
# Un'immagine che parte davvero: busybox serve una pagina sulla porta 8080.
cat >"${src}/Dockerfile" <<'EOF'
FROM docker.io/busybox:1.36
COPY index.html /www/index.html
EXPOSE 8080
CMD ["httpd", "-f", "-p", "8080", "-h", "/www"]
EOF
echo "versione uno" >"${src}/index.html"
sha1=$(push "workflow di build e prima immagine")

run_status() { # sha -> stampa status/conclusion del run di quel commit (vuoto se non c'e')
  curl -s -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/actions/runs" \
    | jq -r --arg s "$1" '[.items[]? | select(.sha == $s)][0] | if . == null then "" else .status + "/" + (.conclusion // "-") end'
}
wait_run() { # sha etichetta
  local i st=""
  for ((i = 0; i < RUN_TIMEOUT / 3; i++)); do
    st=$(run_status "$1" || true)
    case "${st}" in completed/*|cancelled/*) break ;; esac
    sleep 3
  done
  if [ "${st}" != completed/success ]; then echo "KO: $2: run di ${1:0:8} ${st:-assente} (atteso completed/success)" >&2; exit 1; fi
  echo "OK: $2: run di ${1:0:8} completed/success"
}
wait_run "${sha1}" "build del primo commit"

tag1="sha-${sha1:0:7}"
code=$(curl -s -o "${work}/pkgs.json" -w '%{http_code}' -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/packages" || true)
if [ "${code}" != 200 ] || ! jq -e --arg t "${tag1}" '[.. | objects | select((.tags? // []) | index($t))] | length >= 1' "${work}/pkgs.json" >/dev/null; then
  echo "KO: immagine web:${tag1} assente dai pacchetti (status ${code}): $(head -c 300 "${work}/pkgs.json")" >&2; exit 1
fi
if ! jq -e --arg t "${tag1}" '[.. | objects | select((.tagDetails? // []) | any(.name == $t and .signature == "verified" and .pushedBy.kind == "job"))] | length >= 1' "${work}/pkgs.json" >/dev/null; then
  echo "KO: web:${tag1} non risulta pubblicata dal job e firmata (signature verified): $(head -c 400 "${work}/pkgs.json")" >&2; exit 1
fi
echo "OK: web:${tag1} pubblicata dal job e firmata dalla CI (signature verified)"

# --- app che segue main -------------------------------------------------------------------------
code=$(curl -s -o "${work}/app.json" -w '%{http_code}' -H "Authorization: Bearer ${tok}" -X POST -H 'content-type: application/json' \
  -d "{\"name\":\"${APP}\",\"image\":\"${reg_addr}/${OWNER}/${REPO}/web:${tag1}\",\"port\":8080,\"followBranch\":\"main\"}" \
  "${api}/repos/${OWNER}/${REPO}/apps" || true)
if [ "${code}" != 202 ]; then echo "KO: creazione dell'app con followBranch=main: status ${code}, corpo '$(head -c 300 "${work}/app.json")'" >&2; exit 1; fi
if ! jq -e '.version.image.verified == true and .app.follow.branch == "main"' "${work}/app.json" >/dev/null; then
  echo "KO: l'app creata non e' verificata o non segue main: $(head -c 300 "${work}/app.json")" >&2; exit 1
fi
versions() { curl -s -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/apps/${APP}/versions"; }
wait_serving() { # filtro jq sulla versione -> scrive il numero in ${work}/ver
  local i v
  : >"${work}/ver"
  for ((i = 0; i < SERVE_TIMEOUT / 3; i++)); do
    v=$(versions | jq -r "[.items[]? | select($1)] | sort_by(.number) | last | .number // empty" 2>/dev/null || true)
    if [ -n "${v}" ]; then echo "${v}" >"${work}/ver"; return 0; fi
    sleep 3
  done
  return 1
}
if ! wait_serving '.number == 1 and .state == "serving"'; then
  echo "KO: la versione 1 non e' arrivata a serving entro ${SERVE_TIMEOUT}s: $(versions | jq -c '[.items[]? | {number, state, stateReason}]')" >&2; exit 1
fi
echo "OK: app creata sul branch main - versione 1 serving, verified"

# --- c. secondo push: deploy automatico ---------------------------------------------------------
echo "versione due" >"${src}/index.html"
sha2=$(push "seconda versione")
wait_run "${sha2}" "build del secondo commit"
tag2="sha-${sha2:0:7}"
if ! wait_serving '.number > 1 and .trigger == "branch_push" and .image.verified == true and .state == "serving"'; then
  echo "KO: nessuna versione branch_push verificata e serving entro ${SERVE_TIMEOUT}s: $(versions | jq -c '[.items[]? | {number, trigger, state, stateReason, verified: .image.verified}]')" >&2; exit 1
fi
n=$(cat "${work}/ver")
if ! versions | jq -e --argjson n "${n}" --arg t "${tag2}" '.items[] | select(.number == $n) | (.image.reference | endswith("/web:" + $t)) and .branch == "main" and (.commitSha != null)' >/dev/null; then
  echo "KO: la versione ${n} non e' l'immagine ${tag2}, o manca branch/commitSha: $(versions | jq -c --argjson n "${n}" '.items[] | select(.number == $n)')" >&2; exit 1
fi
echo "OK: deploy automatico - versione ${n} serving, trigger branch_push, verified"

# --- c2. regola sui tag (GIT-377) ---------------------------------------------------------------
# L'app passa dal branch alla regola v*-test.* (nella stessa richiesta: insieme sarebbero 422).
# Un terzo push costruisce l'immagine e la pubblica con tre tag: sha-<commit> e v9.9 non
# rispettano la regola, v9.9-test.1 si': la versione nuova nasce da quel tag (trigger=tag_push).
code=$(curl -s -o "${work}/both.json" -w '%{http_code}' -H "Authorization: Bearer ${tok}" -X PATCH -H 'content-type: application/json' \
  -d '{"followBranch":"main","followTagPattern":"v*-test.*"}' "${api}/repos/${OWNER}/${REPO}/apps/${APP}" || true)
if [ "${code}" != 422 ] || ! jq -e '.error.code == "follow_both"' "${work}/both.json" >/dev/null; then
  echo "KO: branch e regola sui tag insieme dovevano dare 422 follow_both, status ${code}: $(head -c 300 "${work}/both.json")" >&2; exit 1
fi
echo "OK: branch e regola sui tag insieme - 422 follow_both"
code=$(curl -s -o "${work}/follow.json" -w '%{http_code}' -H "Authorization: Bearer ${tok}" -X PATCH -H 'content-type: application/json' \
  -d '{"followBranch":null,"followTagPattern":"v*-test.*"}' "${api}/repos/${OWNER}/${REPO}/apps/${APP}" || true)
if [ "${code}" != 200 ] || ! jq -e '.follow.tagPattern == "v*-test.*" and (.follow.branch == null)' "${work}/follow.json" >/dev/null; then
  echo "KO: passaggio alla regola sui tag, status ${code}: $(head -c 300 "${work}/follow.json")" >&2; exit 1
fi
before_tag=$(versions | jq '.total')
cat >"${src}/.gitstack/workflows/image.yml" <<'EOF'
name: image
on: [push]
permissions:
  contents: read
  packages: write
jobs:
  image:
    runs-on: gitstack-build
    steps:
      - uses: actions/checkout@v4
      - run: |
          gitstack-build --name web --tag "sha-$(echo "$GITHUB_SHA" | cut -c1-7)" .
          gitstack-build --name web --tag v9.9 .
          gitstack-build --name web --tag v9.9-test.1 .
EOF
echo "versione tre" >"${src}/index.html"
sha3=$(push "terza versione: tre tag, uno solo rispetta la regola")
wait_run "${sha3}" "build del terzo commit"
if ! wait_serving '.trigger == "tag_push" and .image.verified == true and .state == "serving"'; then
  echo "KO: nessuna versione tag_push verificata e serving entro ${SERVE_TIMEOUT}s: $(versions | jq -c '[.items[]? | {number, trigger, state, stateReason, verified: .image.verified}]')" >&2; exit 1
fi
n=$(cat "${work}/ver")
if ! versions | jq -e --argjson n "${n}" '.items[] | select(.number == $n) | (.image.reference | endswith("/web:v9.9-test.1")) and .branch == null and (.commitSha != null)' >/dev/null; then
  echo "KO: la versione ${n} non nasce da v9.9-test.1, o manca il commitSha: $(versions | jq -c --argjson n "${n}" '.items[] | select(.number == $n)')" >&2; exit 1
fi
if [ "$(versions | jq '[.items[]? | select(.trigger == "tag_push")] | length')" != 1 ] || [ "$(versions | jq '.total')" != "$((before_tag + 1))" ]; then
  echo "KO: i tag che non rispettano la regola hanno creato versioni: $(versions | jq -c '[.items[]? | {number, trigger, image: .image.reference}]')" >&2; exit 1
fi
echo "OK: regola sui tag - versione ${n} da v9.9-test.1 serving, trigger tag_push, verified; sha-* e v9.9 ignorati"

# --- d. immagine non firmata -------------------------------------------------------------------
before=$(versions | jq -r '.total')
# Un tag pubblicato con il token di un utente, non da un job: il registry non lo firma e non
# dice niente a core. Immagine OCI minima (config vuoto, nessun layer) con curl.
cfg_digest="sha256:$(printf '{}' | sha256sum | cut -d' ' -f1)"
img="${OWNER}/${REPO}/web"
reg_auth="Authorization: Basic $(printf 'admin:%s' "${tok}" | base64 -w0)"
loc=$(curl -si -H "${reg_auth}" -X POST "${BASE_URL}/v2/${img}/blobs/uploads/" | tr -d '\r' | sed -n 's/^[Ll]ocation: //p')
if [ -z "${loc}" ]; then echo "KO: il registry non ha aperto l'upload del blob (nessun Location)" >&2; exit 1; fi
case "${loc}" in http*) ;; *) loc="${BASE_URL}${loc}" ;; esac
case "${loc}" in *\?*) sep='&' ;; *) sep='?' ;; esac
code=$(printf '{}' | curl -s -o /dev/null -w '%{http_code}' -H "${reg_auth}" -H 'content-type: application/octet-stream' -X PUT --data-binary @- "${loc}${sep}digest=${cfg_digest}" || true)
if [ "${code}" != 201 ]; then echo "KO: upload del blob di config: status ${code}" >&2; exit 1; fi
manifest="{\"schemaVersion\":2,\"mediaType\":\"application/vnd.oci.image.manifest.v1+json\",\"config\":{\"mediaType\":\"application/vnd.oci.image.config.v1+json\",\"digest\":\"${cfg_digest}\",\"size\":2},\"layers\":[]}"
code=$(printf '%s' "${manifest}" | curl -s -o "${work}/manifest.body" -w '%{http_code}' -H "${reg_auth}" -H 'content-type: application/vnd.oci.image.manifest.v1+json' -X PUT --data-binary @- "${BASE_URL}/v2/${img}/manifests/senza-firma" || true)
if [ "${code}" != 201 ]; then echo "KO: pubblicazione del tag senza-firma: status ${code}, corpo '$(head -c 200 "${work}/manifest.body")'" >&2; exit 1; fi
code=$(curl -s -o "${work}/pkgs2.json" -w '%{http_code}' -H "Authorization: Bearer ${tok}" "${api}/repos/${OWNER}/${REPO}/packages" || true)
if [ "${code}" != 200 ] || ! jq -e '[.. | objects | select((.tagDetails? // []) | any(.name == "senza-firma" and .signature == "unverified"))] | length >= 1' "${work}/pkgs2.json" >/dev/null; then
  echo "KO: il tag senza-firma non risulta pubblicato e unverified (status ${code}): $(head -c 400 "${work}/pkgs2.json")" >&2; exit 1
fi
sleep "${NEG_WAIT}"
after=$(versions | jq -r '.total')
if [ "${after}" != "${before}" ]; then
  echo "KO: l'immagine non firmata ha creato versioni: ${before} -> ${after}: $(versions | jq -c '[.items[]? | {number, trigger, image: .image.reference}]')" >&2; exit 1
fi
echo "OK: immagine non firmata - nessuna versione nuova"
echo "ESITO: ok"
