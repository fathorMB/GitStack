#!/usr/bin/env bash
# Prova locale di gs-db.sh (GIT-558) con sudo/kubectl e psql finti: guarda l'SQL che lo script manda davvero.
# Il database finto ricorda chi possiede e2e_marker: la tabella creata da gitstack_admin senza SET ROLE resta
# del superuser e la sonda LAN, che entra come <nome>_owner, prende "permission denied" (il difetto della
# revisione 1). Le tre sonde negative devono continuare a fallire.
# Uso: bash deploy/test-vm/e2e/gs-db.test.sh
set -u
dir=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"

# sudo finto: `sudo -n env KUBECONFIG=... k3s kubectl -n NS get pods -o name | exec POD -- psql ... -c SQL`
cat >"$tmp/bin/sudo" <<'EOF'
#!/usr/bin/env bash
args="$*"
case "$args" in
  *"get pods"*) echo "pod/pg-0"; exit 0 ;;
  *" exec "*)
    sql=""
    prev=""
    for a in "$@"; do
      if [ "$prev" = "-c" ]; then sql="$a"; fi
      prev="$a"
    done
    printf '%s\n' "$sql" >>"$FAKE_STATE/sql.log"
    role="gitstack_admin"
    case "$sql" in
      'SET ROLE "'*) role="${sql#SET ROLE \"}"; role="${role%%\"; *}"; role="${role//\"\"/\"}" ;;
    esac
    case "$sql" in
      *"create table"*)
        echo "$role" >"$FAKE_STATE/table_owner"
        mark="${sql#*values (1, \'}"; mark="${mark%%\'*}"
        echo "$mark" >"$FAKE_STATE/marker"
        echo "$mark" ;;
      *"select v from e2e_marker"*)
        [ -f "$FAKE_STATE/marker" ] || { echo "ERROR: relation e2e_marker does not exist" >&2; exit 1; }
        if [ "$role" != "$(cat "$FAKE_STATE/table_owner")" ] && [ "$role" != "gitstack_admin" ]; then
          echo "ERROR: permission denied for table e2e_marker" >&2; exit 1
        fi
        cat "$FAKE_STATE/marker" ;;
    esac
    exit 0 ;;
esac
exit 0
EOF

# psql finto della VM (sonda LAN): entra come $2 di -U; legge solo se e2e_marker e suo; il TLS segue le variabili PG*
cat >"$tmp/bin/psql" <<'EOF'
#!/usr/bin/env bash
user=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-U" ]; then user="$a"; fi
  prev="$a"
done
[ "${PGSSLMODE:-}" != "disable" ] || { echo "FATAL: no pg_hba.conf entry (hostnossl)" >&2; exit 2; }
if [ "${PGSSLMODE:-}" = "verify-full" ]; then
  [ -f "${PGSSLROOTCERT:-/nonexistent}" ] || { echo "root certificate file does not exist" >&2; exit 2; }
  [ -z "${PGHOSTADDR:-}" ] || { echo "server certificate does not match host name" >&2; exit 2; }
fi
if [ "$(cat "$FAKE_STATE/table_owner")" != "$user" ]; then echo "ERROR: permission denied for table e2e_marker" >&2; exit 1; fi
cat "$FAKE_STATE/marker"
EOF
chmod +x "$tmp/bin/sudo" "$tmp/bin/psql"
echo CA >"$tmp/ca.crt"

fails=0
check() { # nome condizione-esito(0=ok)
  if [ "$2" -eq 0 ]; then echo "[PASS] $1"; else echo "[FAIL] $1"; fails=$((fails + 1)); fi
}
run() { # script comando [VAR=valore...]
  local script="$1" cmd="$2"
  shift 2
  env PATH="$tmp/bin:$PATH" FAKE_STATE="$tmp/state" GS_DB_NS=gitstack-db-x GS_DB_NAME=e2edb GS_DB_MARK=e2e-db-abc \
    GS_DB_HOST=10.0.0.9 GS_DB_PORT=15432 GS_DB_USER=e2edb_owner GS_DB_PASSWORD=pw GS_CA="$tmp/ca.crt" "$@" bash "$script" "$cmd"
}
reset() { rm -rf "$tmp/state"; mkdir -p "$tmp/state"; }

# 1. script vero: write con SET ROLE, poi LAN come owner: positiva ok e tre negative respinte
reset
out=$(run "$dir/gs-db.sh" write 2>&1); rc=$?
check "write: STEP ok write (exit $rc)" "$([ $rc -eq 0 ] && [[ "$out" == *"STEP ok write"* ]]; echo $?)"
check "write: l'SQL comincia con SET ROLE \"e2edb_owner\"; prima del CREATE" "$([ "$(head -n 1 "$tmp/state/sql.log" | cut -c1-30)" = 'SET ROLE "e2edb_owner"; create' ]; echo $?)"
check "write: la tabella e dell'owner" "$([ "$(cat "$tmp/state/table_owner")" = e2edb_owner ]; echo $?)"
out=$(run "$dir/gs-db.sh" lan 2>&1); rc=$?
for s in lan-verify-full lan-no-ca lan-no-tls lan-wrong-host; do
  check "lan: STEP ok $s" "$([[ "$out" == *"STEP ok $s"* ]]; echo $?)"
done
check "lan: exit 0" "$rc"
out=$(run "$dir/gs-db.sh" read 2>&1); rc=$?
check "read: legge il marcatore come owner" "$([ $rc -eq 0 ] && [[ "$out" == *"STEP ok read"* ]]; echo $?)"
check "read: anche la lettura passa da SET ROLE" "$([ "$(tail -n 1 "$tmp/state/sql.log" | cut -c1-23)" = 'SET ROLE "e2edb_owner";' ]; echo $?)"

# 2. mutazione: lo script senza SET ROLE deve far fallire la prova (la tabella resta di gitstack_admin)
reset
sed 's/SET ROLE[^;]*; //' "$dir/gs-db.sh" >"$tmp/gs-db-noset.sh"
grep -q -- "-c \"SET ROLE" "$tmp/gs-db-noset.sh" && echo "[FAIL] mutazione non applicata" && fails=$((fails + 1))
run "$tmp/gs-db-noset.sh" write >/dev/null 2>&1
check "mutazione senza SET ROLE: la tabella e del superuser" "$([ "$(cat "$tmp/state/table_owner")" = gitstack_admin ]; echo $?)"
out=$(run "$tmp/gs-db-noset.sh" lan 2>&1); rc=$?
check "mutazione senza SET ROLE: la sonda LAN fallisce con permission denied" "$([ $rc -ne 0 ] && [[ "$out" == *"STEP FAIL lan-verify-full"*"permission denied"* ]]; echo $?)"

# 3. identificatore citato: GS_DB_OWNER con virgolette
reset
run "$dir/gs-db.sh" write GS_DB_OWNER='we"ird' >/dev/null 2>&1
check "owner con virgolette raddoppiate nell'identificatore" "$([[ "$(head -n 1 "$tmp/state/sql.log")" == 'SET ROLE "we""ird"; '* ]]; echo $?)"

# 4. le negative falliscono davvero: ognuna da sola, con il resto a posto
reset
run "$dir/gs-db.sh" write >/dev/null 2>&1
ok_lan=$(run "$dir/gs-db.sh" lan 2>&1)
check "negative: senza CA il finto psql respinge (la sonda e capace di fallire)" "$(PATH="$tmp/bin:$PATH" FAKE_STATE="$tmp/state" PGSSLMODE=verify-full PGSSLROOTCERT=/nonexistent psql -U e2edb_owner >/dev/null 2>&1; [ $? -ne 0 ]; echo $?)"
check "negative: sslmode=disable respinto" "$(PATH="$tmp/bin:$PATH" FAKE_STATE="$tmp/state" PGSSLMODE=disable psql -U e2edb_owner >/dev/null 2>&1; [ $? -ne 0 ]; echo $?)"
check "negative: l'esito completo ha quattro STEP ok" "$([ "$(printf '%s\n' "$ok_lan" | grep -c '^STEP ok lan-')" -eq 4 ]; echo $?)"

if [ "$fails" -ne 0 ]; then echo "$fails verifiche fallite"; exit 1; fi
echo "gs-db.sh: OK"
