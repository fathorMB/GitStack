#!/usr/bin/env bash
# Test del dominio delle app (H2, GIT-346): scelta di --apps-domain in
# deploy/install.sh e certificato wildcard di deploy/gitstack-tls.sh, con
# openssl vero e k3s finto. Nessun root, nessuna VM.
# Uso: ./deploy/tests/apps_domain_test.sh
# Le variabili assegnate qui sono lette dalle funzioni di install.sh (caricate con source).
# shellcheck disable=SC2034,SC1091,SC2317,SC2329
set -u
# Git Bash (MSYS) riscrive gli argomenti tipo /O=GitStack: solo sull'host Windows di sviluppo.
export MSYS2_ARG_CONV_EXCL="*"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export GITSTACK_INSTALL_LIB=1
# shellcheck source=../install.sh disable=SC1091
. "${HERE}/../install.sh"
set +e +o pipefail

WORK="$(mktemp -d)"
if command -v cygpath >/dev/null 2>&1; then WORK="$(cygpath -m "${WORK}")"; fi
trap 'rm -rf "${WORK}"' EXIT

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
expect_lacks() { case "${OUT}" in *"$2"*) bad "$1: non doveva esserci '$2'" ;; *) ok ;; esac; }

# --- apps_domain_valid ------------------------------------------------------
for d in apps.azienda.it apps.gitstack.internal a.b x-1.example.com apps.git.azienda.it; do
  apps_domain_valid "${d}"
  RC=$?
  OUT="${d}"
  expect_ok "dominio valido ${d}"
done
for d in apps Apps.azienda.it apps..azienda.it -apps.azienda.it apps-.azienda.it 192.168.1.5 app_s.azienda.it "apps.azienda.it."; do
  apps_domain_valid "${d}"
  RC=$?
  OUT="${d}"
  expect_fail "dominio non valido ${d}"
done

# --- resolve_apps_domain ----------------------------------------------------
TLS_DIR="${WORK}/tls"
GITSTACK_CONFIG_FILE="${WORK}/config.yaml"
mkdir -p "${TLS_DIR}"
PUBLIC_HOST="homehub.local"
GITSTACK_TLS_MODE="internal"

# Senza opzione: default apps.<host> con avviso.
GITSTACK_APPS_DOMAIN=""
APPS_DOMAIN=""
APPS_DOMAIN_NOTE=""
run resolve_apps_domain
expect_ok "default: esito"
expect_has "default: avviso" "--apps-domain non indicato"
expect_has "default: dominio" "apps.homehub.local"
expect_has "default: DNS wildcard" "*.apps.homehub.local"
resolve_apps_domain >/dev/null 2>&1
check_eq() { if [ "$2" = "$3" ]; then ok; else OUT="atteso '$3', trovato '$2'"; RC=1; bad "$1"; fi; }
check_eq "default: APPS_DOMAIN" "${APPS_DOMAIN}" "apps.homehub.local"
check_eq "default: nota" "${APPS_DOMAIN_NOTE}" "default"

# Host con IP: default fisso.
PUBLIC_HOST="10.1.2.3"
APPS_DOMAIN_NOTE=""
resolve_apps_domain >/dev/null 2>&1
check_eq "default con IP" "${APPS_DOMAIN}" "apps.gitstack.internal"

# Con l'opzione (anche in maiuscole): nessun avviso del default.
PUBLIC_HOST="git.azienda.it"
GITSTACK_APPS_DOMAIN="Apps.Azienda.IT"
APPS_DOMAIN_NOTE=""
run resolve_apps_domain
expect_ok "opzione: esito"
expect_lacks "opzione: niente avviso" "--apps-domain non indicato"
resolve_apps_domain >/dev/null 2>&1
check_eq "opzione: dominio minuscolo" "${APPS_DOMAIN}" "apps.azienda.it"
check_eq "opzione: nessuna nota" "${APPS_DOMAIN_NOTE}" ""

# Sottodominio dell'host: ammesso; dominio uguale all'host: rifiutato.
GITSTACK_APPS_DOMAIN="apps.git.azienda.it"
run resolve_apps_domain
expect_ok "sottodominio dell'host"
expect_has "sottodominio dell'host: messaggio" "sottodominio"
GITSTACK_APPS_DOMAIN="git.azienda.it"
run resolve_apps_domain
expect_fail "dominio uguale all'host"
expect_has "dominio uguale all'host: messaggio" "coincide con l'host"
GITSTACK_APPS_DOMAIN="azienda"
run resolve_apps_domain
expect_fail "dominio con una sola etichetta"

# Scelta salvata (install.conf) e v2 senza dominio.
GITSTACK_APPS_DOMAIN=""
printf 'MODE=internal\nHOSTS=git.azienda.it\nEMAIL=\nAPPS_DOMAIN=apps.salvato.it\n' >"${TLS_DIR}/install.conf"
run resolve_apps_domain
expect_lacks "salvato: niente avviso" "--apps-domain non indicato"
resolve_apps_domain >/dev/null 2>&1
check_eq "salvato" "${APPS_DOMAIN}" "apps.salvato.it"
printf 'MODE=internal\nHOSTS=git.azienda.it\nEMAIL=\n' >"${TLS_DIR}/install.conf"
printf 'version: 1\nhost: git.azienda.it\ntls: internal\n' >"${GITSTACK_CONFIG_FILE}"
APPS_DOMAIN_NOTE=""
run resolve_apps_domain
expect_has "v2 senza dominio: avviso" "--apps-domain non indicato"
printf 'version: 1\nhost: git.azienda.it\napps_domain: apps.dalconfig.it\ntls: internal\n' >"${GITSTACK_CONFIG_FILE}"
resolve_apps_domain >/dev/null 2>&1
check_eq "dal config.yaml" "${APPS_DOMAIN}" "apps.dalconfig.it"

# --- gitstack-tls: certificato wildcard con la CA interna ---------------------
if ! command -v openssl >/dev/null 2>&1; then
  printf 'openssl non disponibile: salto la parte del certificato.\n'
else
  TLS="${HERE}/../gitstack-tls.sh"
  T="${WORK}/gt"
  mkdir -p "${T}/bin"
  # k3s finto: registra e accetta ogni comando kubectl.
  cat >"${T}/bin/k3s" <<'EOF'
#!/bin/sh
cat >/dev/null 2>&1 || true
exit 0
EOF
  chmod +x "${T}/bin/k3s"
  # id -u finto non serve: gitstack-tls vuole root, quindi si prova la parte di
  # emissione estraendo le funzioni (senza main).
  # shellcheck disable=SC1090
  (
    export GITSTACK_TLS_DIR="${T}/tls"
    sed '$d' "${TLS}" >"${T}/lib.sh"
    . "${T}/lib.sh"
    set +e
    mkdir -p "${TLS_DIR}"
    TLS_NAMES="homehub.local"
    TLS_IPS="10.1.2.3"
    APPS_DOMAIN="apps.azienda.it"
    validate_sans || exit 10
    ensure_ca >/dev/null 2>&1
    if ! server_cert_needs_issue; then echo "KO: doveva servire un certificato"; exit 11; fi
    issue_server_cert >/dev/null 2>&1 || exit 12
    sans="$(openssl x509 -in "${TLS_DIR}/server.crt" -noout -ext subjectAltName 2>/dev/null)"
    case "${sans}" in *"DNS:*.apps.azienda.it"*) echo "ok: SAN wildcard presente" ;; *) echo "KO: SAN senza wildcard: ${sans}"; exit 13 ;; esac
    case "${sans}" in *"DNS:homehub.local"*"IP Address:10.1.2.3"*) echo "ok: host e IP presenti" ;; *) echo "KO: SAN senza host/IP: ${sans}"; exit 14 ;; esac
    openssl verify -CAfile "${TLS_DIR}/ca.crt" "${TLS_DIR}/server.crt" >/dev/null 2>&1 || { echo "KO: il certificato non si verifica con la CA"; exit 15; }
    echo "ok: verifica con la CA interna"
    for h in web-sito.apps.azienda.it a-b-c.apps.azienda.it; do
      openssl verify -CAfile "${TLS_DIR}/ca.crt" -verify_hostname "${h}" "${TLS_DIR}/server.crt" >/dev/null 2>&1 || { echo "KO: ${h} non coperto"; exit 16; }
      echo "ok: ${h} coperto dal wildcard"
    done
    for h in x.y.apps.azienda.it altro.azienda.it apps.azienda.it; do
      if openssl verify -CAfile "${TLS_DIR}/ca.crt" -verify_hostname "${h}" "${TLS_DIR}/server.crt" >/dev/null 2>&1; then echo "KO: ${h} non doveva essere coperto"; exit 17; fi
      echo "ok: ${h} non coperto (un solo livello)"
    done
    if server_cert_needs_issue; then echo "KO: certificato appena emesso da rifare"; exit 18; fi
    echo "ok: nessun rinnovo se i SAN non cambiano"
    APPS_DOMAIN="apps2.azienda.it"
    if ! server_cert_needs_issue; then echo "KO: cambio dominio senza nuova emissione"; exit 19; fi
    echo "ok: cambio dominio, nuova emissione"
    # Rinnovo: scadenza vicina -> da rifare.
    APPS_DOMAIN="apps.azienda.it"
    issue_server_cert >/dev/null 2>&1
    RENEW_BEFORE_DAYS=400
    if ! server_cert_needs_issue; then echo "KO: certificato in scadenza non rinnovato"; exit 20; fi
    echo "ok: rinnovo quando scade"
    # Dominio non valido rifiutato.
    APPS_DOMAIN="Apps_x"
    if (validate_sans) >/dev/null 2>&1; then echo "KO: dominio non valido accettato"; exit 21; fi
    echo "ok: dominio non valido rifiutato"
    echo FINE
  ) >"${WORK}/tls.out" 2>&1
  RC=$?
  OUT="$(cat "${WORK}/tls.out")"
  expect_ok "gitstack-tls: certificato wildcard"
  expect_has "gitstack-tls: fine" "FINE"
  expect_lacks "gitstack-tls: nessun KO" "KO:"
fi

printf '\n%s controlli ok, %s falliti\n' "${PASS}" "${FAILN}"
[ "${FAILN}" -eq 0 ]
