#!/usr/bin/env bash
# deploy/test-vm/e2e/remote.sh — aiutante remoto del test end-to-end di
# GIT-11. Copiato via scp sulla VM di test da deploy/test-vm/e2e.ps1 e
# lanciato via ssh, un sottocomando alla volta: tutto quello che il test
# deve verificare *dentro* la VM (stato pulito, cluster k3s, JetStream,
# Secret di Postgres, diagnostica), non raggiungibile dall'host Windows del
# board. Le verifiche raggiungibili dall'host (UI, /api/healthz, create+read
# della risorsa di prova) restano in e2e.ps1: il criterio del CTO le vuole
# provate "dall'host Windows, non solo da dentro la VM".
#
# Uso: remote.sh <sottocomando> [argomenti]
#   clean-check                              Criterio 2 del commento CTO del
#                                             28/09: nessun k3s/kubectl,
#                                             nessuna cartella
#                                             /etc/rancher o /var/lib/rancher,
#                                             porte 80/443/6443 libere.
#                                             Exit 0 se pulita, 1 altrimenti.
#   jetstream-count <stream> <release> <ns>  Numero di messaggi nello stream
#                                             JetStream indicato (default
#                                             CORE/gitstack/default), via un
#                                             pod nats-box effimero
#                                             (immagine pinnata, vedi
#                                             NATS_BOX_IMAGE sotto). Stampa
#                                             solo il numero su stdout.
#   postgres-secret-hash <release> <ns>      sha256 della password (ancora
#                                             base64) nel Secret
#                                             "<release>-postgres": per
#                                             l'idempotenza (criterio f del
#                                             piano CTO) senza mai stampare
#                                             la password in chiaro.
#   admin-password <release> <ns>            Password iniziale dell'admin dal
#                                             Secret "<release>-identity-admin"
#                                             (GIT-35). Solo per il login
#                                             dell'e2e: chi lo chiama non
#                                             deve scriverla in nessun log.
#   admin-secret-state <release> <ns>        resourceVersion e sha256 dei dati
#                                             del Secret dell'admin, senza mai
#                                             stampare la password: per la
#                                             prova di idempotenza (GIT-35).
#   k3s-active-since                         ActiveEnterTimestamp del
#                                             servizio systemd k3s: se
#                                             un'installazione idempotente
#                                             non lo cambia, k3s non è stato
#                                             riavviato/reinstallato.
#   mirror-dest-start <porta> <utente> <token> [ip-alias] (GIT-181) Repo bare servito via HTTPS
#                                             da mirror-dest.py col certificato
#                                             della VM, in ascolto sulla porta.
#   mirror-dest-stop | mirror-dest-ref <ref> | mirror-dest-refs
#   mirror-dest-commit <messaggio>            (GIT-201) Aggiunge un commit a main del
#                                             repo bare della destinazione (che fa da
#                                             origine di un mirror in pull) e ne
#                                             stampa lo sha.
#   mirror-enable <release> <ns> <ip>         git si fida della CA della VM per i
#                                             mirror; core ammette <ip> (egress).
#   mirror-disable <release> <ns>             Toglie il cablaggio.
#   collect-diagnostics <cartella>           Raccoglie journalctl -u k3s,
#                                             kubectl get all -A, describe e
#                                             log dei pod, eventi, df/free
#                                             nella cartella indicata (creata
#                                             se manca). Non fallisce mai
#                                             (ogni comando ha "|| true"):
#                                             va bene chiamarla anche dopo
#                                             un'installazione fallita a
#                                             metà.
set -euo pipefail

# Tag pinnato, stesso principio di k3s/Helm/golangci-lint in questo
# repository (mai "latest"): natsio/nats-box è l'immagine ufficiale con la
# CLI "nats" (github.com/nats-io/natscli), qui usata solo per interrogare
# JetStream via un pod effimero (--rm), senza installare nulla sulla VM.
NATS_BOX_IMAGE="${NATS_BOX_IMAGE:-natsio/nats-box:0.20.0}"

KUBECONFIG_PATH="${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"
export KUBECONFIG="${KUBECONFIG_PATH}"

# GIT-228: il kubeconfig di k3s è 600 root (cluster-admin): i comandi kubectl
# di questo script, lanciati da un utente non root, passano da sudo.
k3s_kubectl() {
  sudo -n env "KUBECONFIG=${KUBECONFIG_PATH}" k3s kubectl "$@"
}

# Un solo trap EXIT per tutto lo script (un trap dentro una funzione verrebbe
# sovrascritto o scatterebbe alla fine di una subshell): ripulisce i file
# temporanei e i pod effimeri registrati negli array.
CLEANUP_FILES=()
CLEANUP_PODS=()
cleanup() {
  local f p
  for f in "${CLEANUP_FILES[@]:-}"; do
    [ -n "${f}" ] && rm -f "${f}"
  done
  for p in "${CLEANUP_PODS[@]:-}"; do
    [ -n "${p}" ] && k3s_kubectl delete pod "${p#*/}" --namespace "${p%%/*}" --wait=false --ignore-not-found >/dev/null 2>&1
  done
  return 0
}
trap cleanup EXIT

log() {
  printf '==> %s\n' "$*"
}

fail() {
  printf 'ERRORE: %s\n' "$*" >&2
  exit 1
}

# --- clean-check -------------------------------------------------------

clean_check() {
  local problems=()

  if command -v k3s >/dev/null 2>&1; then
    problems+=("comando 'k3s' già presente in PATH ($(command -v k3s))")
  fi
  if command -v kubectl >/dev/null 2>&1; then
    problems+=("comando 'kubectl' già presente in PATH ($(command -v kubectl))")
  fi
  if systemctl list-unit-files 2>/dev/null | grep -q '^k3s\.service'; then
    problems+=("servizio systemd 'k3s' già registrato")
  fi
  if [ -d /etc/rancher ]; then
    problems+=("cartella /etc/rancher già presente")
  fi
  if [ -d /var/lib/rancher ]; then
    problems+=("cartella /var/lib/rancher già presente")
  fi

  if command -v ss >/dev/null 2>&1; then
    local port busy
    for port in 80 443 6443; do
      busy="$(ss -ltnH "( sport = :${port} )" 2>/dev/null || true)"
      if [ -n "${busy}" ]; then
        problems+=("porta ${port} già occupata: $(printf '%s' "${busy}" | head -n1)")
      fi
    done
  else
    problems+=("comando 'ss' non trovato: non posso verificare le porte 80/443/6443")
  fi

  if [ "${#problems[@]}" -gt 0 ]; then
    echo "NON PULITA:"
    local p
    for p in "${problems[@]}"; do
      printf ' - %s\n' "${p}"
    done
    exit 1
  fi

  echo "PULITA: nessun k3s/kubectl, nessun servizio k3s, nessuna cartella /etc/rancher o /var/lib/rancher, porte 80/443/6443 libere."
}

# --- jetstream-count -----------------------------------------------------

jetstream_count() {
  local stream="${1:-CORE}"
  local release="${2:-gitstack}"
  local ns="${3:-default}"
  local nats_addr="${release}-nats.${ns}.svc.cluster.local:4222"
  local pod_name="gitstack-e2e-natsbox-$$-${RANDOM}"
  local err_file
  err_file="$(mktemp)"
  CLEANUP_FILES+=("${err_file}")
  CLEANUP_PODS+=("${ns}/${pod_name}")

  # NATS vuole utente e password (F8) e le NetworkPolicy lasciano entrare
  # sulla porta client solo core e git: il pod di prova prende le label di
  # core (e quindi le sue regole) e le credenziali dal Secret del chart.
  local nats_user nats_pass
  nats_user="$(k3s_kubectl get secret "${release}-nats-auth" --namespace "${ns}" -o jsonpath='{.data.user}' | base64 -d)"
  nats_pass="$(k3s_kubectl get secret "${release}-nats-auth" --namespace "${ns}" -o jsonpath='{.data.password}' | base64 -d)"
  [ -n "${nats_pass}" ] || fail "Secret ${release}-nats-auth senza password (namespace ${ns})"

  # Le NetworkPolicy di un pod nuovo arrivano con qualche secondo di ritardo:
  # finche' non passano, il client NATS risponde "no servers available". Il
  # pod aspetta quindi che la porta risponda (nc, fino a 30 s) prima di lanciare nats.
  # Niente attach (-i/--rm): con un pod che finisce subito l'attach puo'
  # perdere l'output ("If you don't see a command prompt...") e mescolare
  # stdout e stderr. Il pod gira fino in fondo, si attende la fase finale e
  # il JSON si legge da 'kubectl logs' (solo stdout del container). Il pod
  # viene cancellato dal trap EXIT dello script, anche in caso di errore.
  # shellcheck disable=SC2016 # lo script tra apici singoli lo espande la sh del pod ($1 passato come argomento), non questa shell
  if ! k3s_kubectl run "${pod_name}" --namespace "${ns}" --restart=Never \
      --labels="app.kubernetes.io/name=core,app.kubernetes.io/instance=${release},app.kubernetes.io/part-of=gitstack" \
      --image="${NATS_BOX_IMAGE}" --command -- \
      sh -c 'for _ in $(seq 1 30); do nc -z -w 2 "${1%%:*}" "${1##*:}" && break; sleep 1; done; shift; exec nats "$@"' _ \
      "${nats_addr}" stream info "${stream}" --server "nats://${nats_user}:${nats_pass}@${nats_addr}" --json \
      >/dev/null 2>"${err_file}"; then
    fail "creazione del pod ${pod_name} fallita: $(cat "${err_file}")"
  fi

  local phase="" _i
  for _i in $(seq 1 45); do
    phase="$(k3s_kubectl get pod "${pod_name}" --namespace "${ns}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    if [ "${phase}" = "Succeeded" ] || [ "${phase}" = "Failed" ]; then
      break
    fi
    sleep 2
  done
  if [ "${phase}" != "Succeeded" ]; then
    local pod_state pod_events pod_logs
    pod_state="$(k3s_kubectl get pod "${pod_name}" --namespace "${ns}" -o wide 2>&1 | tail -n 2 | tr '\n' ' ')"
    pod_events="$(k3s_kubectl get events --namespace "${ns}" --field-selector "involvedObject.name=${pod_name}" -o custom-columns=REASON:.reason,MSG:.message --no-headers 2>&1 | tail -n 5 | tr '\n' ' ')"
    pod_logs="$(k3s_kubectl logs "${pod_name}" --namespace "${ns}" 2>&1 | tail -n 5 | tr '\n' ' ')"
    fail "il pod ${pod_name} non e' arrivato a Succeeded (fase: '${phase:-sconosciuta}'). Stato: ${pod_state} Eventi: ${pod_events} Log: ${pod_logs}"
  fi

  local out
  if ! out="$(k3s_kubectl logs "${pod_name}" --namespace "${ns}" 2>"${err_file}")"; then
    fail "lettura dei log del pod ${pod_name} fallita: $(cat "${err_file}")"
  fi

  local messages
  messages="$(printf '%s' "${out}" | grep -o '"messages"[[:space:]]*:[[:space:]]*[0-9]\+' | head -n1 | grep -o '[0-9]\+$' || true)"
  [ -n "${messages}" ] || fail "impossibile leggere 'messages' dai log di 'nats stream info ${stream}' (pod ${pod_name}, fase ${phase}): ${out}"
  printf '%s
' "${messages}"
}

# --- postgres-secret-hash -------------------------------------------------

postgres_secret_hash() {
  local release="${1:-gitstack}"
  local ns="${2:-default}"
  local secret="${release}-postgres"
  local pwd_b64

  pwd_b64="$(k3s_kubectl get secret "${secret}" -n "${ns}" -o jsonpath='{.data.password}' 2>&1)" \
    || fail "impossibile leggere il Secret '${secret}' nel namespace '${ns}': ${pwd_b64}"
  [ -n "${pwd_b64}" ] || fail "il Secret '${secret}' non ha la chiave 'password'."
  printf '%s' "${pwd_b64}" | sha256sum | awk '{print $1}'
}

# --- admin-password / admin-secret-state (GIT-35) ---------------------------

admin_password() {
  local release="${1:-gitstack}"
  local ns="${2:-default}"
  local secret="${release}-identity-admin"
  local pwd_b64
  pwd_b64="$(k3s_kubectl get secret "${secret}" -n "${ns}" -o jsonpath='{.data.password}' 2>&1)" \
    || fail "impossibile leggere il Secret '${secret}' nel namespace '${ns}'."
  [ -n "${pwd_b64}" ] || fail "il Secret '${secret}' non ha la chiave 'password'."
  printf '%s' "${pwd_b64}" | base64 -d
}

admin_secret_state() {
  local release="${1:-gitstack}"
  local ns="${2:-default}"
  local secret="${release}-identity-admin"
  local rv data
  rv="$(k3s_kubectl get secret "${secret}" -n "${ns}" -o jsonpath='{.metadata.resourceVersion}' 2>&1)" \
    || fail "impossibile leggere il Secret '${secret}' nel namespace '${ns}'."
  data="$(k3s_kubectl get secret "${secret}" -n "${ns}" -o jsonpath='{.data}' 2>&1)" \
    || fail "impossibile leggere i dati del Secret '${secret}'."
  printf '%s %s\n' "${rv}" "$(printf '%s' "${data}" | sha256sum | awk '{print $1}')"
}

# --- k3s-active-since ------------------------------------------------------

k3s_active_since() {
  systemctl show -p ActiveEnterTimestamp k3s 2>/dev/null | cut -d= -f2- \
    || fail "impossibile leggere ActiveEnterTimestamp del servizio k3s."
}

# Snapshot prima della reinstallazione e nella diagnostica finale.
collect_ci_diagnostics() {
  local dir="${1:?cartella}" ns="gitstack-ci-jobs" pod service
  mkdir -p "$dir"
  for service in core git; do
    { k3s_kubectl -n default logs "deployment/gitstack-${service}" --all-containers --tail=5000 || true; } >"$dir/${service}.log" 2>&1
  done
  { k3s_kubectl -n "$ns" get events --sort-by=.lastTimestamp || true; } >"$dir/ci-events.txt" 2>&1
  { k3s_kubectl -n "$ns" get pods -o wide || true; } >"$dir/ci-pods.txt" 2>&1
  : >"$dir/ci-describe.txt"
  : >"$dir/ci-logs.txt"
  while read -r pod; do
    [ -n "$pod" ] || continue
    { printf '\n===== %s/%s =====\n' "$ns" "$pod"; k3s_kubectl -n "$ns" describe pod "$pod" || true; } >>"$dir/ci-describe.txt" 2>&1
    { printf '\n===== %s/%s =====\n' "$ns" "$pod"; k3s_kubectl -n "$ns" logs "$pod" --all-containers --prefix --tail=5000 || true; } >>"$dir/ci-logs.txt" 2>&1
  done < <(k3s_kubectl -n "$ns" get pods -o name 2>>"$dir/ci-pods.txt")
}

# --- collect-diagnostics ---------------------------------------------------

collect_diagnostics() {
  local dir="${1:?collect-diagnostics richiede la cartella di destinazione}"
  mkdir -p "${dir}"
  collect_ci_diagnostics "${dir}"

  { sudo journalctl -u k3s --no-pager -n 5000 || echo "journalctl -u k3s non disponibile"; } \
    >"${dir}/journalctl-k3s.log" 2>&1

  { k3s_kubectl get all -A -o wide || echo "kubectl get all -A non disponibile"; } \
    >"${dir}/kubectl-get-all.txt" 2>&1

  { k3s_kubectl get events -A --sort-by=.lastTimestamp || echo "kubectl get events non disponibile"; } \
    >"${dir}/kubectl-events.txt" 2>&1

  { k3s_kubectl get pods -A -o yaml || echo "kubectl get pods -o yaml non disponibile"; } \
    >"${dir}/kubectl-pods.yaml" 2>&1

  : >"${dir}/kubectl-describe-pods.txt"
  : >"${dir}/kubectl-logs-pods.txt"
  local ns pod
  while read -r ns pod; do
    [ -n "${pod}" ] || continue
    {
      printf '\n===== %s/%s =====\n' "${ns}" "${pod}"
      k3s_kubectl -n "${ns}" describe pod "${pod}" || true
    } >>"${dir}/kubectl-describe-pods.txt" 2>&1
    {
      printf '\n===== %s/%s =====\n' "${ns}" "${pod}"
      k3s_kubectl -n "${ns}" logs "${pod}" --all-containers --tail=1000 || true
    } >>"${dir}/kubectl-logs-pods.txt" 2>&1
  done < <(k3s_kubectl get pods -A --no-headers 2>/dev/null | awk '{print $1, $2}')

  { helm status gitstack -n default || echo "helm status non disponibile"; } \
    >"${dir}/helm-status.txt" 2>&1

  { df -h || true; } >"${dir}/df.txt" 2>&1
  { free -h || true; } >"${dir}/free.txt" 2>&1
  { uname -a || true; } >"${dir}/uname.txt" 2>&1

  echo "OK: diagnostica raccolta in ${dir}"
}

# --- mirror (GIT-181) --------------------------------------------------------
#
# Destinazione di prova per i mirror in push: un repo bare servito via HTTPS da
# mirror-dest.py (git http-backend) con il certificato e la CA della VM, e il
# cablaggio per fidarsene: core ammette il suo indirizzo (egress.allow, C8) e il
# servizio git si fida della CA per i soli push dei mirror.

MIRROR_DIR="/tmp/gitstack-e2e/mirror-dest"
MIRROR_CA_MAP="gitstack-e2e-mirror-ca"
TLS_DIR="${GITSTACK_TLS_DIR:-/etc/gitstack/tls}"

mirror_dest_stop() {
  local pid
  if [ -f "${MIRROR_DIR}/pid" ]; then
    pid="$(cat "${MIRROR_DIR}/pid")"
    if [ -n "${pid}" ] && kill -0 "${pid}" 2>/dev/null; then
      kill "${pid}" 2>/dev/null || true
    fi
    rm -f "${MIRROR_DIR}/pid"
  fi
  # Toglie l'alias di indirizzo messo da mirror_dest_start (GIT-309).
  if [ -f "${MIRROR_DIR}/alias-ip" ]; then
    sudo -n ip addr del "$(cat "${MIRROR_DIR}/alias-ip")/32" dev lo 2>/dev/null || true
    rm -f "${MIRROR_DIR}/alias-ip"
  fi
  echo "OK: destinazione ferma"
}

mirror_dest_start() {
  local port="${1:?porta}" user="${2:?utente}" token="${3:?token}" alias_ip="${4:-}"
  command -v python3 >/dev/null 2>&1 || fail "python3 non trovato sulla VM"
  command -v git >/dev/null 2>&1 || fail "git non trovato sulla VM"
  [ -f /tmp/gitstack-e2e/mirror-dest.py ] || fail "manca /tmp/gitstack-e2e/mirror-dest.py"
  mirror_dest_stop >/dev/null
  rm -rf "${MIRROR_DIR}"
  mkdir -p "${MIRROR_DIR}/repos"
  git init -q --bare -b main "${MIRROR_DIR}/repos/mirror.git"
  if [ -n "${alias_ip}" ]; then
    # GIT-309: la policy di uscita (F7) blocca gli indirizzi del nodo, quindi la
    # destinazione ascolta su un secondo indirizzo privato (alias /32 su lo, che
    # i pod raggiungono passando dall'host) non elencato fra quelli del nodo.
    # Certificato autofirmato con SAN = alias, usato come unica radice di fiducia
    # dei mirror (dest.crt): non serve la chiave della CA della VM.
    command -v openssl >/dev/null 2>&1 || fail "openssl non trovato sulla VM"
    if ! ip -4 -o addr show dev lo | grep -q " ${alias_ip}/32 "; then
      sudo -n ip addr add "${alias_ip}/32" dev lo || fail "alias ${alias_ip} su lo non aggiunto"
    fi
    echo "${alias_ip}" >"${MIRROR_DIR}/alias-ip"
    openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=gitstack-e2e-mirror" \
      -keyout "${MIRROR_DIR}/server.key" -out "${MIRROR_DIR}/server.crt" \
      -addext "subjectAltName=IP:${alias_ip}" -addext "basicConstraints=critical,CA:TRUE" \
      -addext "keyUsage=critical,digitalSignature,keyCertSign" -addext "extendedKeyUsage=serverAuth" >/dev/null 2>&1 \
      || fail "certificato della destinazione non generato"
    cp "${MIRROR_DIR}/server.crt" "${MIRROR_DIR}/dest.crt"
  else
    # Il certificato del server di GitStack (SAN con l'IP della VM, firmato dalla
    # CA interna); la chiave e' di root: la si copia per l'utente di test.
    sudo -n install -m 0644 -o "$(id -u)" -g "$(id -g)" "${TLS_DIR}/server.crt" "${MIRROR_DIR}/server.crt" \
      || fail "copia di server.crt non riuscita"
    sudo -n install -m 0600 -o "$(id -u)" -g "$(id -g)" "${TLS_DIR}/server.key" "${MIRROR_DIR}/server.key" \
      || fail "copia di server.key non riuscita"
    sudo -n install -m 0644 -o "$(id -u)" -g "$(id -g)" "${TLS_DIR}/ca.crt" "${MIRROR_DIR}/dest.crt" \
      || fail "copia di ca.crt non riuscita"
  fi
  setsid nohup python3 /tmp/gitstack-e2e/mirror-dest.py "${MIRROR_DIR}/repos" "${port}" \
    "${MIRROR_DIR}/server.crt" "${MIRROR_DIR}/server.key" "${user}" "${token}" \
    >"${MIRROR_DIR}/server.log" 2>&1 </dev/null &
  echo $! >"${MIRROR_DIR}/pid"
  local _
  for _ in $(seq 1 30); do
    if ss -ltnH "( sport = :${port} )" 2>/dev/null | grep -q .; then
      echo "OK: destinazione in ascolto sulla porta ${port}"
      return 0
    fi
    sleep 0.5
  done
  cat "${MIRROR_DIR}/server.log" >&2 || true
  fail "la destinazione non ascolta sulla porta ${port}"
}

# Stampa lo sha di un ref della destinazione (vuoto se non esiste).
mirror_dest_ref() {
  local ref="${1:?ref}"
  git --git-dir="${MIRROR_DIR}/repos/mirror.git" rev-parse --verify -q "${ref}" || true
}

# Stampa i ref della destinazione, per la diagnostica.
mirror_dest_refs() {
  git --git-dir="${MIRROR_DIR}/repos/mirror.git" for-each-ref || true
}

# Aggiunge un commit a main del repo bare della destinazione, senza working
# tree (blob, albero e commit con i comandi idraulici), e ne stampa lo sha.
# Serve al passo e10 (mirror in pull): la destinazione fa da origine.
mirror_dest_commit() {
  local msg="${1:?messaggio}" dir="${MIRROR_DIR}/repos/mirror.git"
  local blob tree parent commit
  [ -d "${dir}" ] || fail "manca il repo bare della destinazione"
  blob="$(printf '%s\n' "${msg}" | git --git-dir="${dir}" hash-object -w --stdin)" || fail "hash-object non riuscito"
  tree="$(printf '100644 blob %s\torigine.txt\n' "${blob}" | git --git-dir="${dir}" mktree)" || fail "mktree non riuscito"
  parent="$(git --git-dir="${dir}" rev-parse --verify -q refs/heads/main || true)"
  export GIT_AUTHOR_NAME="E2E" GIT_AUTHOR_EMAIL="e2e@example.com" GIT_COMMITTER_NAME="E2E" GIT_COMMITTER_EMAIL="e2e@example.com"
  if [ -n "${parent}" ]; then
    commit="$(git --git-dir="${dir}" commit-tree "${tree}" -p "${parent}" -m "${msg}")" || fail "commit-tree non riuscito"
  else
    commit="$(git --git-dir="${dir}" commit-tree "${tree}" -m "${msg}")" || fail "commit-tree non riuscito"
  fi
  git --git-dir="${dir}" update-ref refs/heads/main "${commit}" || fail "update-ref non riuscito"
  echo "${commit}"
}

# Fa fidare il servizio git della CA della VM per i mirror e ammette l'indirizzo
# della destinazione nell'egress di core. Aspetta i rollout.
mirror_enable() {
  local release="${1:-gitstack}" ns="${2:-default}" ip="${3:?indirizzo}"
  k3s_kubectl -n "${ns}" create configmap "${MIRROR_CA_MAP}" --from-file=ca.crt="${MIRROR_DIR}/dest.crt" \
    --dry-run=client -o yaml | k3s_kubectl -n "${ns}" apply -f - || fail "ConfigMap della CA non creato"
  k3s_kubectl -n "${ns}" patch deployment "${release}-git" --type=strategic -p \
    '{"spec":{"template":{"spec":{"volumes":[{"name":"mirror-ca","configMap":{"name":"'"${MIRROR_CA_MAP}"'"}}],"containers":[{"name":"git","env":[{"name":"GITSTACK_GIT_MIRROR_CA_FILE","value":"/etc/gitstack/mirror-ca/ca.crt"}],"volumeMounts":[{"name":"mirror-ca","mountPath":"/etc/gitstack/mirror-ca","readOnly":true}]}]}}}}' \
    || fail "patch del deployment git non riuscito"
  k3s_kubectl -n "${ns}" set env "deployment/${release}-core" "GITSTACK_EGRESS_ALLOW=${ip}/32" \
    || fail "egress.allow di core non impostato"
  k3s_kubectl -n "${ns}" rollout status "deployment/${release}-git" --timeout=240s || fail "rollout di git non riuscito"
  k3s_kubectl -n "${ns}" rollout status "deployment/${release}-core" --timeout=240s || fail "rollout di core non riuscito"
  echo "OK: git si fida della CA della VM per i mirror, core ammette ${ip}"
}

# Toglie quanto ha messo mirror_enable.
mirror_disable() {
  local release="${1:-gitstack}" ns="${2:-default}"
  # $patch è una direttiva del patch strategico, non una variabile.
  # shellcheck disable=SC2016
  k3s_kubectl -n "${ns}" patch deployment "${release}-git" --type=strategic -p \
    '{"spec":{"template":{"spec":{"volumes":[{"name":"mirror-ca","$patch":"delete"}],"containers":[{"name":"git","env":[{"name":"GITSTACK_GIT_MIRROR_CA_FILE","$patch":"delete"}],"volumeMounts":[{"name":"mirror-ca","mountPath":"/etc/gitstack/mirror-ca","$patch":"delete"}]}]}}}}' \
    || true
  k3s_kubectl -n "${ns}" set env "deployment/${release}-core" GITSTACK_EGRESS_ALLOW- || true
  k3s_kubectl -n "${ns}" delete configmap "${MIRROR_CA_MAP}" --ignore-not-found || true
  k3s_kubectl -n "${ns}" rollout status "deployment/${release}-git" --timeout=240s || true
  k3s_kubectl -n "${ns}" rollout status "deployment/${release}-core" --timeout=240s || true
  echo "OK: cablaggio dei mirror rimosso"
}

# --- main -------------------------------------------------------------

usage() {
  cat <<'EOF'
Uso: remote.sh <sottocomando> [argomenti]
Sottocomandi: clean-check, jetstream-count, postgres-secret-hash,
admin-password, admin-secret-state, k3s-active-since, collect-diagnostics,
mirror-dest-start, mirror-dest-stop, mirror-dest-ref, mirror-dest-refs,
mirror-dest-commit,
mirror-enable, mirror-disable. Vedi l'intestazione del file.
EOF
}

main() {
  local cmd="${1:-}"
  [ -n "${cmd}" ] || { usage >&2; exit 2; }
  shift

  case "${cmd}" in
    clean-check) clean_check "$@" ;;
    jetstream-count) jetstream_count "$@" ;;
    postgres-secret-hash) postgres_secret_hash "$@" ;;
    admin-password) admin_password "$@" ;;
    admin-secret-state) admin_secret_state "$@" ;;
    k3s-active-since) k3s_active_since "$@" ;;
    collect-diagnostics) collect_diagnostics "$@" ;;
    collect-ci-diagnostics) collect_ci_diagnostics "$@" ;;
    mirror-dest-start) mirror_dest_start "$@" ;;
    mirror-dest-stop) mirror_dest_stop "$@" ;;
    mirror-dest-ref) mirror_dest_ref "$@" ;;
    mirror-dest-refs) mirror_dest_refs "$@" ;;
    mirror-dest-commit) mirror_dest_commit "$@" ;;
    mirror-enable) mirror_enable "$@" ;;
    mirror-disable) mirror_disable "$@" ;;
    -h|--help) usage ;;
    *)
      echo "remote.sh: sottocomando sconosciuto: ${cmd}" >&2
      usage >&2
      exit 2
      ;;
  esac
}

main "$@"
