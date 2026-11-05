#!/usr/bin/env bash
# Prova di fattibilita dell'ADR 0003 (punti 1, 2, 4, 5, 6) su k3d. Una riga OK/KO per controllo,
# poi ESITO ed exit. Ripulisce sempre il cluster. Nessun `! cmd` sotto set -e: i controlli
# negativi sono `if ...; then KO`.
set -euo pipefail

cluster=${K3D_CLUSTER:-gitstack-db-poc}
lanport=${DB_LAN_PORT:-15432}
image=${PG_IMAGE:-postgres:17-alpine}
k3d_bin=${K3D_BIN:-k3d}
kubectl_bin=${KUBECTL_BIN:-kubectl}
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
workabs=$(mktemp -d)
created=false
cd "$workabs"
work=.
fails=0

case "$(uname -s)" in
  MINGW*|MSYS*) subj=// ;;
  *) subj=/ ;;
esac

ok() { echo "OK: $1"; }
ko() { echo "KO: $1"; fails=$((fails + 1)); }

cleanup() {
  rc=$?
  cd "$root"; rm -rf "$workabs"
  if "$created" && [ "${KEEP:-0}" != 1 ]; then "$k3d_bin" cluster delete "$cluster" >/dev/null 2>&1 || true; fi
  if [ "$fails" -eq 0 ] && [ "$rc" -eq 0 ]; then
    echo 'ESITO: OK'
  else
    echo "ESITO: KO ($fails controlli falliti, exit $rc)"
    [ "$rc" -eq 0 ] && rc=1
  fi
  echo "exit: $rc"
  trap - EXIT
  exit "$rc"
}
trap cleanup EXIT

echo "commit: $(cd "$root" && git rev-parse HEAD 2>/dev/null || echo n/d)"
echo "k3d: $("$k3d_bin" version | head -n 1)"
echo "immagine: $image"
for tool in openssl docker; do command -v "$tool" >/dev/null || { echo "KO: manca $tool"; exit 1; }; done
"$k3d_bin" version >/dev/null 2>&1 || { echo 'KO: manca k3d'; exit 1; }
if "$k3d_bin" cluster list -o json | grep -q "\"$cluster\""; then echo "KO: cluster $cluster gia esistente"; exit 1; fi

docker pull -q "$image" >/dev/null
"$k3d_bin" cluster create "$cluster" --image rancher/k3s:v1.32.5-k3s1 --port "$lanport:$lanport@loadbalancer" \
  --wait --kubeconfig-update-default=false --kubeconfig-switch-context=false >/dev/null
created=true
"$k3d_bin" kubeconfig get "$cluster" > "$work/kubeconfig"
k=("$kubectl_bin" --kubeconfig "$work/kubeconfig")

# CA privata e certificato del server (DB4/DB9: SAN = nome interno, nome corto, nome LAN).
# Le date partono da ieri: l'orologio della VM di Docker Desktop puo essere indietro di minuti
# rispetto al host e un certificato "non ancora valido" darebbe `certificate verify failed`.
nb=$(date -u -d '-1 day' +%y%m%d%H%M%SZ)
na=$(date -u -d '+2 days' +%y%m%d%H%M%SZ)
: > index.txt
printf '01\n' > serial
cat > ca.cnf <<'CNF'
[ca]
default_ca = CA_default
[CA_default]
dir = .
database = ./index.txt
new_certs_dir = .
serial = ./serial
default_md = sha256
policy = pol
unique_subject = no
[pol]
commonName = supplied
[v3_ca]
basicConstraints = critical,CA:true
keyUsage = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
CNF
openssl req -new -newkey rsa:2048 -nodes -subj "${subj}CN=GitStack DB POC CA" -keyout "$work/ca.key" -out "$work/ca.csr" >/dev/null 2>&1
openssl ca -batch -selfsign -config ca.cnf -extensions v3_ca -keyfile "$work/ca.key" -in "$work/ca.csr" -out "$work/ca.crt" -startdate "$nb" -enddate "$na" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -subj "${subj}CN=pg" -keyout "$work/tls.key" -out "$work/tls.csr" >/dev/null 2>&1
printf 'subjectAltName=DNS:pg.gitstack-db-poc.svc.cluster.local,DNS:pg.gitstack-db-poc.svc,DNS:db.gitstack.test\n' > "$work/ext.cnf"
openssl ca -batch -config ca.cnf -cert "$work/ca.crt" -keyfile "$work/ca.key" -in "$work/tls.csr" -out "$work/tls.crt" -startdate "$nb" -enddate "$na" -extfile "$work/ext.cnf" >/dev/null 2>&1

pw1=$(openssl rand -hex 16)
pw2=$(openssl rand -hex 16)

"${k[@]}" apply -f - >/dev/null <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: gitstack-db-poc
EOF
"${k[@]}" -n gitstack-db-poc create secret generic pg-auth --from-literal=password="$pw1" >/dev/null
"${k[@]}" -n gitstack-db-poc create secret generic pg-tls --from-file=tls.crt="$work/tls.crt" --from-file=tls.key="$work/tls.key" >/dev/null
"${k[@]}" apply -f "$root/db.yaml" >/dev/null
for ns in app-linked app-other; do
  "${k[@]}" create namespace "$ns" >/dev/null
  "${k[@]}" -n "$ns" create configmap pg-ca --from-file=ca.crt="$work/ca.crt" >/dev/null
done
# Collegamento: il controller etichetta il namespace dell'app (DB11).
"${k[@]}" label namespace app-linked gitstack.io/db-link-poc=true >/dev/null

if "${k[@]}" -n gitstack-db-poc rollout status statefulset/pg --timeout=300s >/dev/null 2>&1; then
  ok 'StatefulSet pg pronto (PVC local-path, namespace Pod Security restricted)'
else
  ko 'StatefulSet pg non pronto entro 300 s'
  "${k[@]}" -n gitstack-db-poc describe pod pg-0 | tail -n 30 || true
  "${k[@]}" -n gitstack-db-poc logs pg-0 --tail=30 || true
  exit 1
fi

uid=$("${k[@]}" -n gitstack-db-poc exec pg-0 -- id -u)
if [ "$uid" = 70 ]; then ok "postgres gira come uid $uid (non root), root fs in sola lettura"; else ko "uid $uid, atteso 70"; fi
sc=$("${k[@]}" -n gitstack-db-poc get pvc data-pg-0 -o jsonpath='{.spec.storageClassName}/{.status.phase}')
if [ "$sc" = 'local-path/Bound' ]; then ok "PVC data-pg-0: $sc"; else ko "PVC $sc"; fi

# Un pod root nello stesso namespace e rifiutato da Pod Security restricted.
if "${k[@]}" -n gitstack-db-poc run rootpod --image="$image" --restart=Never --command -- sleep 1 >/dev/null 2>&1; then
  ko 'un pod root e stato ammesso nel namespace del database'
  "${k[@]}" -n gitstack-db-poc delete pod rootpod --now >/dev/null 2>&1 || true
else
  ok 'pod root rifiutato da Pod Security restricted nel namespace del database'
fi

# psql da un pod di un namespace. Uso: pq <ns> <nome> <password> <host> <sslmode> <extra> <sql>
pq() {
  local ns=$1 name=$2 pass=$3 host=$4 mode=$5 user=$6 sql=$7
  local ov
  ov='{"spec":{"volumes":[{"name":"ca","configMap":{"name":"pg-ca"}}],"containers":[{"name":"c","image":"'"$image"'","imagePullPolicy":"IfNotPresent","env":[{"name":"PGPASSWORD","value":"'"$pass"'"}],"command":["sh","-c","sleep 10; exec psql \"$0\" -Atc \"$1\"","host='"$host"' port=5432 dbname=app user='"$user"' sslmode='"$mode"' sslrootcert=/ca/ca.crt connect_timeout=5","'"$sql"'"],"volumeMounts":[{"name":"ca","mountPath":"/ca"}]}]}}'
  "${k[@]}" -n "$ns" delete pod "$name" --ignore-not-found --now >/dev/null 2>&1
  "${k[@]}" -n "$ns" run "$name" --image="$image" --restart=Never --overrides="$ov" >/dev/null
  local end=$((SECONDS + 180)) ph=''
  while [ "$SECONDS" -lt "$end" ]; do
    ph=$("${k[@]}" -n "$ns" get pod "$name" -o jsonpath='{.status.phase}' 2>/dev/null || true)
    case "$ph" in Succeeded|Failed) break ;; esac
    sleep 1
  done
  out=$("${k[@]}" -n "$ns" logs "$name" 2>&1 || true)
  pqphase=$ph
}

fqdn=pg.gitstack-db-poc.svc.cluster.local

pq app-linked c1 "$pw1" "$fqdn" verify-full owner 'select 42'
if [ "$pqphase" = Succeeded ] && [ "$out" = 42 ]; then ok 'app collegata: psql verify-full col nome interno e la CA'; else ko "app collegata: fase $pqphase, out=$out"; fi

pq app-other c2 "$pw1" "$fqdn" verify-full owner 'select 42'
if [ "$pqphase" = Failed ] && printf '%s' "$out" | grep -qiE 'timeout expired|Connection refused'; then ok 'app NON collegata: respinta dalla NetworkPolicy (kube-router risponde Connection refused o scarta)'; else ko "app non collegata: fase $pqphase, out=$out"; fi

pq app-linked c3 "$pw1" "$fqdn" disable owner 'select 42'
if [ "$pqphase" = Failed ] && printf '%s' "$out" | grep -q 'rejects connection'; then ok 'sslmode=disable respinto (pg_hba: hostnossl reject)'; else ko "sslmode=disable ammesso: $out"; fi

pq app-linked c4 wrongpass "$fqdn" verify-full owner 'select 42'
if [ "$pqphase" = Failed ] && printf '%s' "$out" | grep -q 'password authentication failed'; then ok 'password sbagliata respinta'; else ko 'password sbagliata ammessa'; fi

# Ruolo di sola lettura (DB5): creato dal controller con exec sul socket locale.
"${k[@]}" -n gitstack-db-poc exec pg-0 -- psql -U owner -d app -v ON_ERROR_STOP=1 -q -c "create table t(a int); insert into t values (1),(2),(3); create role ro login password '$pw1'; grant pg_read_all_data to ro;" >/dev/null
pq app-linked c5 "$pw1" "$fqdn" verify-full ro 'select count(*) from t'
if [ "$pqphase" = Succeeded ] && [ "$out" = 3 ]; then ok 'ruolo ro legge'; else ko "ruolo ro non legge: $out"; fi
pq app-linked c6 "$pw1" "$fqdn" verify-full ro 'insert into t values (4)'
if [ "$pqphase" = Failed ] && printf '%s' "$out" | grep -qi 'permission denied'; then ok 'ruolo ro non scrive (permission denied)'; else ko "ruolo ro scrive: $out"; fi

# Backup e ripristino in un database nuovo (DB13): pg_dump -Fc dal pod, pg_restore in `app2`.
"${k[@]}" -n gitstack-db-poc exec pg-0 -- pg_dump -U owner -d app -Fc > "$work/app.dump"
if [ -s "$work/app.dump" ]; then ok "pg_dump -Fc: $(wc -c < "$work/app.dump") byte"; else ko 'dump vuoto'; fi
"${k[@]}" -n gitstack-db-poc exec pg-0 -- createdb -U owner app2
"${k[@]}" -n gitstack-db-poc exec -i pg-0 -- pg_restore -U owner -d app2 --no-owner < "$work/app.dump"
n=$("${k[@]}" -n gitstack-db-poc exec pg-0 -- psql -U owner -d app2 -Atc 'select count(*) from t')
if [ "$n" = 3 ]; then ok 'ripristino in un database nuovo: 3 righe'; else ko "ripristino: $n righe"; fi

# Rotazione della password (DB12): ALTER ROLE + Secret; la vecchia smette di funzionare.
"${k[@]}" -n gitstack-db-poc exec pg-0 -- psql -U owner -d app -q -c "alter role owner password '$pw2'" >/dev/null
pq app-linked c7 "$pw1" "$fqdn" verify-full owner 'select 1'
if [ "$pqphase" = Failed ] && printf '%s' "$out" | grep -q 'password authentication failed'; then ok 'dopo la rotazione la vecchia password e respinta'; else ko 'vecchia password ancora valida'; fi
pq app-linked c8 "$pw2" "$fqdn" verify-full owner 'select 1'
if [ "$pqphase" = Succeeded ]; then ok 'dopo la rotazione la nuova password funziona'; else ko "nuova password: $out"; fi

# Scollegamento (DB11): tolta la label, l'app non passa piu.
"${k[@]}" label namespace app-linked gitstack.io/db-link-poc- >/dev/null
sleep 3
pq app-linked c9 "$pw2" "$fqdn" verify-full owner 'select 1'
if [ "$pqphase" = Failed ] && printf '%s' "$out" | grep -qE 'timeout expired|Connection refused'; then ok 'dopo lo scollegamento l app e respinta'; else ko 'app ancora ammessa dopo lo scollegamento'; fi

# Misura a vuoto (metrics-server di k3s).
end=$((SECONDS + 150))
top=''
while [ "$SECONDS" -lt "$end" ]; do
  top=$("${k[@]}" -n gitstack-db-poc top pod pg-0 --no-headers 2>/dev/null || true)
  [ -n "$top" ] && break
  sleep 5
done
echo "misura a vuoto (kubectl top, pod pg-0: CPU(cores) MEMORY(bytes)): ${top:-non disponibile entro 150 s}"

# --- Porta LAN (punto 6) -------------------------------------------------------------------------
"${k[@]}" apply -f "$root/lan.yaml" >/dev/null
hostpsql() {
  # psql 17 dalla macchina che ospita k3d, con la CA su stdin. Uso: hostpsql <conninfo-extra> <password>
  MSYS_NO_PATHCONV=1 docker run --rm -i --add-host db.gitstack.test:host-gateway --add-host wrong.gitstack.test:host-gateway -e PGPASSWORD="$2" "$image" \
    sh -c 'cat > /tmp/ca.crt; exec psql "host=db.gitstack.test port='"$lanport"' dbname=app user=owner connect_timeout=5 '"$1"'" -Atc "select current_user"' < "$work/ca.crt" 2>&1
}
end=$((SECONDS + 90)); lanout=''
while [ "$SECONDS" -lt "$end" ]; do
  if lanout=$(hostpsql 'sslmode=verify-full sslrootcert=/tmp/ca.crt' "$pw2"); then break; fi
  sleep 3
done
if [ "$lanout" = owner ]; then ok "LAN: psql sslmode=verify-full da fuori dal cluster sulla porta $lanport"; else ko "LAN verify-full: $lanout"; fi

# Controllo negativo: deve fallire E per il motivo atteso (non per una porta chiusa per caso).
lanneg() { # <descrizione> <motivo atteso (regex)> <conninfo> <password>
  local rc=0 lo
  lo=$(hostpsql "$3" "$4") || rc=$?
  if [ "$rc" -eq 0 ]; then ko "LAN: $1: ammesso ($lo)"
  elif printf '%s' "$lo" | grep -qE "$2"; then ok "LAN: $1: respinto ($2)"
  else ko "LAN: $1: respinto per un motivo inatteso: $lo"; fi
}
lanneg 'sslmode=disable' 'rejects connection' 'sslmode=disable' "$pw2"
lanneg 'verify-full senza la CA privata' 'certificate verify failed' 'sslmode=verify-full sslrootcert=system' "$pw2"
lanneg 'verify-full con hostname sbagliato' 'does not match host name' 'host=wrong.gitstack.test sslmode=verify-full sslrootcert=/tmp/ca.crt' "$pw2"
lanneg 'password sbagliata' 'password authentication failed' 'sslmode=verify-full sslrootcert=/tmp/ca.crt' wrongpass

# Con la LAN accesa, un'app NON collegata resta fuori dal CIDR dei pod (except 10.42.0.0/16).
pq app-other c10 "$pw2" "$fqdn" verify-full owner 'select 1'
if [ "$pqphase" = Failed ] && printf '%s' "$out" | grep -qE 'timeout expired|Connection refused'; then ok 'LAN accesa: l app non collegata e comunque respinta in cluster'; else ko "LAN accesa: app non collegata ammessa: $out"; fi

# Interruttore spento: la porta si chiude.
"${k[@]}" delete -f "$root/lan.yaml" >/dev/null
end=$((SECONDS + 60)); closed=false
while [ "$SECONDS" -lt "$end" ]; do
  if hostpsql 'sslmode=verify-full sslrootcert=/tmp/ca.crt' "$pw2" >/dev/null; then sleep 3; else closed=true; break; fi
done
if "$closed"; then ok 'LAN spenta: la porta non risponde piu'; else ko 'LAN spenta ma la porta risponde ancora'; fi
