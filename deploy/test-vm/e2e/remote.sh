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
#   k3s-active-since                         ActiveEnterTimestamp del
#                                             servizio systemd k3s: se
#                                             un'installazione idempotente
#                                             non lo cambia, k3s non è stato
#                                             riavviato/reinstallato.
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
    [ -n "${p}" ] && k3s kubectl delete pod "${p#*/}" --namespace "${p%%/*}" --wait=false --ignore-not-found >/dev/null 2>&1
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

  # Niente attach (-i/--rm): con un pod che finisce subito l'attach puo'
  # perdere l'output ("If you don't see a command prompt...") e mescolare
  # stdout e stderr. Il pod gira fino in fondo, si attende la fase finale e
  # il JSON si legge da 'kubectl logs' (solo stdout del container). Il pod
  # viene cancellato dal trap EXIT dello script, anche in caso di errore.
  if ! k3s kubectl run "${pod_name}" --namespace "${ns}" --restart=Never \
      --image="${NATS_BOX_IMAGE}" --command -- \
      nats stream info "${stream}" --server "nats://${nats_addr}" --json \
      >/dev/null 2>"${err_file}"; then
    fail "creazione del pod ${pod_name} fallita: $(cat "${err_file}")"
  fi

  local phase="" _i
  for _i in $(seq 1 45); do
    phase="$(k3s kubectl get pod "${pod_name}" --namespace "${ns}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    if [ "${phase}" = "Succeeded" ] || [ "${phase}" = "Failed" ]; then
      break
    fi
    sleep 2
  done
  if [ "${phase}" != "Succeeded" ]; then
    local pod_state pod_events pod_logs
    pod_state="$(k3s kubectl get pod "${pod_name}" --namespace "${ns}" -o wide 2>&1 | tail -n 2 | tr '\n' ' ')"
    pod_events="$(k3s kubectl get events --namespace "${ns}" --field-selector "involvedObject.name=${pod_name}" -o custom-columns=REASON:.reason,MSG:.message --no-headers 2>&1 | tail -n 5 | tr '\n' ' ')"
    pod_logs="$(k3s kubectl logs "${pod_name}" --namespace "${ns}" 2>&1 | tail -n 5 | tr '\n' ' ')"
    fail "il pod ${pod_name} non e' arrivato a Succeeded (fase: '${phase:-sconosciuta}'). Stato: ${pod_state} Eventi: ${pod_events} Log: ${pod_logs}"
  fi

  local out
  if ! out="$(k3s kubectl logs "${pod_name}" --namespace "${ns}" 2>"${err_file}")"; then
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

  pwd_b64="$(k3s kubectl get secret "${secret}" -n "${ns}" -o jsonpath='{.data.password}' 2>&1)" \
    || fail "impossibile leggere il Secret '${secret}' nel namespace '${ns}': ${pwd_b64}"
  [ -n "${pwd_b64}" ] || fail "il Secret '${secret}' non ha la chiave 'password'."
  printf '%s' "${pwd_b64}" | sha256sum | awk '{print $1}'
}

# --- k3s-active-since ------------------------------------------------------

k3s_active_since() {
  systemctl show -p ActiveEnterTimestamp k3s 2>/dev/null | cut -d= -f2- \
    || fail "impossibile leggere ActiveEnterTimestamp del servizio k3s."
}

# --- collect-diagnostics ---------------------------------------------------

collect_diagnostics() {
  local dir="${1:?collect-diagnostics richiede la cartella di destinazione}"
  mkdir -p "${dir}"

  { sudo journalctl -u k3s --no-pager -n 5000 || echo "journalctl -u k3s non disponibile"; } \
    >"${dir}/journalctl-k3s.log" 2>&1

  { k3s kubectl get all -A -o wide || echo "kubectl get all -A non disponibile"; } \
    >"${dir}/kubectl-get-all.txt" 2>&1

  { k3s kubectl get events -A --sort-by=.lastTimestamp || echo "kubectl get events non disponibile"; } \
    >"${dir}/kubectl-events.txt" 2>&1

  { k3s kubectl get pods -A -o yaml || echo "kubectl get pods -o yaml non disponibile"; } \
    >"${dir}/kubectl-pods.yaml" 2>&1

  : >"${dir}/kubectl-describe-pods.txt"
  : >"${dir}/kubectl-logs-pods.txt"
  local ns pod
  while read -r ns pod; do
    [ -n "${pod}" ] || continue
    {
      printf '\n===== %s/%s =====\n' "${ns}" "${pod}"
      k3s kubectl -n "${ns}" describe pod "${pod}" || true
    } >>"${dir}/kubectl-describe-pods.txt" 2>&1
    {
      printf '\n===== %s/%s =====\n' "${ns}" "${pod}"
      k3s kubectl -n "${ns}" logs "${pod}" --all-containers --tail=1000 || true
    } >>"${dir}/kubectl-logs-pods.txt" 2>&1
  done < <(k3s kubectl get pods -A --no-headers 2>/dev/null | awk '{print $1, $2}')

  { helm status gitstack -n default || echo "helm status non disponibile"; } \
    >"${dir}/helm-status.txt" 2>&1

  { df -h || true; } >"${dir}/df.txt" 2>&1
  { free -h || true; } >"${dir}/free.txt" 2>&1
  { uname -a || true; } >"${dir}/uname.txt" 2>&1

  echo "OK: diagnostica raccolta in ${dir}"
}

# --- main -------------------------------------------------------------

usage() {
  cat <<'EOF'
Uso: remote.sh <sottocomando> [argomenti]
Sottocomandi: clean-check, jetstream-count, postgres-secret-hash,
k3s-active-since, collect-diagnostics. Vedi l'intestazione del file.
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
    k3s-active-since) k3s_active_since "$@" ;;
    collect-diagnostics) collect_diagnostics "$@" ;;
    -h|--help) usage ;;
    *)
      echo "remote.sh: sottocomando sconosciuto: ${cmd}" >&2
      usage >&2
      exit 2
      ;;
  esac
}

main "$@"
