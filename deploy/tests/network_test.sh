#!/usr/bin/env bash
# Test della parte «rete aziendale» di deploy/install.sh (N4, N9a; GIT-206):
# parsing delle opzioni, registries.yaml, drop-in di proxy per k3s, valori
# Helm di N9b, sezione network di config.yaml, preflight di rete. Nessun root,
# nessuna rete: le prove di raggiungibilità sono funzioni ridefinite.
# Uso: ./deploy/tests/network_test.sh
# shellcheck disable=SC2034,SC1091,SC2030,SC2031
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL="${HERE}/../install.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

PASS=0
FAILN=0
ok() { PASS=$((PASS + 1)); }
bad() { FAILN=$((FAILN + 1)); printf 'FALLITO: %s\n--- output ---\n%s\n---\n' "$1" "${OUT:-}"; }
expect_eq() { if [ "$2" = "$3" ]; then ok; else OUT="atteso: $3
trovato: $2"; bad "$1"; fi; }
expect_has() { case "$2" in *"$3"*) ok ;; *) OUT="$2"; bad "$1: manca '$3'" ;; esac; }
expect_lacks() { case "$2" in *"$3"*) OUT="$2"; bad "$1: non doveva esserci '$3'" ;; *) ok ;; esac; }

# CA di prova: un certificato autofirmato vero (CN=Corp Test CA, solo per i
# test), così net_check_ca_bundle lo accetta anche con openssl presente.
CA="${WORK}/corp-ca.pem"
cat >"${CA}" <<'PEM'
-----BEGIN CERTIFICATE-----
MIIC3zCCAcegAwIBAgIUZiOhraib7Akhwv+zNdunjgSV+eMwDQYJKoZIhvcNAQEL
BQAwFzEVMBMGA1UEAwwMQ29ycCBUZXN0IENBMCAXDTI2MTAxMjA0NTc1OVoYDzIx
MjYwOTE4MDQ1NzU5WjAXMRUwEwYDVQQDDAxDb3JwIFRlc3QgQ0EwggEiMA0GCSqG
SIb3DQEBAQUAA4IBDwAwggEKAoIBAQDMHaE3uZ4+o0fGkqhSRV+2ujMvcpQ/8L+P
Y0V/E/vLuXZAhp2ssJeOnklvC2OniMrO7GAOLyOmNzdZZH851O/VXjchLpXobj5Y
q5mVvmfYffz2eN3TKeB8kt0OqT+KuHpQFQJlZgHZLe61cHtLdpsn7ZEBrV19jRng
Y3VqXTCt1Zkl5ePlqBSR2c1Qsa/PAVyoHaPfoWS0GZsWXAk8hkBmboRfPbkddmxZ
5nwgOEtJqQZOs0Q34mc6PfQtsbaZcKrDeY3lpJo3drjVZaiaTyh8bS38Izz/Cv9r
q9p8gc43BTNJW4aih1pVH6hZKDluvukpwOtTGmIehO2VpzXSY3PLAgMBAAGjITAf
MB0GA1UdDgQWBBTFfyLCwtYaHyO9DU+jW2DfAsMMmDANBgkqhkiG9w0BAQsFAAOC
AQEAOUqwrT8ce7YcpWw7YKkyWt/hh6y528sG25OxBPhZ1VHM1HpvODOY5WZ+O/tW
hPW4XLf//sZhWLxf+lyIpey6Qvr3bRUiLApNtpn5ptYSZBqhdSLXyPxJ3xbsIqnp
V6xoixtehXKE51gGQjgbJcEpzrRnA71zXKe3TOwKpH2mfqLdr58FeziWYnZOB4Hu
kfQHHkGWEwZfIhlve3SiZtqvl24PxeHoh9oo0T2iUnQlP5OvPcvlCBoSwCe4rJZP
opyS9YbyExePdS9e89JunI+GIHz1a6+a+nbdCbNALYifIaDKk3i/xYMZG6VuBaPc
8RAwKTxKIydXMgY+vUqV10OArw==
-----END CERTIFICATE-----
PEM
printf 's3cr"et\\x\n' >"${WORK}/pw"

# in_install ARGS... -- COMANDI: carica install.sh con quelle opzioni e
# gli percorsi in WORK, poi esegue i comandi (in una subshell).
in_install() {
  local args=()
  while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do args+=("$1"); shift; done
  shift
  local cmd="$*"
  (
    export GITSTACK_INSTALL_LIB=1
    export GITSTACK_NETWORK_DIR="${WORK}/net" GITSTACK_K3S_DIR="${WORK}/k3s" \
      GITSTACK_CA_CERTS_DIR="${WORK}/cacerts" GITSTACK_SYSTEMD_DIR="${WORK}/systemd"
    # shellcheck disable=SC1090
    set -- "${args[@]:-}"
    [ "$1" = "" ] && shift
    . "${INSTALL}"
    GITSTACK_CONFIG_FILE="${WORK}/config.yaml"
    set +e +o pipefail
    eval "${cmd}"
  ) 2>&1
}

# --- Parsing delle opzioni -----------------------------------------------------
OUT="$(in_install --http-proxy http://proxy.corp:3128 --no-proxy git.corp,.corp.test \
  --registry-mirror https://registry.corp:5000/ --registry-username puller \
  --registry-password-file "${WORK}/pw" --download-mirror http://dl.corp:8080/ --ca-bundle "${CA}" \
  -- 'resolve_network; printf "%s|%s|%s|%s|%s|%s|%s|%s" "${GITSTACK_HTTP_PROXY}" "$(net_https_proxy)" "${GITSTACK_NO_PROXY}" "${GITSTACK_REGISTRY_MIRROR}" "${GITSTACK_REGISTRY_USERNAME}" "${GITSTACK_REGISTRY_PASSWORD}" "${GITSTACK_DOWNLOAD_MIRROR}" "${NETWORK_CONFIGURED}"')"
expect_eq "opzioni di rete" "${OUT}" 'http://proxy.corp:3128|http://proxy.corp:3128|git.corp,.corp.test|https://registry.corp:5000|puller|s3cr"et\x|http://dl.corp:8080|1'

OUT="$(in_install -- 'resolve_network; printf "%s" "${NETWORK_CONFIGURED}"')"
expect_eq "senza opzioni: rete non configurata" "${OUT}" "0"

# --- Variabili d'ambiente per curl, k3s e Helm dell'installer ------------------------
OUT="$(in_install --http-proxy http://p:3128 --https-proxy http://sp:3129 --no-proxy a.corp \
  -- 'resolve_network; printf "%s|%s|%s" "${HTTP_PROXY}" "${HTTPS_PROXY}" "${NO_PROXY}"')"
expect_eq "proxy esportato (NO_PROXY come il drop-in)" "${OUT}" "http://p:3128|http://sp:3129|localhost,127.0.0.1,::1,10.42.0.0/16,10.43.0.0/16,.svc,.cluster.local,a.corp"

# --- Valori Helm: contratto di N9b ------------------------------------------------------
OUT="$(in_install --http-proxy http://p:3128 --no-proxy 'a.corp,b.corp' --ca-bundle "${CA}" --release-name gs \
  -- 'resolve_network; printf "%s\n" "${NETWORK_HELM_SETS[@]}"')"
expect_has "helm: proxy.http" "${OUT}" "proxy.http=http://p:3128"
expect_has "helm: proxy.https eredita http" "${OUT}" "proxy.https=http://p:3128"
expect_has "helm: noProxy con virgole protette" "${OUT}" 'proxy.noProxy=a.corp\,b.corp'
expect_has "helm: corporateCA.configMap" "${OUT}" "corporateCA.configMap=gs-corporate-ca"

OUT="$(in_install --registry-mirror http://r:5000 -- 'resolve_network; echo "${#NETWORK_HELM_SETS[@]}"')"
expect_eq "solo mirror: nessun valore Helm" "${OUT}" "0"

# --- registries.yaml ---------------------------------------------------------------------
OUT="$(in_install --registry-mirror https://reg.corp:5000 --ca-bundle "${CA}" -- 'resolve_network; render_registries_yaml')"
WANT='# Scritto da GitStack (install.sh o gitstack config set network): non modificarlo a mano.
mirrors:
  "*":
    endpoint:
      - "https://reg.corp:5000"
configs:
  "reg.corp:5000":
    tls:
      ca_file: "'"${WORK}"'/net/ca.crt"'
expect_eq "registries.yaml con CA" "${OUT}" "${WANT}"

OUT="$(in_install --registry-mirror http://reg.corp:5000 --registry-username u --registry-password-file "${WORK}/pw" \
  -- 'resolve_network; render_registries_yaml')"
expect_has "registries.yaml: mirror http" "${OUT}" '      - "http://reg.corp:5000"'
expect_has "registries.yaml: utente" "${OUT}" '      username: "u"'
expect_has "registries.yaml: password con apici e backslash protetti" "${OUT}" 'password: "s3cr\"et\\x"'
expect_lacks "registries.yaml: niente ca_file con mirror http" "${OUT}" "ca_file"

OUT="$(in_install -- 'resolve_network; render_registries_yaml')"
expect_eq "registries.yaml senza mirror: vuoto" "${OUT}" ""

# --- drop-in di proxy per k3s ----------------------------------------------------------------
OUT="$(in_install --http-proxy 'http://u:p%40ss@proxy.corp:3128' --no-proxy git.corp -- 'resolve_network; render_k3s_dropin')"
expect_has "drop-in: sezione" "${OUT}" "[Service]"
expect_has "drop-in: HTTP_PROXY con % raddoppiato" "${OUT}" 'Environment="HTTP_PROXY=http://u:p%%40ss@proxy.corp:3128"'
expect_has "drop-in: HTTPS_PROXY eredita HTTP" "${OUT}" 'Environment="HTTPS_PROXY=http://u:p%%40ss@proxy.corp:3128"'
expect_has "drop-in: NO_PROXY con le reti del cluster" "${OUT}" 'Environment="NO_PROXY=localhost,127.0.0.1,::1,10.42.0.0/16,10.43.0.0/16,.svc,.cluster.local,git.corp"'
expect_has "drop-in: minuscole" "${OUT}" 'Environment="no_proxy='

# --- File scritti: modo, idempotenza, rimozione ---------------------------------------------------
OUT="$(in_install --registry-mirror http://reg.corp:5000 --registry-username u --registry-password-file "${WORK}/pw" \
  --http-proxy http://p:3128 -- '
resolve_network
apply_k3s_network
first="${NETWORK_K3S_CHANGED}"
NETWORK_K3S_CHANGED=0
apply_k3s_network
second="${NETWORK_K3S_CHANGED}"
printf "%s|%s|%s|%s" "${first}" "${second}" "$(stat -c %a "${K3S_CONFIG_DIR}/registries.yaml")" "$(stat -c %a "${SYSTEMD_UNIT_DIR}/k3s.service.d/10-gitstack-network.conf")"')"
# I modi si controllano solo dove il filesystem li rispetta (non su NTFS).
touch "${WORK}/modo" && chmod 0600 "${WORK}/modo"
if [ "$(stat -c %a "${WORK}/modo")" = "600" ]; then
  WANT_MODES="600|644"
else
  WANT_MODES="$(stat -c %a "${WORK}/modo")|$(stat -c %a "${WORK}/modo")"
fi
case "${OUT}" in
  *"1|0|${WANT_MODES}") ok ;;
  *) bad "scrittura: cambiato al primo giro, non al secondo; registries 0600" ;;
esac
expect_has "drop-in scritto" "$(cat "${WORK}/systemd/k3s.service.d/10-gitstack-network.conf")" "HTTP_PROXY"

# Lo script di k3s mette *_PROXY in k3s.service.env: si tolgono.
printf 'K3S_TOKEN=x\nHTTP_PROXY=http://p\nNO_PROXY=a\nhttps_proxy=http://p\n' >"${WORK}/systemd/k3s.service.env"
OUT="$(in_install -- 'strip_k3s_env_proxy; cat "${SYSTEMD_UNIT_DIR}/k3s.service.env"')"
expect_eq "k3s.service.env senza proxy" "${OUT}" "K3S_TOKEN=x"

# --- CA di sistema ----------------------------------------------------------------------------------
OUT="$(in_install --ca-bundle "${CA}" -- 'update-ca-certificates() { echo UPDATE-CA; }; resolve_network; apply_ca_trust; echo "changed=${NETWORK_K3S_CHANGED}"')"
expect_has "CA: copia gestita" "$(ls "${WORK}/net")" "ca.crt"
expect_has "CA: file di sistema" "$(ls "${WORK}/cacerts")" "gitstack-corporate-ca.crt"
expect_has "CA: k3s da riavviare" "${OUT}" "changed=1"

# --- Convalide -------------------------------------------------------------------------------------------
OUT="$(in_install --http-proxy proxy.corp:3128 -- 'resolve_network')"
expect_has "proxy senza schema" "${OUT}" "non è un URL valido"
OUT="$(in_install --registry-username u --registry-mirror http://r:5000 -- 'resolve_network')"
expect_has "utente senza password" "${OUT}" "senza password"
OUT="$(in_install --registry-username u --registry-password-file "${WORK}/pw" -- 'resolve_network')"
expect_has "utente senza mirror" "${OUT}" "senza --registry-mirror"
printf 'non un certificato\n' >"${WORK}/bad.pem"
OUT="$(in_install --ca-bundle "${WORK}/bad.pem" -- 'resolve_network')"
expect_has "CA non PEM" "${OUT}" "non contiene certificati PEM"
OUT="$(in_install --no-proxy 'a, b' -- 'resolve_network')"
expect_has "no-proxy con spazi" "${OUT}" "senza spazi"
OUT="$(in_install --download-mirror ftp://x -- 'resolve_network')"
expect_has "mirror con schema sbagliato" "${OUT}" "non è un URL valido"

# --- URL dei download ---------------------------------------------------------------------------------------
OUT="$(in_install --download-mirror http://dl.corp:8080/ -- '
resolve_network
printf "%s\n%s\n%s\n%s\n" "$(k3s_script_url)" "$(helm_base_url)" "$(admin_default_url sha-abc)" "$(source_tarball_url 0123)"')"
expect_eq "URL col mirror" "${OUT}" "http://dl.corp:8080/k3s/install.sh
http://dl.corp:8080/helm
http://dl.corp:8080/gitstack/sha-abc/gitstack-linux-amd64
http://dl.corp:8080/gitstack/source/0123.tar.gz"
OUT="$(in_install -- 'resolve_network; printf "%s %s" "$(k3s_script_url)" "$(helm_base_url)"')"
expect_eq "URL senza mirror" "${OUT}" "https://get.k3s.io https://get.helm.sh"

# --- config.yaml: scrittura e rilettura (riesecuzione) -------------------------------------------------------------
OUT="$(in_install --http-proxy http://u:p@proxy.corp:3128 --registry-mirror http://r:5000 --registry-username 'u"x' \
  --registry-password-file "${WORK}/pw" --ca-bundle "${CA}" -- '
resolve_network
{ printf "version: 1\nhost: h\n"; render_config_network; } >"${GITSTACK_CONFIG_FILE}"
cat "${GITSTACK_CONFIG_FILE}"')"
expect_has "config: sezione" "${OUT}" "network:"
expect_has "config: proxy tra apici" "${OUT}" '  http_proxy: "http://u:p@proxy.corp:3128"'
expect_has "config: password protetta" "${OUT}" 'registry_password: "s3cr\"et\\x"'
expect_has "config: CA gestita" "${OUT}" "ca_bundle: \"${WORK}/net/ca.crt\""
# Rilettura: nessuna opzione, riprende ciò che c'è nel file.
install -d "${WORK}/net"
cp "${CA}" "${WORK}/net/ca.crt"
OUT="$(in_install -- 'resolve_network; printf "%s|%s|%s|%s|%s" "${GITSTACK_HTTP_PROXY}" "${GITSTACK_REGISTRY_MIRROR}" "${GITSTACK_REGISTRY_USERNAME}" "${GITSTACK_REGISTRY_PASSWORD}" "${GITSTACK_CA_BUNDLE}"')"
expect_eq "riesecuzione: scelte conservate" "${OUT}" 'http://u:p@proxy.corp:3128|http://r:5000|u"x|s3cr"et\x|'"${WORK}"'/net/ca.crt'
rm -f "${WORK}/config.yaml"
OUT="$(in_install -- 'render_config_network')"
expect_eq "config senza rete: sezione assente" "${OUT}" ""

# --- Preflight di rete ----------------------------------------------------------------------------------------------------
STUBS='
k3s_already_installed() { return 1; }
helm() { :; }
command() { if [ "$1" = "-v" ] && [ "$2" = "helm" ]; then return 1; fi; builtin command "$@"; }
net_probe_tcp() { [ "$1" != "down.corp" ]; }
net_probe_url() { case "$1" in *dead.corp*) printf 000 ;; *registry.corp*) [ "${2:-}" = "u" ] && printf 200 || printf 401 ;; *) printf 200 ;; esac; }
'
# tutto risponde
OUT="$(in_install --http-proxy http://proxy.corp:3128 --registry-mirror http://registry.corp:5000 --download-mirror http://dl.corp:8080 \
  -- "${STUBS}"'resolve_network; preflight_network; echo "rc=$?"')"
expect_has "preflight: tutto risponde" "${OUT}" "rc=0"
expect_has "preflight: dice il proxy" "${OUT}" "proxy http://proxy.corp:3128 raggiungibile"
expect_has "preflight: dice il mirror" "${OUT}" "mirror delle immagini risponde"
expect_lacks "preflight: nessun errore" "${OUT}" "non rispondono"

# cosa non risponde, con opzioni di rete: si ferma e lo elenca
OUT="$(in_install --http-proxy http://down.corp:3128 --registry-mirror http://dead.corp:5000 --download-mirror http://dl.corp:8080 \
  -- "${STUBS}"'resolve_network; preflight_network; echo "rc=$?"')"
expect_has "preflight: proxy che non risponde" "${OUT}" "proxy: http://down.corp:3128 non raggiungibile"
expect_has "preflight: mirror che non risponde" "${OUT}" "mirror delle immagini: http://dead.corp:5000/v2/ non risponde"
expect_has "preflight: si ferma" "${OUT}" "preflight di rete:"
expect_lacks "preflight: non prosegue" "${OUT}" "rc=0"
expect_lacks "preflight: il mirror dei download risponde" "${OUT}" "dl.corp:8080/k3s/install.sh non risponde"

# credenziali rifiutate (il mirror risponde 401 anche con le credenziali)
OUT="$(in_install --registry-mirror http://registry.corp:5000 --registry-username bad --registry-password-file "${WORK}/pw" \
  -- "${STUBS}"'resolve_network; preflight_network; echo "rc=$?"')"
expect_has "preflight: credenziali rifiutate" "${OUT}" "rifiuta le credenziali (HTTP 401)"

# senza opzioni di rete è solo un avviso (installazioni esistenti)
OUT="$(in_install -- "${STUBS}"'net_probe_url() { printf 000; }; resolve_network; preflight_network; echo "rc=$?"')"
expect_has "preflight senza opzioni: avviso" "${OUT}" "ATTENZIONE: preflight di rete"
expect_has "preflight senza opzioni: prosegue" "${OUT}" "rc=0"
expect_has "preflight senza opzioni: suggerisce le opzioni" "${OUT}" "--registry-mirror"

# la password non finisce nei messaggi
expect_lacks "nessuna password nei messaggi" "${OUT}" 's3cr'

printf '\nnetwork_test: %s ok, %s falliti\n' "${PASS}" "${FAILN}"
[ "${FAILN}" -eq 0 ]
