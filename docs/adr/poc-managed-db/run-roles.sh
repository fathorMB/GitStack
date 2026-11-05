#!/usr/bin/env bash
# Prova dei ruoli dell'ADR 0003 (punto 4 e punto 5) con tre istanze PostgreSQL 17 in Docker (una
# per database, come in produzione: ogni database ha la sua istanza e quindi i propri ruoli):
#   A) scollegamento di un collegamento: NOLOGIN -> pg_terminate_backend -> DROP ROLE, con sessioni
#      persistenti aperte prima; un altro collegamento resta vivo;
#   B) ripristino del dump di A in un'istanza vuota con nome DIVERSO (blog), C) in un'istanza vuota
#      con lo STESSO nome (shop, ripristino d'istanza): --no-owner --no-acl --role + privilegi riapplicati.
# Una riga OK/KO per controllo, poi ESITO ed exit. Ripulisce solo i container che ha creato.
set -euo pipefail
export MSYS_NO_PATHCONV=1

image=${PG_IMAGE:-postgres:17-alpine}
tag=gitstack-poc-roles-$$
fails=0
ok() { echo "OK: $1"; }
ko() { echo "KO: $1"; fails=$((fails + 1)); }
cleanup() {
  rc=$?
  docker rm -f "$tag-a" "$tag-b" "$tag-c" >/dev/null 2>&1 || true
  rm -f "${dump:-}" 2>/dev/null || true
  if [ "$fails" -eq 0 ] && [ "$rc" -eq 0 ]; then echo 'ESITO: OK'; else echo "ESITO: KO ($fails controlli falliti, exit $rc)"; [ "$rc" -eq 0 ] && rc=1; fi
  echo "exit: $rc"; trap - EXIT; exit "$rc"
}
trap cleanup EXIT

root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
echo "commit: $(cd "$root" && git rev-parse HEAD 2>/dev/null || echo n/d)"
echo "immagine: $image"
docker version >/dev/null 2>&1 || { echo 'KO: manca docker'; exit 1; }
dump=$(mktemp)

sql() { # <container> <db> <sql>   (superuser sul socket locale, come fa il controller)
  docker exec "$1" psql -U postgres -d "$2" -v ON_ERROR_STOP=1 -Atq -c "$3"
}
start() { # <nome> ; istanza con superuser `postgres` e socket locale in trust
  docker run -d --name "$tag-$1" -e POSTGRES_HOST_AUTH_METHOD=trust "$image" >/dev/null
  local end=$((SECONDS + 60))
  until docker exec "$tag-$1" pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1; do
    [ "$SECONDS" -ge "$end" ] && { echo "KO: istanza $1 non pronta"; exit 1; }
    sleep 1
  done
  sleep 2
}
# Ruoli di un database <db> in un'istanza (idempotente: e quello che il controller rifa al restore).
mkroles() { # <container> <db>
  sql "$1" postgres "create role ${2}_owner login nosuperuser nocreaterole nocreatedb password 'x'"
  sql "$1" postgres "create role ${2}_ro nologin nosuperuser"
  sql "$1" postgres "grant pg_read_all_data to ${2}_ro"
  sql "$1" postgres "create database $2 owner ${2}_owner"
  sql "$1" "$2" "revoke all on database $2 from public; grant connect on database $2 to ${2}_owner, ${2}_ro"
  sql "$1" "$2" "grant usage on schema public to ${2}_ro; alter schema public owner to ${2}_owner"
}
mklink() { # <container> <db> <ruolo> <rw|ro>
  if [ "$4" = rw ]; then
    sql "$1" postgres "create role $3 login nosuperuser nocreaterole nocreatedb password 'x' in role ${2}_owner"
    sql "$1" postgres "alter role $3 set role = '${2}_owner'"
  else
    sql "$1" postgres "create role $3 login nosuperuser nocreaterole nocreatedb password 'x' in role ${2}_ro"
  fi
  sql "$1" postgres "grant connect on database $2 to $3"
}
alive() { sql "$1" postgres "select count(*) from pg_stat_activity where usename='$2'"; }
session() { # <container> <db> <ruolo>: sessione persistente che esegue una query ogni 0,3 s
  docker exec "$1" sh -c 'printf "select clock_timestamp();\n\\\\watch 0.3\n" > /tmp/w.sql'
  docker exec -d "$1" sh -c "psql -h 127.0.0.1 -U $3 -d $2 -At -f /tmp/w.sql > /tmp/$3.out 2>&1"
}
outsize() { docker exec "$1" sh -c "wc -c < /tmp/$2.out" | tr -d ' \r'; }

# --- A) istanza `shop`: scollegamento --------------------------------------------------------------
start a; a=$tag-a
mkroles "$a" shop
mklink "$a" shop l_rw1 rw
mklink "$a" shop l_rw2 rw
mklink "$a" shop l_ro1 ro
sql "$a" shop "set role shop_owner; create table orders(id int primary key, note text); insert into orders values (1,'uno'),(2,'due'); grant select on orders to shop_ro"
docker exec -i "$a" psql -h 127.0.0.1 -U l_rw1 -d shop -v ON_ERROR_STOP=1 -q -c "create table by_l_rw1(a int); insert into orders values (3,'tre')" >/dev/null
own=$(sql "$a" shop "select r.rolname from pg_class c join pg_roles r on r.oid=c.relowner where c.relname='by_l_rw1'")
if [ "$own" = shop_owner ]; then ok 'oggetto creato da l_rw1: proprietario shop_owner (ALTER ROLE SET role)'; else ko "proprietario di by_l_rw1: $own"; fi

session "$a" shop l_rw1; session "$a" shop l_rw2; session "$a" shop l_ro1
sleep 2
if [ "$(alive "$a" l_rw1)" = 1 ] && [ "$(alive "$a" l_rw2)" = 1 ] && [ "$(alive "$a" l_ro1)" = 1 ]; then ok 'tre sessioni persistenti aperte (rw1, rw2, ro1)'; else ko 'sessioni persistenti non aperte'; fi

# Dimostrazione che NOLOGIN e DROP non bastano: l_ro1 resta connesso e continua a eseguire query.
sql "$a" postgres "alter role l_ro1 nologin"
s1=$(outsize "$a" l_ro1); sleep 1.5; s2=$(outsize "$a" l_ro1)
if [ "$(alive "$a" l_ro1)" = 1 ] && [ "$s2" -gt "$s1" ]; then ok 'ALTER ROLE NOLOGIN da solo: la sessione gia aperta continua a eseguire query'; else ko "sessione l_ro1 non piu viva dopo NOLOGIN ($s1 -> $s2)"; fi

# Ordine di scollegamento di l_rw1: 1) NOLOGIN  2) terminare le sessioni  3) DROP OWNED + DROP ROLE.
sql "$a" postgres "alter role l_rw1 nologin"
sql "$a" postgres "select pg_terminate_backend(pid) from pg_stat_activity where usename='l_rw1'" >/dev/null
end=$((SECONDS + 10)); while [ "$(alive "$a" l_rw1)" != 0 ] && [ "$SECONDS" -lt "$end" ]; do sleep 0.5; done
sql "$a" shop "drop owned by l_rw1"
sql "$a" postgres "drop role l_rw1"
s1=$(outsize "$a" l_rw1); sleep 1.5; s2=$(outsize "$a" l_rw1)
if [ "$(alive "$a" l_rw1)" = 0 ] && [ "$s1" = "$s2" ] && docker exec "$a" grep -q 'terminating connection' /tmp/l_rw1.out; then ok 'l_rw1: la connessione persistente ha smesso di eseguire query (terminating connection) e il ruolo e stato eliminato'; else ko "l_rw1 ancora attivo ($s1 -> $s2)"; fi
if lo=$(docker exec "$a" psql -h 127.0.0.1 -U l_rw1 -d shop -Atc 'select 1' 2>&1); then ko "nuovo login di l_rw1 ammesso: $lo"; elif printf '%s' "$lo" | grep -q 'does not exist'; then ok 'nuovo login di l_rw1 respinto (role does not exist)'; else ko "nuovo login di l_rw1 respinto per un motivo inatteso: $lo"; fi
s1=$(outsize "$a" l_rw2); sleep 1.5; s2=$(outsize "$a" l_rw2)
if [ "$(alive "$a" l_rw2)" = 1 ] && [ "$s2" -gt "$s1" ]; then ok 'l_rw2 (altro collegamento): sessione persistente ancora viva'; else ko "l_rw2 non piu viva ($s1 -> $s2)"; fi
if docker exec -i "$a" psql -h 127.0.0.1 -U l_rw2 -d shop -v ON_ERROR_STOP=1 -q -c "insert into orders values (4,'quattro')" >/dev/null 2>&1; then ok 'l_rw2: nuova connessione e scrittura funzionano'; else ko 'l_rw2 non scrive'; fi
sql "$a" postgres "select pg_terminate_backend(pid) from pg_stat_activity where usename in ('l_rw2','l_ro1')" >/dev/null

# --- dump di A ---------------------------------------------------------------------------------------
docker exec "$a" pg_dump -U postgres -d shop -Fc > "$dump"
if [ -s "$dump" ]; then ok "pg_dump -Fc di shop: $(wc -c < "$dump") byte (contiene owner shop_owner e GRANT a shop_ro)"; else ko 'dump vuoto'; fi

# Verifica di un database ripristinato: <container> <db>
verify() {
  local c=$1 d=$2 n
  n=$(sql "$c" "$d" "select count(*) from orders"); if [ "$n" = 4 ]; then ok "$d: 4 righe ripristinate"; else ko "$d: $n righe"; fi
  n=$(sql "$c" postgres "select rolsuper::int from pg_roles where rolname='${d}_owner'")
  if [ "$n" = 0 ]; then ok "$d: ${d}_owner non e superuser"; else ko "$d: ${d}_owner superuser?"; fi
  n=$(sql "$c" "$d" "select string_agg(distinct r.rolname, ',') from pg_class c join pg_roles r on r.oid=c.relowner where c.relnamespace='public'::regnamespace")
  if [ "$n" = "${d}_owner" ]; then ok "$d: tutti gli oggetti sono di ${d}_owner"; else ko "$d: proprietari inattesi: $n"; fi
  n=$(sql "$c" "$d" "select count(*) from pg_class c, aclexplode(c.relacl) x where c.relnamespace='public'::regnamespace and (x.grantee::regrole::text like 'shop_%' or x.grantor::regrole::text like 'shop_%') and '$d' <> 'shop'")
  local refs; refs=$(docker exec "$c" pg_dump -U postgres -d "$d" -s | grep -c 'shop_' || true)
  if [ "$d" = shop ]; then
    ok "$d: stesso nome dell'origine, controllo dei vecchi ruoli non applicabile (stessi nomi per costruzione)"
  elif [ "$n" = 0 ] && [ "$refs" = 0 ]; then ok "$d: nessun riferimento ai vecchi ruoli (ACL e dump dello schema senza 'shop_')"
  else ko "$d: riferimenti ai vecchi ruoli (acl=$n, dump=$refs)"; fi
  mklink "$c" "$d" "lk_rw" rw; mklink "$c" "$d" "lk_ro" ro
  if docker exec "$c" psql -h 127.0.0.1 -U lk_rw -d "$d" -v ON_ERROR_STOP=1 -q -c "insert into orders values (10,'rw')" >/dev/null 2>&1; then ok "$d: il link rw scrive"; else ko "$d: il link rw non scrive"; fi
  n=$(docker exec "$c" psql -h 127.0.0.1 -U lk_ro -d "$d" -Atc 'select count(*) from orders' 2>&1 || true)
  if [ "$n" = 5 ]; then ok "$d: il link ro legge"; else ko "$d: il link ro non legge: $n"; fi
  if lo=$(docker exec "$c" psql -h 127.0.0.1 -U lk_ro -d "$d" -Atc "insert into orders values (11,'ro')" 2>&1); then ko "$d: il link ro scrive"; elif printf '%s' "$lo" | grep -q 'permission denied'; then ok "$d: il link ro non scrive (permission denied)"; else ko "$d: il link ro respinto per un motivo inatteso: $lo"; fi
  # oggetto creato da un link rw dopo il restore: ancora del proprietario
  docker exec "$c" psql -h 127.0.0.1 -U lk_rw -d "$d" -q -c "create table dopo(a int)" >/dev/null 2>&1
  n=$(sql "$c" "$d" "select r.rolname from pg_class c join pg_roles r on r.oid=c.relowner where c.relname='dopo'")
  if [ "$n" = "${d}_owner" ]; then ok "$d: un oggetto creato dopo il restore e di ${d}_owner"; else ko "$d: proprietario di dopo: $n"; fi
}
restore_into() { # <container> <db>: istanza vuota -> ruoli del controller -> pg_restore senza owner/ACL
  local c=$1 d=$2 err
  mkroles "$c" "$d"
  err=$(docker exec -i "$c" pg_restore -U postgres -d "$d" --no-owner --no-acl --role="${d}_owner" < "$dump" 2>&1) || { ko "$d: pg_restore fallito: $err"; return; }
  if [ -z "$err" ]; then ok "$d: pg_restore --no-owner --no-acl --role=${d}_owner senza errori"; else ko "$d: pg_restore ha scritto: $err"; fi
  # privilegi riapplicati in modo esplicito (non vengono dal dump)
  sql "$c" "$d" "grant select on all tables in schema public to ${d}_ro; alter default privileges for role ${d}_owner in schema public grant select on tables to ${d}_ro"
}

# --- B) istanza vuota, nome diverso (blog) -----------------------------------------------------------
start b; restore_into "$tag-b" blog; verify "$tag-b" blog
# --- C) istanza vuota, stesso nome (shop): ripristino d'istanza --------------------------------------
start c; restore_into "$tag-c" shop; verify "$tag-c" shop

# Controllo negativo: senza --no-owner il restore nel database con nome diverso FALLISCE (ruolo shop_owner assente).
mkroles "$tag-b" ctrl
if err=$(docker exec -i "$tag-b" pg_restore -U postgres -d ctrl --no-acl --exit-on-error < "$dump" 2>&1); then ko 'pg_restore senza --no-owner in un database con nome diverso non e fallito'; elif printf '%s' "$err" | grep -q 'role "shop_owner" does not exist'; then ok 'controllo: senza --no-owner il restore con nome diverso fallisce (role "shop_owner" does not exist)'; else ko "controllo negativo fallito per un motivo inatteso: $err"; fi
