#!/bin/sh
# Installa `gs`, la CLI di GitStack, da questa istanza (GIT-171, G6).
#
#   curl -fsSL https://<host>/install-gs.sh | sh
#
# Lo script e' servito dall'nginx dell'immagine web, che al momento del
# servizio sostituisce il segnaposto dell'indirizzo con quello con cui lo hai
# raggiunto: scarica da QUELL'istanza, senza internet. Fuori dall'istanza (o
# in prova) l'indirizzo si passa con GS_BASE_URL.
#
# Variabili:
#   GS_BASE_URL     indirizzo dell'istanza, es. https://git.example.com
#   GS_INSTALL_DIR  dove installare (default: ~/.local/bin, niente sudo)
#   GS_OS, GS_ARCH  forzano il rilevamento (linux|darwin, amd64|arm64)
#   GS_CA_SHA256    impronta SHA-256 della CA interna (come la stampa
#                   l'installer o `sudo gitstack-tls fingerprint`)
#   GS_INSECURE=1   il PRIMO scaricamento della CA avviene con `curl -k`
#                   (senza, e' in HTTP in chiaro: vedi README)
#   GS_TRUST_SYSTEM=1  installa la CA anche nell'archivio di sistema (sudo)
#
# Il checksum di gs viene da SHA256SUMS dell'istanza: protegge da download
# corrotti e da mirror sbagliati, non da un'istanza ostile (la fiducia e'
# nell'istanza e nel suo TLS).
set -eu

BASE_URL="${GS_BASE_URL:-__GS_BASE_URL__}"
INSTALL_DIR="${GS_INSTALL_DIR:-${HOME:-.}/.local/bin}"

say() { printf '%s\n' "$*" >&2; }
die() { say "install-gs: $*"; exit 1; }

# Il segnaposto non sostituito (script letto dal repo, non servito
# dall'istanza): il confronto usa un prefisso, cosi' non viene riscritto
# dal servizio.
case "$BASE_URL" in
  __GS_BASE_*) die "indirizzo dell'istanza sconosciuto: imposta GS_BASE_URL (es. GS_BASE_URL=https://git.example.com)" ;;
esac
BASE_URL="${BASE_URL%/}"

TMP_DIR=""
cleanup() {
  if [ -n "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT INT TERM
TMP_DIR="$(mktemp -d)"

# --- OS e architettura -------------------------------------------------------
os="${GS_OS:-}"
if [ -z "$os" ]; then
  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "sistema operativo non supportato: $(uname -s) (su Windows usa install-gs.ps1)" ;;
  esac
fi
arch="${GS_ARCH:-}"
if [ -z "$arch" ]; then
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "architettura non supportata: $(uname -m)" ;;
  esac
fi
file="gs_${os}_${arch}"

# --- Download ----------------------------------------------------------------
# CURL_OPTS: -k solo per il primo scaricamento della CA, poi --cacert.
CACERT=""
download() { # url destinazione
  if command -v curl >/dev/null 2>&1; then
    if [ -n "$CACERT" ]; then
      curl -fsSL --cacert "$CACERT" -o "$2" "$1"
    else
      curl -fsSL -o "$2" "$1"
    fi
  elif command -v wget >/dev/null 2>&1; then
    if [ -n "$CACERT" ]; then
      wget -q --ca-certificate="$CACERT" -O "$2" "$1"
    else
      wget -q -O "$2" "$1"
    fi
  else
    die "serve curl o wget"
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{ print $1 }'
  else
    die "serve sha256sum o shasum"
  fi
}

# --- CA interna (N5) -----------------------------------------------------------
# Con HTTPS e una CA non ancora fidata, il download fallisce: scarichiamo
# /downloads/ca.crt, ne verifichiamo l'impronta contro GS_CA_SHA256 (o, da
# terminale, con una conferma esplicita) e solo dopo la usiamo.
fingerprint_of() {
  openssl x509 -in "$1" -noout -fingerprint -sha256 | sed 's/^.*=//; s/://g' | tr 'A-F' 'a-f'
}

trust_ca() {
  ca_tmp="$TMP_DIR/ca.crt"
  case "$BASE_URL" in
    https://*)
      if [ "${GS_INSECURE:-0}" = 1 ]; then
        if command -v curl >/dev/null 2>&1; then
          curl -fsSLk -o "$ca_tmp" "$BASE_URL/downloads/ca.crt" || return 1
        else
          wget -q --no-check-certificate -O "$ca_tmp" "$BASE_URL/downloads/ca.crt" || return 1
        fi
      else
        # La CA si scarica anche in HTTP (serve proprio a fidarsi di HTTPS).
        download "http://${BASE_URL#https://}/downloads/ca.crt" "$ca_tmp" || return 1
      fi
      ;;
    *) return 1 ;;
  esac
  command -v openssl >/dev/null 2>&1 || die "serve openssl per verificare la CA"
  got="$(fingerprint_of "$ca_tmp")" || die "ca.crt scaricato non e' un certificato valido"
  want="$(printf '%s' "${GS_CA_SHA256:-}" | sed 's/://g' | tr 'A-F' 'a-f')"
  if [ -n "$want" ]; then
    [ "$got" = "$want" ] || die "impronta della CA diversa da GS_CA_SHA256 (attesa $want, trovata $got): non mi fido"
  elif ( : </dev/tty ) 2>/dev/null; then
    say "CA interna di $BASE_URL, impronta SHA-256: $got"
    printf 'Coincide con quella stampata dall installer? [s/N] ' >&2
    read -r answer </dev/tty || answer=""
    case "$answer" in s | S | y | Y) ;; *) die "CA non confermata" ;; esac
  else
    die "CA interna non ancora fidata: imposta GS_CA_SHA256 con l'impronta stampata dall'installer (sudo gitstack-tls fingerprint)"
  fi
  ca_dir="${XDG_CONFIG_HOME:-${HOME:-.}/.config}/gitstack"
  mkdir -p "$ca_dir"
  ca_host="${BASE_URL#https://}"
  ca_host="${ca_host%%/*}"
  CACERT="$ca_dir/ca-${ca_host}.crt"
  cp "$ca_tmp" "$CACERT"
  chmod 0644 "$CACERT"
  say "CA verificata e salvata in $CACERT"
  # git per questo solo host, senza toccare il sistema.
  if command -v git >/dev/null 2>&1; then
    git config --global "http.${BASE_URL}/.sslCAInfo" "$CACERT" || true
  fi
  if [ "${GS_TRUST_SYSTEM:-0}" = 1 ]; then
    if [ "$os" = darwin ]; then
      sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "$CACERT"
    elif [ -d /usr/local/share/ca-certificates ]; then
      sudo cp "$CACERT" /usr/local/share/ca-certificates/gitstack-ca.crt
      sudo update-ca-certificates
    elif [ -d /etc/pki/ca-trust/source/anchors ]; then
      sudo cp "$CACERT" /etc/pki/ca-trust/source/anchors/gitstack-ca.crt
      sudo update-ca-trust
    else
      say "archivio di sistema non riconosciuto: la CA resta solo in $CACERT"
    fi
  else
    say "CA fidata solo per questo installer e per git su $BASE_URL; per l'intero sistema rilancia con GS_TRUST_SYSTEM=1 (sudo)."
  fi
  return 0
}

if ! download "$BASE_URL/downloads/SHA256SUMS" "$TMP_DIR/SHA256SUMS" 2>/dev/null; then
  case "$BASE_URL" in
    https://*) trust_ca || die "non riesco a scaricare da $BASE_URL (certificato non fidato e CA non disponibile)" ;;
  esac
  download "$BASE_URL/downloads/SHA256SUMS" "$TMP_DIR/SHA256SUMS" || die "download di SHA256SUMS fallito da $BASE_URL"
fi

say "Scarico $file da $BASE_URL ..."
download "$BASE_URL/downloads/$file" "$TMP_DIR/$file" || die "download di $file fallito"

# --- Checksum --------------------------------------------------------------------
want_sum="$(awk -v f="$file" '$2 == f { print $1 }' "$TMP_DIR/SHA256SUMS")"
[ -n "$want_sum" ] || die "$file non e' in SHA256SUMS"
got_sum="$(sha256_of "$TMP_DIR/$file")"
if [ "$want_sum" != "$got_sum" ]; then
  die "checksum SHA-256 di $file diverso (atteso $want_sum, calcolato $got_sum): non installo"
fi

# --- Installazione -----------------------------------------------------------------
mkdir -p "$INSTALL_DIR"
chmod 0755 "$TMP_DIR/$file"
mv "$TMP_DIR/$file" "$INSTALL_DIR/gs"
say "gs installato in $INSTALL_DIR/gs"
case ":${PATH:-}:" in
  *":$INSTALL_DIR:"*) ;;
  *) say "Aggiungi $INSTALL_DIR al PATH, es.: export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac
