#!/usr/bin/env bash
# GitStack Installer v0 (M-01/T-09).
#
# Installa k3s e GitStack (chart Helm interno di deploy/gitstack, T-08) su
# una macchina Linux pulita con un solo comando. Il cliente non deve sapere
# cos'è Kubernetes: lo script fa tutto, controlla i requisiti prima di
# partire e alla fine dice come verificare che sia andato tutto bene.
#
# Uso:
#   sudo ./deploy/install.sh
#   curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/main/deploy/install.sh | sudo bash
#
# Distribuzioni supportate in M-01: solo Ubuntu Server 24.04 LTS x86_64
# (vedi README.md di questa cartella, sezione "Requisiti minimi").
# Debian/RHEL e WSL2 arrivano con M-08 [c_8458909a21d9035f].
#
# Sicurezza (CA interna, certificati, utente admin): non ancora implementati
# in v0. L'installazione parla HTTP in chiaro sull'IP della macchina. Il
# servizio "identity" è nel chart (GIT-36) e crea l'utente admin al primo
# avvio (GIT-35): la password iniziale è generata dal chart in un Secret, mai
# stampata. Completamento previsto in M-02/M-08.
set -euo pipefail

# Cartelle temporanee da rimuovere all'uscita (download del chart quando non
# c'è un checkout locale, download del tarball di Helm). Un unico trap EXIT
# condiviso, impostato una sola volta qui: un secondo `trap ... EXIT` in una
# funzione chiamata più avanti (es. install_helm dopo resolve_chart_dir)
# sovrascriverebbe questo e perderebbe la pulizia della cartella registrata
# per prima.
TMP_DIRS=()
cleanup_tmp_dirs() {
  local d
  for d in "${TMP_DIRS[@]:-}"; do
    [ -n "${d}" ] && rm -rf "${d}"
  done
}
trap cleanup_tmp_dirs EXIT

# --- Parametri (sovrascrivibili da variabile d'ambiente o da flag) --------

# Versione di k3s, pinnata: v1.36.4+k3s1 include il chart Traefik 40.1.0
# (appVersion v3.7.0, cioè Traefik v3), richiesto dal Middleware
# traefik.io/v1alpha1 dell'Ingress del chart (deploy/gitstack/templates/
# ingress.yaml, criterio 1 di GIT-9). Mai "latest" non pinnato: un
# aggiornamento controllato di k3s è fuori perimetro di M-01 (M-08).
INSTALL_K3S_VERSION="${INSTALL_K3S_VERSION:-v1.36.4+k3s1}"

# Versione di Helm, pinnata per lo stesso motivo (verificata in GIT-8).
GITSTACK_HELM_VERSION="${GITSTACK_HELM_VERSION:-v3.16.3}"

# Repository sorgente (owner/nome) usato solo per: (a) scaricare il chart
# quando questo script gira da solo, senza un checkout locale del monorepo
# (es. curl | sh); (b) risolvere lo sha del commit da usare come tag
# immagine di default quando GITSTACK_IMAGE_TAG non è impostato.
GITSTACK_REPO="${GITSTACK_REPO:-fathorMB/GitStack}"
GITSTACK_REF="${GITSTACK_REF:-main}"

GITSTACK_RELEASE_NAME="${GITSTACK_RELEASE_NAME:-gitstack}"
GITSTACK_NAMESPACE="${GITSTACK_NAMESPACE:-default}"
GITSTACK_CHART_DIR="${GITSTACK_CHART_DIR:-}"
GITSTACK_IMAGE_TAG="${GITSTACK_IMAGE_TAG:-}"
GITSTACK_IMAGE_REGISTRY="${GITSTACK_IMAGE_REGISTRY:-}"
GITSTACK_SKIP_PREFLIGHT="${GITSTACK_SKIP_PREFLIGHT:-0}"
GITSTACK_KUBECONFIG="/etc/rancher/k3s/k3s.yaml"

# Requisiti minimi della macchina di destinazione in M-01 (decisione di
# Atlas, provvisoria, da rivedere con M-08 [c_8458909a21d9035f]): Ubuntu
# Server 24.04 LTS x86_64, 4 vCPU, 8 GB di RAM, disco da 60 GB (profilo
# della VM di GIT-12).
MIN_CPU=4
MIN_RAM_GB=8
# Profilo nominale, citato nei messaggi (il disco della macchina dovrebbe
# essere di questa taglia): non è la soglia controllata direttamente, vedi
# MIN_DISK_TOTAL_GB/MIN_DISK_FREE_GB e preflight_disk più sotto.
MIN_DISK_GB=60
# Soglie realmente verificate da preflight_disk. Il profilo minimo (GIT-12)
# è un disco da 60 GB pieno: dopo le partizioni di boot/EFI il filesystem
# risultante è un po' più piccolo del disco nominale, e lo spazio libero
# cala ulteriormente dopo k3s e le immagini di GitStack (il preflight gira
# anche alle esecuzioni successive, per idempotenza). Richiedere 60 GB
# liberi fallirebbe quindi proprio sulla macchina di riferimento: due
# soglie più realistiche, con margine dichiarato invece che il disco pieno.
MIN_DISK_TOTAL_GB=55
MIN_DISK_FREE_GB=20
REQUIRED_OS_ID="ubuntu"
REQUIRED_OS_VERSION="24.04"
REQUIRED_ARCH="x86_64"
REQUIRED_PORTS="80 443 6443"
# Porta SSH di git (git.ssh.port del chart, default 2222). Mai la 22: l'installer
# non tocca l'sshd dell'host (R7). Si cambia con --set git.ssh.port=N.
GIT_SSH_PORT_DEFAULT=2222

extra_helm_set=()
extra_helm_values=()

# --- Aiuto -----------------------------------------------------------------

usage() {
  cat <<'EOF'
Uso: install.sh [opzioni]

Installa k3s e GitStack (deploy/gitstack) su una macchina Linux pulita.
Va eseguito come root (o con sudo). Rieseguirlo è sicuro: non reinstalla
k3s se è già presente e usa `helm upgrade --install` (idempotente).

Opzioni:
  --skip-preflight          Salta i controlli preliminari (OS, CPU, RAM,
                             disco, porte). Mai attivo di default: usalo solo
                             se sai cosa stai facendo (es. macchina già
                             verificata a mano).
  --release-name NOME       Nome della release Helm (default: gitstack).
  --namespace NS            Namespace Kubernetes (default: default).
  --chart-dir PATH          Usa un chart locale invece di quello accanto a
                             questo script o scaricato dal repository.
  --image-tag TAG           Tag immagine per gateway/identity/core/web (default:
                             risolto automaticamente dal commit corrente).
  --image-registry HOST     Registry delle immagini GitStack (default: dal
                             chart, ghcr.io). Utile per un mirror (M-08).
  --values FILE             File di valori Helm aggiuntivo (-f), ripetibile.
  --set CHIAVE=VALORE       Valore Helm aggiuntivo (--set), ripetibile.
  -h, --help                Stampa questo aiuto ed esce.

Variabili d'ambiente equivalenti (i flag ripetibili --values/--set non ne
hanno una, solo da riga di comando): INSTALL_K3S_VERSION,
GITSTACK_HELM_VERSION, GITSTACK_REPO, GITSTACK_REF, GITSTACK_RELEASE_NAME,
GITSTACK_NAMESPACE, GITSTACK_CHART_DIR, GITSTACK_IMAGE_TAG,
GITSTACK_IMAGE_REGISTRY, GITSTACK_SKIP_PREFLIGHT=1.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --skip-preflight)
      GITSTACK_SKIP_PREFLIGHT=1
      shift
      ;;
    --release-name)
      GITSTACK_RELEASE_NAME="$2"
      shift 2
      ;;
    --namespace)
      GITSTACK_NAMESPACE="$2"
      shift 2
      ;;
    --chart-dir)
      GITSTACK_CHART_DIR="$2"
      shift 2
      ;;
    --image-tag)
      GITSTACK_IMAGE_TAG="$2"
      shift 2
      ;;
    --image-registry)
      GITSTACK_IMAGE_REGISTRY="$2"
      shift 2
      ;;
    --values)
      extra_helm_values+=("$2")
      shift 2
      ;;
    --set)
      extra_helm_set+=("$2")
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "install.sh: opzione sconosciuta: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

# --- Log ---------------------------------------------------------------

log() {
  printf '==> %s\n' "$*"
}

warn() {
  printf 'ATTENZIONE: %s\n' "$*" >&2
}

fail() {
  printf 'ERRORE: %s\n' "$*" >&2
  exit 1
}

# --- Preflight ---------------------------------------------------------

check_root() {
  if [ "$(id -u)" -ne 0 ]; then
    fail "servono i permessi di root. Rilancia con sudo: sudo $0 $*"
  fi
}

k3s_already_installed() {
  command -v k3s >/dev/null 2>&1
}

preflight_os() {
  if [ ! -r /etc/os-release ]; then
    fail "impossibile leggere /etc/os-release: distribuzione non riconosciuta. Distribuzione supportata in M-01: ${REQUIRED_OS_ID} ${REQUIRED_OS_VERSION}."
  fi
  # shellcheck disable=SC1091
  . /etc/os-release
  local found_id="${ID:-sconosciuto}"
  local found_version="${VERSION_ID:-sconosciuta}"
  if [ "${found_id}" != "${REQUIRED_OS_ID}" ] || [ "${found_version}" != "${REQUIRED_OS_VERSION}" ]; then
    fail "distribuzione non supportata in M-01. Trovato: ${found_id} ${found_version}. Richiesto: ${REQUIRED_OS_ID} ${REQUIRED_OS_VERSION} (Debian/RHEL arrivano con M-08 [c_8458909a21d9035f])."
  fi
  log "OS: ${found_id} ${found_version} (richiesto: ${REQUIRED_OS_ID} ${REQUIRED_OS_VERSION}) OK"
}

preflight_arch() {
  local found_arch
  found_arch="$(uname -m)"
  if [ "${found_arch}" != "${REQUIRED_ARCH}" ]; then
    fail "architettura non supportata. Trovata: ${found_arch}. Richiesta: ${REQUIRED_ARCH}."
  fi
  log "Architettura: ${found_arch} (richiesta: ${REQUIRED_ARCH}) OK"
}

preflight_cpu() {
  local found_cpu
  found_cpu="$(nproc)"
  if [ "${found_cpu}" -lt "${MIN_CPU}" ]; then
    fail "CPU insufficienti. Trovate: ${found_cpu} vCPU. Richieste almeno: ${MIN_CPU} vCPU."
  fi
  log "CPU: ${found_cpu} vCPU (richiesti almeno: ${MIN_CPU}) OK"
}

preflight_ram() {
  # /proc/meminfo riporta MemTotal in KiB. Confrontiamo in GB decimali
  # (1 GB = 10^9 byte, non GiB): una VM/macchina configurata con "8 GB" di
  # RAM riporta spesso qualche centinaio di MiB in meno in MemTotal (memoria
  # riservata a firmware/hypervisor) — con i GiB (1024^3) una macchina da 8
  # GB nominali fallirebbe quasi sempre questo controllo. I GB decimali
  # danno un margine realistico restando fedeli al numero "8 GB" richiesto.
  local mem_total_kib mem_total_gb
  mem_total_kib="$(awk '/^MemTotal:/ { print $2 }' /proc/meminfo)"
  mem_total_gb="$(awk -v kib="${mem_total_kib}" 'BEGIN { printf "%.1f", (kib * 1024) / 1000000000 }')"
  if ! awk -v gb="${mem_total_gb}" -v min="${MIN_RAM_GB}" 'BEGIN { exit !(gb >= min) }'; then
    fail "RAM insufficiente. Trovati: ${mem_total_gb} GB. Richiesti almeno: ${MIN_RAM_GB} GB."
  fi
  log "RAM: ${mem_total_gb} GB (richiesti almeno: ${MIN_RAM_GB} GB) OK"
}

preflight_disk() {
  # Filesystem che ospiterà i dati di k3s (PVC di Postgres/NATS inclusi):
  # /var/lib/rancher se esiste già (creata da un'installazione k3s
  # precedente, sullo stesso filesystem di / nel caso comune a disco
  # singolo di M-01), altrimenti /. Non distingue un mountpoint dedicato da
  # una sottocartella di /: a disco singolo sono la stessa cosa.
  local target="/var/lib/rancher"
  [ -d "${target}" ] || target="/"

  # Due soglie, non una (vedi il commento su MIN_DISK_TOTAL_GB/
  # MIN_DISK_FREE_GB più sopra): la dimensione del filesystem (vicina al
  # disco nominale di MIN_DISK_GB, con margine per l'overhead di
  # partizionamento) e lo spazio libero (che si riduce dopo k3s e le
  # immagini, anche alle esecuzioni successive). GB decimali (10^9 byte),
  # stesso motivo del controllo RAM.
  local size_bytes size_gb
  size_bytes="$(df --output=size -B1 "${target}" | tail -n1 | tr -d ' ')"
  size_gb="$(awk -v b="${size_bytes}" 'BEGIN { printf "%.1f", b / 1000000000 }')"
  if ! awk -v gb="${size_gb}" -v min="${MIN_DISK_TOTAL_GB}" 'BEGIN { exit !(gb >= min) }'; then
    fail "disco troppo piccolo su ${target}. Filesystem trovato: ${size_gb} GB. Richiesti almeno: ${MIN_DISK_TOTAL_GB} GB (profilo minimo: disco da ${MIN_DISK_GB} GB, vedi README)."
  fi

  local avail_bytes avail_gb
  avail_bytes="$(df --output=avail -B1 "${target}" | tail -n1 | tr -d ' ')"
  avail_gb="$(awk -v b="${avail_bytes}" 'BEGIN { printf "%.1f", b / 1000000000 }')"
  if ! awk -v gb="${avail_gb}" -v min="${MIN_DISK_FREE_GB}" 'BEGIN { exit !(gb >= min) }'; then
    fail "disco libero insufficiente su ${target}. Trovati: ${avail_gb} GB. Richiesti almeno: ${MIN_DISK_FREE_GB} GB."
  fi

  log "Disco su ${target}: ${size_gb} GB di filesystem (richiesti almeno: ${MIN_DISK_TOTAL_GB} GB), ${avail_gb} GB liberi (richiesti almeno: ${MIN_DISK_FREE_GB} GB) OK"
}

# Porta SSH di git: ultimo --set git.ssh.port=N, altrimenti il default.
git_ssh_port() {
  local kv port="${GIT_SSH_PORT_DEFAULT}"
  for kv in "${extra_helm_set[@]:-}"; do
    case "${kv}" in
      git.ssh.port=*) port="${kv#git.ssh.port=}" ;;
    esac
  done
  printf '%s' "${port}"
}

preflight_ports() {
  local ssh_port
  ssh_port="$(git_ssh_port)"
  case "${ssh_port}" in
    ''|*[!0-9]*) fail "git.ssh.port non valida: '${ssh_port}' (serve un numero di porta)." ;;
  esac
  # Se k3s è già installato (rieseguo lo script su un'installazione
  # esistente), le porte 80/443/6443 sono normalmente occupate da Traefik e
  # dall'API server di k3s stessi: è atteso, non un conflitto. Il controllo
  # ha senso solo su una macchina che non ha ancora GitStack.
  if k3s_already_installed; then
    log "Porte 80/443/6443: k3s è già installato, salto il controllo (occupate dalla stessa installazione)."
    return 0
  fi
  if ! command -v ss >/dev/null 2>&1; then
    warn "comando 'ss' non trovato: salto il controllo delle porte 80/443/6443 e SSH di git. Verificale a mano se l'installazione fallisce."
    return 0
  fi
  local port busy
  for port in ${REQUIRED_PORTS} ${ssh_port}; do
    busy="$(ss -ltnH "( sport = :${port} )" 2>/dev/null || true)"
    if [ -n "${busy}" ]; then
      fail "porta ${port} già in uso da un altro processo. Richiesta libera per k3s/Traefik e per l'SSH di git (porta ${ssh_port}: libera la porta oppure scegline un'altra con --set git.ssh.port=N; l'installer non modifica mai l'sshd dell'host). Trovato: $(printf '%s' "${busy}" | head -n1)"
    fi
  done
  log "Porte 80/443/6443 e ${ssh_port} (SSH di git): libere OK"
}

run_preflight() {
  if [ "${GITSTACK_SKIP_PREFLIGHT}" = "1" ]; then
    warn "--skip-preflight attivo: nessun controllo preliminare eseguito. Non è il comportamento di default: usalo solo su una macchina già verificata."
    return 0
  fi
  log "Controlli preliminari (requisiti minimi M-01, provvisori, da rivedere con M-08 [c_8458909a21d9035f]):"
  preflight_os
  preflight_arch
  preflight_cpu
  preflight_ram
  preflight_disk
  preflight_ports
}

# --- Chart: locale o scaricato -----------------------------------------

# Imposta la variabile globale CHART_DIR. Non usa "$(...)" per restituire il
# risultato di proposito: quando scarica il sorgente (ramo remoto, sotto)
# aggiunge la cartella temporanea a TMP_DIRS (pulita dal trap EXIT comune,
# vedi sopra) e questo deve valere per il processo principale, non per una
# subshell che sparisce subito dopo — una command substitution gira in una
# subshell, dove un `TMP_DIRS+=(...)` non sarebbe visibile al chiamante.
CHART_DIR=""

resolve_chart_dir() {
  if [ -n "${GITSTACK_CHART_DIR}" ]; then
    [ -f "${GITSTACK_CHART_DIR}/Chart.yaml" ] || fail "--chart-dir/${GITSTACK_CHART_DIR} non contiene Chart.yaml."
    CHART_DIR="${GITSTACK_CHART_DIR}"
    return 0
  fi

  local script_dir=""
  if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  fi
  if [ -n "${script_dir}" ] && [ -f "${script_dir}/gitstack/Chart.yaml" ]; then
    CHART_DIR="${script_dir}/gitstack"
    return 0
  fi

  # Nessun checkout locale (es. eseguito via curl | sh): scarica il
  # tarball del repository sorgente al ref richiesto e usa solo
  # deploy/gitstack da lì. Nessuna dipendenza da git: curl + tar bastano.
  command -v curl >/dev/null 2>&1 || fail "serve 'curl' per scaricare il chart (nessun checkout locale trovato)."
  command -v tar >/dev/null 2>&1 || fail "serve 'tar' per estrarre il chart scaricato."

  local ref="${GITSTACK_REF}"
  local sha
  sha="$(resolve_commit_sha "${ref}")"
  local workdir
  workdir="$(mktemp -d)"
  TMP_DIRS+=("${workdir}")

  log "Nessun checkout locale del repository: scarico deploy/gitstack da ${GITSTACK_REPO}@${sha} ..."
  local tarball="${workdir}/gitstack.tar.gz"
  curl -fsSL -o "${tarball}" "https://codeload.github.com/${GITSTACK_REPO}/tar.gz/${sha}" \
    || fail "download del sorgente fallito (https://codeload.github.com/${GITSTACK_REPO}/tar.gz/${sha})."
  tar -xzf "${tarball}" -C "${workdir}"

  local extracted
  extracted="$(find "${workdir}" -mindepth 1 -maxdepth 1 -type d | head -n1)"
  if [ -n "${extracted}" ] && [ -f "${extracted}/deploy/gitstack/Chart.yaml" ]; then
    CHART_DIR="${extracted}/deploy/gitstack"
  else
    fail "il sorgente scaricato non contiene deploy/gitstack/Chart.yaml."
  fi
}

# Sha del commit di GITSTACK_REPO al ref dato, via l'API di GitHub (nessuna
# autenticazione richiesta per un repository pubblico).
resolve_commit_sha() {
  local ref="$1"
  command -v curl >/dev/null 2>&1 || fail "serve 'curl' per risolvere il commit di ${GITSTACK_REPO}@${ref}."
  local response sha
  response="$(curl -fsSL "https://api.github.com/repos/${GITSTACK_REPO}/commits/${ref}")" \
    || fail "impossibile risolvere il commit di ${GITSTACK_REPO}@${ref} tramite l'API di GitHub."
  sha="$(printf '%s' "${response}" | grep -m1 '"sha"' | sed -E 's/.*"sha": *"([^"]+)".*/\1/')"
  [ -n "${sha}" ] || fail "risposta inattesa dall'API di GitHub per ${GITSTACK_REPO}@${ref}."
  printf '%s' "${sha}"
}

# Tag immagine di default: lo sha del commit corrente. Mai "latest" (nota
# vincolante del CTO su GIT-9): un tag sha è un riferimento immutabile, non
# una tag mobile. Se il chart usato è un checkout git locale, uso lo sha di
# quel checkout (installo esattamente quel che è su disco); altrimenti
# risolvo GITSTACK_REPO@GITSTACK_REF tramite l'API di GitHub.
resolve_image_tag() {
  if [ -n "${GITSTACK_IMAGE_TAG}" ]; then
    printf '%s' "${GITSTACK_IMAGE_TAG}"
    return 0
  fi

  local chart_dir="$1"
  local repo_root
  repo_root="$(cd "${chart_dir}/../.." && pwd)"
  if command -v git >/dev/null 2>&1 && git -C "${repo_root}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    local sha
    sha="$(git -C "${repo_root}" rev-parse HEAD)"
    printf 'sha-%s' "${sha}"
    return 0
  fi

  local sha
  sha="$(resolve_commit_sha "${GITSTACK_REF}")"
  printf 'sha-%s' "${sha}"
}

# --- k3s -----------------------------------------------------------------

install_k3s() {
  if k3s_already_installed; then
    log "k3s è già installato ($(k3s --version | head -n1)): non reinstallo (idempotenza)."
    if ! systemctl is-active --quiet k3s 2>/dev/null; then
      log "Il servizio k3s non è attivo: lo avvio."
      systemctl start k3s
    fi
    return 0
  fi

  log "Installo k3s ${INSTALL_K3S_VERSION} (pinnato, include Traefik v3) ..."
  command -v curl >/dev/null 2>&1 || fail "serve 'curl' per installare k3s."
  curl -sfL https://get.k3s.io \
    | INSTALL_K3S_VERSION="${INSTALL_K3S_VERSION}" \
      INSTALL_K3S_EXEC="server --write-kubeconfig-mode 644" \
      sh - \
    || fail "installazione di k3s fallita."
}

wait_for_k3s_ready() {
  log "Attendo che il nodo k3s sia Ready ..."
  local _i
  for _i in $(seq 1 60); do
    if KUBECONFIG="${GITSTACK_KUBECONFIG}" k3s kubectl get nodes --no-headers 2>/dev/null \
        | awk '{print $2}' | grep -qx "Ready"; then
      log "Nodo k3s Ready."
      return 0
    fi
    sleep 5
  done
  fail "il nodo k3s non è diventato Ready in tempo. Diagnostica: journalctl -u k3s -n 100, k3s kubectl get nodes."
}

# Diagnostica stampata su stderr quando l'attesa della CRD Traefik fallisce:
# lo stato del HelmChart interno di k3s (il Job che lo applica, i suoi pod)
# in kube-system, che spiega perché la CRD non è ancora arrivata.
traefik_crd_diagnostics() {
  KUBECONFIG="${GITSTACK_KUBECONFIG}" k3s kubectl -n kube-system get helmchart,job,pods 2>&1 || true
}

# Il nodo Ready (wait_for_k3s_ready) non garantisce che il HelmChart
# "traefik" interno di k3s abbia già applicato le sue CRD: k3s lo installa
# in modo asincrono tramite un Job separato dopo l'avvio del nodo. Senza
# questa attesa, su una macchina pulita `helm upgrade --install` del chart
# di GitStack incontra la stessa race del job "chart" su main (run
# 36437112985, GIT-21): il Middleware `traefik.io/v1alpha1` dell'Ingress
# fallisce con "no matches for kind Middleware" perché
# `middlewares.traefik.io` non esiste ancora. Due fasi, come da nota CTO:
# prima un'attesa che la CRD compaia, poi che sia Established (accettata
# dall'API server). Alle esecuzioni successive (idempotenza) la CRD esiste
# già ed è già Established: entrambe le fasi passano subito.
wait_for_traefik_crd() {
  log "Attendo la CRD Traefik middlewares.traefik.io (il HelmChart interno di k3s la applica in modo asincrono dopo che il nodo è Ready) ..."
  local _i found=0
  for _i in $(seq 1 60); do
    if KUBECONFIG="${GITSTACK_KUBECONFIG}" k3s kubectl get crd middlewares.traefik.io >/dev/null 2>&1; then
      found=1
      break
    fi
    sleep 5
  done
  if [ "${found}" -ne 1 ]; then
    traefik_crd_diagnostics >&2
    fail "la CRD middlewares.traefik.io non è comparsa entro 300s. Diagnostica sopra (kube-system: helmchart/job/pods). Vedi anche: journalctl -u k3s -n 100."
  fi
  # Polling su jsonpath: su una CRD appena creata "kubectl wait" esce subito
  # se status.conditions non c'è ancora (GIT-50).
  local established=0
  for _i in $(seq 1 60); do
    if [ "$(KUBECONFIG="${GITSTACK_KUBECONFIG}" k3s kubectl get crd middlewares.traefik.io -o jsonpath='{.status.conditions[?(@.type=="Established")].status}' 2>/dev/null || true)" = "True" ]; then
      established=1
      break
    fi
    sleep 2
  done
  if [ "${established}" -ne 1 ]; then
    traefik_crd_diagnostics >&2
    fail "la CRD middlewares.traefik.io non è diventata Established entro 120s. Diagnostica sopra (kube-system: helmchart/job/pods)."
  fi
  log "CRD middlewares.traefik.io Established."
}

# --- Helm ------------------------------------------------------------------

# Tarball ufficiale pinnato alla versione esatta più il suo checksum
# pubblicato, invece dello script get-helm-3 di helm/helm@main: quello
# script è un riferimento mobile su un branch (si aggiorna sotto i piedi,
# anche se poi installa comunque la DESIRED_VERSION richiesta), e dipende
# da raw.githubusercontent.com invece che dal dominio ufficiale delle
# release (get.helm.sh). Stesso principio del pin di k3s: niente scaricato
# da un riferimento non pinnato.
install_helm() {
  if command -v helm >/dev/null 2>&1; then
    log "helm è già presente ($(helm version --short 2>/dev/null || echo versione sconosciuta)): non reinstallo."
    return 0
  fi
  log "Installo Helm ${GITSTACK_HELM_VERSION} (pinnato) ..."
  command -v sha256sum >/dev/null 2>&1 || fail "serve 'sha256sum' per verificare il tarball di Helm."
  local tarball_name="helm-${GITSTACK_HELM_VERSION}-linux-amd64.tar.gz"
  local workdir
  workdir="$(mktemp -d)"
  TMP_DIRS+=("${workdir}")

  curl -fsSL -o "${workdir}/${tarball_name}" "https://get.helm.sh/${tarball_name}" \
    || fail "download del tarball di Helm fallito (https://get.helm.sh/${tarball_name})."
  curl -fsSL -o "${workdir}/${tarball_name}.sha256sum" "https://get.helm.sh/${tarball_name}.sha256sum" \
    || fail "download del checksum di Helm fallito (https://get.helm.sh/${tarball_name}.sha256sum)."
  (cd "${workdir}" && sha256sum -c "${tarball_name}.sha256sum") \
    || fail "il tarball di Helm scaricato non corrisponde al checksum pubblicato."

  tar -xzf "${workdir}/${tarball_name}" -C "${workdir}"
  [ -f "${workdir}/linux-amd64/helm" ] || fail "il tarball di Helm non contiene linux-amd64/helm."
  install -m 0755 "${workdir}/linux-amd64/helm" /usr/local/bin/helm
}

install_gitstack() {
  local chart_dir="$1"
  local image_tag="$2"

  local helm_args=(
    upgrade --install "${GITSTACK_RELEASE_NAME}" "${chart_dir}"
    --namespace "${GITSTACK_NAMESPACE}" --create-namespace
    --set "global.image.tag=${image_tag}"
    --wait --timeout 5m
  )
  if [ -n "${GITSTACK_IMAGE_REGISTRY}" ]; then
    helm_args+=(--set "global.image.registry=${GITSTACK_IMAGE_REGISTRY}")
  fi
  local f
  for f in "${extra_helm_values[@]:-}"; do
    [ -n "${f}" ] && helm_args+=(-f "${f}")
  done
  local kv
  for kv in "${extra_helm_set[@]:-}"; do
    [ -n "${kv}" ] && helm_args+=(--set "${kv}")
  done

  log "Installo/aggiorno GitStack (release '${GITSTACK_RELEASE_NAME}', namespace '${GITSTACK_NAMESPACE}', tag immagine '${image_tag}') ..."
  KUBECONFIG="${GITSTACK_KUBECONFIG}" helm "${helm_args[@]}" \
    || fail "'helm upgrade --install' fallito. Diagnostica: KUBECONFIG=${GITSTACK_KUBECONFIG} k3s kubectl get pods -n ${GITSTACK_NAMESPACE}, k3s kubectl describe pod ..., k3s kubectl logs ..."
}

# --- Riepilogo finale -------------------------------------------------

primary_ip() {
  local ip
  ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  if [ -z "${ip}" ]; then
    ip="$(KUBECONFIG="${GITSTACK_KUBECONFIG}" k3s kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || true)"
  fi
  if [ -n "${ip}" ]; then
    printf '%s' "${ip}"
  else
    printf 'localhost'
  fi
}

print_summary() {
  local ip
  ip="$(primary_ip)"
  cat <<EOF

==> GitStack installato.

UI:            http://${ip}/
API (salute):  http://${ip}/api/healthz

Verifica lo stato:
  KUBECONFIG=${GITSTACK_KUBECONFIG} k3s kubectl get pods -n ${GITSTACK_NAMESPACE}
  KUBECONFIG=${GITSTACK_KUBECONFIG} helm status ${GITSTACK_RELEASE_NAME} -n ${GITSTACK_NAMESPACE}
  curl -i http://${ip}/api/healthz

Log di k3s:
  journalctl -u k3s -f

Utente admin: al primo avvio identity crea l'utente 'admin' con una password
iniziale generata e salvata in un Secret Kubernetes (non viene stampata qui).
Per leggerla:
  KUBECONFIG=${GITSTACK_KUBECONFIG} k3s kubectl -n ${GITSTACK_NAMESPACE} get secret ${GITSTACK_RELEASE_NAME}-identity-admin -o jsonpath='{.data.password}' | base64 -d; echo
Al primo login la password va cambiata prima di qualunque altra operazione.

Sicurezza (CA interna, certificati): non ancora implementati in v0.
Completamento previsto in M-02/M-08 [c_8458909a21d9035f]. Per ora
l'accesso è HTTP in chiaro sull'IP della macchina.

Rieseguire questo script in qualsiasi momento è sicuro: non reinstalla k3s
se è già presente e usa 'helm upgrade --install', che non rompe
un'installazione esistente (la password di Postgres e quella
dell'admin restano quelle già generate, vedi deploy/gitstack/README.md).
EOF
}

# --- Main ------------------------------------------------------------------

main() {
  check_root "$@"
  run_preflight

  resolve_chart_dir
  local chart_dir="${CHART_DIR}"
  log "Chart: ${chart_dir}"

  local image_tag
  image_tag="$(resolve_image_tag "${chart_dir}")"
  log "Tag immagine: ${image_tag}"

  install_k3s
  wait_for_k3s_ready
  wait_for_traefik_crd
  install_helm
  install_gitstack "${chart_dir}" "${image_tag}"

  print_summary
}

main "$@"
