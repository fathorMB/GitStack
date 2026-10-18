#!/usr/bin/env bash
# Test del checksum dello script di k3s (F19, V14.2.2, GIT-239): lo script
# scaricato si esegue solo se il suo sha256 è quello fissato in
# deploy/k3s-install.sha256 (o in GITSTACK_K3S_SCRIPT_SHA256). Nessuna rete.
# Uso: ./deploy/tests/k3s_script_test.sh
# shellcheck disable=SC2034,SC1091
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export GITSTACK_INSTALL_LIB=1
# shellcheck source=../install.sh disable=SC1091
. "${HERE}/../install.sh"
set +e +o pipefail

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

PASS=0
FAILN=0
ok() { PASS=$((PASS + 1)); }
bad() { FAILN=$((FAILN + 1)); printf 'FALLITO: %s\n' "$1"; }

# Il file fissato nel repo è uno sha256 vero.
pinned="$(tr -d '[:space:]' <"${HERE}/../k3s-install.sha256")"
if [[ "${pinned}" =~ ^[0-9a-f]{64}$ ]]; then ok; else bad "deploy/k3s-install.sha256 non è uno sha256: '${pinned}'"; fi

mkdir -p "${WORK}/deploy/gitstack"
CHART_DIR="${WORK}/deploy/gitstack"
script="${WORK}/get-k3s.sh"
printf '#!/bin/sh\necho installato\n' >"${script}"
good="$(sha256sum "${script}" | awk '{ print $1 }')"

# Senza file e senza variabile: rifiutato.
unset GITSTACK_K3S_SCRIPT_SHA256
if verify_k3s_script "${script}" >/dev/null 2>&1; then bad "senza sha fissato doveva rifiutare"; else ok; fi

# Sha giusto nel file: accettato.
printf '%s\n' "${good}" >"${WORK}/deploy/k3s-install.sha256"
if verify_k3s_script "${script}" >/dev/null 2>&1; then ok; else bad "sha giusto doveva passare"; fi

# Script alterato: rifiutato, con il messaggio.
printf '#!/bin/sh\necho alterato\n' >"${script}"
out="$(verify_k3s_script "${script}" 2>&1)"
rc=$?
if [ "${rc}" -ne 0 ]; then ok; else bad "script alterato doveva essere rifiutato"; fi
case "${out}" in *"non corrisponde allo sha256 fissato"*) ok ;; *) bad "messaggio mancante: ${out}" ;; esac

# La variabile d'ambiente ha la precedenza sul file.
altered="$(sha256sum "${script}" | awk '{ print $1 }')"
if GITSTACK_K3S_SCRIPT_SHA256="${altered}" verify_k3s_script "${script}" >/dev/null 2>&1; then ok; else bad "GITSTACK_K3S_SCRIPT_SHA256 doveva avere la precedenza"; fi

printf 'k3s_script_test: %d ok, %d falliti\n' "${PASS}" "${FAILN}"
[ "${FAILN}" -eq 0 ]
