#!/usr/bin/env bash
# Test delle metriche di Traefik per le app (H12, GIT-403): la HelmChartConfig `traefik` che scrive
# l'installer (entrypoint `metrics` solo interno) e la NetworkPolicy del chart da core a Traefik.
# Nessun cluster: install.sh caricato con source e `helm template`.
# Uso: ./deploy/tests/traefik_metrics_test.sh
# shellcheck disable=SC2034,SC1091,SC2317,SC2329
set -u
export MSYS2_ARG_CONV_EXCL="*"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART="${HERE}/../gitstack"
HAVE_HELM=1
command -v helm >/dev/null 2>&1 || HAVE_HELM=0
export GITSTACK_INSTALL_LIB=1
# shellcheck source=../install.sh disable=SC1091
. "${HERE}/../install.sh"
set +e +o pipefail

WORK="$(mktemp -d)"
if command -v cygpath >/dev/null 2>&1; then WORK="$(cygpath -m "${WORK}")"; fi
trap 'rm -rf "${WORK}"' EXIT

PASS=0
FAILN=0
ok() { PASS=$((PASS + 1)); }
bad() { FAILN=$((FAILN + 1)); printf 'FALLITO: %s\n' "$1"; }
expect_has() { case "$2" in *"$3"*) ok ;; *) bad "$1: manca '$3'" ;; esac; }
expect_lacks() { case "$2" in *"$3"*) bad "$1: non doveva esserci '$3'" ;; *) ok ;; esac; }
render() { helm template gitstack "${CHART}" "$@" | tr -d '\r'; }
doc() {
  printf '%s\n' "$1" | awk -v kind="$2" -v name="$3" '
    BEGIN { RS = "\n---\n" }
    $0 ~ "(^|\n)kind: " kind "\n" && $0 ~ "\n  name: " name "\n" && !done { print; done = 1 }'
}

# --- HelmChartConfig scritta dall'installer ------------------------------------
K3S_MANIFESTS_DIR="${WORK}/manifests"
TRAEFIK_MANIFEST="${K3S_MANIFESTS_DIR}/gitstack-traefik.yaml"
OLD_ACME_MANIFEST="${K3S_MANIFESTS_DIR}/gitstack-traefik-letsencrypt.yaml"
GITSTACK_TLS_EMAIL="admin@example.com"
GITSTACK_ACME_CA_SERVER=""
PUBLIC_HOST="git.example.com"
mkdir -p "${K3S_MANIFESTS_DIR}"
echo "vecchio" >"${OLD_ACME_MANIFEST}"

write_traefik_manifest "${CHART}" no >/dev/null 2>&1
m="$(cat "${TRAEFIK_MANIFEST}" 2>/dev/null)"
echo "== HelmChartConfig traefik (TLS interno)"
expect_has "kind" "${m}" "kind: HelmChartConfig"
expect_has "nome" "${m}" "  name: traefik"
expect_has "namespace kube-system" "${m}" "  namespace: kube-system"
expect_has "prometheus sull'entrypoint metrics" "${m}" "    metrics:
      prometheus:
        entryPoint: metrics"
expect_has "etichette per service" "${m}" "addServicesLabels: true"
expect_has "Service headless delle metriche" "${m}" "        service:
          enabled: true"
expect_has "porta 9100" "${m}" "port: 9100"
# Solo interno: nessuna esposizione dell'entrypoint metrics sul Service LoadBalancer.
expect_lacks "metrics non esposto" "${m}" "expose"
expect_lacks "metrics non e' un hostPort" "${m}" "hostPort"
expect_lacks "niente resolver senza letsencrypt" "${m}" "certificatesResolvers"
if [ -e "${OLD_ACME_MANIFEST}" ]; then bad "il vecchio file del solo Let's Encrypt va tolto (stesso oggetto traefik)"; else ok; fi
if command -v ruby >/dev/null 2>&1; then
  if ruby -ryaml -e 'YAML.load_file(ARGV[0]); v = YAML.load(YAML.load_file(ARGV[0])["spec"]["valuesContent"]); exit(v.dig("metrics","prometheus","entryPoint") == "metrics" ? 0 : 1)' "${TRAEFIK_MANIFEST}"; then ok; else bad "valuesContent non e' YAML valido"; fi
fi

write_traefik_manifest "${CHART}" yes >/dev/null 2>&1
m="$(cat "${TRAEFIK_MANIFEST}")"
echo "== HelmChartConfig traefik (Let's Encrypt): un solo oggetto con metriche e resolver"
expect_has "metriche presenti" "${m}" "entryPoint: metrics"
expect_has "resolver presente" "${m}" "certificatesResolvers:"
expect_has "email" "${m}" "email: admin@example.com"
expect_has "storage acme" "${m}" "storage: /data/acme.json"
n="$(grep -c '^kind: HelmChartConfig' "${TRAEFIK_MANIFEST}")"
if [ "${n}" = 1 ]; then ok; else bad "attesa una sola HelmChartConfig, trovate ${n}"; fi

# --- Una sola configurazione ACME (GIT-459) ----------------------------------------------
# Con le app accese un'email ACME (o un server di prova) fa scrivere il resolver anche con TLS interno,
# e il chart passa a core lo stesso nome (apps.acme.resolver) che l'host usa per ingress.tls.certResolver.
echo "== resolver ACME condiviso fra host e domini delle app"
TLS_DIR="${WORK}/tls"
mkdir -p "${TLS_DIR}"
log() { :; }
APPS_DOMAIN="apps.example.com"
GITSTACK_TLS_MODE="internal"
GITSTACK_ENABLE_APPS=1
GITSTACK_TLS_EMAIL=""
GITSTACK_ACME_CA_SERVER=""
resolve_apps_enabled >/dev/null 2>&1
if [ "${ACME_ENABLED}" = no ]; then ok; else bad "TLS interno, app accese, nessuna email: niente resolver"; fi
GITSTACK_TLS_EMAIL="admin@example.com"
resolve_apps_enabled >/dev/null 2>&1
if [ "${ACME_ENABLED}" = yes ]; then ok; else bad "TLS interno, app accese, con email: serve il resolver"; fi
expect_has "core riceve il nome del resolver" "${APPS_HELM_SETS[*]}" "apps.acme.resolver=${ACME_RESOLVER_NAME}"
expect_has "core riceve l'email" "${APPS_HELM_SETS[*]}" "apps.acme.email=admin@example.com"
GITSTACK_ENABLE_APPS=0
resolve_apps_enabled >/dev/null 2>&1
if [ "${ACME_ENABLED}" = no ]; then ok; else bad "app spente con TLS interno: niente resolver"; fi
GITSTACK_TLS_MODE="letsencrypt"
resolve_apps_enabled >/dev/null 2>&1
if [ "${ACME_ENABLED}" = yes ]; then ok; else bad "--tls letsencrypt: resolver sempre"; fi
GITSTACK_ENABLE_APPS=1
GITSTACK_TLS_MODE="internal"
GITSTACK_ACME_CA_SERVER="https://pebble.pebble.svc:14000/dir"
resolve_apps_enabled >/dev/null 2>&1
write_traefik_manifest "${CHART}" "${ACME_ENABLED}" >/dev/null 2>&1
m="$(cat "${TRAEFIK_MANIFEST}")"
expect_has "resolver col nome condiviso" "${m}" "      ${ACME_RESOLVER_NAME}:
        acme:"
expect_has "server ACME configurabile (Pebble)" "${m}" "caServer: https://pebble.pebble.svc:14000/dir"
expect_has "archivio persistente" "${m}" "storage: /data/acme.json"

# --- NetworkPolicy core -> Traefik -----------------------------------------------
# In CI helm c e sempre (come per chart_hardening_test.sh); su una macchina senza helm si prova solo la prima parte.
if [ "${HAVE_HELM}" = 0 ]; then
  echo "SALTATO: helm non trovato, NetworkPolicy non provata qui"
  echo "Passati: ${PASS}  Falliti: ${FAILN}"
  [ "${FAILN}" -eq 0 ]
  exit
fi
echo "== NetworkPolicy core -> Traefik"
OUT="$(render --namespace prova-ns --set apps.enabled=true --set apps.domain=apps.acme.test)" || { bad "helm template con apps.enabled fallito"; exit 1; }
d="$(doc "${OUT}" NetworkPolicy gitstack-core-traefik-metrics)"
if [ -z "${d}" ]; then bad "NetworkPolicy gitstack-core-traefik-metrics non renderizzata"; fi
expect_has "pod di core" "${d}" "app.kubernetes.io/name: core"
expect_has "solo uscita" "${d}" "- Egress"
expect_lacks "nessun ingresso" "${d}" "- Ingress"
expect_has "namespace di Traefik" "${d}" "kubernetes.io/metadata.name: kube-system"
expect_has "pod traefik" "${d}" "app.kubernetes.io/name: traefik"
expect_has "porta metrics" "${d}" "port: 9100"
expect_lacks "nessun ipBlock" "${d}" "ipBlock"
if [ "$(printf '%s\n' "${d}" | grep -c 'port:')" = 1 ]; then ok; else bad "una sola porta ammessa"; fi
if [ "$(printf '%s\n' "${d}" | grep -c -- '- to:')" = 1 ]; then ok; else bad "una sola destinazione ammessa"; fi
c="$(doc "${OUT}" Deployment gitstack-core)"
expect_has "core conosce le metriche di Traefik" "${c}" 'value: "http://traefik-metrics.kube-system.svc.cluster.local:9100/metrics"'

OUT="$(render --namespace prova-ns --set apps.enabled=true --set apps.domain=apps.acme.test --set apps.traefikMetrics.port=9200 --set networkPolicy.ingressNamespace=ingress)" || { bad "helm template con porta diversa fallito"; exit 1; }
d="$(doc "${OUT}" NetworkPolicy gitstack-core-traefik-metrics)"
expect_has "porta configurabile" "${d}" "port: 9200"
expect_has "namespace configurabile" "${d}" "kubernetes.io/metadata.name: ingress"
expect_has "URL segue porta e namespace" "$(doc "${OUT}" Deployment gitstack-core)" 'traefik-metrics.ingress.svc.cluster.local:9200/metrics'

OUT="$(render --namespace prova-ns)" || { bad "helm template di default fallito"; exit 1; }
if [ -n "$(doc "${OUT}" NetworkPolicy gitstack-core-traefik-metrics)" ]; then bad "app spente: nessuna uscita verso Traefik"; else ok; fi
expect_lacks "app spente: core senza URL delle metriche" "$(doc "${OUT}" Deployment gitstack-core)" "GITSTACK_CORE_APPS_TRAEFIK_METRICS_URL"
OUT="$(render --namespace prova-ns --set apps.enabled=true --set apps.domain=apps.acme.test --set networkPolicy.enabled=false)" || { bad "helm template senza policy fallito"; exit 1; }
if [ -n "$(doc "${OUT}" NetworkPolicy gitstack-core-traefik-metrics)" ]; then bad "networkPolicy.enabled=false: nessuna policy"; else ok; fi

echo "== chart: ACME dei domini delle app"
OUT="$(render --namespace prova-ns --set apps.enabled=true --set apps.domain=apps.acme.test --set apps.acme.resolver=letsencrypt --set apps.acme.email=a@example.com)" || { bad "helm template con apps.acme fallito"; exit 1; }
c="$(doc "${OUT}" Deployment gitstack-core)"
expect_has "resolver a core" "${c}" 'GITSTACK_CORE_APPS_ACME_RESOLVER'
expect_has "email a core" "${c}" 'value: "a@example.com"'
expect_has "indirizzo TLS di Traefik" "${c}" 'value: "traefik.kube-system.svc.cluster.local:443"'
d="$(doc "${OUT}" NetworkPolicy gitstack-core-traefik-metrics)"
expect_has "core legge il certificato sulla porta del pod (websecure)" "${d}" "port: websecure"
expect_lacks "non la porta 443 del Service" "${d}" "port: 443"
OUT="$(render --namespace prova-ns --set apps.enabled=true --set apps.domain=apps.acme.test)" || { bad "helm template senza acme fallito"; exit 1; }
expect_lacks "senza resolver core non riceve variabili ACME" "$(doc "${OUT}" Deployment gitstack-core)" "GITSTACK_CORE_APPS_ACME"
expect_lacks "senza resolver la policy non apre websecure" "$(doc "${OUT}" NetworkPolicy gitstack-core-traefik-metrics)" "websecure"

echo
echo "Passati: ${PASS}  Falliti: ${FAILN}"
[ "${FAILN}" -eq 0 ]
