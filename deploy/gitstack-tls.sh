#!/usr/bin/env bash
# gitstack-tls: certificati HTTPS di GitStack sull'host (N5, GIT-143).
#
# Lo installa deploy/install.sh in /usr/local/sbin/gitstack-tls e lo esegue un
# timer systemd ogni giorno (gitstack-tls-renew.timer). Gira come root.
#
# Modalità (TLS_MODE in /etc/gitstack/tls/tls.conf):
#   internal  CA interna generata qui + certificato per i nomi e gli IP indicati.
#             La chiave della CA (ca.key) resta SOLO sull'host, 0600, mai in un
#             Secret. In Kubernetes vanno solo il certificato del server (Secret
#             kubernetes.io/tls) e il certificato pubblico della CA (ConfigMap,
#             servito da /downloads/ca.crt).
#   custom    certificato e chiave del cliente (custom.crt/custom.key): li
#             convalida e li pubblica nel Secret; il rinnovo e' del cliente, che
#             sostituisce i file e rilancia `gitstack-tls ensure` (o aspetta il timer).
#
# Uso:
#   gitstack-tls ensure [opzioni]   crea/rinnova se serve e pubblica (idempotente)
#   gitstack-tls renew              come ensure, per il timer
#   gitstack-tls status             scadenze e impronte, senza modificare niente
# Opzioni di ensure (le salva in tls.conf; senza opzioni si usa tls.conf):
#   --mode internal|custom   --names a,b,c   --ips 1.2.3.4,...
#   --release NOME  --namespace NS  --kubeconfig FILE
#   --secret NOME   --ca-configmap NOME
#   --days N (validita' del certificato, default 397)  --renew-before N (default 30)
set -euo pipefail
umask 077

TLS_DIR="${GITSTACK_TLS_DIR:-/etc/gitstack/tls}"
CONF="${TLS_DIR}/tls.conf"

TLS_MODE="internal"
TLS_NAMES=""
TLS_IPS=""
RELEASE="gitstack"
NAMESPACE="default"
KUBECONFIG_FILE="/etc/rancher/k3s/k3s.yaml"
SECRET_NAME=""
CA_CONFIGMAP=""
CERT_DAYS=397
RENEW_BEFORE_DAYS=30
CA_DAYS=3650

log() { printf '==> %s\n' "$*"; }
warn() { printf 'ATTENZIONE: %s\n' "$*" >&2; }
fail() { printf 'ERRORE: %s\n' "$*" >&2; exit 1; }

# Un solo trap EXIT per tutto lo script, con un array di file temporanei.
TMP_FILES=()
cleanup() {
  local f
  for f in "${TMP_FILES[@]:-}"; do
    if [ -n "${f}" ]; then rm -rf "${f}"; fi
  done
}
trap cleanup EXIT

load_conf() {
  [ -f "${CONF}" ] || return 0
  local key val
  while IFS='=' read -r key val; do
    case "${key}" in
      TLS_MODE) TLS_MODE="${val}" ;;
      TLS_NAMES) TLS_NAMES="${val}" ;;
      TLS_IPS) TLS_IPS="${val}" ;;
      RELEASE) RELEASE="${val}" ;;
      NAMESPACE) NAMESPACE="${val}" ;;
      KUBECONFIG_FILE) KUBECONFIG_FILE="${val}" ;;
      SECRET_NAME) SECRET_NAME="${val}" ;;
      CA_CONFIGMAP) CA_CONFIGMAP="${val}" ;;
      CERT_DAYS) CERT_DAYS="${val}" ;;
      RENEW_BEFORE_DAYS) RENEW_BEFORE_DAYS="${val}" ;;
      *) ;;
    esac
  done <"${CONF}"
}

# Non si fa mai "source" del file: si leggono solo le chiavi note (sopra).
save_conf() {
  mkdir -p "${TLS_DIR}"
  chmod 0700 "${TLS_DIR}"
  local tmp
  tmp="$(mktemp "${TLS_DIR}/.tls.conf.XXXXXX")"
  TMP_FILES+=("${tmp}")
  cat >"${tmp}" <<EOF
TLS_MODE=${TLS_MODE}
TLS_NAMES=${TLS_NAMES}
TLS_IPS=${TLS_IPS}
RELEASE=${RELEASE}
NAMESPACE=${NAMESPACE}
KUBECONFIG_FILE=${KUBECONFIG_FILE}
SECRET_NAME=${SECRET_NAME}
CA_CONFIGMAP=${CA_CONFIGMAP}
CERT_DAYS=${CERT_DAYS}
RENEW_BEFORE_DAYS=${RENEW_BEFORE_DAYS}
EOF
  chmod 0600 "${tmp}"
  mv -f "${tmp}" "${CONF}"
}

kubectl_() {
  KUBECONFIG="${KUBECONFIG_FILE}" k3s kubectl "$@"
}

# Valida nomi e IP prima di metterli nel certificato: finiscono in un file di
# estensioni openssl, quindi niente caratteri imprevisti.
validate_sans() {
  local n
  [ -n "${TLS_NAMES}${TLS_IPS}" ] || fail "nessun nome host o IP per il certificato (--names/--ips)."
  local IFS=','
  for n in ${TLS_NAMES}; do
    [ -z "${n}" ] && continue
    printf '%s' "${n}" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$' \
      || fail "nome host non valido per il certificato: '${n}'."
  done
  for n in ${TLS_IPS}; do
    [ -z "${n}" ] && continue
    printf '%s' "${n}" | grep -Eq '^([0-9]{1,3}\.){3}[0-9]{1,3}$|^[0-9A-Fa-f:]+:[0-9A-Fa-f:]*$' \
      || fail "indirizzo IP non valido per il certificato: '${n}'."
  done
}

# Elenco SAN nel formato di openssl: DNS:a,DNS:b,IP:1.2.3.4. Include sempre
# localhost e 127.0.0.1 (prove locali sull'host).
san_list() {
  local out="" n
  local IFS=','
  for n in ${TLS_NAMES}; do
    [ -n "${n}" ] && out="${out:+${out},}DNS:${n}"
  done
  for n in ${TLS_IPS}; do
    [ -n "${n}" ] && out="${out:+${out},}IP:${n}"
  done
  printf '%s' "${out},DNS:localhost,IP:127.0.0.1"
}

ensure_ca() {
  if [ -f "${TLS_DIR}/ca.key" ] && [ -f "${TLS_DIR}/ca.crt" ]; then
    if ! openssl x509 -in "${TLS_DIR}/ca.crt" -noout -checkend $((90 * 86400)) >/dev/null; then
      warn "la CA interna scade tra meno di 90 giorni (o e' gia' scaduta): vedi docs/tls.md, sezione «Rotazione della CA»."
    fi
    return 0
  fi
  log "Genero la CA interna (chiave in ${TLS_DIR}/ca.key, solo root) ..."
  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "${TLS_DIR}/ca.key" 2>/dev/null
  chmod 0600 "${TLS_DIR}/ca.key"
  openssl req -x509 -new -key "${TLS_DIR}/ca.key" -sha256 -days "${CA_DAYS}" \
    -subj "/O=GitStack/CN=GitStack internal CA ($(hostname -s 2>/dev/null || echo host))" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -addext "subjectKeyIdentifier=hash" \
    -out "${TLS_DIR}/ca.crt"
  chmod 0644 "${TLS_DIR}/ca.crt"
}

# Il certificato del server va rifatto se manca, se scade entro RENEW_BEFORE_DAYS,
# se non e' firmato dalla CA corrente o se l'elenco dei SAN e' cambiato.
server_cert_needs_issue() {
  local crt="${TLS_DIR}/server.crt" key="${TLS_DIR}/server.key"
  [ -f "${crt}" ] && [ -f "${key}" ] || return 0
  [ -f "${TLS_DIR}/server.sans" ] || return 0
  [ "$(cat "${TLS_DIR}/server.sans")" = "$(san_list)" ] || return 0
  openssl x509 -in "${crt}" -noout -checkend $((RENEW_BEFORE_DAYS * 86400)) >/dev/null || return 0
  openssl verify -CAfile "${TLS_DIR}/ca.crt" "${crt}" >/dev/null 2>&1 || return 0
  return 1
}

issue_server_cert() {
  local work
  work="$(mktemp -d "${TLS_DIR}/.issue.XXXXXX")"
  TMP_FILES+=("${work}")
  local sans cn
  sans="$(san_list)"
  cn="${TLS_NAMES%%,*}"
  [ -n "${cn}" ] || cn="${TLS_IPS%%,*}"
  log "Emetto il certificato del server (${CERT_DAYS} giorni): ${sans}"
  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "${work}/server.key" 2>/dev/null
  openssl req -new -key "${work}/server.key" -subj "/O=GitStack/CN=${cn}" -out "${work}/server.csr"
  cat >"${work}/ext.cnf" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=serverAuth
subjectAltName=${sans}
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid
EOF
  openssl x509 -req -in "${work}/server.csr" -CA "${TLS_DIR}/ca.crt" -CAkey "${TLS_DIR}/ca.key" \
    -set_serial "0x$(openssl rand -hex 16)" -days "${CERT_DAYS}" -sha256 \
    -extfile "${work}/ext.cnf" -out "${work}/server.crt" 2>/dev/null
  openssl verify -CAfile "${TLS_DIR}/ca.crt" "${work}/server.crt" >/dev/null \
    || fail "il certificato appena emesso non si verifica con la CA."
  # Prima il certificato e la chiave, poi l'elenco dei SAN: se si interrompe a
  # meta', la prossima esecuzione rifa' tutto.
  chmod 0600 "${work}/server.key"
  mv -f "${work}/server.key" "${TLS_DIR}/server.key"
  mv -f "${work}/server.crt" "${TLS_DIR}/server.crt"
  printf '%s' "${sans}" >"${TLS_DIR}/server.sans"
}

# Certificato del cliente: stesso controllo di una chiave che non c'entra o di
# un certificato scaduto, che altrimenti Traefik servirebbe in silenzio.
check_custom() {
  local crt="${TLS_DIR}/custom.crt" key="${TLS_DIR}/custom.key"
  [ -f "${crt}" ] && [ -f "${key}" ] || fail "certificato del cliente mancante: servono ${crt} e ${key}."
  openssl x509 -in "${crt}" -noout >/dev/null 2>&1 || fail "${crt} non e' un certificato PEM valido."
  local a b
  a="$(openssl x509 -in "${crt}" -noout -pubkey | openssl pkey -pubin -outform DER | openssl dgst -sha256)"
  b="$(openssl pkey -in "${key}" -pubout -outform DER | openssl dgst -sha256)" \
    || fail "${key} non e' una chiave privata PEM valida (con passphrase non e' supportata)."
  [ "${a}" = "${b}" ] || fail "la chiave ${key} non corrisponde al certificato ${crt}."
  if ! openssl x509 -in "${crt}" -noout -checkend 0 >/dev/null; then
    fail "il certificato ${crt} e' scaduto."
  fi
  if ! openssl x509 -in "${crt}" -noout -checkend $((RENEW_BEFORE_DAYS * 86400)) >/dev/null; then
    warn "il certificato del cliente scade tra meno di ${RENEW_BEFORE_DAYS} giorni: sostituisci ${crt} e ${key} e rilancia 'gitstack-tls ensure'."
  fi
  local n
  local IFS=','
  for n in ${TLS_NAMES}; do
    [ -z "${n}" ] && continue
    openssl x509 -in "${crt}" -noout -checkhost "${n}" 2>/dev/null | grep -q 'does match' \
      || warn "il certificato del cliente non copre il nome '${n}' (SAN): i client lo rifiuteranno."
  done
  for n in ${TLS_IPS}; do
    [ -z "${n}" ] && continue
    openssl x509 -in "${crt}" -noout -checkip "${n}" 2>/dev/null | grep -q 'does match' \
      || warn "il certificato del cliente non copre l'IP ${n} (SAN): accedendo per IP i client lo rifiuteranno."
  done
}

ensure_namespace() {
  kubectl_ create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl_ apply -f - >/dev/null
}

# Pubblica il certificato in Kubernetes solo se e' cambiato (confronto con il
# Secret esistente): Traefik rilegge il Secret da solo, nessun riavvio.
publish() {
  local crt="$1" key="$2"
  local current
  current="$(kubectl_ -n "${NAMESPACE}" get secret "${SECRET_NAME}" -o jsonpath='{.data.tls\.crt}' 2>/dev/null || true)"
  local new
  new="$(base64 -w0 <"${crt}")"
  if [ "${current}" != "${new}" ]; then
    kubectl_ -n "${NAMESPACE}" create secret tls "${SECRET_NAME}" --cert="${crt}" --key="${key}" \
      --dry-run=client -o yaml | kubectl_ apply -f - >/dev/null
    log "Secret ${NAMESPACE}/${SECRET_NAME} aggiornato."
  else
    log "Secret ${NAMESPACE}/${SECRET_NAME} gia' aggiornato."
  fi
  if [ "${TLS_MODE}" = "internal" ] && [ -n "${CA_CONFIGMAP}" ]; then
    kubectl_ -n "${NAMESPACE}" create configmap "${CA_CONFIGMAP}" --from-file=ca.crt="${TLS_DIR}/ca.crt" \
      --dry-run=client -o yaml | kubectl_ apply -f - >/dev/null
    log "ConfigMap ${NAMESPACE}/${CA_CONFIGMAP} (certificato della CA) aggiornato."
  fi
}

fingerprint() {
  openssl x509 -in "$1" -noout -fingerprint -sha256 | sed 's/^.*=//'
}

cmd_ensure() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --mode) TLS_MODE="$2"; shift 2 ;;
      --names) TLS_NAMES="$2"; shift 2 ;;
      --ips) TLS_IPS="$2"; shift 2 ;;
      --release) RELEASE="$2"; shift 2 ;;
      --namespace) NAMESPACE="$2"; shift 2 ;;
      --kubeconfig) KUBECONFIG_FILE="$2"; shift 2 ;;
      --secret) SECRET_NAME="$2"; shift 2 ;;
      --ca-configmap) CA_CONFIGMAP="$2"; shift 2 ;;
      --days) CERT_DAYS="$2"; shift 2 ;;
      --renew-before) RENEW_BEFORE_DAYS="$2"; shift 2 ;;
      *) fail "opzione sconosciuta: $1" ;;
    esac
  done
  case "${TLS_MODE}" in
    internal|custom) ;;
    *) fail "modalita' TLS non supportata da gitstack-tls: '${TLS_MODE}' (internal|custom)." ;;
  esac
  case "${CERT_DAYS}${RENEW_BEFORE_DAYS}" in
    ''|*[!0-9]*) fail "--days e --renew-before vogliono numeri interi." ;;
  esac
  [ -n "${SECRET_NAME}" ] || SECRET_NAME="${RELEASE}-tls"
  command -v openssl >/dev/null 2>&1 || fail "serve 'openssl'."
  command -v k3s >/dev/null 2>&1 || fail "serve k3s (usa k3s kubectl)."
  mkdir -p "${TLS_DIR}"
  chmod 0700 "${TLS_DIR}"
  validate_sans

  if [ "${TLS_MODE}" = "internal" ]; then
    ensure_ca
    if server_cert_needs_issue; then
      issue_server_cert
    else
      log "Il certificato del server e' valido: niente da rinnovare."
    fi
    ensure_namespace
    publish "${TLS_DIR}/server.crt" "${TLS_DIR}/server.key"
  else
    check_custom
    ensure_namespace
    publish "${TLS_DIR}/custom.crt" "${TLS_DIR}/custom.key"
  fi
  save_conf
}

cmd_status() {
  load_conf
  printf 'Modalita'"'"': %s\nNomi: %s\nIP: %s\n' "${TLS_MODE}" "${TLS_NAMES}" "${TLS_IPS}"
  local f
  for f in ca.crt server.crt custom.crt; do
    [ -f "${TLS_DIR}/${f}" ] || continue
    printf '%s: scade %s, SHA-256 %s\n' "${f}" \
      "$(openssl x509 -in "${TLS_DIR}/${f}" -noout -enddate | sed 's/^notAfter=//')" "$(fingerprint "${TLS_DIR}/${f}")"
  done
}

main() {
  [ "$(id -u)" -eq 0 ] || fail "servono i permessi di root (sudo gitstack-tls ...)."
  local cmd="${1:-}"
  [ "$#" -gt 0 ] && shift
  case "${cmd}" in
    ensure) load_conf; cmd_ensure "$@" ;;
    renew) load_conf; [ -f "${CONF}" ] || fail "${CONF} non esiste: l'installazione non e' stata fatta con HTTPS."; cmd_ensure "$@" ;;
    status) cmd_status ;;
    fingerprint) fingerprint "${TLS_DIR}/ca.crt" ;;
    *) fail "uso: gitstack-tls ensure|renew|status|fingerprint" ;;
  esac
}

main "$@"
