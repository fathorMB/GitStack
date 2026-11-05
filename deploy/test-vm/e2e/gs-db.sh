#!/bin/bash
# Database gestiti nel ciclo backup -> restore (GIT-558, DB13-DB17), sulla VM.
# Lo lancia e2e.ps1 (lib/databases.ps1) con questi comandi; ognuno stampa "STEP ok <passo>"
# o "STEP FAIL <passo>: motivo" ed esce con 1 al primo errore. Mai la password in uscita.
#
#   write      Scrive il marcatore nel database, dal pod, come gitstack_admin (socket locale).
#              Variabili: GS_DB_NS (gitstack-db-<id>), GS_DB_NAME, GS_DB_MARK; GS_DB_OWNER (default <nome>_owner):
#              le istruzioni girano dopo SET ROLE "<owner>", cosi' la tabella e dell'owner.
#   read       Rilegge il marcatore e lo confronta con GS_DB_MARK (dopo il restore).
#   lan        Accesso dalla LAN, dalla VM con psql (se manca si installa postgresql-client):
#              verify-full con la CA privata accettato; senza CA, con sslmode=disable e con un
#              nome host sbagliato respinti. Variabili: GS_DB_HOST, GS_DB_PORT, GS_DB_NAME,
#              GS_DB_USER, GS_DB_PASSWORD, GS_DB_MARK, GS_CA (file della CA dell'istanza).
#              Passi: lan-verify-full, lan-no-ca, lan-no-tls, lan-wrong-host.
#   diag       Pod, eventi e log del namespace GS_DB_NS.
#
# Niente `set -e`: i controlli negativi si scrivono con if (con `!` set -e non ferma).
set -u

KUBECONFIG_PATH="${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"
kc() { sudo -n env "KUBECONFIG=${KUBECONFIG_PATH}" k3s kubectl "$@"; }
fail() { echo "STEP FAIL $1: $2"; exit 1; }
ok() { echo "STEP ok $1"; }

db_pod() {
  kc -n "${GS_DB_NS:?}" get pods -o name 2>/dev/null | head -n 1 | sed 's#^pod/##'
}

# psql nel pod come gitstack_admin sul socket locale (DB7): ritorna lo stdout senza intestazioni
# Prima di ogni SQL si passa al proprietario del database (SET ROLE "<nome>_owner", o GS_DB_OWNER): gitstack_admin e
# superuser e gli oggetti che crea sarebbero suoi, illeggibili dall'owner (DB7); cosi' la tabella e dell'owner, come
# quelle di un'app, e la sonda LAN (che entra come owner) la legge. L'identificatore e' fra virgolette doppie.
pod_psql() { # sql
  local pod owner
  pod="$(db_pod)"
  [ -n "$pod" ] || return 1
  owner="${GS_DB_OWNER:-${GS_DB_NAME:?}_owner}"
  kc -n "$GS_DB_NS" exec "$pod" -- psql -X -q -v ON_ERROR_STOP=1 -At -U gitstack_admin -d "$GS_DB_NAME" -c "SET ROLE \"${owner//\"/\"\"}\"; $1"
}

cmd_write() {
  local out
  out="$(pod_psql "create table if not exists e2e_marker (id int primary key, v text not null); insert into e2e_marker values (1, '${GS_DB_MARK:?}') on conflict (id) do update set v = excluded.v; select v from e2e_marker where id = 1" 2>&1)" || fail write "psql nel pod: $out"
  case "$out" in *"$GS_DB_MARK"*) ok write ;; *) fail write "marcatore non riletto: $out" ;; esac
}

cmd_read() {
  local out
  out="$(pod_psql 'select v from e2e_marker where id = 1' 2>&1)" || fail read "psql nel pod: $out"
  out="$(printf '%s
' "$out" | tail -n 1)"
  if [ "$out" = "${GS_DB_MARK:?}" ]; then ok read; else fail read "atteso il marcatore, letto: $out"; fi
}

lan_psql() { # sslmode sslrootcert host [hostaddr]
  PGPASSWORD="${GS_DB_PASSWORD:?}" PGCONNECT_TIMEOUT=15 PGSSLMODE="$1" PGSSLROOTCERT="$2" PGHOSTADDR="${4:-}" \
    psql -X -At -h "$3" -p "${GS_DB_PORT:?}" -U "${GS_DB_USER:?}" -d "${GS_DB_NAME:?}" -c 'select v from e2e_marker where id = 1' 2>&1
}

cmd_lan() {
  if ! command -v psql >/dev/null 2>&1; then
    sudo -n apt-get update >/dev/null 2>&1
    sudo -n apt-get install -y postgresql-client >/dev/null 2>&1
    command -v psql >/dev/null 2>&1 || fail lan-verify-full "psql non c'e sulla VM e apt-get install postgresql-client e fallito"
  fi
  local out host="${GS_DB_HOST:?}" ca="${GS_CA:?}"
  # 1. verify-full con la CA: accettato e legge il marcatore
  out="$(lan_psql verify-full "$ca" "$host")" || fail lan-verify-full "$out"
  if [ "$out" = "${GS_DB_MARK:?}" ]; then ok lan-verify-full; else fail lan-verify-full "letto: $out"; fi
  # 2. senza CA (nessuna radice fidata): respinto
  if out="$(lan_psql verify-full /nonexistent/ca.crt "$host")"; then fail lan-no-ca "verify-full senza CA e stato accettato: $out"; fi
  ok lan-no-ca
  # 3. senza TLS: respinto (hostnossl)
  if out="$(lan_psql disable "" "$host")"; then fail lan-no-tls "sslmode=disable e stato accettato: $out"; fi
  ok lan-no-tls
  # 4. hostname sbagliato: si arriva allo stesso IP (PGHOSTADDR) ma il nome da verificare non e nei SAN
  if out="$(lan_psql verify-full "$ca" "altro-nome.invalid" "$host")"; then fail lan-wrong-host "un nome fuori dai SAN e stato accettato: $out"; fi
  ok lan-wrong-host
}

cmd_diag() {
  echo "--- pod"; kc -n "${GS_DB_NS:?}" get pods -o wide 2>&1
  echo "--- eventi"; kc -n "$GS_DB_NS" get events --sort-by=.lastTimestamp 2>&1 | tail -n 30
  local pod
  pod="$(db_pod)"
  if [ -n "$pod" ]; then echo "--- log $pod"; kc -n "$GS_DB_NS" logs "$pod" --tail=80 2>&1; fi
}

case "${1:-}" in
  write) cmd_write ;;
  read) cmd_read ;;
  lan) cmd_lan ;;
  diag) cmd_diag ;;
  *) echo "uso: gs-db.sh write|read|lan|diag" >&2; exit 2 ;;
esac
