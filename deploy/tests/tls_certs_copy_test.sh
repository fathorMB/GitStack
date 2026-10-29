#!/usr/bin/env bash
# Test della copia di gitstack-tls nel namespace dei certificati (GIT-494): publish_certs_copy di
# deploy/gitstack-tls.sh con k3s finto. La copia la crea il chart (lookup); lo script la aggiorna SOLO se
# esiste gia', altrimenti un `kubectl apply` la lascerebbe senza i metadati di helm e il successivo
# `helm upgrade` fallirebbe con «invalid ownership metadata».
# Uso: ./deploy/tests/tls_certs_copy_test.sh
# shellcheck disable=SC2034,SC1090,SC2317,SC2329
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TLS="${HERE}/../gitstack-tls.sh"

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT
mkdir -p "${WORK}/bin"
PASS=0
FAILN=0
ok() { PASS=$((PASS + 1)); }
bad() { FAILN=$((FAILN + 1)); printf 'FALLITO: %s\n' "$1"; }

# k3s finto: registra ogni chiamata in $STUB_LOG. `get secret` riesce solo se STUB_COPY=yes; con
# -o jsonpath={.data.tls\.crt} (e SOLO quell'espressione) stampa $STUB_CURRENT, ogni altro jsonpath stampa vuoto.
cat >"${WORK}/bin/k3s" <<'STUB'
#!/bin/sh
printf '%s\n' "$*" >>"${STUB_LOG}"
case "$*" in
  *"get secret"*'jsonpath={.data.tls\.crt}'*) [ "${STUB_COPY}" = yes ] || exit 1; printf '%s' "${STUB_CURRENT}"; exit 0 ;;
  *"get secret"*"jsonpath"*) [ "${STUB_COPY}" = yes ] || exit 1; exit 0 ;;
  *"get secret"*) [ "${STUB_COPY}" = yes ] || exit 1; exit 0 ;;
esac
exit 0
STUB
chmod +x "${WORK}/bin/k3s"
export PATH="${WORK}/bin:${PATH}"
export STUB_LOG="${WORK}/calls.log"

sed '$d' "${TLS}" >"${WORK}/lib.sh"
. "${WORK}/lib.sh"
set +e
RELEASE=gitstack
SECRET_NAME=gitstack-tls
KUBECONFIG_FILE=/dev/null
printf crt >"${WORK}/c.crt"
printf key >"${WORK}/c.key"

run_case() { # nome-copia-presente corrente
  : >"${STUB_LOG}"
  STUB_COPY="$1" STUB_CURRENT="$2" publish_certs_copy "${WORK}/c.crt" "${WORK}/c.key" "NUOVO" >/dev/null 2>&1
}
applied() { grep -q 'apply' "${STUB_LOG}"; }

# 1) copia assente: niente apply (la crea il chart).
run_case no ""
if applied; then bad "copia assente: non deve fare apply"; else ok; fi
# 2) copia presente con lo stesso certificato: niente apply.
run_case yes "NUOVO"
if applied; then bad "copia aggiornata: non deve fare apply"; else ok; fi
# 3) copia presente con certificato diverso: apply nel namespace <release>-certs.
run_case yes "VECCHIO"
if applied && grep -q -- '-n gitstack-certs' "${STUB_LOG}"; then ok; else bad "copia diversa: deve fare apply in gitstack-certs"; fi
# 4) release che darebbe un namespace gitstack-app-*: mai nessuna chiamata.
RELEASE=gitstack-app-x
run_case yes "VECCHIO"
if [ -s "${STUB_LOG}" ]; then bad "gitstack-app-*: nessuna chiamata a kubectl"; else ok; fi

printf 'ok: %d, falliti: %d\n' "${PASS}" "${FAILN}"
[ "${FAILN}" -eq 0 ]
