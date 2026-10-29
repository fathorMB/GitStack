#!/bin/bash
# App pubbliche e domini propri con Pebble (v3-c, GIT-468, H1/H2), sulla VM.
# Lo lancia e2e.ps1 (lib/phases.ps1, Test-E2eAppPublic) con questi comandi. Ognuno stampa
# "STEP ok <nome>" o "STEP FAIL <nome>: <motivo>" (exit 1); niente `set -e`: i controlli negativi
# si scrivono con if ...; then fail ...; fi, mai con `!`.
#
#   pebble-up      Pebble (server ACME di prova, ghcr.io/letsencrypt/pebble) in kube-system, con la CA di
#                  prova (minica) in un ConfigMap. Pebble risolve i nomi con il CoreDNS del cluster
#                  (-dnsserver <ClusterIP di kube-dns>:53), cioe' con la zona di dns-set.
#                  Variabile facoltativa: GS_PEBBLE_REF (default v2.7.0).
#   traefik-acme   Dopo l'installer con GITSTACK_ACME_CA_SERVER=https://pebble:14000/dir: aggiunge alla
#                  HelmChartConfig `traefik` scritta dall'installer l'env LEGO_CA_CERTIFICATES e il volume
#                  con la CA di Pebble (l'installer non li ha), aspetta che Traefik riparta e controlla che
#                  il resolver punti a Pebble. Idempotente.
#   dns-set        Il resolver DNS di prova: una zona nel CoreDNS del cluster (ConfigMap coredns-custom,
#                  plugin file) con <GS_PUB_DOMAIN> CNAME verso <GS_APP_LABEL>.<GS_APPS_DOMAIN> e questo A
#                  verso il ClusterIP di Traefik. Core e Pebble risolvono cosi' lo stesso nome. La zona
#                  e' il dominio delle app senza la prima etichetta (apps.e2e.test -> e2e.test): il
#                  dominio proprio deve starci dentro. Verifica con nslookup da un pod.
#   pebble-root    Scrive in GS_ROOT_OUT la radice di Pebble (GET /roots/0 sul Service, CA di prova).
#   domain-open    Senza credenziale e con la radice di Pebble (GS_ROOT), https://<GS_DOMAIN>/ risponde
#                  200 col corpo GS_EXPECT (--resolve verso 127.0.0.1: il nome non e' nel DNS dell'host).
#   domain-closed  https://<GS_DOMAIN>/ senza credenziale non da' piu' l'app: niente 200 con GS_EXPECT.
#   address-open   Senza credenziale, sull'indirizzo automatico (GS_APP_HOST, CA dell'istanza GS_CA)
#                  l'app pubblica risponde 200 col corpo GS_EXPECT.
#   cert-info      Il certificato servito da Traefik per GS_DOMAIN (SNI): fp=, issuer=, san=.
#   diag           Log di Pebble, Traefik e CoreDNS e i pod di kube-system.
#
# Un solo trap EXIT in testa.
set -u

W="$(mktemp -d)"
trap 'rm -rf "$W"' EXIT

KUBECONFIG_PATH="${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"
TRAEFIK_MANIFEST="${GS_TRAEFIK_MANIFEST:-/var/lib/rancher/k3s/server/manifests/gitstack-traefik.yaml}"
kc() { sudo -n env "KUBECONFIG=${KUBECONFIG_PATH}" k3s kubectl "$@"; }
fail() { echo "STEP FAIL $1: $2"; exit 1; }

# --- Pebble --------------------------------------------------------------------------------------
pebble_up() {
  local ref="${GS_PEBBLE_REF:-v2.7.0}" dns
  dns="$(kc -n kube-system get svc kube-dns -o jsonpath='{.spec.clusterIP}' 2>/dev/null)"
  if [ -z "$dns" ]; then fail pebble-up "Service kube-dns senza ClusterIP"; fi
  if ! curl -fsSL --max-time 60 "https://raw.githubusercontent.com/letsencrypt/pebble/${ref}/test/certs/pebble.minica.pem" -o "$W/minica.pem"; then
    fail pebble-up "download della CA di Pebble (${ref}) non riuscito"
  fi
  if ! grep -q 'BEGIN CERTIFICATE' "$W/minica.pem"; then fail pebble-up "la CA di Pebble scaricata non e' un certificato"; fi
  if ! kc -n kube-system create configmap pebble-ca --from-file=pebble.minica.pem="$W/minica.pem" --dry-run=client -o yaml | kc apply -f - >/dev/null; then
    fail pebble-up "ConfigMap pebble-ca"
  fi
  # Stessa forma di deploy/tests/acme_pebble_k3d_test.sh (provata in CI su k3d). Il certificato di
  # Pebble ha tra i SAN `pebble`: il resolver di Traefik lo chiama per nome corto (stesso namespace).
  if ! kc apply -f - >/dev/null <<EOF
apiVersion: v1
kind: ConfigMap
metadata: {name: pebble-config, namespace: kube-system}
data:
  pebble-config.json: |
    {"pebble": {"listenAddress": "0.0.0.0:14000", "managementListenAddress": "0.0.0.0:15000",
      "certificate": "test/certs/localhost/cert.pem", "privateKey": "test/certs/localhost/key.pem",
      "httpPort": 80, "tlsPort": 443, "ocspResponderURL": "", "externalAccountBindingRequired": false}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: pebble, namespace: kube-system}
spec:
  replicas: 1
  selector: {matchLabels: {app: pebble}}
  template:
    metadata: {labels: {app: pebble}}
    spec:
      containers:
        - name: pebble
          image: ghcr.io/letsencrypt/pebble:${ref#v}
          args: ["-config", "/test/config/pebble-config.json", "-dnsserver", "${dns}:53"]
          env: [{name: PEBBLE_VA_NOSLEEP, value: "1"}, {name: PEBBLE_WFE_NONCEREJECT, value: "0"}]
          volumeMounts: [{name: cfg, mountPath: /test/config}]
      volumes: [{name: cfg, configMap: {name: pebble-config}}]
---
apiVersion: v1
kind: Service
metadata: {name: pebble, namespace: kube-system}
spec: {selector: {app: pebble}, ports: [{name: acme, port: 14000}, {name: mgmt, port: 15000}]}
EOF
  then
    fail pebble-up "manifest di Pebble non applicato"
  fi
  if ! kc -n kube-system rollout status deployment/pebble --timeout=240s >/dev/null 2>&1; then
    fail pebble-up "Pebble non diventa pronto in 240 s: $(kc -n kube-system get pods -l app=pebble -o wide 2>&1 | tr '\n' ' ')"
  fi
  echo "STEP ok pebble-up"
}

# --- Traefik: fiducia nella CA di Pebble ----------------------------------------------------------
traefik_acme() {
  if ! sudo -n test -f "$TRAEFIK_MANIFEST"; then fail traefik-acme "manca $TRAEFIK_MANIFEST (l'installer non l'ha scritto)"; fi
  if ! sudo -n grep -q 'caServer: https://pebble:14000/dir' "$TRAEFIK_MANIFEST"; then
    fail traefik-acme "il resolver dell'installer non punta a Pebble: lancia l'installer con GITSTACK_ACME_CA_SERVER=https://pebble:14000/dir (le app accese)"
  fi
  if ! sudo -n grep -q 'LEGO_CA_CERTIFICATES' "$TRAEFIK_MANIFEST"; then
    if sudo -n grep -q -e '^    env:' -e '^    volumes:' "$TRAEFIK_MANIFEST"; then
      fail traefik-acme "la HelmChartConfig ha gia' env o volumes: la fiducia nella CA di Pebble va fusa a mano"
    fi
    # valuesContent e' l'ultima chiave del file: le chiavi nuove vanno in coda, con lo stesso rientro.
    if ! sudo -n tee -a "$TRAEFIK_MANIFEST" >/dev/null <<'EOF'
    env:
      - name: LEGO_CA_CERTIFICATES
        value: /pebble/pebble.minica.pem
    volumes:
      - name: pebble-ca
        mountPath: /pebble
        type: configMap
EOF
    then
      fail traefik-acme "scrittura di $TRAEFIK_MANIFEST"
    fi
  fi
  # k3s applica il file e l'helm-controller riscrive il Deployment: si aspetta l'env nuovo, poi il rollout.
  local seen="" _
  for _ in $(seq 1 60); do
    seen="$(kc -n kube-system get deploy traefik -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' 2>/dev/null)"
    case "$seen" in *LEGO_CA_CERTIFICATES*) break ;; esac
    seen=""
    sleep 5
  done
  if [ -z "$seen" ]; then fail traefik-acme "il Deployment traefik non ha LEGO_CA_CERTIFICATES dopo 300 s"; fi
  if ! kc -n kube-system rollout status deployment/traefik --timeout=300s >/dev/null 2>&1; then
    fail traefik-acme "Traefik non riparte in 300 s: $(kc -n kube-system get pods -l app.kubernetes.io/name=traefik -o wide 2>&1 | tr '\n' ' ')"
  fi
  local args
  args="$(kc -n kube-system get deploy traefik -o jsonpath='{.spec.template.spec.containers[0].args}' 2>/dev/null)"
  case "$args" in
    *certificatesresolvers.letsencrypt.acme.caserver=https://pebble:14000/dir*) ;;
    *) fail traefik-acme "gli argomenti di Traefik non hanno il caServer di Pebble: $args" ;;
  esac
  echo "STEP ok traefik-acme"
}

# --- Il resolver DNS di prova ----------------------------------------------------------------------
dns_set() {
  local apex tip
  if [ -z "${GS_APPS_DOMAIN:-}" ] || [ -z "${GS_APP_LABEL:-}" ] || [ -z "${GS_PUB_DOMAIN:-}" ]; then
    fail dns-set "servono GS_APPS_DOMAIN, GS_APP_LABEL e GS_PUB_DOMAIN"
  fi
  apex="${GS_APPS_DOMAIN#*.}"
  case "$apex" in *.*) ;; *) fail dns-set "il dominio delle app '${GS_APPS_DOMAIN}' ha troppe poche etichette per una zona di prova" ;; esac
  case "$GS_PUB_DOMAIN" in *".${apex}") ;; *) fail dns-set "il dominio proprio '${GS_PUB_DOMAIN}' deve stare nella zona ${apex}" ;; esac
  tip="$(kc -n kube-system get svc traefik -o jsonpath='{.spec.clusterIP}' 2>/dev/null)"
  if [ -z "$tip" ]; then fail dns-set "Service traefik senza ClusterIP"; fi
  {
    # shellcheck disable=SC2016  # $ORIGIN e $TTL sono direttive della zona, non variabili di shell
    printf '$ORIGIN %s.\n' "$apex"
    # shellcheck disable=SC2016
    printf '$TTL 30\n'
    printf '@\tIN\tSOA\tns.%s. hostmaster.%s. 1 30 30 30 30\n' "$apex" "$apex"
    printf '@\tIN\tNS\tns.%s.\n' "$apex"
    printf 'ns\tIN\tA\t127.0.0.1\n'
    printf '%s.\tIN\tA\t%s\n' "${GS_APP_LABEL}.${GS_APPS_DOMAIN}" "$tip"
    printf '*.%s.\tIN\tA\t%s\n' "$GS_APPS_DOMAIN" "$tip"
    printf '%s.\tIN\tCNAME\t%s.\n' "$GS_PUB_DOMAIN" "${GS_APP_LABEL}.${GS_APPS_DOMAIN}"
  } >"$W/zone.db"
  printf '%s:53 {\n    errors\n    file /etc/coredns/custom/%s.db\n}\n' "$apex" "$apex" >"$W/e2e-public.server"
  if ! kc -n kube-system create configmap coredns-custom --from-file=e2e-public.server="$W/e2e-public.server" --from-file="${apex}.db=$W/zone.db" --dry-run=client -o yaml | kc apply -f - >/dev/null; then
    fail dns-set "ConfigMap coredns-custom"
  fi
  if ! kc -n kube-system rollout restart deployment/coredns >/dev/null 2>&1; then fail dns-set "riavvio di CoreDNS"; fi
  if ! kc -n kube-system rollout status deployment/coredns --timeout=180s >/dev/null 2>&1; then
    fail dns-set "CoreDNS non riparte in 180 s: $(kc -n kube-system get pods -l k8s-app=kube-dns -o wide 2>&1 | tr '\n' ' ')"
  fi
  # Prova dal cluster, come la vedono core e Pebble: il dominio proprio deve arrivare al ClusterIP di Traefik.
  local out="" _
  for _ in $(seq 1 12); do
    kc -n kube-system delete pod gs-dnsprobe --ignore-not-found >/dev/null 2>&1
    if kc -n kube-system run gs-dnsprobe --restart=Never --image=docker.io/library/busybox:1.36 --image-pull-policy=IfNotPresent --command -- nslookup "$GS_PUB_DOMAIN" >/dev/null 2>&1 \
      && kc -n kube-system wait --for=jsonpath='{.status.phase}'=Succeeded pod/gs-dnsprobe --timeout=60s >/dev/null 2>&1; then
      out="$(kc -n kube-system logs gs-dnsprobe 2>/dev/null)"
      if printf '%s\n' "$out" | grep -qF "$tip"; then
        kc -n kube-system delete pod gs-dnsprobe --ignore-not-found >/dev/null 2>&1
        echo "STEP ok dns-set ${GS_PUB_DOMAIN} -> ${GS_APP_LABEL}.${GS_APPS_DOMAIN} -> ${tip}"
        return 0
      fi
    fi
    sleep 5
  done
  kc -n kube-system delete pod gs-dnsprobe --ignore-not-found >/dev/null 2>&1
  fail dns-set "nslookup ${GS_PUB_DOMAIN} dal cluster non da' ${tip}: $(printf '%s' "$out" | tr '\n' ' ')"
}

# --- Radice di Pebble -------------------------------------------------------------------------------
pebble_root() {
  local pip _
  if [ -z "${GS_ROOT_OUT:-}" ]; then fail pebble-root "serve GS_ROOT_OUT"; fi
  pip="$(kc -n kube-system get svc pebble -o jsonpath='{.spec.clusterIP}' 2>/dev/null)"
  if [ -z "$pip" ]; then fail pebble-root "Service pebble senza ClusterIP"; fi
  if ! kc -n kube-system get configmap pebble-ca -o jsonpath='{.data.pebble\.minica\.pem}' >"$W/minica.pem" 2>/dev/null; then
    fail pebble-root "lettura della CA di prova dal ConfigMap pebble-ca"
  fi
  for _ in $(seq 1 12); do
    if curl -fsS --max-time 15 --cacert "$W/minica.pem" --resolve "pebble:15000:${pip}" "https://pebble:15000/roots/0" -o "$GS_ROOT_OUT" 2>"$W/root.err" \
      && grep -q 'BEGIN CERTIFICATE' "$GS_ROOT_OUT"; then
      echo "STEP ok pebble-root"
      return 0
    fi
    sleep 5
  done
  fail pebble-root "GET /roots/0 su Pebble non riuscita: $(tr '\n' ' ' <"$W/root.err" 2>/dev/null)"
}

# --- Sonde HTTPS --------------------------------------------------------------------------------------
# curl_to <file-corpo> <host> <opzioni-di-fiducia...>: stampa il codice HTTP. Il nome non e' nel DNS dell'host:
# si porta alla 443 locale (Traefik).
curl_to() {
  local out="$1" host="$2"
  shift 2
  curl -sS --max-time 15 --resolve "${host}:443:127.0.0.1" -o "$out" -w '%{http_code}' "$@" "https://${host}/" 2>>"$W/curl.err"
}

# open_probe <passo> <host> <corpo-atteso> <opzioni-di-fiducia...>: 200 senza credenziale e corpo atteso, ritenta 120 s.
open_probe() {
  local step="$1" host="$2" expect="$3" code="000" _
  shift 3
  for _ in $(seq 1 24); do
    code="$(curl_to "$W/open.out" "$host" "$@")" || code="000"
    if [ "$code" = 200 ] && grep -q "^${expect}\$" "$W/open.out" 2>/dev/null; then
      echo "STEP ok ${step}"
      return 0
    fi
    sleep 5
  done
  fail "$step" "senza credenziale HTTP $code, corpo '$(head -c 200 "$W/open.out" 2>/dev/null | tr '\n' ' ')' (atteso 200 con '${expect}') $(tr '\n' ' ' <"$W/curl.err" 2>/dev/null)"
}

domain_open() {
  if [ -z "${GS_DOMAIN:-}" ] || [ -z "${GS_ROOT:-}" ] || [ -z "${GS_EXPECT:-}" ]; then fail domain-open "servono GS_DOMAIN, GS_ROOT e GS_EXPECT"; fi
  open_probe domain-open "$GS_DOMAIN" "$GS_EXPECT" --cacert "$GS_ROOT"
}

address_open() {
  if [ -z "${GS_APP_HOST:-}" ] || [ -z "${GS_CA:-}" ] || [ -z "${GS_EXPECT:-}" ]; then fail address-open "servono GS_APP_HOST, GS_CA e GS_EXPECT"; fi
  open_probe address-open "$GS_APP_HOST" "$GS_EXPECT" --cacert "$GS_CA"
}

# domain-closed: dopo lo spegnimento la rotta del dominio sparisce (Traefik risponde 404 col certificato di
# default, quindi -k): ritenta finche' non c'e' piu' ne' 200 ne' il corpo dell'app, fino a 120 s.
domain_closed() {
  local code="000" _
  if [ -z "${GS_DOMAIN:-}" ] || [ -z "${GS_EXPECT:-}" ]; then fail domain-closed "servono GS_DOMAIN e GS_EXPECT"; fi
  for _ in $(seq 1 24); do
    code="$(curl_to "$W/closed.out" "$GS_DOMAIN" -k)" || code="000"
    if [ "$code" != 200 ] && ! grep -q "${GS_EXPECT}" "$W/closed.out" 2>/dev/null; then
      echo "STEP ok domain-closed HTTP ${code}"
      return 0
    fi
    sleep 5
  done
  fail domain-closed "dopo lo spegnimento l'app risponde ancora senza login (HTTP $code, corpo '$(head -c 200 "$W/closed.out" 2>/dev/null | tr '\n' ' ')')"
}

# --- Certificato servito -----------------------------------------------------------------------------
cert_info() {
  local pem
  if [ -z "${GS_DOMAIN:-}" ]; then fail cert-info "serve GS_DOMAIN"; fi
  pem="$(echo | timeout 30 openssl s_client -connect 127.0.0.1:443 -servername "$GS_DOMAIN" 2>/dev/null | openssl x509 2>/dev/null)"
  if [ -z "$pem" ]; then fail cert-info "nessun certificato servito per ${GS_DOMAIN}"; fi
  echo "fp=$(printf '%s\n' "$pem" | openssl x509 -noout -fingerprint -sha256 | cut -d= -f2)"
  echo "issuer=$(printf '%s\n' "$pem" | openssl x509 -noout -issuer)"
  echo "san=$(printf '%s\n' "$pem" | openssl x509 -noout -ext subjectAltName 2>/dev/null | tr '\n' ' ')"
}

diag() {
  echo "== pod di kube-system"
  kc -n kube-system get pods -o wide 2>&1
  echo "== Pebble"
  kc -n kube-system logs deploy/pebble --tail=60 2>&1
  echo "== Traefik"
  kc -n kube-system logs deploy/traefik --tail=80 2>&1
  echo "== CoreDNS"
  kc -n kube-system logs deploy/coredns --tail=30 2>&1
  echo "== coredns-custom"
  kc -n kube-system get configmap coredns-custom -o yaml 2>&1
  return 0
}

case "${1:-}" in
  pebble-up) pebble_up ;;
  traefik-acme) traefik_acme ;;
  dns-set) dns_set ;;
  pebble-root) pebble_root ;;
  domain-open) domain_open ;;
  domain-closed) domain_closed ;;
  address-open) address_open ;;
  cert-info) cert_info ;;
  diag) diag ;;
  *) echo "uso: $0 pebble-up|traefik-acme|dns-set|pebble-root|domain-open|domain-closed|address-open|cert-info|diag" >&2; exit 2 ;;
esac
