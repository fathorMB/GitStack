#!/usr/bin/env bash
# Test delle scelte sulla CI di deploy/install.sh (v2-a: N1, CI7, N4, N9, N7; GIT-285).
# Carica solo le funzioni (GITSTACK_INSTALL_LIB=1), ridefinisce hardware e kubectl:
# nessun root, nessuna VM. Uso: ./deploy/tests/ci_install_test.sh
# Le variabili assegnate qui sono lette dalle funzioni di install.sh (caricate con source).
# SC2317 (shellcheck <= 0.10) e SC2329 (>= 0.11): stub ridefiniti e chiamati da install.sh.
# shellcheck disable=SC2034,SC1091,SC2317,SC2329
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export GITSTACK_INSTALL_LIB=1
# shellcheck source=../install.sh disable=SC1091
. "${HERE}/../install.sh"
set +e +o pipefail

PASS=0
FAILN=0
OUT=""
RC=0

run() {
  OUT="$("$@" 2>&1)"
  RC=$?
}
ok() { PASS=$((PASS + 1)); }
bad() { FAILN=$((FAILN + 1)); printf 'FALLITO: %s\n--- output (rc=%s) ---\n%s\n---\n' "$1" "${RC}" "${OUT}"; }
expect_ok() { if [ "${RC}" -eq 0 ]; then ok; else bad "$1: atteso rc=0"; fi; }
expect_fail() { if [ "${RC}" -ne 0 ]; then ok; else bad "$1: atteso rc!=0"; fi; }
expect_has() { case "${OUT}" in *"$2"*) ok ;; *) bad "$1: manca '$2'" ;; esac; }

T_CPU=8 T_RAM_GB=16 T_PREV=0 T_PREV_CAP=0
cpu_count() { printf '%s' "${T_CPU}"; }
mem_total_kib() { awk -v g="${T_RAM_GB}" 'BEGIN { printf "%d", g * 1000000000 / 1024 }'; }
ci_previously_enabled() { [ "${T_PREV}" = 1 ]; }
ci_existing_capacity() { printf '%s' "${T_PREV_CAP}"; }

# sets: i valori Helm prodotti, uno per riga.
reset() {
  GITSTACK_CI="" GITSTACK_CI_CAPACITY="" GITSTACK_CI_ACTION_MIRROR="" GITSTACK_CI_BUILD=0
  GITSTACK_CI_STORAGE_SIZE="" GITSTACK_CI_S3_ENDPOINT="" GITSTACK_CI_S3_BUCKET=""
  GITSTACK_CI_S3_ACCESS_KEY="" GITSTACK_CI_S3_SECRET_KEY="" GITSTACK_CI_S3_REGION="" GITSTACK_CI_S3_PREFIX=""
  GITSTACK_RELEASE_NAME=gitstack GITSTACK_HTTP_PROXY="" GITSTACK_HTTPS_PROXY=""
  T_CPU=8 T_RAM_GB=16 T_PREV=0 T_PREV_CAP=0
}
sets() { printf '%s\n' "${CI_HELM_SETS[@]:-}"; }
# resolve in modo da poter leggere le variabili (nessuna subshell).
resolve() { resolve_ci >/dev/null 2>&1; RC=$?; OUT="$(sets)"; }

# 1. Prima installazione senza opzioni: CI spenta, nessun valore.
reset; resolve
if [ "${CI_ENABLED}" = 0 ] && [ -z "$(sets)" ]; then ok; else OUT="$(sets)"; bad "senza --ci la CI resta spenta"; fi

# 2. --ci sul profilo consigliato: 3 posti.
reset; GITSTACK_CI=1; resolve
if [ "${CI_ENABLED}" = 1 ] && [ "${CI_CAPACITY}" = 3 ]; then ok; else bad "profilo consigliato: 3 posti (trovati ${CI_CAPACITY})"; fi
expect_has "profilo consigliato: ci.enabled" "ci.enabled=true"
expect_has "profilo consigliato: ci.capacity" "ci.capacity=3"

# 3. --ci sul profilo minimo (4 vCPU, 8 GB): 1 posto.
reset; GITSTACK_CI=1; T_CPU=4 T_RAM_GB=8; resolve
if [ "${CI_CAPACITY}" = 1 ]; then ok; else bad "profilo minimo: 1 posto (trovati ${CI_CAPACITY})"; fi

# 4. Solo la CPU o solo la RAM al consigliato non bastano: 1 posto.
reset; GITSTACK_CI=1; T_CPU=8 T_RAM_GB=8; resolve
if [ "${CI_CAPACITY}" = 1 ]; then ok; else bad "8 vCPU ma 8 GB: 1 posto (trovati ${CI_CAPACITY})"; fi

# 5. --ci-capacity esplicito vince sul profilo; fuori da 1-3 si rifiuta.
reset; GITSTACK_CI=1; GITSTACK_CI_CAPACITY=2; T_CPU=4 T_RAM_GB=8; resolve
if [ "${CI_CAPACITY}" = 2 ]; then ok; else bad "--ci-capacity 2 (trovati ${CI_CAPACITY})"; fi
for v in 0 4 abc; do
  reset; GITSTACK_CI=1; GITSTACK_CI_CAPACITY="${v}"
  run resolve_ci
  expect_fail "--ci-capacity ${v} rifiutato"
  expect_has "--ci-capacity ${v}" "non valido"
done

# 6. Riesecuzione senza opzioni: CI già accesa si conserva, con i suoi posti.
reset; T_PREV=1 T_PREV_CAP=2; resolve
if [ "${CI_ENABLED}" = 1 ] && [ "${CI_CAPACITY}" = 2 ]; then ok; else bad "riesecuzione: CI e posti conservati (CI=${CI_ENABLED} posti=${CI_CAPACITY})"; fi

# 7. --no-ci spegne anche una CI accesa.
reset; T_PREV=1 T_PREV_CAP=2; GITSTACK_CI=0; resolve
if [ "${CI_ENABLED}" = 0 ]; then ok; else bad "--no-ci spegne la CI"; fi

# 8. Opzioni --ci-* con la CI spenta: errore chiaro.
reset; GITSTACK_CI_CAPACITY=2
run resolve_ci
expect_fail "--ci-capacity senza --ci"
expect_has "--ci-capacity senza --ci" "servono con --ci"

# 9. Mirror delle action: valore Helm; con un IPv4 anche la regola di uscita.
reset; GITSTACK_CI=1; GITSTACK_CI_ACTION_MIRROR="https://10.20.0.5:8443/actions"; resolve
expect_has "mirror: ci.runner.actionMirror" "ci.runner.actionMirror=https://10.20.0.5:8443/actions"
expect_has "mirror IPv4: cidr" "ci.runner.actionMirrorEgress[0].to[0].ipBlock.cidr=10.20.0.5/32"
expect_has "mirror IPv4: porta" "ci.runner.actionMirrorEgress[0].ports[0].port=8443"
reset; GITSTACK_CI=1; GITSTACK_CI_ACTION_MIRROR="https://mirror.corp.example/"; resolve
case "${OUT}" in *actionMirrorEgress*) bad "mirror con nome: nessuna regola di uscita automatica" ;; *) ok ;; esac
reset; GITSTACK_CI=1; GITSTACK_CI_ACTION_MIRROR="ftp://x"
run resolve_ci
expect_fail "mirror con schema ftp rifiutato"

# 9b. Proxy aziendale: con un IPv4 la regola di uscita verso <ip>/32 e porta.
reset; GITSTACK_CI=1; GITSTACK_HTTPS_PROXY="http://10.0.0.5:3128"; resolve
expect_has "proxy IPv4: cidr" "ci.runner.proxyEgress[0].to[0].ipBlock.cidr=10.0.0.5/32"
expect_has "proxy IPv4: protocollo" "ci.runner.proxyEgress[0].ports[0].protocol=TCP"
expect_has "proxy IPv4: porta" "ci.runner.proxyEgress[0].ports[0].port=3128"
reset; GITSTACK_CI=1; GITSTACK_HTTP_PROXY="http://u:p@10.0.0.5:3128"; GITSTACK_HTTPS_PROXY="http://10.0.0.5:3128"; resolve
expect_has "proxy con credenziali: cidr" "ci.runner.proxyEgress[0].to[0].ipBlock.cidr=10.0.0.5/32"
case "${OUT}" in *proxyEgress\[1\]*) bad "proxy uguale: una sola regola" ;; *) ok ;; esac
reset; GITSTACK_CI=1; GITSTACK_HTTP_PROXY="http://10.0.0.5:3128"; GITSTACK_HTTPS_PROXY="http://10.0.0.6"; resolve
expect_has "due proxy: seconda regola" "ci.runner.proxyEgress[1].to[0].ipBlock.cidr=10.0.0.6/32"
expect_has "due proxy: porta predefinita" "ci.runner.proxyEgress[1].ports[0].port=80"
reset; GITSTACK_CI=1; GITSTACK_HTTPS_PROXY="http://proxy.corp.example:3128"; resolve
case "${OUT}" in *proxyEgress*) bad "proxy con nome: nessuna regola automatica" ;; *) ok ;; esac
reset; GITSTACK_HTTPS_PROXY="http://10.0.0.5:3128"; resolve
case "${OUT}" in *proxyEgress*) bad "senza --ci: nessuna regola" ;; *) ok ;; esac

# 10. S3: endpoint, bucket e chiavi insieme; il Secret è quello della release.
reset; GITSTACK_CI=1; GITSTACK_CI_S3_ENDPOINT=s3.corp.example; GITSTACK_CI_S3_BUCKET=ci-data
run resolve_ci
expect_fail "S3 senza chiavi rifiutato"
expect_has "S3 senza chiavi" "GITSTACK_CI_S3_ACCESS_KEY"
reset; GITSTACK_CI=1; GITSTACK_CI_S3_BUCKET=ci-data
run resolve_ci
expect_fail "S3 senza endpoint rifiutato"
reset; GITSTACK_CI=1; GITSTACK_CI_S3_ENDPOINT=s3.corp.example; GITSTACK_CI_S3_BUCKET=ci-data
GITSTACK_CI_S3_ACCESS_KEY=AK GITSTACK_CI_S3_SECRET_KEY=SK GITSTACK_CI_S3_REGION=eu-west-1 GITSTACK_CI_S3_PREFIX=gs/; resolve
expect_has "S3: bucket" "ci.storage.s3.bucket=ci-data"
expect_has "S3: Secret" "ci.storage.s3.existingSecret=gitstack-ci-s3"
expect_has "S3: regione" "ci.storage.s3.region=eu-west-1"
case "${OUT}" in *AK*|*SK*) bad "le chiavi non devono comparire nei valori Helm" ;; *) ok ;; esac

# 11. Dimensione del volume dei log e build rootless.
reset; GITSTACK_CI=1; GITSTACK_CI_STORAGE_SIZE=40Gi; GITSTACK_CI_BUILD=1; resolve
expect_has "volume: dimensione" "ci.storage.persistence.size=40Gi"
expect_has "build rootless" "ci.build.enabled=true"
reset; GITSTACK_CI=1; GITSTACK_CI_STORAGE_SIZE=molto
run resolve_ci
expect_fail "dimensione non valida rifiutata"

# 12. I valori entrano in helm_value_args prima di --set dell'utente.
reset; GITSTACK_CI=1; resolve_ci >/dev/null 2>&1
TLS_HELM_SETS=()
extra_helm_set=("ci.capacity=1")
helm_value_args sha-test
joined=" ${HELM_VALUE_ARGS[*]} "
case "${joined}" in *" ci.enabled=true "*) ok ;; *) OUT="${joined}"; bad "helm_value_args porta ci.enabled=true" ;; esac
a="${joined%%ci.capacity=3*}"
b="${joined%%ci.capacity=1*}"
if [ "${#a}" -lt "${#b}" ]; then ok; else OUT="${joined}"; bad "il --set dell'utente viene dopo quello dell'installer"; fi

# 13. Riepilogo.
reset; GITSTACK_CI=1; resolve_ci >/dev/null 2>&1
run print_ci_summary
expect_has "riepilogo con CI" "accesa, 3 job"
reset; resolve
run print_ci_summary
expect_has "riepilogo senza CI" "spenta"

printf 'ci_install_test: %s ok, %s falliti\n' "${PASS}" "${FAILN}"
[ "${FAILN}" -eq 0 ]
