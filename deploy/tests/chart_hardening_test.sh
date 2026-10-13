#!/usr/bin/env bash
# Test dell'hardening del chart Helm (F8, ASVS V1.2.2, V1.14.1, V1.14.5; GIT-234):
# NetworkPolicy default-deny con allow espliciti, securityContext di ogni
# container e credenziali NATS. Renderizza il chart con `helm template`
# (nessun cluster, nessuna rete).
# Uso: ./deploy/tests/chart_hardening_test.sh
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART="${HERE}/../gitstack"

command -v helm >/dev/null 2>&1 || { echo "ERRORE: helm non trovato nel PATH" >&2; exit 2; }

PASS=0
FAILN=0
OUT=""
ok() { PASS=$((PASS + 1)); }
bad() { FAILN=$((FAILN + 1)); printf 'FALLITO: %s\n' "$1"; }
expect_eq() { if [ "$2" = "$3" ]; then ok; else bad "$1 (atteso '$3', trovato '$2')"; fi; }
expect_has() { case "$2" in *"$3"*) ok ;; *) bad "$1: manca '$3'" ;; esac; }
expect_lacks() { case "$2" in *"$3"*) bad "$1: non doveva esserci '$3'" ;; *) ok ;; esac; }

render() { helm template gitstack "${CHART}" "$@" | tr -d '\r'; }
# count PATTERN: righe del rendering che combaciano (grep -E).
count() { printf '%s\n' "${OUT}" | grep -c -E "$1" || true; }
# doc KIND NAME: il documento YAML di una risorsa (il testo fra due '---').
doc() {
  printf '%s\n' "${OUT}" | awk -v kind="$1" -v name="$2" '
    BEGIN { RS = "\n---\n" }
    $0 ~ "(^|\n)kind: " kind "\n" && $0 ~ "\n  name: " name "\n" { print; exit }'
}

# ---------------------------------------------------------------------------
echo "== NetworkPolicy and securityContext are rendered"
OUT="$(render)" || { bad "helm template di default fallito"; exit 1; }

for n in default-deny allow-dns web gateway core identity git nats postgres; do
  d="$(doc NetworkPolicy "gitstack-${n}")"
  if [ -n "${d}" ]; then ok; else bad "NetworkPolicy gitstack-${n} non renderizzata"; fi
done
d="$(doc NetworkPolicy gitstack-default-deny)"
expect_has "default-deny" "${d}" "- Ingress"
expect_has "default-deny" "${d}" "- Egress"
expect_lacks "default-deny" "${d}" "ingress:"
expect_lacks "default-deny" "${d}" "egress:"

# Nessuna policy lascia entrare da "qualunque posto" senza porte, tranne SSH.
d="$(doc NetworkPolicy gitstack-nats)"
expect_has "nats ingress da core" "${d}" "app.kubernetes.io/name: core"
expect_has "nats ingress da git" "${d}" "app.kubernetes.io/name: git"
expect_lacks "nats non da gateway" "${d}" "app.kubernetes.io/name: gateway"
d="$(doc NetworkPolicy gitstack-postgres)"
expect_has "postgres da core" "${d}" "app.kubernetes.io/name: core"
expect_has "postgres da identity" "${d}" "app.kubernetes.io/name: identity"
expect_lacks "postgres non da gateway" "${d}" "app.kubernetes.io/name: gateway"
d="$(doc NetworkPolicy gitstack-identity)"
expect_lacks "identity non da web" "${d}" "app.kubernetes.io/name: web"
d="$(doc NetworkPolicy gitstack-core)"
expect_has "core: uscita verso nats" "${d}" "app.kubernetes.io/name: nats"

# securityContext: ogni container (init compresi) ha le stesse quattro
# garanzie; ogni pod ha il profilo seccomp.
IMAGES="$(count '^ +image: ')"
if [ "${IMAGES}" -ge 8 ]; then ok; else bad "troppo pochi container nel rendering (${IMAGES})"; fi
expect_eq "allowPrivilegeEscalation: false su ogni container" "$(count '^ +allowPrivilegeEscalation: false$')" "${IMAGES}"
expect_eq "drop ALL su ogni container" "$(count '^ +- ALL$')" "${IMAGES}"
expect_eq "readOnlyRootFilesystem dichiarato su ogni container" "$(count '^ +readOnlyRootFilesystem: (true|false)$')" "${IMAGES}"
expect_lacks "nessun container privilegiato" "${OUT}" "privileged: true"
PODS="$(count '^      securityContext:$')"
expect_eq "seccomp RuntimeDefault su ogni pod" "$(count '^ +type: RuntimeDefault$')" "${PODS}"
if [ "${PODS}" -ge 7 ]; then ok; else bad "pod con securityContext: ${PODS}, attesi almeno 7"; fi
if [ "$(count '^ +readOnlyRootFilesystem: true$')" -ge 5 ]; then ok; else bad "filesystem radice in sola lettura troppo raro"; fi
# I servizi Go girano come utente non root numerico.
for c in gateway core identity git web; do
  d="$(doc Deployment "gitstack-${c}")"
  expect_has "${c}: runAsNonRoot" "${d}" "runAsNonRoot: true"
done

# Spenta, il chart non crea policy.
OUT="$(render --set networkPolicy.enabled=false)"
expect_eq "networkPolicy.enabled=false" "$(count '^kind: NetworkPolicy$')" "0"

# Con Postgres esterno: policy del pod client, uscita sulla porta del database.
OUT="$(render --set postgres.enabled=false --set postgres.external.host=db.example.com \
  --set postgres.external.database=gs --set postgres.external.username=gs \
  --set postgres.external.existingSecret=pg --set postgres.external.port=6432)"
d="$(doc NetworkPolicy gitstack-pgclient)"
expect_has "pgclient policy" "${d}" "port: 6432"
d="$(doc NetworkPolicy gitstack-core)"
expect_has "core: porta del database esterno" "${d}" "port: 6432"

# ---------------------------------------------------------------------------
echo "== NATS requires credentials"
OUT="$(render)" || { bad "helm template di default fallito"; exit 1; }

d="$(doc StatefulSet gitstack-nats)"
expect_has "nats --user" "${d}" '--user=$(NATS_USER)'
expect_has "nats --pass" "${d}" '--pass=$(NATS_PASSWORD)'
expect_has "nats env dal Secret" "${d}" "name: gitstack-nats-auth"

d="$(doc Secret gitstack-nats-auth)"
if [ -n "${d}" ]; then ok; else bad "Secret gitstack-nats-auth non renderizzato"; fi
expect_has "Secret NATS keep" "${d}" "helm.sh/resource-policy: keep"
expect_has "Secret NATS user" "${d}" "user: "
PW="$(printf '%s\n' "${d}" | sed -n 's/^  password: "\(.*\)"$/\1/p')"
if [ "${#PW}" -ge 32 ]; then ok; else bad "password NATS generata troppo corta (${#PW})"; fi

# Nessun client usa un URL senza credenziali.
expect_lacks "URL NATS senza credenziali" "${OUT}" "nats://gitstack-nats"
for c in core git; do
  d="$(doc Deployment "gitstack-${c}")"
  expect_has "${c}: NATS_USER" "${d}" "name: NATS_USER"
  expect_has "${c}: NATS_PASSWORD" "${d}" "name: NATS_PASSWORD"
  expect_has "${c}: URL con utente e password" "${d}" 'nats://$(NATS_USER):$(NATS_PASSWORD)@gitstack-nats:4222'
done
# Le variabili vanno dichiarate prima di essere espanse da $(VAR).
d="$(doc Deployment gitstack-core)"
first_user="$(printf '%s\n' "${d}" | grep -n 'name: NATS_USER' | head -n1 | cut -d: -f1)"
first_url="$(printf '%s\n' "${d}" | grep -n 'GITSTACK_CORE_NATS_URL' | head -n1 | cut -d: -f1)"
if [ -n "${first_user}" ] && [ -n "${first_url}" ] && [ "${first_user}" -lt "${first_url}" ]; then ok; else bad "core: NATS_USER deve precedere GITSTACK_CORE_NATS_URL"; fi
d="$(doc Deployment gitstack-git)"
first_user="$(printf '%s\n' "${d}" | grep -n 'name: NATS_USER' | head -n1 | cut -d: -f1)"
first_url="$(printf '%s\n' "${d}" | grep -n 'GITSTACK_GIT_NATS_URL' | head -n1 | cut -d: -f1)"
if [ -n "${first_user}" ] && [ -n "${first_url}" ] && [ "${first_user}" -lt "${first_url}" ]; then ok; else bad "git: NATS_USER deve precedere GITSTACK_GIT_NATS_URL"; fi

# Secret esistente: il chart non ne genera uno e i client usano quello.
OUT="$(render --set nats.auth.existingSecret=mio-nats)"
expect_eq "nessun Secret generato con existingSecret" "$(doc Secret gitstack-nats-auth)" ""
expect_has "client con il Secret dato" "$(doc Deployment gitstack-core)" "name: mio-nats"
expect_has "server con il Secret dato" "$(doc StatefulSet gitstack-nats)" "name: mio-nats"

echo
echo "Passati: ${PASS}  Falliti: ${FAILN}"
[ "${FAILN}" -eq 0 ]
