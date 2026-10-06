#!/usr/bin/env bash
# Test del preflight di deploy/install.sh (N1, N2, N6; GIT-144).
# Carica solo le funzioni (GITSTACK_INSTALL_LIB=1) e ridefinisce le letture
# dell'hardware: nessun root, nessuna VM. Uso: ./deploy/tests/preflight_test.sh
# Le variabili assegnate qui sono lette dalle funzioni di install.sh (caricate con source).
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
OUT=""
RC=0

# run CMD...: esegue in una subshell (fail fa exit), cattura output e codice.
run() {
  OUT="$("$@" 2>&1)"
  RC=$?
}

ok() { PASS=$((PASS + 1)); }
bad() { FAILN=$((FAILN + 1)); printf 'FALLITO: %s\n--- output (rc=%s) ---\n%s\n---\n' "$1" "${RC}" "${OUT}"; }

# check ETICHETTA CMD...: ok se il comando riesce.
check() {
  local label="$1"
  shift
  if "$@"; then ok; else RC=0; OUT="(condizione falsa)"; bad "${label}"; fi
}

expect_ok() {
  if [ "${RC}" -eq 0 ]; then ok; else bad "$1: atteso rc=0"; fi
}
expect_fail() {
  if [ "${RC}" -ne 0 ]; then ok; else bad "$1: atteso rc!=0"; fi
}
expect_has() {
  case "${OUT}" in *"$2"*) ok ;; *) bad "$1: manca '$2'" ;; esac
}
expect_lacks() {
  case "${OUT}" in *"$2"*) bad "$1: non doveva esserci '$2'" ;; *) ok ;; esac
}

# --- Hardware finto: valori in variabili, funzioni ridefinite ---------------
T_CPU=8 T_RAM_GB=16 T_SIZE_GB=200 T_FREE_GB=150 T_ROT=0
cpu_count() { printf '%s' "${T_CPU}"; }
mem_total_kib() { awk -v g="${T_RAM_GB}" 'BEGIN { printf "%d", g * 1000000000 / 1024 }'; }
disk_target() { printf '/var/lib/rancher'; }
fs_size_bytes() { awk -v g="${T_SIZE_GB}" 'BEGIN { printf "%.0f", g * 1000000000 }'; }
fs_avail_bytes() { awk -v g="${T_FREE_GB}" 'BEGIN { printf "%.0f", g * 1000000000 }'; }
fs_source_device() { printf '/dev/sda2'; }
block_rotational() { printf '%s' "${T_ROT}"; }

profile() { T_CPU=$1 T_RAM_GB=$2 T_SIZE_GB=$3 T_FREE_GB=$4 T_ROT=$5; }
preflight_hw() { preflight_cpu && preflight_ram && preflight_disk; }

# --- Profilo consigliato: nessun avviso ----------------------------------------
profile 8 16 200 150 0
run preflight_hw
expect_ok "consigliato"
expect_lacks "consigliato senza avvisi" "ATTENZIONE"

# --- Profilo minimo: passa, con avvisi sul consigliato -------------------------
profile 4 8 60 25 0
run preflight_hw
expect_ok "minimo passa"
expect_has "minimo: avviso CPU" "CPU sotto il profilo consigliato"
expect_has "minimo: avviso RAM" "RAM sotto il profilo consigliato"
expect_has "minimo: avviso disco" "disco sotto il profilo consigliato"

# --- Sotto il minimo: l'installer si ferma ------------------------------------
profile 2 16 200 150 0
run preflight_cpu
expect_fail "2 vCPU"
expect_has "2 vCPU" "CPU insufficienti"

profile 8 6 200 150 0
run preflight_ram
expect_fail "6 GB di RAM"
expect_has "6 GB di RAM" "RAM insufficiente"

profile 8 16 40 30 0
run preflight_disk
expect_fail "disco da 40 GB"
expect_has "disco da 40 GB" "disco troppo piccolo"

# dimensione e spazio libero sono controlli diversi
profile 8 16 200 10 0
run preflight_disk
expect_fail "10 GB liberi su 200"
expect_has "10 GB liberi" "disco libero insufficiente"

# 8 GB nominali con qualche centinaio di MiB riservati (7.9 decimali) non passano
# per colpa dei GiB: 8 GiB = 8.6 GB decimali.
profile 4 8.6 60 20 0
run preflight_hw
expect_ok "8 GiB di RAM e 20 GB liberi esatti"

# --- SSD: avviso se rotazionale o sconosciuto ---------------------------------
profile 8 16 200 150 1
run preflight_disk
expect_ok "HDD non blocca"
expect_has "HDD" "rotazionale"
profile 8 16 200 150 '?'
run preflight_disk
expect_ok "SSD sconosciuto non blocca"
expect_has "SSD sconosciuto" "non riesco a stabilire"

# --- block_rotational su un sysfs finto -----------------------------------------
# Ricarica install.sh: ripristina block_rotational vera (sopra era uno stub).
. "${HERE}/../install.sh"
set +e +o pipefail
SYS_CLASS_BLOCK="${WORK}/sys/class/block"
mkdir -p "${WORK}/sys/block/sda/queue" "${WORK}/sys/block/nvme0n1/queue" "${WORK}/sys/block/sda/sda2" "${WORK}/sys/block/dm-0/slaves"
mkdir -p "${SYS_CLASS_BLOCK}"
printf '1\n' >"${WORK}/sys/block/sda/queue/rotational"
printf '0\n' >"${WORK}/sys/block/nvme0n1/queue/rotational"
touch "${WORK}/sys/block/sda/sda2/partition"
ln -s "${WORK}/sys/block/sda" "${SYS_CLASS_BLOCK}/sda"
ln -s "${WORK}/sys/block/nvme0n1" "${SYS_CLASS_BLOCK}/nvme0n1"
ln -s "${WORK}/sys/block/sda/sda2" "${SYS_CLASS_BLOCK}/sda2"
ln -s "${WORK}/sys/block/dm-0" "${SYS_CLASS_BLOCK}/dm-0"
ln -s "${WORK}/sys/block/nvme0n1" "${WORK}/sys/block/dm-0/slaves/nvme0n1"
check "rotational nvme" test "$(block_rotational nvme0n1)" = "0"
check "rotational sda" test "$(block_rotational sda)" = "1"
check "rotational partizione" test "$(block_rotational sda2)" = "1"
check "rotational dm su nvme" test "$(block_rotational dm-0)" = "0"
check "rotational ignoto" test "$(block_rotational nope)" = "?"

# --- Sistema operativo -----------------------------------------------------------
os_file() { printf 'ID=%s\nVERSION_ID="%s"\n' "$1" "$2" >"${WORK}/os-release"; OS_RELEASE_FILE="${WORK}/os-release"; }

GITSTACK_FORCE=0
os_file ubuntu 24.04
run preflight_os
expect_ok "ubuntu 24.04"
expect_lacks "ubuntu 24.04" "non supportato"

os_file ubuntu 22.04
run preflight_os
expect_fail "ubuntu 22.04 senza --force"
expect_has "ubuntu 22.04" "non supportato"
expect_has "ubuntu 22.04 suggerisce --force" "--force"

os_file debian 12
run preflight_os
expect_fail "debian senza --force"

os_file pop 24.04
run preflight_os
expect_fail "Pop!_OS senza --force"

GITSTACK_FORCE=1
os_file ubuntu 22.04
run preflight_os
expect_ok "ubuntu 22.04 con --force"
expect_has "22.04 con --force: avviso" "non supportato"
GITSTACK_FORCE=0

OS_RELEASE_FILE="${WORK}/manca"
run preflight_os
expect_fail "os-release illeggibile"

# --force da riga di comando: lo imposta il parsing (stesso file, in un processo)
run bash -c '. "$1" --force >/dev/null 2>&1; echo F=${GITSTACK_FORCE}; [ "${GITSTACK_FORCE}" = 1 ]' _ "${HERE}/../install.sh"
expect_ok "--force imposta GITSTACK_FORCE"

# --- Nome host: getent via resolver di sistema --------------------------------
local_addrs() { printf '%s\n' 127.0.0.1 192.168.1.20 fe80::1; }
declare -A T_DNS=()
resolve_host_addrs() { printf '%s' "${T_DNS[$1]:-}"; }
T_DNS[homehub.local]="192.168.1.20"
T_DNS[altro.example]="10.9.9.9"
T_DNS[loop.example]="127.0.1.1"

HOST_ARGS=(homehub.local)
run preflight_host
expect_ok "homehub.local risolve"
expect_has "homehub.local passa" "risolve a un indirizzo della macchina OK"
expect_has "homehub.local: pod non risolvono .local" "i pod di k3s non lo risolvono"
expect_lacks "homehub.local: nessun avviso di mancata risoluzione" "non risolve su questa macchina"

HOST_ARGS=(manca.local)
run preflight_host
expect_ok "nome .local che non risolve non ferma"
expect_has "nome che non risolve: avviso" "ATTENZIONE: il nome 'manca.local' non risolve"
expect_has "nome .local: avahi" "avahi-daemon"

HOST_ARGS=(manca.example.com)
run preflight_host
expect_ok "nome che non risolve non ferma"
expect_has "nome che non risolve: avviso" "non risolve su questa macchina"
expect_lacks "nome non .local: niente avahi" "avahi"

HOST_ARGS=(altro.example)
run preflight_host
expect_has "risolve altrove" "non è un indirizzo di questa macchina"

HOST_ARGS=(loop.example)
run preflight_host
expect_has "loopback non conta" "non è un indirizzo di questa macchina"

HOST_ARGS=(192.168.1.20)
run preflight_host
expect_has "IP della macchina" "indirizzo della macchina OK"
HOST_ARGS=(10.1.1.1)
run preflight_host
expect_has "IP non della macchina" "non è un indirizzo di questa macchina"

HOST_ARGS=()
DEFAULT_HOST_NOTE="nota di prova"
run preflight_host
expect_has "default senza nome risolvibile" "nota di prova"

# --- Nome host di default (resolve_tls) ----------------------------------------
primary_ip() { printf '192.168.1.20'; }
hostname() { case "$1" in -s) printf 'homehub' ;; -f) printf '%s' "${T_FQDN}" ;; esac; }
TLS_DIR="${WORK}/tls-vuoto"
T_DNS[homehub]="192.168.1.20"
T_FQDN=homehub

HOST_ARGS=()
GITSTACK_TLS_MODE=internal
resolve_tls
check "default: nome completo se risolve" test "${PUBLIC_HOST}" = "homehub"
case ",${TLS_IPS}," in *",192.168.1.20,"*) ok ;; *) RC=0; OUT="${TLS_IPS}"; bad "default: IP nei SAN" ;; esac

unset 'T_DNS[homehub]'
HOST_ARGS=()
DEFAULT_HOST_NOTE=""
resolve_tls
check "default: IP se il nome non risolve" test "${PUBLIC_HOST}" = "192.168.1.20"
check "default: avviso se il nome non risolve" test -n "${DEFAULT_HOST_NOTE}"

HOST_ARGS=(homehub.local)
resolve_tls
check "--host .local: URL pubblico" test "${PUBLIC_HOST}" = "homehub.local"
check "--host .local: nei SAN come nome" test "${TLS_NAMES}" = "homehub.local"
case ",${TLS_IPS}," in *",192.168.1.20,"*) ok ;; *) RC=0; OUT="${TLS_IPS}"; bad "--host: IP sempre nei SAN" ;; esac

printf '\npreflight_test: %s ok, %s falliti\n' "${PASS}" "${FAILN}"
[ "${FAILN}" -eq 0 ]
