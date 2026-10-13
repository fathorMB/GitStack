#!/usr/bin/env bash
# Test della verifica delle firme (V5, GIT-209) di deploy/install.sh: binario,
# chart e immagini con cosign pinnato, identità e issuer fissati, bundle
# obbligatorio ed eccezione N9 esplicita. Carica solo le funzioni
# (GITSTACK_INSTALL_LIB=1) e usa un cosign finto: nessun root, nessuna rete.
# Uso: ./deploy/tests/v5_verify_test.sh
# Le variabili assegnate qui sono lette dalle funzioni di install.sh.
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

run() {
  OUT="$("$@" 2>&1)"
  RC=$?
}
ok() { PASS=$((PASS + 1)); }
bad() { FAILN=$((FAILN + 1)); printf 'FALLITO: %s\n--- output (rc=%s) ---\n%s\n---\n' "$1" "${RC}" "${OUT}"; }
expect_ok() { if [ "${RC}" -eq 0 ]; then ok; else bad "$1: atteso rc=0"; fi; }
expect_fail() { if [ "${RC}" -ne 0 ]; then ok; else bad "$1: atteso rc!=0"; fi; }
expect_has() { case "${OUT}" in *"$2"*) ok ;; *) bad "$1: manca '$2'" ;; esac; }
expect_lacks() { case "${OUT}" in *"$2"*) bad "$1: non doveva esserci '$2'" ;; *) ok ;; esac; }

# --- cosign finto -----------------------------------------------------------
# Controlla gli argomenti come il vero: identità col regexp, issuer, bundle e
# digest del file. Bundle finto: una riga "sha=<sha256 del firmato> id=<identità>".
FAKE="${WORK}/cosign"
cat >"${FAKE}" <<'EOF'
#!/usr/bin/env bash
echo "$*" >>"${FAKE_COSIGN_LOG:-/dev/null}"
[ -z "${DOCKER_CONFIG:-}" ] || [ ! -f "${DOCKER_CONFIG}/config.json" ] || cat "${DOCKER_CONFIG}/config.json" >>"${FAKE_COSIGN_LOG:-/dev/null}"
cmd="$1"; shift
re=""; iss=""; bundle=""; pos=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --certificate-identity-regexp) re="$2"; shift 2 ;;
    --certificate-oidc-issuer) iss="$2"; shift 2 ;;
    --bundle) bundle="$2"; shift 2 ;;
    --allow-insecure-registry) echo "insecure-registry" >>"${FAKE_COSIGN_LOG:-/dev/null}"; shift ;;
    --*) echo "flag non ammesso: $1" >&2; exit 1 ;;
    *) pos="$1"; shift ;;
  esac
done
[ -n "${re}" ] && [ -n "${iss}" ] && [ -n "${pos}" ] || { echo "identità/issuer obbligatori" >&2; exit 1; }
[ "${iss}" = "https://token.actions.githubusercontent.com" ] || { echo "issuer inatteso: ${iss}" >&2; exit 1; }
case "${cmd}" in
  verify-blob)
    [ -f "${bundle}" ] || { echo "bundle assente" >&2; exit 1; }
    want="$(sed -n 's/^sha=\([0-9a-f]*\) id=.*/\1/p' "${bundle}")"
    id="$(sed -n 's/^sha=[0-9a-f]* id=//p' "${bundle}")"
    got="$(sha256sum "${pos}" | awk '{ print $1 }')"
    [ "${want}" = "${got}" ] || { echo "invalid signature: il file non è quello firmato" >&2; exit 1; }
    ;;
  verify)
    id="$(awk -v i="${pos}" '$1 == i { print $2 }' "${FAKE_COSIGN_IMAGES}")"
    [ -n "${id}" ] || { echo "no signatures found for image ${pos}" >&2; exit 1; }
    ;;
  *) echo "sottocomando inatteso: ${cmd}" >&2; exit 1 ;;
esac
printf '%s' "${id}" | grep -Eq "${re}" || { echo "none of the expected identities matched: ${id}" >&2; exit 1; }
exit 0
EOF
chmod 0755 "${FAKE}"
FAKE_SHA="$(sha256sum "${FAKE}" | awk '{ print $1 }')"

COSIGN_PATH="${FAKE}"
GITSTACK_COSIGN_SHA256="${FAKE_SHA}"
GITSTACK_COSIGN_URL="${FAKE}"
GITSTACK_REPO="o/r"
GITSTACK_ALLOW_UNSIGNED=0
export FAKE_COSIGN_LOG="${WORK}/cosign.log"
export FAKE_COSIGN_IMAGES="${WORK}/images.txt"
VALID_ID="https://github.com/o/r/.github/workflows/ci.yml@refs/heads/main"
OTHER_ID="https://github.com/altro/r/.github/workflows/ci.yml@refs/heads/main"

# bundle_for FILE ID: scrive FILE.sigstore.json come se ID avesse firmato FILE.
bundle_for() {
  printf 'sha=%s id=%s\n' "$(sha256sum "$1" | awk '{ print $1 }')" "$2" >"$1.sigstore.json"
}

# --- Identità e cosign pinnato ----------------------------------------------
run v5_identity_regexp
expect_ok "regexp identità"
expect_has "regexp identità: workflow" 'ci\.yml@refs/'
expect_has "regexp identità: repo senza metacaratteri non scappati" 'github\.com/o/r/'
check_match() { printf '%s' "$1" | grep -Eq "$(v5_identity_regexp)"; }
if check_match "${VALID_ID}"; then ok; else RC=0; OUT="${VALID_ID}"; bad "identità valida accettata"; fi
if check_match "https://github.com/o/r/.github/workflows/ci.yml@refs/tags/v1.2.3"; then ok; else RC=0; OUT=tag; bad "tag v* accettato"; fi
if check_match "${OTHER_ID}"; then RC=0; OUT="${OTHER_ID}"; bad "altro repo rifiutato"; else ok; fi
if check_match "https://github.com/o/r/.github/workflows/altro.yml@refs/heads/main"; then RC=0; OUT=altro; bad "altro workflow rifiutato"; else ok; fi
if check_match "https://github.com/o/r/.github/workflows/ci.yml@refs/heads/feature"; then RC=0; OUT=ramo; bad "ramo non main rifiutato"; else ok; fi

# cosign con checksum sbagliato: non si installa.
COSIGN_PATH="${WORK}/nuovo/cosign"
GITSTACK_COSIGN_SHA256="$(printf '0%.0s' $(seq 1 64))"
COSIGN_READY=0
run ensure_cosign
expect_fail "cosign con checksum sbagliato"
expect_has "cosign con checksum sbagliato: messaggio" "checksum SHA-256 pinnato"
if [ -e "${WORK}/nuovo/cosign" ]; then RC=0; OUT="installato"; bad "cosign non va installato se il checksum non torna"; else ok; fi

# cosign con checksum giusto: installato in COSIGN_PATH.
GITSTACK_COSIGN_SHA256="${FAKE_SHA}"
COSIGN_READY=0
run ensure_cosign
expect_ok "cosign con checksum giusto"
if [ -x "${WORK}/nuovo/cosign" ]; then ok; else bad "cosign installato"; fi
COSIGN_PATH="${FAKE}"
COSIGN_READY=0

# --- Binario: prepare_admin --------------------------------------------------
BIN="${WORK}/gitstack"
printf 'BINARIO-NUOVO' >"${BIN}"
GITSTACK_ADMIN_BINARY="${BIN}"
GITSTACK_ADMIN_SHA256="$(sha256sum "${BIN}" | awk '{ print $1 }')"
GITSTACK_ADMIN_REQUIRED=1
rm -f "${BIN}.sigstore.json"

run prepare_admin sha-x
expect_fail "binario senza bundle"
expect_has "binario senza bundle: messaggio" "non risulta firmato"
expect_lacks "binario senza bundle: non installato" "verificato (SHA-256"
expect_has "binario senza bundle: indica l'eccezione" "--allow-unsigned-mirrors"

bundle_for "${BIN}" "${OTHER_ID}"
run prepare_admin sha-x
expect_fail "binario firmato da un'altra identità"
expect_has "altra identità: messaggio" "verifica della firma cosign fallita"
expect_lacks "altra identità: non installato" "verificato (SHA-256"

bundle_for "${BIN}" "${VALID_ID}"
printf 'BINARIO-NUOVO-ALTERATO' >"${BIN}"
GITSTACK_ADMIN_SHA256="$(sha256sum "${BIN}" | awk '{ print $1 }')"
run prepare_admin sha-x
expect_fail "binario alterato (checksum aggiornato, firma no)"
expect_has "binario alterato: messaggio" "verifica della firma cosign fallita"

printf 'BINARIO-NUOVO' >"${BIN}"
bundle_for "${BIN}" "${VALID_ID}"
GITSTACK_ADMIN_SHA256="$(sha256sum "${BIN}" | awk '{ print $1 }')"
: >"${FAKE_COSIGN_LOG}"
run prepare_admin sha-x
expect_ok "binario firmato dal workflow"
expect_has "binario firmato: verificato" "Firma cosign verificata: il binario di gitstack"
if grep -q "certificate-identity-regexp" "${FAKE_COSIGN_LOG}" && ! grep -q "insecure" "${FAKE_COSIGN_LOG}"; then ok; else RC=0; OUT="$(cat "${FAKE_COSIGN_LOG}")"; bad "cosign chiamato con identità e senza flag insicuri"; fi

# N9: accettato, con avviso e senza chiamare cosign.
rm -f "${BIN}.sigstore.json"
GITSTACK_ALLOW_UNSIGNED=1
UNSIGNED_WARNED=0
: >"${FAKE_COSIGN_LOG}"
run prepare_admin sha-x
expect_ok "N9: binario non firmato accettato"
expect_has "N9: avviso visibile" "ECCEZIONE N9 ATTIVA"
if [ -s "${FAKE_COSIGN_LOG}" ]; then RC=0; OUT="$(cat "${FAKE_COSIGN_LOG}")"; bad "N9: cosign non va chiamato"; else ok; fi
GITSTACK_ALLOW_UNSIGNED=0
UNSIGNED_WARNED=0

# --- Chart: verify_downloaded_chart -------------------------------------------
mkdir -p "${WORK}/rel/sha-abc" "${WORK}/src/deploy/gitstack" "${WORK}/chartpkg/gitstack/templates"
printf 'apiVersion: v2\nname: gitstack\nversion: 0.1.0\n' >"${WORK}/src/deploy/gitstack/Chart.yaml"
printf 'apiVersion: v2\nname: gitstack\nversion: 0.1.0\n' >"${WORK}/chartpkg/gitstack/Chart.yaml"
printf 'x' >"${WORK}/chartpkg/gitstack/templates/a.yaml"
tar -czf "${WORK}/rel/sha-abc/gitstack-0.1.0.tgz" -C "${WORK}/chartpkg" gitstack
GITSTACK_RELEASE_URL="${WORK}/rel"
chart_case() {
  CHART_DIR="${WORK}/src/deploy/gitstack"
  mkdir -p "${CHART_DIR}"
  printf 'apiVersion: v2\nname: gitstack\nversion: 0.1.0\n' >"${CHART_DIR}/Chart.yaml"
  rm -rf "${CHART_DIR}/templates"
  verify_downloaded_chart sha-abc "${WORK}/w"
  # il chart in uso deve essere quello firmato (ha templates/a.yaml)
  [ -f "${CHART_DIR}/templates/a.yaml" ]
}
mkdir -p "${WORK}/w"

rm -f "${WORK}/rel/sha-abc/gitstack-0.1.0.tgz.sigstore.json"
run chart_case
expect_fail "chart senza bundle"
expect_has "chart senza bundle: messaggio" "bundle cosign del chart non trovato"

bundle_for "${WORK}/rel/sha-abc/gitstack-0.1.0.tgz" "${OTHER_ID}"
run chart_case
expect_fail "chart firmato da un'altra identità"
expect_has "chart altra identità: messaggio" "verifica della firma cosign fallita"

printf 'sha=%s id=%s\n' "$(printf 'altro' | sha256sum | awk '{ print $1 }')" "${VALID_ID}" >"${WORK}/rel/sha-abc/gitstack-0.1.0.tgz.sigstore.json"
run chart_case
expect_fail "chart alterato"

bundle_for "${WORK}/rel/sha-abc/gitstack-0.1.0.tgz" "${VALID_ID}"
run chart_case
expect_ok "chart firmato dal workflow"

GITSTACK_ALLOW_UNSIGNED=1
UNSIGNED_WARNED=0
rm -f "${WORK}/rel/sha-abc/gitstack-0.1.0.tgz.sigstore.json"
run verify_downloaded_chart sha-abc "${WORK}/w"
expect_ok "N9: chart non firmato accettato"
expect_has "N9: avviso" "ECCEZIONE N9 ATTIVA"
GITSTACK_ALLOW_UNSIGNED=0
UNSIGNED_WARNED=0

# --- Immagini: verify_deployed_images -----------------------------------------
mkdir -p "${WORK}/bin"
cat >"${WORK}/bin/helm" <<'EOF'
#!/usr/bin/env bash
[ "$1" = "template" ] || exit 1
cat <<'YAML'
      containers:
        - image: "ghcr.io/o/gitstack-core:sha-abc"
        - image: ghcr.io/o/gitstack-web:sha-abc
        - image: postgres:16
YAML
EOF
chmod 0755 "${WORK}/bin/helm"
PATH_ORIG="${PATH}"
PATH="${WORK}/bin:${PATH}"
TLS_HELM_SETS=()
extra_helm_values=()
extra_helm_set=()
postgres_helm_sets() { POSTGRES_HELM_SETS=(); }

printf 'ghcr.io/o/gitstack-core:sha-abc %s\n' "${VALID_ID}" >"${FAKE_COSIGN_IMAGES}"
run verify_deployed_images "${WORK}/src/deploy/gitstack" sha-abc
expect_fail "immagine non firmata"
expect_has "immagine non firmata: messaggio" "gitstack-web"
expect_has "immagine non firmata: motivo" "no signatures found"

printf 'ghcr.io/o/gitstack-core:sha-abc %s\nghcr.io/o/gitstack-web:sha-abc %s\n' "${VALID_ID}" "${OTHER_ID}" >"${FAKE_COSIGN_IMAGES}"
run verify_deployed_images "${WORK}/src/deploy/gitstack" sha-abc
expect_fail "immagine firmata da un'altra identità"

printf 'ghcr.io/o/gitstack-core:sha-abc %s\nghcr.io/o/gitstack-web:sha-abc %s\n' "${VALID_ID}" "${VALID_ID}" >"${FAKE_COSIGN_IMAGES}"
: >"${FAKE_COSIGN_LOG}"
run verify_deployed_images "${WORK}/src/deploy/gitstack" sha-abc
expect_ok "immagini firmate dal workflow"
expect_has "immagini firmate: conteggio" "Immagini verificate: 2"
if grep -q postgres "${FAKE_COSIGN_LOG}"; then RC=0; OUT="$(cat "${FAKE_COSIGN_LOG}")"; bad "le immagini di terzi non si verificano"; else ok; fi

GITSTACK_ALLOW_UNSIGNED=1
UNSIGNED_WARNED=0
: >"${FAKE_COSIGN_IMAGES}"
run verify_deployed_images "${WORK}/src/deploy/gitstack" sha-abc
expect_ok "N9: immagini non firmate accettate"
expect_has "N9: avviso immagini" "ECCEZIONE N9 ATTIVA"
GITSTACK_ALLOW_UNSIGNED=0
UNSIGNED_WARNED=0
PATH="${PATH_ORIG}"

# --- Mirror dei download (--download-mirror, N9a) --------------------------------
# Ciò che arriva dal mirror si verifica come ciò che arriva da GitHub.
MIRROR="${WORK}/mirror"
mkdir -p "${MIRROR}/gitstack/sha-abc" "${MIRROR}/cosign/${COSIGN_VERSION}"
cp "${WORK}/rel/sha-abc/gitstack-0.1.0.tgz" "${MIRROR}/gitstack/sha-abc/gitstack-0.1.0.tgz"
printf 'BINARIO-DAL-MIRROR' >"${MIRROR}/gitstack/sha-abc/gitstack-linux-amd64"
sha256sum "${MIRROR}/gitstack/sha-abc/gitstack-linux-amd64" | awk '{ print $1 }' >"${MIRROR}/gitstack/sha-abc/gitstack-linux-amd64.sha256"
cp "${FAKE}" "${MIRROR}/cosign/${COSIGN_VERSION}/cosign-linux-amd64"
GITSTACK_DOWNLOAD_MIRROR="${MIRROR}"
GITSTACK_COSIGN_URL=""
GITSTACK_ADMIN_BINARY=""
GITSTACK_ADMIN_SHA256=""
GITSTACK_ADMIN_REQUIRED=1
MBIN="${MIRROR}/gitstack/sha-abc/gitstack-linux-amd64"
MTGZ="${MIRROR}/gitstack/sha-abc/gitstack-0.1.0.tgz"

run release_base sha-abc
expect_has "mirror: release_base punta al mirror" "${MIRROR}/gitstack/sha-abc"
run admin_default_url sha-abc
expect_has "mirror: binario dal mirror" "${MIRROR}/gitstack/sha-abc/gitstack-linux-amd64"
run cosign_url
expect_has "mirror: cosign dal mirror" "${MIRROR}/cosign/${COSIGN_VERSION}/cosign-linux-amd64"

# cosign dal mirror: checksum pinnato verificato anche lì.
COSIGN_PATH="${WORK}/dalmirror/cosign"
COSIGN_READY=0
run ensure_cosign
expect_ok "mirror: cosign dal mirror con checksum giusto"
GITSTACK_COSIGN_SHA256="$(printf '1%.0s' $(seq 1 64))"
COSIGN_PATH="${WORK}/dalmirror2/cosign"
COSIGN_READY=0
run ensure_cosign
expect_fail "mirror: cosign dal mirror con checksum sbagliato"
GITSTACK_COSIGN_SHA256="${FAKE_SHA}"
COSIGN_PATH="${FAKE}"
COSIGN_READY=0

# binario dal mirror
rm -f "${MBIN}.sigstore.json"
run prepare_admin sha-abc
expect_fail "mirror: binario senza bundle"
expect_has "mirror: binario senza bundle, messaggio" "non risulta firmato"
bundle_for "${MBIN}" "${OTHER_ID}"
run prepare_admin sha-abc
expect_fail "mirror: binario firmato da un'altra identità"
printf 'ALTERATO' >"${MBIN}.alt" && bundle_for "${MBIN}.alt" "${VALID_ID}"
cp "${MBIN}.alt.sigstore.json" "${MBIN}.sigstore.json"
run prepare_admin sha-abc
expect_fail "mirror: binario alterato (bundle di un altro contenuto)"
bundle_for "${MBIN}" "${VALID_ID}"
run prepare_admin sha-abc
expect_ok "mirror: binario firmato dal workflow"
expect_has "mirror: binario verificato" "Firma cosign verificata: il binario di gitstack"
rm -f "${MBIN}.sigstore.json"
GITSTACK_ALLOW_UNSIGNED=1
UNSIGNED_WARNED=0
run prepare_admin sha-abc
expect_ok "mirror N9: binario non firmato accettato"
expect_has "mirror N9: avviso" "ECCEZIONE N9 ATTIVA"
GITSTACK_ALLOW_UNSIGNED=0
UNSIGNED_WARNED=0

# chart dal mirror
mirror_chart_case() {
  CHART_DIR="${WORK}/src/deploy/gitstack"
  mkdir -p "${CHART_DIR}"
  printf 'apiVersion: v2\nname: gitstack\nversion: 0.1.0\n' >"${CHART_DIR}/Chart.yaml"
  rm -rf "${CHART_DIR}/templates"
  verify_downloaded_chart sha-abc "${WORK}/w"
  [ -f "${CHART_DIR}/templates/a.yaml" ]
}
run mirror_chart_case
expect_fail "mirror: chart senza bundle"
expect_has "mirror: chart, URL del mirror" "${MIRROR}/gitstack/sha-abc/gitstack-0.1.0.tgz"
bundle_for "${MTGZ}" "${OTHER_ID}"
run mirror_chart_case
expect_fail "mirror: chart firmato da un'altra identità"
bundle_for "${MTGZ}" "${VALID_ID}"
run mirror_chart_case
expect_ok "mirror: chart firmato dal workflow"

# immagini dal registry mirror
GITSTACK_DOWNLOAD_MIRROR=""
GITSTACK_REGISTRY_MIRROR="http://registry.corp:5000"
GITSTACK_REGISTRY_USERNAME="puller"
GITSTACK_REGISTRY_PASSWORD="segreto"
PATH="${WORK}/bin:${PATH}"
printf 'registry.corp:5000/o/gitstack-core:sha-abc %s\nregistry.corp:5000/o/gitstack-web:sha-abc %s\n' "${VALID_ID}" "${VALID_ID}" >"${FAKE_COSIGN_IMAGES}"
: >"${FAKE_COSIGN_LOG}"
run verify_deployed_images "${WORK}/src/deploy/gitstack" sha-abc
expect_ok "mirror immagini: firmate sul mirror"
if grep -q "registry.corp:5000/o/gitstack-core" "${FAKE_COSIGN_LOG}" && grep -q "^insecure-registry" "${FAKE_COSIGN_LOG}"; then ok; else RC=0; OUT="$(cat "${FAKE_COSIGN_LOG}")"; bad "mirror immagini: verifica sul mirror con --allow-insecure-registry"; fi
if grep -q "ghcr.io" "${FAKE_COSIGN_LOG}"; then RC=0; OUT="$(cat "${FAKE_COSIGN_LOG}")"; bad "mirror immagini: non si va sul registry originale"; else ok; fi
if grep -q '"registry.corp:5000"' "${FAKE_COSIGN_LOG}"; then ok; else RC=0; OUT="$(cat "${FAKE_COSIGN_LOG}")"; bad "mirror immagini: credenziali nel config docker temporaneo"; fi
if grep -q "segreto" "${FAKE_COSIGN_LOG}"; then RC=0; OUT="password negli argomenti"; bad "mirror immagini: password mai negli argomenti"; else ok; fi
printf 'registry.corp:5000/o/gitstack-core:sha-abc %s\n' "${VALID_ID}" >"${FAKE_COSIGN_IMAGES}"
run verify_deployed_images "${WORK}/src/deploy/gitstack" sha-abc
expect_fail "mirror immagini: immagine senza firma sul mirror"
expect_has "mirror immagini: messaggio con l'immagine del mirror" "registry.corp:5000/o/gitstack-web"
GITSTACK_ALLOW_UNSIGNED=1
UNSIGNED_WARNED=0
run verify_deployed_images "${WORK}/src/deploy/gitstack" sha-abc
expect_ok "mirror N9: immagini senza firma accettate"
GITSTACK_ALLOW_UNSIGNED=0
UNSIGNED_WARNED=0
GITSTACK_REGISTRY_MIRROR=""
GITSTACK_REGISTRY_USERNAME=""
PATH="${PATH_ORIG}"

# --- Config e riepilogo --------------------------------------------------------
run v5_config_section
expect_has "config: sezione v5" "v5:"
expect_has "config: cosign pinnato" "cosign_path: ${COSIGN_PATH}"
expect_has "config: eccezione assente" "allow_unsigned: false"
GITSTACK_ALLOW_UNSIGNED=1
run v5_config_section
expect_has "config: eccezione N9 salvata" "allow_unsigned: true"
run print_v5_summary
expect_has "riepilogo: avviso N9" "eccezione N9"
GITSTACK_ALLOW_UNSIGNED=0

# --- Rilascio con versione (--version vX.Y.Z, GIT-222) ------------------------
# La release ha il nome del tag: binario, chart gitstack-X.Y.Z.tgz e bundle
# stanno lì, e le firme si verificano come per sha-<commit>.
GITSTACK_DOWNLOAD_MIRROR=""
GITSTACK_REGISTRY_MIRROR=""
GITSTACK_ALLOW_UNSIGNED=0
UNSIGNED_WARNED=0
GITSTACK_IMAGE_TAG=""
GITSTACK_REF="main"

for good in v1.2.3 v0.0.0-test.1 v1.0.0-rc.1 v10.20.30; do
  GITSTACK_VERSION="${good}"
  run apply_version
  expect_ok "apply_version accetta ${good}"
done
for bad_v in 1.2.3 v1 v1.2 v1.2.3+build sha-abc main v01.2.3 'v1.2.3;x'; do
  GITSTACK_VERSION="${bad_v}"
  run apply_version
  expect_fail "apply_version rifiuta ${bad_v}"
  expect_has "apply_version ${bad_v}: messaggio" "formato non valido"
done

GITSTACK_VERSION="v1.2.3"
GITSTACK_IMAGE_TAG=""
GITSTACK_REF="main"
apply_version
if [ "${GITSTACK_IMAGE_TAG}" = "v1.2.3" ] && [ "${GITSTACK_REF}" = "v1.2.3" ]; then ok; else RC=0; OUT="tag=${GITSTACK_IMAGE_TAG} ref=${GITSTACK_REF}"; bad "--version fissa tag immagine e ref"; fi

GITSTACK_IMAGE_TAG="sha-abc"
run apply_version
expect_fail "--version con --image-tag diverso"
expect_has "--version con --image-tag: messaggio" "non coincidono"
GITSTACK_IMAGE_TAG="v1.2.3"
run apply_version
expect_ok "--version con lo stesso --image-tag"

# Senza --version non cambia niente (tag sha, ref di default).
GITSTACK_VERSION=""
GITSTACK_IMAGE_TAG=""
GITSTACK_REF="main"
apply_version
if [ -z "${GITSTACK_IMAGE_TAG}" ] && [ "${GITSTACK_REF}" = "main" ]; then ok; else RC=0; OUT="tag=${GITSTACK_IMAGE_TAG}"; bad "senza --version nessun effetto"; fi

# Il parsing dei flag: --version arriva in GITSTACK_VERSION.
# shellcheck disable=SC2016
if grep -q -- '--version)' "${HERE}/../install.sh" && grep -q 'GITSTACK_VERSION="\$2"' "${HERE}/../install.sh"; then ok; else RC=0; OUT="manca"; bad "install.sh parsa --version"; fi

GITSTACK_RELEASE_URL="${WORK}/rel"
GITSTACK_VERSION="v1.2.3"
GITSTACK_IMAGE_TAG="v1.2.3"
mkdir -p "${WORK}/rel/v1.2.3" "${WORK}/chartver/gitstack/templates" "${WORK}/w2"
printf 'apiVersion: v2\nname: gitstack\nversion: 1.2.3\nappVersion: "1.2.3"\n' >"${WORK}/chartver/gitstack/Chart.yaml"
printf 'x' >"${WORK}/chartver/gitstack/templates/a.yaml"
tar -czf "${WORK}/rel/v1.2.3/gitstack-1.2.3.tgz" -C "${WORK}/chartver" gitstack
VBIN="${WORK}/rel/v1.2.3/gitstack-linux-amd64"
printf 'BINARIO-V1.2.3' >"${VBIN}"
sha256sum "${VBIN}" | awk '{ print $1 }' >"${VBIN}.sha256"
rm -f "${VBIN}.sigstore.json"

# Il Chart.yaml del sorgente dice 0.1.0: la release usa gitstack-1.2.3.tgz.
version_chart_case() {
  CHART_DIR="${WORK}/src/deploy/gitstack"
  mkdir -p "${CHART_DIR}"
  printf 'apiVersion: v2\nname: gitstack\nversion: 0.1.0\n' >"${CHART_DIR}/Chart.yaml"
  rm -rf "${CHART_DIR}/templates"
  verify_downloaded_chart "${GITSTACK_VERSION}" "${WORK}/w2"
  [ -f "${CHART_DIR}/templates/a.yaml" ]
}
run version_chart_case
expect_fail "versione: chart senza bundle"
expect_has "versione: chart senza bundle, URL della release" "${WORK}/rel/v1.2.3/gitstack-1.2.3.tgz"
bundle_for "${WORK}/rel/v1.2.3/gitstack-1.2.3.tgz" "${OTHER_ID}"
run version_chart_case
expect_fail "versione: chart firmato da un'altra identità"
expect_has "versione: chart altra identità: messaggio" "verifica della firma cosign fallita"
bundle_for "${WORK}/rel/v1.2.3/gitstack-1.2.3.tgz" "https://github.com/o/r/.github/workflows/ci.yml@refs/tags/v1.2.3"
: >"${FAKE_COSIGN_LOG}"
run version_chart_case
expect_ok "versione: chart firmato dal workflow su tag"
expect_has "versione: chart verificato" "Firma cosign verificata: il chart gitstack-1.2.3.tgz"
if grep -q "certificate-identity-regexp" "${FAKE_COSIGN_LOG}"; then ok; else RC=0; OUT="$(cat "${FAKE_COSIGN_LOG}")"; bad "versione: cosign chiamato con identità"; fi

# Chart firmato ma di un'altra versione (appVersion diversa): rifiutato.
printf 'apiVersion: v2\nname: gitstack\nversion: 1.2.3\nappVersion: "0.9.0"\n' >"${WORK}/chartver/gitstack/Chart.yaml"
tar -czf "${WORK}/rel/v1.2.3/gitstack-1.2.3.tgz" -C "${WORK}/chartver" gitstack
bundle_for "${WORK}/rel/v1.2.3/gitstack-1.2.3.tgz" "${VALID_ID}"
run version_chart_case
expect_fail "versione: chart con appVersion diversa"
expect_has "versione: appVersion diversa: messaggio" "non è il chart di questa versione"

# Binario della release: URL dal nome del tag, firma obbligatoria.
GITSTACK_ADMIN_BINARY=""
GITSTACK_ADMIN_SHA256=""
GITSTACK_ADMIN_REQUIRED=1
run prepare_admin v1.2.3
expect_fail "versione: binario senza bundle"
expect_has "versione: binario senza bundle, messaggio" "non risulta firmato"
bundle_for "${VBIN}" "${VALID_ID}"
run prepare_admin v1.2.3
expect_ok "versione: binario firmato dal workflow"
expect_has "versione: binario verificato" "Firma cosign verificata: il binario di gitstack"
bundle_for "${VBIN}" "${OTHER_ID}"
run prepare_admin v1.2.3
expect_fail "versione: binario firmato da un'altra identità"

GITSTACK_VERSION=""
GITSTACK_IMAGE_TAG=""

# --- Niente scorciatoie nello script -------------------------------------------
if grep -q -- "insecure-ignore-tlog" "${HERE}/../install.sh"; then RC=0; OUT="trovato"; bad "install.sh non deve usare --insecure-ignore-tlog"; else ok; fi

printf '\nv5_verify_test: %d ok, %d falliti\n' "${PASS}" "${FAILN}"
[ "${FAILN}" -eq 0 ]
