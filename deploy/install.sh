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
# Distribuzioni supportate (N2): Ubuntu Server 22.04 e 24.04 LTS,
# Pop!_OS 22.04 e 24.04, solo x86_64; altri sistemi con --force.
# (vedi README.md di questa cartella, sezione "Distribuzioni supportate").
#
# HTTPS (N5, GIT-143): di default l'installer crea una CA interna (chiave solo
# sull'host, root-only) e un certificato per il nome dell'host e il suo IP;
# Traefik serve 443 e reindirizza 80 a 443. Alternative: --tls letsencrypt,
# --tls-cert/--tls-key (certificato del cliente), --insecure-http (solo prove
# locali). Dettagli, rinnovo e come fidarsi della CA: docs/tls.md.
# Il servizio "identity" è nel chart (GIT-36) e crea l'utente admin al primo
# avvio (GIT-35): la password iniziale è generata dal chart in un Secret, mai
# stampata.
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
    if [ -n "${d}" ]; then rm -rf "${d}"; fi
  done
}
trap cleanup_tmp_dirs EXIT

# --- Parametri (sovrascrivibili da variabile d'ambiente o da flag) --------

# Versione di k3s, pinnata: la v1.36 include il chart Traefik 40.1.0
# (appVersion v3.7.0, cioè Traefik v3), richiesto dal Middleware
# traefik.io/v1alpha1 dell'Ingress del chart (deploy/gitstack/templates/
# ingress.yaml, criterio 1 di GIT-9). Mai "latest" non pinnato.
# La versione provata con ogni versione di GitStack è dichiarata nel file
# deploy/k3s-version (N10, GIT-210), letto da qui (resolve_k3s_version) e da
# `gitstack upgrade`, che aggiorna k3s in modo controllato: non va ripetuta
# in questo script. INSTALL_K3S_VERSION, se impostata, la sovrascrive.
INSTALL_K3S_VERSION="${INSTALL_K3S_VERSION:-}"

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

# Comando di amministrazione `gitstack` (admin/, GIT-142). Sorgente del
# binario: un percorso locale o un URL http(s). Senza, si prova la release
# GitHub di ${GITSTACK_REPO} taggata come il tag immagine (asset
# gitstack-linux-amd64 e gitstack-linux-amd64.sha256): se non è pubblicata si
# avvisa e si prosegue (finché una CI non la pubblica, vedi admin/README.md).
# Il checksum SHA-256 (V5) è obbligatorio: GITSTACK_ADMIN_SHA256 oppure il
# file `<sorgente>.sha256`. GITSTACK_ADMIN_REQUIRED=1 rende fatale l'assenza.
GITSTACK_ADMIN_BINARY="${GITSTACK_ADMIN_BINARY:-}"
GITSTACK_ADMIN_SHA256="${GITSTACK_ADMIN_SHA256:-}"
GITSTACK_ADMIN_REQUIRED="${GITSTACK_ADMIN_REQUIRED:-0}"
ADMIN_BIN_PATH="/usr/local/bin/gitstack"
# File di configurazione dell'installazione, root-only: lo scrive questo
# script, lo leggono i comandi di `gitstack` (formato: admin/README.md).
GITSTACK_CONFIG_DIR="/etc/gitstack"
GITSTACK_CONFIG_FILE="${GITSTACK_CONFIG_DIR}/config.yaml"
# Copia locale del chart, per `gitstack config set host` (helm upgrade senza
# rifare il download): la cartella temporanea del download sparisce all'uscita.
GITSTACK_CHART_COPY="${GITSTACK_CHART_COPY:-/usr/local/share/gitstack/chart}"
GITSTACK_BACKUP_DIR="${GITSTACK_BACKUP_DIR:-}"
GITSTACK_BACKUP_RETENTION="${GITSTACK_BACKUP_RETENTION:-}"
# Postgres esterno (N8, GIT-205): vedi resolve_postgres. La password si dà con
# un file o con GITSTACK_POSTGRES_PASSWORD, non sulla riga di comando.
GITSTACK_POSTGRES_URL="${GITSTACK_POSTGRES_URL:-}"
GITSTACK_POSTGRES_PASSWORD_FILE="${GITSTACK_POSTGRES_PASSWORD_FILE:-}"
GITSTACK_POSTGRES_CA_FILE="${GITSTACK_POSTGRES_CA_FILE:-}"
# Backup su S3 (N7): endpoint, bucket e file della chiave di cifratura sono
# obbligatori insieme; le chiavi di accesso solo da ambiente (mai da argomento:
# si vedono con ps).
GITSTACK_S3_ENDPOINT="${GITSTACK_S3_ENDPOINT:-}"
GITSTACK_S3_BUCKET="${GITSTACK_S3_BUCKET:-}"
GITSTACK_S3_PREFIX="${GITSTACK_S3_PREFIX:-}"
GITSTACK_S3_REGION="${GITSTACK_S3_REGION:-}"
GITSTACK_S3_KEY_FILE="${GITSTACK_S3_KEY_FILE:-}"
GITSTACK_S3_RETENTION="${GITSTACK_S3_RETENTION:-}"
GITSTACK_S3_ACCESS_KEY="${GITSTACK_S3_ACCESS_KEY:-}"
GITSTACK_S3_SECRET_KEY="${GITSTACK_S3_SECRET_KEY:-}"

# Requisiti della macchina (N1 [c_d345922b89557a3f], confermati il 2026-10-05).
# Profilo MINIMO, bloccante: 4 vCPU, 8 GB di RAM, 60 GB di disco con almeno
# 20 GB liberi sul filesystem che ospita /var/lib/rancher (dimensione del
# filesystem e spazio libero sono due controlli diversi: lo spazio libero cala
# dopo k3s e le immagini, e il preflight gira anche alle riesecuzioni).
# Profilo CONSIGLIATO, solo avviso: 8 vCPU, 16 GB di RAM, 200 GB su SSD (fino
# a circa 100 utenti). Tutti i GB sono decimali (10^9 byte), come per la RAM:
# un disco da 60 GiB dichiara oltre 64 GB, quindi i margini di partizionamento
# non fanno fallire la macchina di riferimento.
MIN_CPU=4
MIN_RAM_GB=8
MIN_DISK_GB=60
MIN_DISK_TOTAL_GB=60
MIN_DISK_FREE_GB=20
REC_CPU=8
REC_RAM_GB=16
REC_DISK_GB=200
# Sistemi supportati (N2): ubuntu 22.04, ubuntu 24.04, pop 22.04, pop 24.04,
# solo x86_64. Un altro sistema si installa solo con --force.
GITSTACK_FORCE="${GITSTACK_FORCE:-0}"
# Percorsi letti dal preflight, sostituibili nei test (deploy/tests/preflight_test.sh).
OS_RELEASE_FILE="${OS_RELEASE_FILE:-/etc/os-release}"
MEMINFO_FILE="${MEMINFO_FILE:-/proc/meminfo}"
SYS_CLASS_BLOCK="${SYS_CLASS_BLOCK:-/sys/class/block}"
# Elenco dei sistemi supportati: coppie "ID VERSIONE" separate da spazio.
REQUIRED_OS=(
  "ubuntu 22.04"
  "ubuntu 24.04"
  "pop 22.04"
  "pop 24.04"
)
REQUIRED_ARCH="x86_64"
REQUIRED_PORTS="80 443 6443"
# Porta SSH di git (git.ssh.port del chart, default 2222). Mai la 22: l'installer
# non tocca l'sshd dell'host (R7). Si cambia con --set git.ssh.port=N.
GIT_SSH_PORT_DEFAULT=2222

extra_helm_set=()
extra_helm_values=()

# --- Rete aziendale (N4, N9a) ------------------------------------------
# Vuoto = non indicato: vale la scelta della volta prima (sezione `network:`
# di config.yaml, resolve_network). Proxy, mirror delle immagini e dei
# download, CA aziendale: docs/operations.md, sezione «Rete aziendale».
GITSTACK_HTTP_PROXY="${GITSTACK_HTTP_PROXY:-}"
GITSTACK_HTTPS_PROXY="${GITSTACK_HTTPS_PROXY:-}"
GITSTACK_NO_PROXY="${GITSTACK_NO_PROXY:-}"
GITSTACK_REGISTRY_MIRROR="${GITSTACK_REGISTRY_MIRROR:-}"
GITSTACK_REGISTRY_USERNAME="${GITSTACK_REGISTRY_USERNAME:-}"
# La password non si passa mai in una riga di comando: file o variabile.
GITSTACK_REGISTRY_PASSWORD="${GITSTACK_REGISTRY_PASSWORD:-}"
GITSTACK_REGISTRY_PASSWORD_FILE="${GITSTACK_REGISTRY_PASSWORD_FILE:-}"
GITSTACK_DOWNLOAD_MIRROR="${GITSTACK_DOWNLOAD_MIRROR:-}"
GITSTACK_CA_BUNDLE="${GITSTACK_CA_BUNDLE:-}"
# Percorsi scritti, sostituibili nei test (deploy/tests/network_test.sh).
GITSTACK_NETWORK_DIR="${GITSTACK_NETWORK_DIR:-/etc/gitstack/network}"
K3S_CONFIG_DIR="${GITSTACK_K3S_DIR:-/etc/rancher/k3s}"
SYSTEM_CA_DIR="${GITSTACK_CA_CERTS_DIR:-/usr/local/share/ca-certificates}"
SYSTEMD_UNIT_DIR="${GITSTACK_SYSTEMD_DIR:-/etc/systemd/system}"
# Reti del cluster k3s (pod e Service) e suffissi interni: mai dietro il proxy.
K3S_BUILTIN_NO_PROXY="localhost,127.0.0.1,::1,10.42.0.0/16,10.43.0.0/16,.svc,.cluster.local"
# Valorizzate da resolve_network.
NETWORK_CONFIGURED=0
NETWORK_HELM_SETS=()
NETWORK_K3S_CHANGED=0

# --- HTTPS (N5, GIT-143) -----------------------------------------------
# Vuoto = non indicato: vale la scelta della volta prima o internal (resolve_tls).
# internal (default): CA interna + certificato dell'host; letsencrypt: ACME
# HTTP-01 di Traefik; custom: --tls-cert/--tls-key; insecure: solo HTTP.
GITSTACK_TLS_MODE="${GITSTACK_TLS_MODE:-}"
GITSTACK_TLS_CERT="${GITSTACK_TLS_CERT:-}"
GITSTACK_TLS_KEY="${GITSTACK_TLS_KEY:-}"
GITSTACK_TLS_EMAIL="${GITSTACK_TLS_EMAIL:-}"
# Solo per prove con Let's Encrypt staging
# (https://acme-staging-v02.api.letsencrypt.org/directory).
GITSTACK_ACME_CA_SERVER="${GITSTACK_ACME_CA_SERVER:-}"
HOST_ARGS=()
TLS_DIR="/etc/gitstack/tls"
TLS_BIN_PATH="/usr/local/sbin/gitstack-tls"
TLS_SYSTEMD_DIR="/etc/systemd/system"
BACKUP_SYSTEMD_DIR="/etc/systemd/system"
K3S_MANIFESTS_DIR="/var/lib/rancher/k3s/server/manifests"
ACME_MANIFEST="${K3S_MANIFESTS_DIR}/gitstack-traefik-letsencrypt.yaml"
# Valorizzate da resolve_tls (nomi e IP dei SAN, host dell'URL pubblico, schema).
TLS_NAMES=""
TLS_IPS=""
PUBLIC_HOST=""
PUBLIC_SCHEME="https"
# Avviso del preflight quando senza --host il nome della macchina non risolve.
DEFAULT_HOST_NOTE=""

# --- Aiuto -----------------------------------------------------------------

usage() {
  cat <<'EOF'
Uso: install.sh [opzioni]

Installa k3s e GitStack (deploy/gitstack) su una macchina Linux pulita.
Va eseguito come root (o con sudo). Rieseguirlo è sicuro: non reinstalla
k3s se è già presente e usa `helm upgrade --install` (idempotente).

Opzioni:
  --force                   Procede anche su un sistema diverso da Ubuntu
                             Server 24.04 (avviso "non supportato", nessuna
                             garanzia). Non salta i requisiti hardware.
  --skip-preflight          Salta i controlli preliminari (OS, CPU, RAM,
                             disco, porte, nome host). Mai attivo di default: usalo solo
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
  --admin-binary PERCORSO|URL
                            Binario di `gitstack` (admin/) da installare in
                             /usr/local/bin, con il checksum verificato.
  --admin-sha256 HEX        SHA-256 atteso del binario (default: il file
                             <binario>.sha256 accanto al sorgente).
  --host NOME|IP            Nome (es. homehub.local) o IP con cui i client
                             raggiungono GitStack, ripetibile: finiscono nei SAN
                             del certificato; il primo è nell'URL pubblico.
                             Il preflight verifica (getent hosts, quindi anche
                             i .local via mDNS) che il nome risolva a un
                             indirizzo della macchina e avvisa se no. Default:
                             il nome completo della macchina (hostname -f) se
                             risolve, altrimenti l'IP; l'IP è sempre nei SAN.
  --tls internal|letsencrypt
                            HTTPS (default: internal): CA interna generata qui,
                             con chiave solo sull'host; letsencrypt: certificato
                             pubblico (HTTP-01, serve --host raggiungibile da
                             internet sulla porta 80).
  --tls-cert FILE --tls-key FILE
                            Certificato e chiave PEM del cliente (sostituiscono
                             la CA interna).
  --tls-email EMAIL         Email per Let's Encrypt (consigliata).
  --insecure-http           Solo HTTP, senza TLS: SOLO per prove locali. La UI
                             mostra un avviso.
  --postgres-url URL        Usa un PostgreSQL esterno (15, 16 o 17) invece del
                             Postgres interno: postgres://UTENTE@HOST:5432/
                             DATABASE[?sslmode=require|verify-full|...]. Serve
                             un database dedicato e un utente che può creare
                             schemi e ruoli: lo prepara il DBA con
                             deploy/postgres-prepare.sql. Il preflight controlla
                             versione, connessione e permessi prima di toccare
                             la macchina. Default di sslmode: require.
  --postgres-password-file FILE
                            File con la password dell'utente del database (o la
                             variabile GITSTACK_POSTGRES_PASSWORD): meglio che
                             nell'URL. Solo lettere, cifre e - . _ ~
  --postgres-ca-file FILE   CA (PEM) che ha firmato il certificato del database,
                             per sslmode verify-ca o verify-full con una CA
                             privata.
  --s3-endpoint HOST|URL    Backup su S3 (AWS, MinIO, B2, Wasabi): endpoint. Con
                             gli altri --s3-* configura backup.s3; servono anche
                             --s3-bucket e --s3-key-file, e le chiavi di accesso
                             nelle variabili GITSTACK_S3_ACCESS_KEY e
                             GITSTACK_S3_SECRET_KEY (non da argomento).
  --s3-bucket NOME          Bucket dei backup.
  --s3-prefix PREFISSO      Prefisso (cartella) nel bucket. Default: nessuno.
  --s3-region REGIONE       Regione. Default: us-east-1.
  --s3-key-file FILE        Chiave di cifratura degli archivi (almeno 16
                             caratteri, per esempio openssl rand -base64 32):
                             senza, la configurazione S3 è rifiutata.
  --s3-retention N|bucket   Archivi da tenere su S3 (default 30) oppure «bucket»
                             per lasciar fare al bucket.
  --values FILE             File di valori Helm aggiuntivo (-f), ripetibile.
  --set CHIAVE=VALORE       Valore Helm aggiuntivo (--set), ripetibile.
  --http-proxy URL          Proxy per le destinazioni http (download di k3s e
                             Helm, containerd di k3s, pod di GitStack).
  --https-proxy URL         Proxy per le destinazioni https (default: quello
                             indicato con --http-proxy).
  --no-proxy LISTA          Destinazioni senza proxy, separate da virgole (si
                             aggiungono a localhost, reti e suffissi del cluster).
  --registry-mirror URL     Mirror per TUTTE le immagini, di sistema di k3s
                             comprese (registries.yaml, endpoint "*").
  --registry-username U     Credenziali del mirror (facoltative).
  --registry-password-file FILE
                            Password del mirror (o $GITSTACK_REGISTRY_PASSWORD).
  --download-mirror URL     Mirror per i binari di k3s, Helm e gitstack e per il
                             sorgente (struttura: docs/operations.md).
  --ca-bundle FILE          CA aziendale in PEM: fidata dal sistema, da
                             containerd e dai pod. Con le opzioni di rete il
                             preflight prova a raggiungere ogni destinazione e
                             ferma l'installazione se una non risponde.
  -h, --help                Stampa questo aiuto ed esce.

Variabili d'ambiente equivalenti (i flag ripetibili --values/--set non ne
hanno una, solo da riga di comando): INSTALL_K3S_VERSION,
GITSTACK_HELM_VERSION, GITSTACK_REPO, GITSTACK_REF, GITSTACK_RELEASE_NAME,
GITSTACK_NAMESPACE, GITSTACK_CHART_DIR, GITSTACK_IMAGE_TAG,
GITSTACK_IMAGE_REGISTRY, GITSTACK_SKIP_PREFLIGHT=1, GITSTACK_FORCE=1, GITSTACK_TLS_MODE
(internal|letsencrypt|insecure), GITSTACK_TLS_CERT, GITSTACK_TLS_KEY,
GITSTACK_TLS_EMAIL, GITSTACK_ADMIN_BINARY,
GITSTACK_ADMIN_SHA256, GITSTACK_ADMIN_REQUIRED=1, GITSTACK_BACKUP_DIR,
GITSTACK_BACKUP_RETENTION, GITSTACK_BACKUP_TIMER_HOUR (0-23, default 02),
GITSTACK_HTTP_PROXY, GITSTACK_HTTPS_PROXY, GITSTACK_NO_PROXY,
GITSTACK_REGISTRY_MIRROR, GITSTACK_REGISTRY_USERNAME, GITSTACK_REGISTRY_PASSWORD,
GITSTACK_DOWNLOAD_MIRROR, GITSTACK_CA_BUNDLE,
GITSTACK_POSTGRES_URL, GITSTACK_POSTGRES_PASSWORD_FILE, GITSTACK_POSTGRES_PASSWORD,
GITSTACK_POSTGRES_CA_FILE,
GITSTACK_S3_ENDPOINT, GITSTACK_S3_BUCKET, GITSTACK_S3_PREFIX,
GITSTACK_S3_REGION, GITSTACK_S3_KEY_FILE, GITSTACK_S3_RETENTION,
GITSTACK_S3_ACCESS_KEY, GITSTACK_S3_SECRET_KEY.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --skip-preflight)
      GITSTACK_SKIP_PREFLIGHT=1
      shift
      ;;
    --force)
      GITSTACK_FORCE=1
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
    --admin-binary)
      GITSTACK_ADMIN_BINARY="$2"
      shift 2
      ;;
    --admin-sha256)
      GITSTACK_ADMIN_SHA256="$2"
      shift 2
      ;;
    --host)
      HOST_ARGS+=("$2")
      shift 2
      ;;
    --tls)
      GITSTACK_TLS_MODE="$2"
      shift 2
      ;;
    --tls-cert)
      GITSTACK_TLS_CERT="$2"
      shift 2
      ;;
    --tls-key)
      GITSTACK_TLS_KEY="$2"
      shift 2
      ;;
    --tls-email)
      GITSTACK_TLS_EMAIL="$2"
      shift 2
      ;;
    --insecure-http)
      GITSTACK_TLS_MODE="insecure"
      shift
      ;;
    --http-proxy)
      GITSTACK_HTTP_PROXY="$2"
      shift 2
      ;;
    --https-proxy)
      GITSTACK_HTTPS_PROXY="$2"
      shift 2
      ;;
    --no-proxy)
      GITSTACK_NO_PROXY="$2"
      shift 2
      ;;
    --registry-mirror)
      GITSTACK_REGISTRY_MIRROR="$2"
      shift 2
      ;;
    --registry-username)
      GITSTACK_REGISTRY_USERNAME="$2"
      shift 2
      ;;
    --registry-password-file)
      GITSTACK_REGISTRY_PASSWORD_FILE="$2"
      shift 2
      ;;
    --download-mirror)
      GITSTACK_DOWNLOAD_MIRROR="$2"
      shift 2
      ;;
    --ca-bundle)
      GITSTACK_CA_BUNDLE="$2"
      shift 2
      ;;
    --postgres-url)
      GITSTACK_POSTGRES_URL="$2"
      shift 2
      ;;
    --postgres-password-file)
      GITSTACK_POSTGRES_PASSWORD_FILE="$2"
      shift 2
      ;;
    --postgres-ca-file)
      GITSTACK_POSTGRES_CA_FILE="$2"
      shift 2
      ;;
    --s3-endpoint)
      GITSTACK_S3_ENDPOINT="$2"
      shift 2
      ;;
    --s3-bucket)
      GITSTACK_S3_BUCKET="$2"
      shift 2
      ;;
    --s3-prefix)
      GITSTACK_S3_PREFIX="$2"
      shift 2
      ;;
    --s3-region)
      GITSTACK_S3_REGION="$2"
      shift 2
      ;;
    --s3-key-file)
      GITSTACK_S3_KEY_FILE="$2"
      shift 2
      ;;
    --s3-retention)
      GITSTACK_S3_RETENTION="$2"
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
  if [ ! -r "${OS_RELEASE_FILE}" ]; then
    unsupported_os "impossibile leggere ${OS_RELEASE_FILE}: distribuzione non riconosciuta"
    return 0
  fi
  local found_id found_version
  # shellcheck disable=SC1090,SC1091
  found_id="$(. "${OS_RELEASE_FILE}" >/dev/null 2>&1; printf '%s' "${ID:-sconosciuto}")"
  # shellcheck disable=SC1090,SC1091
  found_version="$(. "${OS_RELEASE_FILE}" >/dev/null 2>&1; printf '%s' "${VERSION_ID:-sconosciuta}")"
  local supported=0
  for entry in "${REQUIRED_OS[@]}"; do
    if [ "${found_id}" = "${entry%% *}" ] && [ "${found_version}" = "${entry#* }" ]; then
      supported=1
      break
    fi
  done
  if [ "${supported}" -ne 1 ]; then
    unsupported_os "sistema non supportato: ${found_id} ${found_version}"
    return 0
  fi
  log "OS: ${found_id} ${found_version} (supportato: ${REQUIRED_OS[*]}) OK"
}

# Sistema non nell'elenco supportato: avviso "non supportato" e si
# prosegue solo con --force.
unsupported_os() {
  local why="$1"
  if [ "${GITSTACK_FORCE}" = "1" ]; then
    warn "${why}. Supportato: ${REQUIRED_OS[*]}. Procedo perché c'è --force: nessuna garanzia."
    return 0
  fi
  fail "${why}. Supportato: ${REQUIRED_OS[*]}. Per provare lo stesso, a tuo rischio, rilancia con --force."
}

preflight_arch() {
  local found_arch
  found_arch="$(uname -m)"
  if [ "${found_arch}" != "${REQUIRED_ARCH}" ]; then
    fail "architettura non supportata. Trovata: ${found_arch}. Richiesta: ${REQUIRED_ARCH}."
  fi
  log "Architettura: ${found_arch} (richiesta: ${REQUIRED_ARCH}) OK"
}

# Letture dell'hardware in funzioni a sé: i test le ridefiniscono.
cpu_count() {
  nproc
}

mem_total_kib() {
  awk '/^MemTotal:/ { print $2 }' "${MEMINFO_FILE}"
}

# Filesystem che ospiterà i dati di k3s (PVC di Postgres/NATS inclusi):
# /var/lib/rancher se esiste già, altrimenti /. Non distingue un mountpoint
# dedicato da una sottocartella di /: a disco singolo sono la stessa cosa.
disk_target() {
  local target="/var/lib/rancher"
  if [ ! -d "${target}" ]; then
    target="/"
  fi
  printf '%s' "${target}"
}

fs_size_bytes() {
  df --output=size -B1 "$1" | tail -n1 | tr -d ' '
}

fs_avail_bytes() {
  df --output=avail -B1 "$1" | tail -n1 | tr -d ' '
}

fs_source_device() {
  df --output=source "$1" | tail -n1 | tr -d ' '
}

# gb_ge A B: A >= B (numeri decimali).
gb_ge() {
  awk -v a="$1" -v b="$2" 'BEGIN { exit !(a >= b) }'
}

bytes_to_gb() {
  awk -v b="$1" 'BEGIN { printf "%.1f", b / 1000000000 }'
}

preflight_cpu() {
  local found_cpu
  found_cpu="$(cpu_count)"
  if [ "${found_cpu}" -lt "${MIN_CPU}" ]; then
    fail "CPU insufficienti. Trovate: ${found_cpu} vCPU. Richieste almeno: ${MIN_CPU} vCPU (consigliate: ${REC_CPU})."
  fi
  log "CPU: ${found_cpu} vCPU (minimo: ${MIN_CPU}) OK"
  if [ "${found_cpu}" -lt "${REC_CPU}" ]; then
    warn "CPU sotto il profilo consigliato: ${found_cpu} vCPU, consigliate ${REC_CPU} per circa 100 utenti."
  fi
}

preflight_ram() {
  # MemTotal è in KiB. Si confronta in GB decimali (1 GB = 10^9 byte): una
  # macchina da "8 GB" riporta qualche centinaio di MiB in meno (memoria
  # riservata a firmware/hypervisor), e coi GiB fallirebbe quasi sempre.
  local mem_total_kib mem_total_gb
  mem_total_kib="$(mem_total_kib)"
  mem_total_gb="$(awk -v kib="${mem_total_kib}" 'BEGIN { printf "%.1f", (kib * 1024) / 1000000000 }')"
  if ! gb_ge "${mem_total_gb}" "${MIN_RAM_GB}"; then
    fail "RAM insufficiente. Trovati: ${mem_total_gb} GB. Richiesti almeno: ${MIN_RAM_GB} GB (consigliati: ${REC_RAM_GB} GB)."
  fi
  log "RAM: ${mem_total_gb} GB (minimo: ${MIN_RAM_GB} GB) OK"
  if ! gb_ge "${mem_total_gb}" "${REC_RAM_GB}"; then
    warn "RAM sotto il profilo consigliato: ${mem_total_gb} GB, consigliati ${REC_RAM_GB} GB per circa 100 utenti."
  fi
}

# Dice se il disco che ospita il device $1 (nome in /sys/class/block) è
# rotazionale: stampa 1, 0 o "?" se non si capisce. Segue le partizioni fino al
# disco e i device mapper (LVM, RAID) fino ai loro slaves.
block_rotational() {
  local name="$1" path
  path="${SYS_CLASS_BLOCK}/${name}"
  [ -e "${path}" ] || { printf '?'; return 0; }
  if [ -n "$(ls -A "${path}/slaves" 2>/dev/null)" ]; then
    local s r out="0"
    for s in "${path}"/slaves/*; do
      r="$(block_rotational "$(basename "${s}")")"
      if [ "${r}" = "1" ]; then out="1"; fi
      if [ "${r}" = "?" ] && [ "${out}" = "0" ]; then out="?"; fi
    done
    printf '%s' "${out}"
    return 0
  fi
  if [ -f "${path}/partition" ]; then
    local parent
    parent="$(basename "$(dirname "$(readlink -f "${path}")")")"
    block_rotational "${parent}"
    return 0
  fi
  if [ -f "${path}/queue/rotational" ]; then
    tr -d '[:space:]' <"${path}/queue/rotational"
    return 0
  fi
  printf '?'
}

preflight_disk() {
  local target
  target="$(disk_target)"

  # Minimo, bloccante: due soglie. Dimensione del filesystem (profilo da
  # ${MIN_DISK_GB} GB) e spazio libero (che cala dopo k3s e le immagini).
  local size_bytes size_gb
  size_bytes="$(fs_size_bytes "${target}")"
  size_gb="$(bytes_to_gb "${size_bytes}")"
  if ! gb_ge "${size_gb}" "${MIN_DISK_TOTAL_GB}"; then
    fail "disco troppo piccolo su ${target}. Filesystem trovato: ${size_gb} GB. Richiesti almeno: ${MIN_DISK_TOTAL_GB} GB (profilo minimo: disco da ${MIN_DISK_GB} GB; consigliati: ${REC_DISK_GB} GB su SSD, vedi README)."
  fi

  local avail_bytes avail_gb
  avail_bytes="$(fs_avail_bytes "${target}")"
  avail_gb="$(bytes_to_gb "${avail_bytes}")"
  if ! gb_ge "${avail_gb}" "${MIN_DISK_FREE_GB}"; then
    fail "disco libero insufficiente su ${target}. Trovati: ${avail_gb} GB. Richiesti almeno: ${MIN_DISK_FREE_GB} GB."
  fi

  log "Disco su ${target}: ${size_gb} GB di filesystem (minimo: ${MIN_DISK_TOTAL_GB} GB), ${avail_gb} GB liberi (minimo: ${MIN_DISK_FREE_GB} GB) OK"

  # Consigliato, solo avviso: dimensione e SSD.
  if ! gb_ge "${size_gb}" "${REC_DISK_GB}"; then
    warn "disco sotto il profilo consigliato: ${size_gb} GB, consigliati ${REC_DISK_GB} GB (i repo occupano circa il doppio della loro dimensione)."
  fi
  local dev rot
  dev="$(basename "$(fs_source_device "${target}")")"
  rot="$(block_rotational "${dev}")"
  case "${rot}" in
    1) warn "il disco di ${target} è rotazionale (HDD): si consiglia un SSD." ;;
    0) log "Disco di ${target}: SSD (profilo consigliato) OK" ;;
    *) warn "non riesco a stabilire se il disco di ${target} è un SSD (/sys/class/block/${dev}): si consiglia un SSD." ;;
  esac
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
  log "Controlli preliminari (N1 [c_d345922b89557a3f]: il profilo minimo ferma l'installazione, il consigliato avvisa soltanto):"
  preflight_os
  preflight_arch
  preflight_cpu
  preflight_ram
  preflight_disk
  preflight_ports
  preflight_host
}

# --- Nome host (N6) -------------------------------------------------------

# Indirizzi assegnati alle interfacce della macchina, uno per riga.
local_addrs() {
  ip -o addr show 2>/dev/null | awk '{ split($4, a, "/"); print a[1] }'
}

# Indirizzi a cui il resolver di SISTEMA risolve il nome. Passa da getent
# hosts e non da un server DNS: con libnss-mdns risolve anche i .local.
resolve_host_addrs() {
  getent hosts "$1" 2>/dev/null | awk '{ print $1 }'
}

# Vero se il nome risolve a un indirizzo della macchina che non sia di
# loopback (hostname -f spesso dà 127.0.1.1, inutile per i client).
host_is_local() {
  local name="$1" a mine
  mine="$(local_addrs)"
  for a in $(resolve_host_addrs "${name}"); do
    case "${a}" in 127.*|::1) continue ;; esac
    if printf '%s
' "${mine}" | grep -Fxq -- "${a}"; then
      return 0
    fi
  done
  return 1
}

is_local_domain() {
  case "$1" in *.local) return 0 ;; *) return 1 ;; esac
}

preflight_host() {
  local h
  for h in "${HOST_ARGS[@]:-}"; do
    [ -n "${h}" ] || continue
    if is_ipv4 "${h}"; then
      if printf '%s
' "$(local_addrs)" | grep -Fxq -- "${h}"; then
        log "Host ${h}: indirizzo della macchina OK"
      else
        warn "--host ${h} non è un indirizzo di questa macchina: i client non la raggiungeranno con quell'IP."
      fi
      continue
    fi
    if host_is_local "${h}"; then
      log "Host ${h}: risolve a un indirizzo della macchina OK"
    elif [ -z "$(resolve_host_addrs "${h}")" ]; then
      warn "il nome '${h}' non risolve su questa macchina (getent hosts). Crea il record DNS prima che gli utenti lo usino; l'installazione prosegue, e l'IP è nei SAN del certificato."
      if is_local_domain "${h}"; then
        warn "'${h}' è un nome .local (mDNS): serve avahi-daemon attivo e libnss-mdns sul server (sudo apt install avahi-daemon libnss-mdns), vedi README, sezione «Nomi .local»."
      fi
    else
      warn "il nome '${h}' risolve a $(resolve_host_addrs "${h}" | head -n1), che non è un indirizzo di questa macchina: i client non la raggiungeranno con quel nome."
    fi
    if is_local_domain "${h}"; then
      warn "'${h}' è un nome .local: i pod di k3s non lo risolvono, quindi nessun servizio deve chiamare dall'interno del cluster il proprio indirizzo pubblico."
    fi
  done
  if [ "${#HOST_ARGS[@]}" -eq 0 ] && [ -n "${DEFAULT_HOST_NOTE}" ]; then
    warn "${DEFAULT_HOST_NOTE}"
  fi
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
  curl -fsSL -o "${tarball}" "$(source_tarball_url "${sha}")" \
    || fail "download del sorgente fallito ($(source_tarball_url "${sha}"))."
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
  if [ -n "${GITSTACK_DOWNLOAD_MIRROR}" ]; then
    # Senza l'API di GitHub: uno sha pieno vale com'è, altrimenti il mirror
    # pubblica <ref>.sha accanto ai sorgenti.
    if printf '%s' "${ref}" | grep -Eq '^[0-9a-f]{40}$'; then
      printf '%s' "${ref}"
      return 0
    fi
    sha="$(curl -fsSL "${GITSTACK_DOWNLOAD_MIRROR}/gitstack/source/${ref}.sha" | head -n1 | tr -d '[:space:]')" \
      || fail "impossibile risolvere ${ref} dal mirror (${GITSTACK_DOWNLOAD_MIRROR}/gitstack/source/${ref}.sha): usa --image-tag o un checkout locale."
    printf '%s' "${sha}" | grep -Eq '^[0-9a-f]{40}$' || fail "il mirror non dà uno sha valido per ${ref}."
    printf '%s' "${sha}"
    return 0
  fi
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

# Imposta INSTALL_K3S_VERSION dal file deploy/k3s-version accanto al chart
# (la cartella del chart è <radice>/deploy/gitstack), se non è già impostata.
resolve_k3s_version() {
  if [ -n "${INSTALL_K3S_VERSION}" ]; then
    return 0
  fi
  local version_file="${CHART_DIR}/../k3s-version"
  [ -f "${version_file}" ] \
    || fail "manca ${version_file} (versione di k3s provata con questa versione di GitStack): impostala con INSTALL_K3S_VERSION."
  INSTALL_K3S_VERSION="$(tr -d '[:space:]' < "${version_file}")"
  case "${INSTALL_K3S_VERSION}" in
    v[0-9]*.[0-9]*.[0-9]*+k3s[0-9]*) ;;
    *) fail "${version_file}: versione di k3s non valida («${INSTALL_K3S_VERSION}», attesa vX.Y.Z+k3sN)." ;;
  esac
}

install_k3s() {
  if k3s_already_installed; then
    log "k3s è già installato ($(k3s --version | head -n1)): non reinstallo (idempotenza)."
    if [ "${NETWORK_K3S_CHANGED}" -eq 1 ]; then
      # Mirror, proxy o CA cambiati: containerd e kubelet li leggono solo
      # all'avvio. Riavvio controllato, poi wait_for_k3s_ready.
      log "La configurazione di rete di k3s è cambiata: riavvio k3s."
      systemctl daemon-reload
      systemctl restart k3s
      NETWORK_K3S_CHANGED=0
    elif ! systemctl is-active --quiet k3s 2>/dev/null; then
      log "Il servizio k3s non è attivo: lo avvio."
      systemctl start k3s
    fi
    return 0
  fi

  resolve_k3s_version
  log "Installo k3s ${INSTALL_K3S_VERSION} (pinnato, include Traefik v3) ..."
  command -v curl >/dev/null 2>&1 || fail "serve 'curl' per installare k3s."
  local k3s_env=("INSTALL_K3S_VERSION=${INSTALL_K3S_VERSION}" "INSTALL_K3S_EXEC=server --write-kubeconfig-mode 644")
  if [ -n "${GITSTACK_DOWNLOAD_MIRROR}" ]; then
    # Lo script di k3s scarica binario e checksum da ${GITHUB_URL}/download/<versione>/.
    k3s_env+=("GITHUB_URL=${GITSTACK_DOWNLOAD_MIRROR}/k3s/releases")
  fi
  curl -sfL "$(k3s_script_url)" \
    | env "${k3s_env[@]}" sh - \
    || fail "installazione di k3s fallita (script: $(k3s_script_url))."
  NETWORK_K3S_CHANGED=0
  strip_k3s_env_proxy
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

  local helm_base
  helm_base="$(helm_base_url)"
  curl -fsSL -o "${workdir}/${tarball_name}" "${helm_base}/${tarball_name}" \
    || fail "download del tarball di Helm fallito (${helm_base}/${tarball_name})."
  curl -fsSL -o "${workdir}/${tarball_name}.sha256sum" "${helm_base}/${tarball_name}.sha256sum" \
    || fail "download del checksum di Helm fallito (${helm_base}/${tarball_name}.sha256sum)."
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
  # HTTPS e URL pubblici (resolve_tls): prima di --set/--values dell'utente,
  # che possono sovrascriverli.
  local tls_set
  for tls_set in "${TLS_HELM_SETS[@]}"; do
    helm_args+=(--set "${tls_set}")
  done
  postgres_helm_sets
  local pg_set
  for pg_set in "${POSTGRES_HELM_SETS[@]:-}"; do
    [ -n "${pg_set}" ] && helm_args+=(--set "${pg_set}")
  done
  # Proxy e CA aziendale (N9): dopo il TLS, prima dei valori dell'utente.
  local net_set
  for net_set in "${NETWORK_HELM_SETS[@]:-}"; do
    [ -n "${net_set}" ] && helm_args+=(--set "${net_set}")
  done
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

# Copia il chart in GITSTACK_CHART_COPY (sostituzione atomica per rename).
persist_chart() {
  local chart_dir="$1"
  local dest="${GITSTACK_CHART_COPY}"
  mkdir -p "$(dirname "${dest}")"
  rm -rf "${dest}.new"
  cp -r "${chart_dir}" "${dest}.new"
  rm -rf "${dest}"
  mv "${dest}.new" "${dest}"
}

# --- Comando `gitstack` e file di configurazione -----------------------

# Percorso del binario verificato, pronto da installare (vuoto: niente da
# installare). Variabile globale e non "$(...)": TMP_DIRS deve restare del
# processo principale (vedi resolve_chart_dir).
ADMIN_STAGED=""
ADMIN_SOURCE_DESC=""

# Copia in $2 la risorsa $1 (percorso locale o URL). Ritorna 1 se manca.
fetch_resource() {
  local src="$1" dest="$2"
  case "${src}" in
    http://*|https://*)
      command -v curl >/dev/null 2>&1 || fail "serve 'curl' per scaricare ${src}."
      curl -fsSL -o "${dest}" "${src}" 2>/dev/null
      ;;
    *)
      [ -f "${src}" ] && cp "${src}" "${dest}"
      ;;
  esac
}

# Scarica (o copia) il binario di `gitstack` e ne verifica lo SHA-256 prima
# di toccare il sistema: se il checksum non torna, esce con errore e non
# installa niente.
prepare_admin() {
  local image_tag="$1"
  local src="${GITSTACK_ADMIN_BINARY}" explicit=0
  if [ -n "${src}" ]; then
    explicit=1
  else
    src="$(admin_default_url "${image_tag}")"
  fi
  if [ "${GITSTACK_ADMIN_REQUIRED}" = "1" ]; then
    explicit=1
  fi
  command -v sha256sum >/dev/null 2>&1 || fail "serve 'sha256sum' per verificare il binario di gitstack."

  local workdir
  workdir="$(mktemp -d)"
  TMP_DIRS+=("${workdir}")

  if ! fetch_resource "${src}" "${workdir}/gitstack"; then
    if [ "${explicit}" -eq 1 ]; then
      fail "binario di gitstack non trovato: ${src}"
    fi
    warn "binario di gitstack non pubblicato (${src}): il comando 'gitstack' non viene installato. Costruiscilo e rilancia con --admin-binary (vedi admin/README.md)."
    return 0
  fi

  local expected="${GITSTACK_ADMIN_SHA256}"
  if [ -z "${expected}" ]; then
    fetch_resource "${src}.sha256" "${workdir}/gitstack.sha256" \
      || fail "checksum SHA-256 di gitstack non trovato (${src}.sha256): passa --admin-sha256 oppure pubblica il file accanto al binario."
    expected="$(awk 'NR==1 { print $1 }' "${workdir}/gitstack.sha256")"
  fi
  expected="$(printf '%s' "${expected}" | tr 'A-F' 'a-f')"
  case "${expected}" in
    *[!0-9a-f]*|'') fail "checksum SHA-256 di gitstack non valido: '${expected}'." ;;
  esac
  [ "${#expected}" -eq 64 ] || fail "checksum SHA-256 di gitstack non valido: '${expected}' (servono 64 cifre esadecimali)."

  local actual
  actual="$(sha256sum "${workdir}/gitstack" | awk '{ print $1 }')"
  if [ "${actual}" != "${expected}" ]; then
    fail "il binario di gitstack (${src}) non corrisponde al checksum SHA-256: atteso ${expected}, trovato ${actual}. Non installato."
  fi
  chmod 0755 "${workdir}/gitstack"
  ADMIN_STAGED="${workdir}/gitstack"
  ADMIN_SOURCE_DESC="${src}"
  log "Binario di gitstack verificato (SHA-256 ${actual})."
}

# Installa il binario verificato in /usr/local/bin; se è diverso da quello
# presente lo aggiorna, con una sostituzione atomica.
install_admin() {
  [ -n "${ADMIN_STAGED}" ] || return 0
  if [ -f "${ADMIN_BIN_PATH}" ] && cmp -s "${ADMIN_STAGED}" "${ADMIN_BIN_PATH}"; then
    log "gitstack in ${ADMIN_BIN_PATH} è già alla versione indicata."
    return 0
  fi
  install -m 0755 "${ADMIN_STAGED}" "${ADMIN_BIN_PATH}.new"
  mv -f "${ADMIN_BIN_PATH}.new" "${ADMIN_BIN_PATH}"
  log "Installato ${ADMIN_BIN_PATH} da ${ADMIN_SOURCE_DESC}: $("${ADMIN_BIN_PATH}" version 2>/dev/null || echo 'versione non leggibile')."
}

# Valore di una chiave del config esistente: di primo livello (section vuota)
# o dentro la sezione indicata (qui "backup"). Una riesecuzione non cancella
# così le scelte sul backup.
existing_config_value() {
  local section="$1" key="$2"
  [ -f "${GITSTACK_CONFIG_FILE}" ] || return 0
  if [ -n "${section}" ]; then
    awk -v sec="${section}:" -v key="${key}:" '
      $0 == sec { insec = 1; next }
      /^[^ ]/ { insec = 0 }
      insec && $1 == key { print $2; exit }' "${GITSTACK_CONFIG_FILE}"
  else
    awk -v key="${key}:" '$1 == key { print $2; exit }' "${GITSTACK_CONFIG_FILE}"
  fi
}

# Sezione `network:` di config.yaml (vuota se non c'è niente da ricordare):
# le riesecuzioni la rileggono (resolve_network) e `gitstack config set
# network` la cambia. La password è nello stesso file, che è root-only (0600).
render_config_network() {
  local ca=""
  [ -z "${GITSTACK_CA_BUNDLE}" ] || ca="$(net_ca_copy_path)"
  if [ -z "${GITSTACK_HTTP_PROXY}${GITSTACK_HTTPS_PROXY}${GITSTACK_NO_PROXY}${GITSTACK_REGISTRY_MIRROR}${GITSTACK_DOWNLOAD_MIRROR}${ca}" ]; then
    return 0
  fi
  printf 'network:\n'
  local kv key val
  for kv in http_proxy:"${GITSTACK_HTTP_PROXY}" https_proxy:"${GITSTACK_HTTPS_PROXY}" no_proxy:"${GITSTACK_NO_PROXY}" \
    registry_mirror:"${GITSTACK_REGISTRY_MIRROR}" registry_username:"${GITSTACK_REGISTRY_USERNAME}" \
    registry_password:"${GITSTACK_REGISTRY_PASSWORD}" download_mirror:"${GITSTACK_DOWNLOAD_MIRROR}" ca_bundle:"${ca}"; do
    key="${kv%%:*}"
    val="${kv#*:}"
    [ -n "${val}" ] || continue
    printf '  %s: %s\n' "${key}" "$(net_quote "${val}")"
  done
}

# --- Backup su S3 (N7) -------------------------------------------------

# Vero se almeno un'opzione S3 è stata data.
s3_requested() {
  [ -n "${GITSTACK_S3_ENDPOINT}${GITSTACK_S3_BUCKET}${GITSTACK_S3_PREFIX}${GITSTACK_S3_REGION}${GITSTACK_S3_KEY_FILE}${GITSTACK_S3_RETENTION}${GITSTACK_S3_ACCESS_KEY}${GITSTACK_S3_SECRET_KEY}" ]
}

# Controlla le opzioni S3 prima di toccare il sistema. Senza chiave di
# cifratura la configurazione è rifiutata: un archivio in chiaro non lascia
# mai la macchina. Le chiavi non compaiono mai nei messaggi.
validate_s3_options() {
  s3_requested || return 0
  [ -n "${GITSTACK_S3_ENDPOINT}" ] || fail "backup S3: manca --s3-endpoint (o GITSTACK_S3_ENDPOINT)."
  [ -n "${GITSTACK_S3_BUCKET}" ] || fail "backup S3: manca --s3-bucket (o GITSTACK_S3_BUCKET)."
  if [ -z "${GITSTACK_S3_ACCESS_KEY}" ] || [ -z "${GITSTACK_S3_SECRET_KEY}" ]; then
    fail "backup S3: servono GITSTACK_S3_ACCESS_KEY e GITSTACK_S3_SECRET_KEY nell'ambiente."
  fi
  [ -n "${GITSTACK_S3_KEY_FILE}" ] ||
    fail "backup S3: manca --s3-key-file. Gli archivi caricati su S3 sono sempre cifrati: senza chiave la configurazione e' rifiutata (per esempio: openssl rand -base64 32 > /etc/gitstack/backup.key)."
  case "${GITSTACK_S3_KEY_FILE}" in
    /*) ;;
    *) fail "backup S3: --s3-key-file deve essere un percorso assoluto." ;;
  esac
  [ -f "${GITSTACK_S3_KEY_FILE}" ] || fail "backup S3: il file della chiave '${GITSTACK_S3_KEY_FILE}' non esiste."
  if [ "$(tr -d '[:space:]' <"${GITSTACK_S3_KEY_FILE}" | wc -c)" -lt 16 ]; then
    fail "backup S3: la chiave in ${GITSTACK_S3_KEY_FILE} e' troppo corta (almeno 16 caratteri)."
  fi
  case "${GITSTACK_S3_RETENTION}" in
    ''|bucket|[1-9]|[1-9][0-9]*) ;;
    *) fail "backup S3: --s3-retention non valida: '${GITSTACK_S3_RETENTION}' (numero intero >= 1 oppure 'bucket')." ;;
  esac
}

# Una stringa YAML tra apici singoli.
yaml_squote() {
  printf "'%s'" "$(printf '%s' "$1" | sed "s/'/''/g")"
}

# La sezione backup.s3 del config esistente, tale e quale (rieseguire
# l'installer non cancella la configurazione S3 né notify_command).
existing_s3_block() {
  [ -f "${GITSTACK_CONFIG_FILE}" ] || return 0
  awk '
    /^backup:/ { insec = 1; next }
    /^[^ #]/ { insec = 0; ins3 = 0 }
    insec && /^  s3:/ { ins3 = 1; print; next }
    ins3 && /^   / { print; next }
    ins3 { ins3 = 0 }' "${GITSTACK_CONFIG_FILE}"
}

# Righe da aggiungere sotto "backup:" (vuoto se S3 non è configurato).
s3_config_block() {
  if ! s3_requested; then
    existing_s3_block
    return 0
  fi
  printf '  s3:\n'
  printf '    endpoint: %s\n' "$(yaml_squote "${GITSTACK_S3_ENDPOINT}")"
  printf '    bucket: %s\n' "$(yaml_squote "${GITSTACK_S3_BUCKET}")"
  [ -z "${GITSTACK_S3_PREFIX}" ] || printf '    prefix: %s\n' "$(yaml_squote "${GITSTACK_S3_PREFIX}")"
  [ -z "${GITSTACK_S3_REGION}" ] || printf '    region: %s\n' "$(yaml_squote "${GITSTACK_S3_REGION}")"
  printf '    access_key: %s\n' "$(yaml_squote "${GITSTACK_S3_ACCESS_KEY}")"
  printf '    secret_key: %s\n' "$(yaml_squote "${GITSTACK_S3_SECRET_KEY}")"
  printf '    key_file: %s\n' "$(yaml_squote "${GITSTACK_S3_KEY_FILE}")"
  [ -z "${GITSTACK_S3_RETENTION}" ] || printf '    retention: %s\n' "$(yaml_squote "${GITSTACK_S3_RETENTION}")"
  return 0
}

# Scrive /etc/gitstack/config.yaml (0600, root). Formato e significato dei
# campi: admin/README.md.
write_config() {
  local image_tag="$1"
  local backup_dir="${GITSTACK_BACKUP_DIR:-$(existing_config_value backup destination)}"
  local retention="${GITSTACK_BACKUP_RETENTION:-$(existing_config_value backup retention)}"
  backup_dir="${backup_dir:-/var/backups/gitstack}"
  retention="${retention:-7}"
  case "${backup_dir}" in
    /*) ;;
    *) fail "GITSTACK_BACKUP_DIR deve essere un percorso assoluto: '${backup_dir}'." ;;
  esac
  case "${retention}" in
    ''|*[!0-9]*|0) fail "GITSTACK_BACKUP_RETENTION non valida: '${retention}' (numero intero >= 1)." ;;
  esac

  # Con la CA interna, i comandi di gitstack si fidano del suo certificato per
  # parlare con GitStack in HTTPS (la chiave resta in ${TLS_DIR}/ca.key).
  local s3_block
  s3_block="$(s3_config_block)"
  local ca_line=""
  if [ "${GITSTACK_TLS_MODE}" = "internal" ]; then
    ca_line="ca_cert: ${TLS_DIR}/ca.crt
"
  fi
  mkdir -p "${GITSTACK_CONFIG_DIR}"
  chmod 0700 "${GITSTACK_CONFIG_DIR}"
  local tmp
  tmp="$(mktemp "${GITSTACK_CONFIG_DIR}/.config.XXXXXX")"
  TMP_DIRS+=("${tmp}")
  cat >"${tmp}" <<EOF
# Scritto da deploy/install.sh: lo leggono i comandi di gitstack.
# Root-only (0600). Rieseguire l'installer lo riscrive, mantenendo la sezione backup.
version: 1
host: ${PUBLIC_HOST}
ssh_port: $(git_ssh_port)
chart_dir: ${GITSTACK_CHART_COPY}
tls: ${GITSTACK_TLS_MODE}
${ca_line}release: ${GITSTACK_RELEASE_NAME}
namespace: ${GITSTACK_NAMESPACE}
image_tag: ${image_tag}
kubeconfig: ${GITSTACK_KUBECONFIG}
backup:
  destination: ${backup_dir}
  retention: ${retention}
EOF
  # backup.s3 sta dentro backup: va subito dopo; le sezioni di primo livello
  # (postgres) dopo, altrimenti s3 finirebbe dentro postgres.
  [ -z "${s3_block}" ] || printf '%s\n' "${s3_block}" >>"${tmp}"
  postgres_config_section >>"${tmp}"
  render_config_network >>"${tmp}"
  chmod 0600 "${tmp}"
  mv -f "${tmp}" "${GITSTACK_CONFIG_FILE}"
  log "Configurazione scritta in ${GITSTACK_CONFIG_FILE} (0600)."
}

# --- HTTPS (N5, GIT-143) -----------------------------------------------

is_ipv4() {
  printf '%s' "$1" | grep -Eq '^([0-9]{1,3}\.){3}[0-9]{1,3}$'
}

# Valore di una chiave KEY=valore di un file (vuoto se manca).
read_kv() {
  local file="$1" key="$2"
  [ -f "${file}" ] || return 0
  awk -F= -v k="${key}" '$1 == k { sub(/^[^=]*=/, ""); print; exit }' "${file}"
}

# Imposta GITSTACK_TLS_MODE, TLS_NAMES, TLS_IPS, PUBLIC_HOST, PUBLIC_SCHEME e
# TLS_HELM_SETS. Una riesecuzione senza opzioni conserva le scelte precedenti
# (modalità, --host, email) da ${TLS_DIR}/install.conf: un'installazione con
# certificato del cliente non diventa "internal" per aver dimenticato un flag.
TLS_HELM_SETS=()
resolve_tls() {
  local conf="${TLS_DIR}/install.conf"
  local saved_mode saved_hosts saved_email
  saved_mode="$(read_kv "${conf}" MODE)"
  saved_hosts="$(read_kv "${conf}" HOSTS)"
  saved_email="$(read_kv "${conf}" EMAIL)"

  if [ -n "${GITSTACK_TLS_CERT}" ] || [ -n "${GITSTACK_TLS_KEY}" ]; then
    if [ -z "${GITSTACK_TLS_CERT}" ] || [ -z "${GITSTACK_TLS_KEY}" ]; then
      fail "--tls-cert e --tls-key vanno passati insieme."
    fi
    case "${GITSTACK_TLS_MODE}" in
      ''|internal|custom) GITSTACK_TLS_MODE="custom" ;;
      *) fail "--tls-cert/--tls-key non si combinano con --tls ${GITSTACK_TLS_MODE} / --insecure-http." ;;
    esac
  fi
  if [ -z "${GITSTACK_TLS_MODE}" ]; then
    GITSTACK_TLS_MODE="${saved_mode:-internal}"
  fi
  case "${GITSTACK_TLS_MODE}" in
    internal|letsencrypt|insecure) ;;
    custom)
      if [ -z "${GITSTACK_TLS_CERT}" ] && { [ ! -f "${TLS_DIR}/custom.crt" ] || [ ! -f "${TLS_DIR}/custom.key" ]; }; then
        fail "modalità certificato del cliente senza --tls-cert/--tls-key (e nessun certificato già installato in ${TLS_DIR})."
      fi
      ;;
    *) fail "--tls '${GITSTACK_TLS_MODE}' non valido (internal, letsencrypt; per il certificato del cliente usa --tls-cert/--tls-key; per solo HTTP --insecure-http)." ;;
  esac
  if [ -z "${GITSTACK_TLS_EMAIL}" ]; then
    GITSTACK_TLS_EMAIL="${saved_email}"
  fi

  if [ "${#HOST_ARGS[@]}" -eq 0 ] && [ -n "${saved_hosts}" ]; then
    IFS=',' read -r -a HOST_ARGS <<<"${saved_hosts}"
  fi

  local ip h
  ip="$(primary_ip)"
  TLS_NAMES=""
  TLS_IPS=""
  if [ "${#HOST_ARGS[@]}" -gt 0 ]; then
    PUBLIC_HOST="${HOST_ARGS[0]}"
    for h in "${HOST_ARGS[@]}"; do
      if is_ipv4 "${h}"; then
        TLS_IPS="${TLS_IPS:+${TLS_IPS},}${h}"
      else
        TLS_NAMES="${TLS_NAMES:+${TLS_NAMES},}${h}"
      fi
    done
  else
    # Default: nome completo della macchina (hostname -f) come URL pubblico se
    # risolve a un suo indirizzo, altrimenti l'IP (funziona senza DNS) con un
    # avviso nel preflight. Nei SAN: nome breve, <nome>.local (mDNS/avahi),
    # nome completo e IP.
    PUBLIC_HOST="${ip}"
    local short long
    short="$(hostname -s 2>/dev/null || true)"
    long="$(hostname -f 2>/dev/null || true)"
    if [ -n "${short}" ]; then
      TLS_NAMES="${short}"
      case "${short}" in *.*) ;; *) TLS_NAMES="${TLS_NAMES},${short}.local" ;; esac
    fi
    if [ -n "${long}" ] && [ "${long}" != "${short}" ] && ! is_ipv4 "${long}" && [ "${long}" != "localhost" ]; then
      TLS_NAMES="${TLS_NAMES:+${TLS_NAMES},}${long}"
    fi
    local pub="${long}"
    [ -n "${pub}" ] || pub="${short}"
    if [ -n "${pub}" ] && [ "${pub}" != "localhost" ] && ! is_ipv4 "${pub}" && host_is_local "${pub}"; then
      PUBLIC_HOST="${pub}"
    else
      DEFAULT_HOST_NOTE="il nome della macchina ('${pub:-?}') non risolve a un suo indirizzo: l'URL pubblico è l'IP ${ip}. Per un nome usa --host <nome> (anche .local con avahi-daemon) o 'gitstack config set host <nome>' dopo l'installazione."
    fi
  fi
  # L'IP principale e' sempre fra i SAN (nome .local piu' IP, nota del CEO).
  if is_ipv4 "${ip}"; then
    case ",${TLS_IPS}," in *",${ip},"*) ;; *) TLS_IPS="${TLS_IPS:+${TLS_IPS},}${ip}" ;; esac
  fi
  [ -n "${PUBLIC_HOST}" ] || PUBLIC_HOST="localhost"

  local release_tls="${GITSTACK_RELEASE_NAME}-tls"
  case "${GITSTACK_TLS_MODE}" in
    insecure)
      PUBLIC_SCHEME="http"
      TLS_HELM_SETS=("ingress.tls.enabled=false")
      ;;
    internal)
      PUBLIC_SCHEME="https"
      TLS_HELM_SETS=("ingress.tls.enabled=true" "ingress.tls.secretName=${release_tls}" "ingress.tls.caConfigMap=${GITSTACK_RELEASE_NAME}-ca")
      ;;
    custom)
      PUBLIC_SCHEME="https"
      TLS_HELM_SETS=("ingress.tls.enabled=true" "ingress.tls.secretName=${release_tls}" "ingress.tls.caConfigMap=")
      ;;
    letsencrypt)
      PUBLIC_SCHEME="https"
      is_ipv4 "${PUBLIC_HOST}" && fail "--tls letsencrypt vuole un nome di dominio raggiungibile da internet (--host NOME), non un IP."
      case "${PUBLIC_HOST}" in
        *.*) ;;
        *) fail "--tls letsencrypt: '${PUBLIC_HOST}' non e' un nome di dominio pubblico (--host NOME)." ;;
      esac
      case "${PUBLIC_HOST}" in
        *.local|*.lan|*.internal|*.home|*.localdomain)
          fail "--tls letsencrypt: '${PUBLIC_HOST}' non e' un nome pubblico: Let's Encrypt non emette certificati per domini locali (usa la CA interna)."
          ;;
      esac
      TLS_HELM_SETS=("ingress.tls.enabled=true" "ingress.tls.secretName=" "ingress.tls.certResolver=letsencrypt" "ingress.tls.caConfigMap=")
      ;;
  esac
  TLS_HELM_SETS+=("core.env.publicUrl=${PUBLIC_SCHEME}://${PUBLIC_HOST}" "identity.oidc.publicUrl=${PUBLIC_SCHEME}://${PUBLIC_HOST}")

  case "${PUBLIC_HOST}" in
    *[!A-Za-z0-9.:-]*) fail "--host '${PUBLIC_HOST}' non valido." ;;
  esac
  case "${GITSTACK_TLS_EMAIL}" in
    *[!A-Za-z0-9.@+_-]*) fail "--tls-email '${GITSTACK_TLS_EMAIL}' non valida." ;;
  esac
}

# Salva le scelte per le riesecuzioni (vedi resolve_tls).
save_tls_choices() {
  mkdir -p "${TLS_DIR}"
  chmod 0700 "${TLS_DIR}"
  local hosts="" h
  for h in "${HOST_ARGS[@]:-}"; do
    [ -n "${h}" ] && hosts="${hosts:+${hosts},}${h}"
  done
  local tmp
  tmp="$(mktemp "${TLS_DIR}/.install.conf.XXXXXX")"
  TMP_DIRS+=("${tmp}")
  cat >"${tmp}" <<EOF
MODE=${GITSTACK_TLS_MODE}
HOSTS=${hosts}
EMAIL=${GITSTACK_TLS_EMAIL}
EOF
  chmod 0600 "${tmp}"
  mv -f "${tmp}" "${TLS_DIR}/install.conf"
}

# Installa /usr/local/sbin/gitstack-tls dal file accanto allo script (deploy/).
install_tls_tool() {
  local chart_dir="$1"
  local src="${chart_dir}/../gitstack-tls.sh"
  [ -f "${src}" ] || fail "gitstack-tls.sh non trovato accanto al chart (${src}): --chart-dir deve puntare a deploy/gitstack di un checkout completo."
  install -m 0755 "${src}" "${TLS_BIN_PATH}"
}

# Timer systemd per il rinnovo (certificato interno) e il controllo delle
# scadenze (certificato del cliente). Ogni notte, con un ritardo casuale.
enable_renew_timer() {
  if ! command -v systemctl >/dev/null 2>&1 || [ ! -d /run/systemd/system ]; then
    warn "systemd non disponibile: il rinnovo automatico del certificato non e' attivo. Pianifica a mano '${TLS_BIN_PATH} renew' (almeno una volta al giorno)."
    return 0
  fi
  cat >"${TLS_SYSTEMD_DIR}/gitstack-tls-renew.service" <<EOF
[Unit]
Description=GitStack: rinnovo del certificato HTTPS
After=k3s.service

[Service]
Type=oneshot
ExecStart=${TLS_BIN_PATH} renew
EOF
  cat >"${TLS_SYSTEMD_DIR}/gitstack-tls-renew.timer" <<EOF
[Unit]
Description=GitStack: controllo giornaliero del certificato HTTPS

[Timer]
OnCalendar=*-*-* 03:30:00
RandomizedDelaySec=30m
Persistent=true

[Install]
WantedBy=timers.target
EOF
  systemctl daemon-reload
  systemctl enable --now gitstack-tls-renew.timer >/dev/null
  log "Rinnovo automatico attivo: gitstack-tls-renew.timer (ogni notte, rinnova a meno di 30 giorni dalla scadenza)."
}

disable_renew_timer() {
  if command -v systemctl >/dev/null 2>&1 && [ -f "${TLS_SYSTEMD_DIR}/gitstack-tls-renew.timer" ]; then
    systemctl disable --now gitstack-tls-renew.timer >/dev/null 2>&1 || true
    rm -f "${TLS_SYSTEMD_DIR}/gitstack-tls-renew.timer" "${TLS_SYSTEMD_DIR}/gitstack-tls-renew.service"
    systemctl daemon-reload || true
  fi
}

# Timer systemd per il backup giornaliero. Orario configurabile con
# GITSTACK_BACKUP_TIMER_HOUR (default 02).
enable_backup_timer() {
  if ! command -v systemctl >/dev/null 2>&1 || [ ! -d /run/systemd/system ]; then
    warn "systemd non disponibile: il backup automatico non e' attivo. Pianifica a mano '${ADMIN_BIN_PATH}' backup."
    return 0
  fi
  if [ ! -x "${ADMIN_BIN_PATH}" ]; then
    warn "il binario ${ADMIN_BIN_PATH} non e' presente: il timer del backup non viene installato, pianifica a mano."
    return 0
  fi
  local hour="${GITSTACK_BACKUP_TIMER_HOUR:-02}"
  case "${hour}" in
    ''|*[!0-9]*) fail "GITSTACK_BACKUP_TIMER_HOUR non valida: '${hour}' (solo un numero intero da 0 a 23)." ;;
  esac
  if [ "$((10#${hour}))" -lt 0 ] || [ "$((10#${hour}))" -gt 23 ]; then
    fail "GITSTACK_BACKUP_TIMER_HOUR non valida: '${hour}' (solo un numero intero da 0 a 23)."
  fi
  cat >"${BACKUP_SYSTEMD_DIR}/gitstack-backup.service" <<EOF
[Unit]
Description=GitStack: backup giornaliero
After=k3s.service

[Service]
Type=oneshot
ExecStart=${ADMIN_BIN_PATH} backup
EOF
  cat >"${BACKUP_SYSTEMD_DIR}/gitstack-backup.timer" <<EOF
[Unit]
Description=GitStack: timer backup giornaliero

[Timer]
OnCalendar=*-*-* ${hour}:00:00
Persistent=true

[Install]
WantedBy=timers.target
EOF
  systemctl daemon-reload
  systemctl enable --now gitstack-backup.timer >/dev/null
  log "Backup automatico attivo: gitstack-backup.timer (ogni giorno alle ${hour}:00)."
}

disable_backup_timer() {
  if command -v systemctl >/dev/null 2>&1 && [ -f "${BACKUP_SYSTEMD_DIR}/gitstack-backup.timer" ]; then
    systemctl disable --now gitstack-backup.timer >/dev/null 2>&1 || true
    rm -f "${BACKUP_SYSTEMD_DIR}/gitstack-backup.timer" "${BACKUP_SYSTEMD_DIR}/gitstack-backup.service"
    systemctl daemon-reload || true
  fi
}

# Let's Encrypt: il Traefik di k3s e' un HelmChart; la sua configurazione si
# estende con un HelmChartConfig (certificatesResolvers + storage persistente
# per acme.json). k3s lo applica da solo.
write_acme_manifest() {
  mkdir -p "${K3S_MANIFESTS_DIR}"
  local email_line="" server_line=""
  [ -n "${GITSTACK_TLS_EMAIL}" ] && email_line="          email: ${GITSTACK_TLS_EMAIL}"
  [ -n "${GITSTACK_ACME_CA_SERVER}" ] && server_line="          caServer: ${GITSTACK_ACME_CA_SERVER}"
  {
    cat <<'EOF'
# Scritto da deploy/install.sh (--tls letsencrypt, N5/GIT-143). Non modificarlo
# a mano: rilancia l'installer.
apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata:
  name: traefik
  namespace: kube-system
spec:
  valuesContent: |-
    persistence:
      enabled: true
      path: /data
      size: 128Mi
    certificatesResolvers:
      letsencrypt:
        acme:
EOF
    [ -n "${email_line}" ] && printf '%s\n' "${email_line}"
    [ -n "${server_line}" ] && printf '%s\n' "${server_line}"
    cat <<'EOF'
          storage: /data/acme.json
          httpChallenge:
            entryPoint: web
EOF
  } >"${ACME_MANIFEST}.tmp"
  mv -f "${ACME_MANIFEST}.tmp" "${ACME_MANIFEST}"
  log "Let's Encrypt: scritta la configurazione di Traefik in ${ACME_MANIFEST} (HTTP-01 sulla porta 80, host ${PUBLIC_HOST})."
  if [ -z "${GITSTACK_TLS_EMAIL}" ]; then
    warn "--tls-email non indicata: Let's Encrypt non potra' avvisarti delle scadenze."
  fi
}

# Prepara i certificati prima di helm: il Secret TLS e il ConfigMap della CA
# devono esistere quando partono Traefik (Ingress) e web (volume).
setup_tls() {
  local chart_dir="$1"
  save_tls_choices
  case "${GITSTACK_TLS_MODE}" in
    internal|custom)
      rm -f "${ACME_MANIFEST}"
      install_tls_tool "${chart_dir}"
      local args=(ensure --mode "${GITSTACK_TLS_MODE}" --names "${TLS_NAMES}" --ips "${TLS_IPS}"
        --release "${GITSTACK_RELEASE_NAME}" --namespace "${GITSTACK_NAMESPACE}" --kubeconfig "${GITSTACK_KUBECONFIG}")
      if [ "${GITSTACK_TLS_MODE}" = "internal" ]; then
        args+=(--ca-configmap "${GITSTACK_RELEASE_NAME}-ca")
      else
        local dst_crt="${TLS_DIR}/custom.crt" dst_key="${TLS_DIR}/custom.key"
        if [ -n "${GITSTACK_TLS_CERT}" ]; then
          [ -f "${GITSTACK_TLS_CERT}" ] || fail "--tls-cert: file non trovato: ${GITSTACK_TLS_CERT}"
          [ -f "${GITSTACK_TLS_KEY}" ] || fail "--tls-key: file non trovato: ${GITSTACK_TLS_KEY}"
          install -m 0600 "${GITSTACK_TLS_CERT}" "${dst_crt}.new"
          install -m 0600 "${GITSTACK_TLS_KEY}" "${dst_key}.new"
          mv -f "${dst_crt}.new" "${dst_crt}"
          mv -f "${dst_key}.new" "${dst_key}"
        fi
      fi
      "${TLS_BIN_PATH}" "${args[@]}"
      enable_renew_timer
      ;;
    letsencrypt)
      disable_renew_timer
      write_acme_manifest
      ;;
    insecure)
      rm -f "${ACME_MANIFEST}"
      disable_renew_timer
      warn "--insecure-http: GitStack parla HTTP in chiaro, password e token viaggiano senza cifratura. Solo per prove locali: la UI mostra un avviso."
      ;;
  esac
}

# --- Rete aziendale (N4, N9a) ------------------------------------------
# Proxy, mirror delle immagini e dei download, CA aziendale. Il formato dei
# file generati (registries.yaml, drop-in di k3s) è lo stesso di
# `gitstack config set network` (admin/internal/cli/confignetwork.go).
# La verifica dei checksum (V5) e le regole di uscita dei servizi (C8,
# pkg/egress) restano quelle di prima: il mirror cambia da dove si scarica,
# non cosa si verifica.

CORPORATE_CA_FILE_NAME="ca.crt"
SYSTEM_CA_FILE_NAME="gitstack-corporate-ca.crt"
K3S_DROPIN_NAME="10-gitstack-network.conf"
# Secondi di attesa per ogni prova di raggiungibilità.
NET_PROBE_TIMEOUT="${NET_PROBE_TIMEOUT:-10}"

# Valore di una chiave della sezione `network:` di config.yaml (tra apici,
# come la scrive write_config). Vuoto se manca.
existing_network_value() {
  [ -f "${GITSTACK_CONFIG_FILE}" ] || return 0
  awk -v key="$1" '
    $0 == "network:" { insec = 1; next }
    /^[^ ]/ { insec = 0 }
    insec {
      k = $1; sub(/:$/, "", k)
      if (k == key) {
        v = $0; sub(/^[^:]*: */, "", v)
        if (v ~ /^".*"$/) { v = substr(v, 2, length(v) - 2); gsub(/\\"/, "\"", v); gsub(/\\\\/, "\\", v) }
        print v; exit
      }
    }' "${GITSTACK_CONFIG_FILE}"
}

# Scrive la stringa tra apici doppi, con \ e " protetti (stile YAML).
net_quote() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  printf '"%s"' "${s}"
}

# URL senza la password (per i messaggi).
net_redact() {
  printf '%s' "$1" | sed -E 's#(://[^:/@]*):[^@/]*@#\1:xxxxx@#'
}

# host[:porta] di un URL.
net_authority() {
  printf '%s' "$1" | sed -E 's#^[a-z0-9]+://##; s#^[^/@]*@##; s#/.*$##'
}

# net_valid_url OPZIONE VALORE SCHEMA...: URL con uno degli schemi.
net_valid_url() {
  local what="$1" v="$2" ok=0 sch
  shift 2
  [ -n "${v}" ] || return 0
  for sch in "$@"; do
    case "${v}" in "${sch}://"?*) ok=1 ;; esac
  done
  [ "${ok}" -eq 1 ] || fail "${what}: '$(net_redact "${v}")' non è un URL valido (atteso <schema>://host[:porta], schemi: $*)."
}

# Proxy HTTPS effettivo: senza --https-proxy vale quello HTTP.
net_https_proxy() {
  printf '%s' "${GITSTACK_HTTPS_PROXY:-${GITSTACK_HTTP_PROXY}}"
}

# Indirizzi IPv4 della macchina (senza loopback), ordinati, separati da
# virgole: l'API server parla al kubelet su <IP del nodo>:10250, che non deve
# passare dal proxy. Stesso elenco e stesso ordine di `gitstack config set
# network` (nodeAddrs in admin/internal/cli/confignetwork.go).
net_node_addrs() {
  local_addrs | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | grep -v '^127\.' | LC_ALL=C sort -u | paste -sd, -
}

# NO_PROXY di k3s (e dell'installer): reti del cluster, indirizzi del nodo e
# quanto indicato.
net_k3s_no_proxy() {
  local out="${K3S_BUILTIN_NO_PROXY}" addrs
  addrs="$(net_node_addrs)"
  if [ -n "${addrs}" ]; then
    out="${out},${addrs}"
  fi
  if [ -n "${GITSTACK_NO_PROXY}" ]; then
    out="${out},${GITSTACK_NO_PROXY}"
  fi
  printf '%s' "${out}"
}

net_ca_copy_path() {
  printf '%s/%s' "${GITSTACK_NETWORK_DIR}" "${CORPORATE_CA_FILE_NAME}"
}

# Protegge virgole e backslash di un valore per helm --set.
net_helm_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//,/\\,}"
  printf '%s' "${s}"
}

# Il bundle è un file PEM con almeno un certificato.
net_check_ca_bundle() {
  local f="$1"
  [ -f "${f}" ] || fail "--ca-bundle: file non trovato: ${f}"
  grep -q -- '-----BEGIN CERTIFICATE-----' "${f}" || fail "--ca-bundle: ${f} non contiene certificati PEM (-----BEGIN CERTIFICATE-----)."
  if command -v openssl >/dev/null 2>&1; then
    openssl x509 -in "${f}" -noout >/dev/null 2>&1 || fail "--ca-bundle: il primo certificato di ${f} non è valido (openssl x509)."
  fi
}

# Imposta le variabili di rete (opzioni, altrimenti le scelte della volta
# prima), le convalida, esporta il proxy per curl, k3s e Helm dell'installer e
# prepara i valori Helm di N9b (proxy.*, corporateCA.configMap).
resolve_network() {
  local user_given="${GITSTACK_REGISTRY_USERNAME}" v var key saved_ca
  for v in HTTP_PROXY HTTPS_PROXY NO_PROXY REGISTRY_MIRROR REGISTRY_USERNAME DOWNLOAD_MIRROR; do
    var="GITSTACK_${v}"
    key="$(printf '%s' "${v}" | tr '[:upper:]' '[:lower:]')"
    if [ -z "${!var}" ]; then
      printf -v "${var}" '%s' "$(existing_network_value "${key}")"
    fi
  done
  if [ -n "${GITSTACK_REGISTRY_PASSWORD_FILE}" ]; then
    [ -f "${GITSTACK_REGISTRY_PASSWORD_FILE}" ] || fail "--registry-password-file: file non trovato: ${GITSTACK_REGISTRY_PASSWORD_FILE}"
    GITSTACK_REGISTRY_PASSWORD="$(head -n1 "${GITSTACK_REGISTRY_PASSWORD_FILE}" | tr -d '\r\n')"
  fi
  if [ -z "${GITSTACK_REGISTRY_PASSWORD}" ] && [ -z "${user_given}" ]; then
    GITSTACK_REGISTRY_PASSWORD="$(existing_network_value registry_password)"
  fi
  if [ -z "${GITSTACK_CA_BUNDLE}" ]; then
    saved_ca="$(existing_network_value ca_bundle)"
    if [ -n "${saved_ca}" ] && [ -f "${saved_ca}" ]; then
      GITSTACK_CA_BUNDLE="${saved_ca}"
    fi
  fi

  net_valid_url "--http-proxy" "${GITSTACK_HTTP_PROXY}" http https socks5
  net_valid_url "--https-proxy" "${GITSTACK_HTTPS_PROXY}" http https socks5
  net_valid_url "--registry-mirror" "${GITSTACK_REGISTRY_MIRROR}" http https
  net_valid_url "--download-mirror" "${GITSTACK_DOWNLOAD_MIRROR}" http https
  case "${GITSTACK_NO_PROXY}" in
    *[[:space:]]*) fail "--no-proxy: elementi separati da virgole, senza spazi." ;;
  esac
  if [ -n "${GITSTACK_REGISTRY_USERNAME}" ] && [ -z "${GITSTACK_REGISTRY_PASSWORD}" ]; then
    fail "--registry-username senza password: usa --registry-password-file FILE (o GITSTACK_REGISTRY_PASSWORD)."
  fi
  if [ -z "${GITSTACK_REGISTRY_USERNAME}" ] && [ -n "${GITSTACK_REGISTRY_PASSWORD}" ]; then
    fail "password del mirror senza --registry-username."
  fi
  if [ -n "${GITSTACK_REGISTRY_USERNAME}" ] && [ -z "${GITSTACK_REGISTRY_MIRROR}" ]; then
    fail "--registry-username senza --registry-mirror."
  fi
  GITSTACK_DOWNLOAD_MIRROR="${GITSTACK_DOWNLOAD_MIRROR%/}"
  GITSTACK_REGISTRY_MIRROR="${GITSTACK_REGISTRY_MIRROR%/}"
  if [ -n "${GITSTACK_CA_BUNDLE}" ]; then
    net_check_ca_bundle "${GITSTACK_CA_BUNDLE}"
  fi

  NETWORK_CONFIGURED=0
  if [ -n "${GITSTACK_HTTP_PROXY}${GITSTACK_HTTPS_PROXY}${GITSTACK_REGISTRY_MIRROR}${GITSTACK_DOWNLOAD_MIRROR}${GITSTACK_CA_BUNDLE}" ]; then
    NETWORK_CONFIGURED=1
  fi

  # Proxy per tutto ciò che lancia l'installer (curl, script di k3s, Helm).
  # NO_PROXY è quello di k3s: lo script di k3s lo copia in k3s.service.env, e
  # così coincide con il drop-in.
  if [ -n "${GITSTACK_HTTP_PROXY}" ]; then
    export HTTP_PROXY="${GITSTACK_HTTP_PROXY}" http_proxy="${GITSTACK_HTTP_PROXY}"
  fi
  local https_proxy_value no_proxy_value
  https_proxy_value="$(net_https_proxy)"
  if [ -n "${https_proxy_value}" ]; then
    export HTTPS_PROXY="${https_proxy_value}" https_proxy="${https_proxy_value}"
    no_proxy_value="$(net_k3s_no_proxy)"
    export NO_PROXY="${no_proxy_value}" no_proxy="${no_proxy_value}"
  fi

  # Valori del chart (contratto di N9b: proxy.http, proxy.https, proxy.noProxy,
  # corporateCA.configMap).
  NETWORK_HELM_SETS=()
  if [ -n "${GITSTACK_HTTP_PROXY}$(net_https_proxy)" ]; then
    NETWORK_HELM_SETS+=("proxy.http=$(net_helm_escape "${GITSTACK_HTTP_PROXY}")"
      "proxy.https=$(net_helm_escape "$(net_https_proxy)")"
      "proxy.noProxy=$(net_helm_escape "${GITSTACK_NO_PROXY}")")
  fi
  if [ -n "${GITSTACK_CA_BUNDLE}" ]; then
    NETWORK_HELM_SETS+=("corporateCA.configMap=${GITSTACK_RELEASE_NAME}-corporate-ca")
  fi
}

# --- Download: da dove (--download-mirror) ----------------------------------
# Struttura attesa del mirror (docs/operations.md):
#   <mirror>/k3s/install.sh
#   <mirror>/k3s/releases/download/<versione>/{k3s,sha256sum-amd64.txt}
#   <mirror>/helm/helm-<versione>-linux-amd64.tar.gz[.sha256sum]
#   <mirror>/gitstack/<tag>/gitstack-linux-amd64[.sha256]
#   <mirror>/gitstack/source/<sha>.tar.gz  e  <ref>.sha (solo senza checkout)
k3s_script_url() {
  if [ -n "${GITSTACK_DOWNLOAD_MIRROR}" ]; then
    printf '%s/k3s/install.sh' "${GITSTACK_DOWNLOAD_MIRROR}"
  else
    printf 'https://get.k3s.io'
  fi
}

helm_base_url() {
  if [ -n "${GITSTACK_DOWNLOAD_MIRROR}" ]; then
    printf '%s/helm' "${GITSTACK_DOWNLOAD_MIRROR}"
  else
    printf 'https://get.helm.sh'
  fi
}

admin_default_url() {
  local tag="$1"
  if [ -n "${GITSTACK_DOWNLOAD_MIRROR}" ]; then
    printf '%s/gitstack/%s/gitstack-linux-amd64' "${GITSTACK_DOWNLOAD_MIRROR}" "${tag}"
  else
    printf 'https://github.com/%s/releases/download/%s/gitstack-linux-amd64' "${GITSTACK_REPO}" "${tag}"
  fi
}

source_tarball_url() {
  local sha="$1"
  if [ -n "${GITSTACK_DOWNLOAD_MIRROR}" ]; then
    printf '%s/gitstack/source/%s.tar.gz' "${GITSTACK_DOWNLOAD_MIRROR}" "${sha}"
  else
    printf 'https://codeload.github.com/%s/tar.gz/%s' "${GITSTACK_REPO}" "${sha}"
  fi
}

# --- File generati per k3s ---------------------------------------------------

# registries.yaml: il mirror vale per ogni immagine ("*"), comprese quelle di
# sistema di k3s. Stampa niente senza mirror.
render_registries_yaml() {
  [ -n "${GITSTACK_REGISTRY_MIRROR}" ] || return 0
  printf '%s\n' '# Scritto da GitStack (install.sh o gitstack config set network): non modificarlo a mano.'
  printf 'mirrors:\n  "*":\n    endpoint:\n      - %s\n' "$(net_quote "${GITSTACK_REGISTRY_MIRROR}")"
  local has_ca=0
  if [ -n "${GITSTACK_CA_BUNDLE}" ]; then
    case "${GITSTACK_REGISTRY_MIRROR}" in https://*) has_ca=1 ;; esac
  fi
  if [ -n "${GITSTACK_REGISTRY_USERNAME}" ] || [ "${has_ca}" -eq 1 ]; then
    printf 'configs:\n  %s:\n' "$(net_quote "$(net_authority "${GITSTACK_REGISTRY_MIRROR}")")"
    if [ -n "${GITSTACK_REGISTRY_USERNAME}" ]; then
      printf '    auth:\n      username: %s\n      password: %s\n' \
        "$(net_quote "${GITSTACK_REGISTRY_USERNAME}")" "$(net_quote "${GITSTACK_REGISTRY_PASSWORD}")"
    fi
    if [ "${has_ca}" -eq 1 ]; then
      printf '    tls:\n      ca_file: %s\n' "$(net_quote "$(net_ca_copy_path)")"
    fi
  fi
}

# Una riga Environment= di systemd: apici doppi, % raddoppiato.
net_systemd_env() {
  local v="$2"
  v="${v//\\/\\\\}"
  v="${v//\"/\\\"}"
  v="${v//%/%%}"
  printf 'Environment="%s=%s"\n' "$1" "${v}"
}

# Drop-in systemd di k3s con le variabili di proxy: containerd e kubelet le
# ereditano. Stampa niente senza proxy.
render_k3s_dropin() {
  [ -n "${GITSTACK_HTTP_PROXY}$(net_https_proxy)" ] || return 0
  printf '%s\n[Service]\n' '# Scritto da GitStack (install.sh o gitstack config set network): non modificarlo a mano.'
  if [ -n "${GITSTACK_HTTP_PROXY}" ]; then
    net_systemd_env HTTP_PROXY "${GITSTACK_HTTP_PROXY}"
    net_systemd_env http_proxy "${GITSTACK_HTTP_PROXY}"
  fi
  net_systemd_env HTTPS_PROXY "$(net_https_proxy)"
  net_systemd_env https_proxy "$(net_https_proxy)"
  net_systemd_env NO_PROXY "$(net_k3s_no_proxy)"
  net_systemd_env no_proxy "$(net_k3s_no_proxy)"
}

# write_managed_file PERCORSO MODO CONTENUTO: scrive solo se cambia (e imposta
# NETWORK_K3S_CHANGED=1); contenuto vuoto = toglie il file. Il contenuto non
# arriva da una pipe: la variabile deve restare nel processo principale. Il
# file provvisorio ha già il modo giusto: una password non è mai leggibile da
# altri.
write_managed_file() {
  local dest="$1" mode="$2" content="$3" tmp
  mkdir -p "$(dirname "${dest}")"
  tmp="$(mktemp "${dest}.XXXXXX")"
  chmod "${mode}" "${tmp}"
  if [ -n "${content}" ]; then
    printf '%s
' "${content}" >"${tmp}"
  fi
  if [ ! -s "${tmp}" ]; then
    rm -f "${tmp}"
    if [ -e "${dest}" ]; then
      rm -f "${dest}"
      NETWORK_K3S_CHANGED=1
    fi
    return 0
  fi
  if [ -f "${dest}" ] && cmp -s "${tmp}" "${dest}"; then
    rm -f "${tmp}"
    return 0
  fi
  mv -f "${tmp}" "${dest}"
  NETWORK_K3S_CHANGED=1
}

# Fiducia nella CA aziendale: copia gestita e CA di sistema (curl, git, i
# comandi dell'host). Containerd la legge da registries.yaml (ca_file) e dal
# sistema; i pod dal ConfigMap (ensure_corporate_ca_configmap).
apply_ca_trust() {
  [ -n "${GITSTACK_CA_BUNDLE}" ] || return 0
  mkdir -p "${GITSTACK_NETWORK_DIR}"
  chmod 0755 "${GITSTACK_NETWORK_DIR}"
  if [ "${GITSTACK_CA_BUNDLE}" != "$(net_ca_copy_path)" ]; then
    install -m 0644 "${GITSTACK_CA_BUNDLE}" "$(net_ca_copy_path).new"
    mv -f "$(net_ca_copy_path).new" "$(net_ca_copy_path)"
  fi
  mkdir -p "${SYSTEM_CA_DIR}"
  local dest="${SYSTEM_CA_DIR}/${SYSTEM_CA_FILE_NAME}"
  if [ ! -f "${dest}" ] || ! cmp -s "$(net_ca_copy_path)" "${dest}"; then
    install -m 0644 "$(net_ca_copy_path)" "${dest}"
    NETWORK_K3S_CHANGED=1
    if command -v update-ca-certificates >/dev/null 2>&1; then
      update-ca-certificates >/dev/null 2>&1 || warn "update-ca-certificates è fallito: la CA aziendale potrebbe non essere fidata dal sistema."
    else
      warn "update-ca-certificates non trovato (pacchetto ca-certificates): la CA aziendale non è nel sistema, solo in containerd e nei pod."
    fi
  fi
  log "CA aziendale: $(net_ca_copy_path) e ${dest}."
}

# registries.yaml (0600: può contenere la password) e drop-in del proxy. Va
# fatto prima di installare k3s (parte già col mirror) e a ogni riesecuzione.
apply_k3s_network() {
  [ "${NETWORK_CONFIGURED}" -eq 1 ] || return 0
  write_managed_file "${K3S_CONFIG_DIR}/registries.yaml" 0600 "$(render_registries_yaml)"
  write_managed_file "${SYSTEMD_UNIT_DIR}/k3s.service.d/${K3S_DROPIN_NAME}" 0644 "$(render_k3s_dropin)"
  if [ -n "${GITSTACK_REGISTRY_MIRROR}" ]; then
    log "Mirror delle immagini: ${GITSTACK_REGISTRY_MIRROR} (registries.yaml, tutte le immagini)."
  fi
  if [ -n "${GITSTACK_HTTP_PROXY}$(net_https_proxy)" ]; then
    log "Proxy per k3s: drop-in ${SYSTEMD_UNIT_DIR}/k3s.service.d/${K3S_DROPIN_NAME}."
  fi
}

# Lo script di k3s copia le variabili *_PROXY nell'ambiente in k3s.service.env:
# il drop-in resta l'unica fonte, così `gitstack config set network` le cambia
# in un posto solo.
strip_k3s_env_proxy() {
  local f="${SYSTEMD_UNIT_DIR}/k3s.service.env" tmp
  [ -f "${f}" ] || return 0
  if grep -qiE '^(http_proxy|https_proxy|no_proxy)=' "${f}"; then
    tmp="$(mktemp "${f}.XXXXXX")"
    grep -viE '^(http_proxy|https_proxy|no_proxy)=' "${f}" >"${tmp}" || true
    chmod 0600 "${tmp}"
    mv -f "${tmp}" "${f}"
  fi
}

# ConfigMap <release>-corporate-ca (chiave ca.crt) nel namespace della
# release: lo monta il chart (corporateCA.configMap). Prima di helm.
# Il file contiene le CA di sistema (dopo apply_ca_trust includono già quella
# aziendale) più la CA aziendale: per il mirror git il chart SOSTITUISCE le CA
# di sistema con questo file, e un mirror verso un host pubblico (in NO_PROXY)
# deve continuare a fidarsi delle CA pubbliche. Stessa logica di
# `gitstack config set network` (corporateCABundle in confignetwork.go).
SYSTEM_CA_BUNDLE_FILE="${GITSTACK_SYSTEM_CA_BUNDLE:-/etc/ssl/certs/ca-certificates.crt}"
ensure_corporate_ca_configmap() {
  [ -n "${GITSTACK_CA_BUNDLE}" ] || return 0
  local name="${GITSTACK_RELEASE_NAME}-corporate-ca" dir combined
  dir="$(mktemp -d)"
  TMP_DIRS+=("${dir}")
  combined="${dir}/${CORPORATE_CA_FILE_NAME}"
  {
    if [ -f "${SYSTEM_CA_BUNDLE_FILE}" ]; then cat "${SYSTEM_CA_BUNDLE_FILE}"; printf '\n'; fi
    cat "$(net_ca_copy_path)"
  } >"${combined}"
  KUBECONFIG="${GITSTACK_KUBECONFIG}" k3s kubectl create namespace "${GITSTACK_NAMESPACE}" >/dev/null 2>&1 || true
  KUBECONFIG="${GITSTACK_KUBECONFIG}" k3s kubectl -n "${GITSTACK_NAMESPACE}" create configmap "${name}" \
    --from-file="${CORPORATE_CA_FILE_NAME}=${combined}" --dry-run=client -o yaml \
    | KUBECONFIG="${GITSTACK_KUBECONFIG}" k3s kubectl apply -f - >/dev/null \
    || fail "impossibile creare il ConfigMap ${name} con la CA aziendale."
  log "ConfigMap ${name} aggiornato (CA di sistema e aziendale per i pod)."
}

# --- Preflight di rete ----------------------------------------------------------

# Bundle per curl nel preflight: CA di sistema più quella aziendale (creato
# da net_probe_url al primo uso), senza toccare il sistema prima che il
# preflight abbia detto di sì.
NET_PROBE_CACERT=""

# net_probe_url URL [UTENTE PASSWORD]: stampa il codice HTTP (000 = nessuna
# risposta). Una risposta qualunque, anche 401 o 404, vuol dire che la
# destinazione è raggiungibile: è il silenzio a segnalare il guasto. I test
# ridefiniscono questa funzione.
net_probe_url() {
  local url="$1" user="${2:-}" pass="${3:-}" cacert code
  local args=(-s -o /dev/null --max-time "${NET_PROBE_TIMEOUT}" -w '%{http_code}')
  if [ -n "${GITSTACK_CA_BUNDLE}" ] && [ -z "${NET_PROBE_CACERT}" ]; then
    local d
    d="$(mktemp -d)"
    TMP_DIRS+=("${d}")
    NET_PROBE_CACERT="${d}/bundle.pem"
    { cat /etc/ssl/certs/ca-certificates.crt 2>/dev/null || true; cat "${GITSTACK_CA_BUNDLE}"; } >"${NET_PROBE_CACERT}"
  fi
  cacert="${NET_PROBE_CACERT}"
  [ -z "${cacert}" ] || args+=(--cacert "${cacert}")
  if [ -n "${user}" ]; then
    code="$(printf 'user = %s\n' "$(net_quote "${user}:${pass}")" | curl "${args[@]}" -K - "${url}" 2>/dev/null)" || code="000"
  else
    code="$(curl "${args[@]}" "${url}" 2>/dev/null)" || code="000"
  fi
  printf '%s' "${code:-000}"
}

# net_probe_tcp HOST PORTA: 0 se la porta risponde. Ridefinita nei test.
net_probe_tcp() {
  if command -v timeout >/dev/null 2>&1; then
    timeout "${NET_PROBE_TIMEOUT}" bash -c "exec 3<>/dev/tcp/$1/$2" 2>/dev/null
  else
    (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null
  fi
}

NET_PROBE_BAD=()

# net_probe_check ETICHETTA URL TIPO [UTENTE PASSWORD]
# TIPO: "registry" (un endpoint /v2/: risponde 200, o 401 se chiede le
# credenziali) o "file" (un file da scaricare: 2xx o reindirizzamento).
# Altri codici, il 403 o il 503 di un proxy compresi, sono un guasto: un proxy
# che nega la destinazione risponde, ma non è la destinazione.
net_probe_check() {
  local label="$1" url="$2" kind="$3" user="${4:-}" pass="${5:-}" code via=""
  code="$(net_probe_url "${url}" "${user}" "${pass}")"
  case "${url}" in
    https://*) [ -z "$(net_https_proxy)" ] || via=" (via proxy $(net_redact "$(net_https_proxy)"))" ;;
    http://*) [ -z "${GITSTACK_HTTP_PROXY}" ] || via=" (via proxy $(net_redact "${GITSTACK_HTTP_PROXY}"))" ;;
  esac
  if [ "${code}" = "000" ]; then
    NET_PROBE_BAD+=("${label}: ${url} non risponde${via}")
    return 0
  fi
  case "${kind}:${code}" in
    registry:200)
      log "Rete: ${label} risponde (HTTP ${code})${via} OK"
      ;;
    registry:401)
      if [ -n "${user}" ]; then
        NET_PROBE_BAD+=("${label}: ${url} rifiuta le credenziali (HTTP 401)")
      else
        log "Rete: ${label} risponde (HTTP 401, chiede le credenziali)${via} OK"
      fi
      ;;
    file:2??|file:3??)
      log "Rete: ${label} risponde (HTTP ${code})${via} OK"
      ;;
    *)
      NET_PROBE_BAD+=("${label}: ${url} risponde HTTP ${code} (atteso $([ "${kind}" = registry ] && echo '200 o 401' || echo '2xx o 3xx'))${via}")
      ;;
  esac
}

# net_probe_proxy URL: la porta del proxy risponde.
net_probe_proxy() {
  local p="$1" h port
  h="$(net_authority "${p}")"
  case "${h}" in
    *:*) port="${h##*:}"; h="${h%:*}" ;;
    *) port=3128 ;;
  esac
  if net_probe_tcp "${h}" "${port}"; then
    log "Rete: proxy $(net_redact "${p}") raggiungibile OK"
  else
    NET_PROBE_BAD+=("proxy: $(net_redact "${p}") non raggiungibile (${h}:${port})")
  fi
}

# Prova a raggiungere, attraverso proxy e mirror, tutto ciò che l'installazione
# scarica; dice cosa non risponde. Con opzioni di rete un guasto ferma
# l'installazione (altrimenti fallirebbe a metà); senza, è solo un avviso.
preflight_network() {
  NET_PROBE_BAD=()
  [ -z "${GITSTACK_HTTP_PROXY}" ] || net_probe_proxy "${GITSTACK_HTTP_PROXY}"
  if [ -n "${GITSTACK_HTTPS_PROXY}" ] && [ "${GITSTACK_HTTPS_PROXY}" != "${GITSTACK_HTTP_PROXY}" ]; then
    net_probe_proxy "${GITSTACK_HTTPS_PROXY}"
  fi

  if [ -n "${GITSTACK_DOWNLOAD_MIRROR}" ]; then
    k3s_already_installed || net_probe_check "download di k3s (mirror)" "$(k3s_script_url)" file
    command -v helm >/dev/null 2>&1 || net_probe_check "download di Helm (mirror)" \
      "$(helm_base_url)/helm-${GITSTACK_HELM_VERSION}-linux-amd64.tar.gz.sha256sum" file
  else
    k3s_already_installed || net_probe_check "download di k3s" "$(k3s_script_url)" file
    k3s_already_installed || net_probe_check "binario di k3s" "https://github.com/k3s-io/k3s/releases" file
    command -v helm >/dev/null 2>&1 || net_probe_check "download di Helm" \
      "$(helm_base_url)/helm-${GITSTACK_HELM_VERSION}-linux-amd64.tar.gz.sha256sum" file
  fi
  if [ -n "${GITSTACK_REGISTRY_MIRROR}" ]; then
    net_probe_check "mirror delle immagini" "${GITSTACK_REGISTRY_MIRROR}/v2/" registry \
      "${GITSTACK_REGISTRY_USERNAME}" "${GITSTACK_REGISTRY_PASSWORD}"
  else
    local reg
    for reg in https://ghcr.io/v2/ https://registry-1.docker.io/v2/ https://registry.k8s.io/v2/; do
      net_probe_check "registry delle immagini" "${reg}" registry
    done
  fi

  if [ "${#NET_PROBE_BAD[@]}" -eq 0 ]; then
    return 0
  fi
  local b msg="non rispondono:"
  for b in "${NET_PROBE_BAD[@]}"; do
    msg="${msg}
  - ${b}"
  done
  if [ "${NETWORK_CONFIGURED}" -eq 1 ]; then
    fail "preflight di rete: ${msg}
Controlla proxy, mirror e CA (--http-proxy, --https-proxy, --no-proxy, --registry-mirror, --download-mirror, --ca-bundle) e rilancia."
  fi
  warn "preflight di rete: ${msg}
L'installazione deve raggiungerli: in una rete chiusa usa --http-proxy, --registry-mirror e --download-mirror (docs/operations.md, «Rete aziendale»)."
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

# Blocco del riepilogo sull'HTTPS, per modalità.
print_tls_summary() {
  case "${GITSTACK_TLS_MODE}" in
    internal)
      cat <<EOF
HTTPS: certificato firmato dalla CA interna di questa installazione (porta 80
reindirizzata a 443). Per fidarsene sui client:
  scarica:       http://${PUBLIC_HOST}/downloads/ca.crt
  impronta SHA-256 da verificare prima di fidarsi:
    $("${TLS_BIN_PATH}" fingerprint 2>/dev/null || echo "(sudo gitstack-tls fingerprint)")
  procedura per Linux, macOS, Windows, git e browser: docs/tls.md
La chiave della CA è solo qui: ${TLS_DIR}/ca.key (root, 0600). Il certificato del
server si rinnova da solo ogni notte quando mancano meno di 30 giorni (gitstack-tls-renew.timer).
EOF
      ;;
    custom)
      cat <<EOF
HTTPS: certificato del cliente (${TLS_DIR}/custom.crt). Per sostituirlo copia i nuovi
file lì e lancia: sudo gitstack-tls ensure  (il timer avvisa delle scadenze).
EOF
      ;;
    letsencrypt)
      cat <<EOF
HTTPS: Let's Encrypt (HTTP-01) per ${PUBLIC_HOST}. Traefik ottiene e rinnova il
certificato da solo; servono le porte 80 e 443 raggiungibili da internet e il
DNS di ${PUBLIC_HOST} che punta a questa macchina. Se il certificato non arriva:
  KUBECONFIG=${GITSTACK_KUBECONFIG} k3s kubectl -n kube-system logs deploy/traefik | grep -i acme
EOF
      ;;
    insecure)
      cat <<EOF
ATTENZIONE: installato con --insecure-http. Il traffico non è cifrato (password e
token in chiaro). Solo per prove locali: reinstalla senza il flag per attivare HTTPS.
EOF
      ;;
  esac
}

print_summary() {
  local base="${PUBLIC_SCHEME}://${PUBLIC_HOST}"
  local curl_opts=""
  if [ "${GITSTACK_TLS_MODE}" = "internal" ]; then
    curl_opts="--cacert ${TLS_DIR}/ca.crt "
  fi
  cat <<EOF

==> GitStack installato.

UI:            ${base}/
API (salute):  ${base}/api/healthz
Database:      $(if [ "${PG_EXTERNAL}" = "1" ]; then printf 'PostgreSQL %s esterno, %s:%s/%s' "${PG_VERSION:-?}" "${PG_HOST}" "${PG_PORT}" "${PG_DB}"; else printf 'Postgres interno (bundle)'; fi)

Verifica lo stato (con il comando di amministrazione: sudo gitstack status):
  KUBECONFIG=${GITSTACK_KUBECONFIG} k3s kubectl get pods -n ${GITSTACK_NAMESPACE}
  KUBECONFIG=${GITSTACK_KUBECONFIG} helm status ${GITSTACK_RELEASE_NAME} -n ${GITSTACK_NAMESPACE}
  curl -i ${curl_opts}${base}/api/healthz

Log di k3s:
  journalctl -u k3s -f

Utente admin: al primo avvio identity crea l'utente 'admin' con una password
iniziale generata e salvata in un Secret Kubernetes (non viene stampata qui).
Per leggerla:
  KUBECONFIG=${GITSTACK_KUBECONFIG} k3s kubectl -n ${GITSTACK_NAMESPACE} get secret ${GITSTACK_RELEASE_NAME}-identity-admin -o jsonpath='{.data.password}' | base64 -d; echo
Al primo login la password va cambiata prima di qualunque altra operazione.

$(print_tls_summary)

Rieseguire questo script in qualsiasi momento è sicuro: non reinstalla k3s
se è già presente e usa 'helm upgrade --install', che non rompe
un'installazione esistente (la password di Postgres e quella
dell'admin restano quelle già generate, vedi deploy/gitstack/README.md).
EOF
}

# --- Postgres esterno (N8, GIT-205) --------------------------------------
# Con --postgres-url GitStack non installa il Postgres interno: usa il
# database del cliente (PostgreSQL 15, 16 o 17, un database dedicato e un
# utente che può creare schemi e ruoli: lo prepara il DBA con
# deploy/postgres-prepare.sql). GitStack crea uno schema e un ruolo per
# servizio, come con il Postgres interno.

# Valorizzate da resolve_postgres. PG_PASSWORD_FILE è un file 0600 in una
# cartella temporanea, rimossa all'uscita: la password non passa mai dalla
# riga di comando di un processo (né qui né in kubectl).
PG_EXTERNAL=0
PG_HOST=""
PG_PORT=5432
PG_DB=""
PG_USER=""
PG_SSLMODE="require"
PG_VERSION=""
PG_MAJOR=""
PG_PASSWORD_FILE=""
PG_PASSWORD_SECRET=""
PG_CA_SECRET=""
POSTGRES_HELM_SETS=()

# Legge e valida --postgres-url, la password e la CA. Non si connette.
# Senza --postgres-url non fa nulla, tranne rifiutare una riesecuzione su
# un'installazione che usa già un database esterno: un `helm upgrade` senza i
# valori del database lo riporterebbe al Postgres interno, vuoto.
resolve_postgres() {
  local url="${GITSTACK_POSTGRES_URL}"
  if [ -z "${url}" ]; then
    if [ -n "${GITSTACK_POSTGRES_PASSWORD_FILE}${GITSTACK_POSTGRES_CA_FILE}" ]; then
      fail "--postgres-password-file e --postgres-ca-file servono insieme a --postgres-url."
    fi
    if [ "$(existing_config_value postgres external 2>/dev/null || true)" = "true" ]; then
      fail "questa installazione usa un Postgres esterno (${GITSTACK_CONFIG_FILE}): rilancia l'installer con gli stessi --postgres-url, --postgres-password-file e --postgres-ca-file, altrimenti passerebbe al Postgres interno e vuoto."
    fi
    return 0
  fi
  PG_EXTERNAL=1

  case "${url}" in
    postgres://*|postgresql://*) ;;
    *) fail "--postgres-url non valido: serve postgres://UTENTE@HOST:5432/DATABASE (schema postgres:// o postgresql://)." ;;
  esac
  local rest="${url#*://}" query="" path="" userinfo="" hostport="" pw_url=""
  case "${rest}" in
    *\?*) query="${rest#*\?}"; rest="${rest%%\?*}" ;;
  esac
  case "${rest}" in
    */*) path="${rest#*/}"; rest="${rest%%/*}" ;;
  esac
  hostport="${rest}"
  case "${rest}" in
    *@*) userinfo="${rest%@*}"; hostport="${rest##*@}" ;;
  esac
  PG_USER="${userinfo%%:*}"
  case "${userinfo}" in
    *:*) pw_url="${userinfo#*:}" ;;
  esac
  case "${hostport}" in
    \[*\]*) # IPv6: [::1]:5432
      PG_HOST="${hostport%%]*}"; PG_HOST="${PG_HOST#\[}"
      case "${hostport}" in *\]:*) PG_PORT="${hostport##*]:}" ;; esac
      ;;
    *:*) PG_HOST="${hostport%%:*}"; PG_PORT="${hostport##*:}" ;;
    *) PG_HOST="${hostport}" ;;
  esac
  PG_DB="${path}"

  local kv
  local -a params=()
  IFS='&' read -r -a params <<<"${query}" || true
  for kv in "${params[@]:-}"; do
    case "${kv}" in
      sslmode=*) PG_SSLMODE="${kv#sslmode=}" ;;
      '') ;;
      *) warn "parametro dell'URL del database ignorato: '${kv%%=*}' (si usano solo sslmode e, con --postgres-ca-file, la CA)." ;;
    esac
  done

  [ -n "${PG_HOST}" ] || fail "--postgres-url senza host: postgres://UTENTE@HOST:5432/DATABASE."
  [ -n "${PG_USER}" ] || fail "--postgres-url senza utente: postgres://UTENTE@HOST:5432/DATABASE."
  [ -n "${PG_DB}" ] || fail "--postgres-url senza nome del database: postgres://UTENTE@HOST:5432/DATABASE."
  case "${PG_PORT}" in
    ''|*[!0-9]*) fail "porta del database non valida in --postgres-url: '${PG_PORT}'." ;;
  esac
  case "${PG_USER}${PG_DB}" in
    *[!A-Za-z0-9_.-]*) fail "utente e database in --postgres-url devono usare solo lettere, cifre, '_', '.' e '-' (trovato: utente '${PG_USER}', database '${PG_DB}')." ;;
  esac
  case "${PG_HOST}" in
    *[!A-Za-z0-9_.:-]*) fail "host del database non valido in --postgres-url: '${PG_HOST}'." ;;
  esac
  case "${PG_SSLMODE}" in
    disable|allow|prefer|require|verify-ca|verify-full) ;;
    *) fail "sslmode '${PG_SSLMODE}' non valido (disable, allow, prefer, require, verify-ca, verify-full)." ;;
  esac
  if [ -n "${GITSTACK_POSTGRES_CA_FILE}" ]; then
    [ -f "${GITSTACK_POSTGRES_CA_FILE}" ] || fail "la CA del database non esiste: ${GITSTACK_POSTGRES_CA_FILE}"
    grep -q 'BEGIN CERTIFICATE' "${GITSTACK_POSTGRES_CA_FILE}" || fail "la CA del database non è un certificato PEM: ${GITSTACK_POSTGRES_CA_FILE}"
  fi

  # Password: file, poi variabile d'ambiente, poi (sconsigliato) l'URL.
  local workdir
  workdir="$(mktemp -d)"
  TMP_DIRS+=("${workdir}")
  chmod 0700 "${workdir}"
  PG_PASSWORD_FILE="${workdir}/password"
  local pw=""
  if [ -n "${GITSTACK_POSTGRES_PASSWORD_FILE}" ]; then
    [ -f "${GITSTACK_POSTGRES_PASSWORD_FILE}" ] || fail "il file della password del database non esiste: ${GITSTACK_POSTGRES_PASSWORD_FILE}"
    pw="$(tr -d '\r\n' <"${GITSTACK_POSTGRES_PASSWORD_FILE}")"
  elif [ -n "${GITSTACK_POSTGRES_PASSWORD:-}" ]; then
    pw="${GITSTACK_POSTGRES_PASSWORD}"
  elif [ -n "${pw_url}" ]; then
    pw="${pw_url}"
    warn "la password del database è nell'URL: resta nella cronologia della shell e in 'ps'. Meglio --postgres-password-file FILE o la variabile GITSTACK_POSTGRES_PASSWORD."
  fi
  [ -n "${pw}" ] || fail "manca la password del database: usa --postgres-password-file FILE, la variabile GITSTACK_POSTGRES_PASSWORD, oppure la password nell'URL."
  (umask 077 && printf '%s' "${pw}" >"${PG_PASSWORD_FILE}")
  # La password finisce in un URL di connessione dentro il cluster: i
  # caratteri riservati vanno evitati (lo stesso controllo lo fa
  # `gitstack postgres check`, con un messaggio più preciso).
  case "${pw}" in
    *[!A-Za-z0-9._~-]*) fail "la password del database contiene caratteri che GitStack non può mettere in un URL di connessione (ammessi: lettere, cifre, - . _ ~). Cambia la password dell'utente '${PG_USER}' con una di soli caratteri URL-safe." ;;
  esac

  PG_PASSWORD_SECRET="${GITSTACK_RELEASE_NAME}-postgres-external"
  PG_CA_SECRET=""
  if [ -n "${GITSTACK_POSTGRES_CA_FILE}" ]; then
    PG_CA_SECRET="${GITSTACK_RELEASE_NAME}-postgres-ca"
  fi
}

# URL senza password per `gitstack postgres check` (la password va dal file).
postgres_check_url() {
  local host="${PG_HOST}"
  case "${host}" in *:*) host="[${host}]" ;; esac
  printf 'postgres://%s@%s:%s/%s?sslmode=%s' "${PG_USER}" "${host}" "${PG_PORT}" "${PG_DB}" "${PG_SSLMODE}"
}

# Preflight del database (N8): versione 15-17, connessione e permessi, con il
# comando `gitstack postgres check` già scaricato e verificato da
# prepare_admin. Gira prima di toccare l'host. Valorizza PG_VERSION/PG_MAJOR.
preflight_postgres() {
  [ "${PG_EXTERNAL}" = "1" ] || return 0
  if [ "${GITSTACK_SKIP_PREFLIGHT}" = "1" ]; then
    warn "--skip-preflight attivo: il Postgres esterno non è stato controllato (versione, connessione, permessi)."
    PG_MAJOR="${GITSTACK_POSTGRES_MAJOR:-16}"
    return 0
  fi
  [ -n "${ADMIN_STAGED}" ] || fail "il preflight del Postgres esterno usa il comando 'gitstack', che non è disponibile (binario non pubblicato per questo tag): passa --admin-binary PERCORSO|URL (vedi admin/README.md)."
  log "Controllo del Postgres esterno ${PG_HOST}:${PG_PORT}/${PG_DB} (utente ${PG_USER}, sslmode ${PG_SSLMODE}) ..."
  local args=(postgres check --url "$(postgres_check_url)" --password-file "${PG_PASSWORD_FILE}")
  if [ -n "${GITSTACK_POSTGRES_CA_FILE}" ]; then
    args+=(--ca-file "${GITSTACK_POSTGRES_CA_FILE}")
  fi
  local out rc=0
  out="$("${ADMIN_STAGED}" "${args[@]}" 2>&1)" || rc=$?
  if [ "${rc}" -ne 0 ]; then
    printf '%s\n' "${out}" >&2
    fail "preflight del Postgres esterno non superato (versioni supportate: 15, 16, 17; serve un database dedicato e un utente che crea schemi e ruoli: deploy/postgres-prepare.sql). Nessuna modifica fatta all'host."
  fi
  printf '%s\n' "${out}"
  PG_VERSION="$(printf '%s\n' "${out}" | awk 'NR==1 { print $2 }')"
  PG_MAJOR="${PG_VERSION%%.*}"
  case "${PG_MAJOR}" in
    15|16|17) ;;
    *) fail "versione di PostgreSQL non riconosciuta dal preflight: '${PG_VERSION}'." ;;
  esac
}

# Secret con la password (e la CA) del database, fuori da Helm: il chart li
# referenzia con existingSecret e caSecret. kubectl legge i valori da file,
# mai dalla riga di comando.
create_postgres_secrets() {
  [ "${PG_EXTERNAL}" = "1" ] || return 0
  local kc=(env "KUBECONFIG=${GITSTACK_KUBECONFIG}" k3s kubectl)
  "${kc[@]}" create namespace "${GITSTACK_NAMESPACE}" --dry-run=client -o yaml | "${kc[@]}" apply -f - >/dev/null \
    || fail "non riesco a creare il namespace '${GITSTACK_NAMESPACE}'."
  log "Creo il Secret '${PG_PASSWORD_SECRET}' con la password del database ..."
  "${kc[@]}" -n "${GITSTACK_NAMESPACE}" create secret generic "${PG_PASSWORD_SECRET}" \
    --from-file=password="${PG_PASSWORD_FILE}" --dry-run=client -o yaml | "${kc[@]}" apply -f - >/dev/null \
    || fail "non riesco a creare il Secret '${PG_PASSWORD_SECRET}'."
  if [ -n "${PG_CA_SECRET}" ]; then
    log "Creo il Secret '${PG_CA_SECRET}' con la CA del database ..."
    "${kc[@]}" -n "${GITSTACK_NAMESPACE}" create secret generic "${PG_CA_SECRET}" \
      --from-file=ca.crt="${GITSTACK_POSTGRES_CA_FILE}" --dry-run=client -o yaml | "${kc[@]}" apply -f - >/dev/null \
      || fail "non riesco a creare il Secret '${PG_CA_SECRET}'."
  fi
}

# Valori Helm del database esterno, applicati prima dei --set dell'utente.
postgres_helm_sets() {
  POSTGRES_HELM_SETS=()
  [ "${PG_EXTERNAL}" = "1" ] || return 0
  POSTGRES_HELM_SETS=(
    "postgres.enabled=false"
    "postgres.external.host=${PG_HOST}"
    "postgres.external.port=${PG_PORT}"
    "postgres.external.database=${PG_DB}"
    "postgres.external.username=${PG_USER}"
    "postgres.external.existingSecret=${PG_PASSWORD_SECRET}"
    "postgres.external.sslMode=${PG_SSLMODE}"
    # Il client (pg_dump, psql) ha la versione principale del server: un
    # dump 17 non si ripristina su un 15.
    "postgres.external.client.image.tag=${PG_MAJOR:-16}-alpine"
  )
  if [ -n "${PG_CA_SECRET}" ]; then
    POSTGRES_HELM_SETS+=("postgres.external.caSecret=${PG_CA_SECRET}")
  fi
}

# Sezione postgres di config.yaml (vuota con il Postgres interno).
postgres_config_section() {
  [ "${PG_EXTERNAL}" = "1" ] || return 0
  cat <<EOF
postgres:
  external: true
  host: ${PG_HOST}
  port: ${PG_PORT}
  database: ${PG_DB}
  user: ${PG_USER}
  ssl_mode: ${PG_SSLMODE}
  version: "${PG_VERSION}"
  password_secret: ${PG_PASSWORD_SECRET}
EOF
  if [ -n "${PG_CA_SECRET}" ]; then
    printf '  ca_secret: %s\n' "${PG_CA_SECRET}"
  fi
}

# --- Main ------------------------------------------------------------------

main() {
  check_root "$@"
  validate_s3_options
  resolve_tls
  resolve_network
  resolve_postgres
  run_preflight
  if [ "${GITSTACK_SKIP_PREFLIGHT}" != "1" ]; then
    preflight_network
  fi
  apply_ca_trust

  resolve_chart_dir
  local chart_dir="${CHART_DIR}"
  log "Chart: ${chart_dir}"

  local image_tag
  image_tag="$(resolve_image_tag "${chart_dir}")"
  log "Tag immagine: ${image_tag}"

  # Il binario di gitstack si scarica e si verifica prima di toccare il
  # sistema: un checksum sbagliato ferma l'installazione senza effetti.
  prepare_admin "${image_tag}"
  # Postgres esterno: versione, connessione e permessi, ancora prima di
  # installare k3s.
  preflight_postgres

  apply_k3s_network
  install_k3s
  wait_for_k3s_ready
  wait_for_traefik_crd
  install_helm
  create_postgres_secrets
  ensure_corporate_ca_configmap
  setup_tls "${chart_dir}"
  install_gitstack "${chart_dir}" "${image_tag}"
  persist_chart "${chart_dir}"
  install_admin
  write_config "${image_tag}"
  enable_backup_timer

  print_summary
}

# GITSTACK_INSTALL_LIB=1: carica solo le funzioni (deploy/tests/preflight_test.sh).
if [ "${GITSTACK_INSTALL_LIB:-0}" != "1" ]; then
  main "$@"
fi
