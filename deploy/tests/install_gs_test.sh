#!/usr/bin/env bash
# Prova di install-gs.sh (GIT-171) senza istanza: i file stanno in una cartella
# e GS_BASE_URL e' un file:// (curl lo scarica come un URL qualsiasi).
#   deploy/tests/install_gs_test.sh
set -u
here=$(cd "$(dirname "$0")" && pwd)
script="${here}/../../web/deploy/install-gs.sh"
work=$(mktemp -d)
trap 'rm -rf "${work}"' EXIT

fail=0
check() { # descrizione, esito atteso (0|1), codice reale
  if { [ "$2" = 0 ] && [ "$3" = 0 ]; } || { [ "$2" = 1 ] && [ "$3" != 0 ]; }; then
    echo "ok   - $1"
  else
    echo "FAIL - $1 (rc=$3)"
    fail=1
  fi
}

mkdir -p "${work}/site/downloads"
printf '#!/bin/sh\necho gs finto\n' > "${work}/site/downloads/gs_linux_amd64"
good=$(sha256sum "${work}/site/downloads/gs_linux_amd64" | awk '{print $1}')
printf '%s  gs_linux_amd64\n' "${good}" > "${work}/site/downloads/SHA256SUMS"
base="file://${work}/site"

# 1. checksum giusto: installa.
GS_BASE_URL="${base}" GS_OS=linux GS_ARCH=amd64 GS_INSTALL_DIR="${work}/bin" sh "${script}" >"${work}/out1" 2>&1
check "checksum corretto: installa" 0 $?
[ -x "${work}/bin/gs" ]; check "gs e' eseguibile in GS_INSTALL_DIR" 0 $?

# 2. checksum sbagliato: fallisce e non installa.
printf '%064d  gs_linux_amd64\n' 0 > "${work}/site/downloads/SHA256SUMS"
GS_BASE_URL="${base}" GS_OS=linux GS_ARCH=amd64 GS_INSTALL_DIR="${work}/bin2" sh "${script}" >"${work}/out2" 2>&1
check "checksum sbagliato: fallisce" 1 $?
grep -q 'checksum SHA-256' "${work}/out2"; check "il messaggio parla del checksum" 0 $?
[ ! -e "${work}/bin2/gs" ]; check "con checksum sbagliato non installa nulla" 0 $?

# 3. file assente da SHA256SUMS.
printf '%s  altro_file\n' "${good}" > "${work}/site/downloads/SHA256SUMS"
GS_BASE_URL="${base}" GS_OS=linux GS_ARCH=amd64 GS_INSTALL_DIR="${work}/bin3" sh "${script}" >"${work}/out3" 2>&1
check "file assente da SHA256SUMS: fallisce" 1 $?

# 4. senza indirizzo (segnaposto non sostituito): fallisce con un messaggio chiaro.
env -u GS_BASE_URL GS_OS=linux GS_ARCH=amd64 GS_INSTALL_DIR="${work}/bin4" sh "${script}" >"${work}/out4" 2>&1
check "senza GS_BASE_URL: fallisce" 1 $?
grep -q 'GS_BASE_URL' "${work}/out4"; check "il messaggio cita GS_BASE_URL" 0 $?

# 5. architettura non supportata.
GS_BASE_URL="${base}" GS_OS=linux GS_ARCH=mips sh "${script}" >"${work}/out5" 2>&1
check "GS_ARCH=mips: il file non esiste, fallisce" 1 $?

exit "${fail}"
