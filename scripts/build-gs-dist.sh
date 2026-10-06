#!/usr/bin/env bash
# Costruisce la distribuzione di `gs` (GIT-171, G6): i 6 binari
# (linux, darwin, windows x amd64, arm64), SHA256SUMS, il pacchetto delle
# skills (gs-skills.zip) e index.json. Lo usano sia web/Dockerfile (le
# istanze servono i file su /downloads, senza internet) sia il job CI
# `gs-binaries` (copia nella release sha-<commit>): un solo posto, quindi
# l'immagine e la release hanno gli stessi byte.
#
# Uso: scripts/build-gs-dist.sh <cartella-di-uscita> <versione>
#   versione: es. sha-<commit> (stesso tag delle immagini); "dev" in locale.
#
# Variabili: GS_DIST_PLATFORMS ("linux/amd64 linux/arm64 ..." per ridurre
# l'elenco in prova), GS_SKILLS_DIR (default skills/).
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "uso: $0 <cartella-di-uscita> <versione>" >&2
  exit 2
fi
out=$1
version=$2

repo=$(cd "$(dirname "$0")/.." && pwd)
skills_dir=${GS_SKILLS_DIR:-${repo}/skills}
platforms=${GS_DIST_PLATFORMS:-"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"}

mkdir -p "${out}"
out=$(cd "${out}" && pwd)

# 1. Binari. CGO spento e -trimpath: stessi byte a parità di sorgente.
for p in ${platforms}; do
  os=${p%/*}
  arch=${p#*/}
  file="gs_${os}_${arch}"
  [ "${os}" = windows ] && file="${file}.exe"
  (cd "${repo}/cli" && CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" \
    go build -trimpath -ldflags "-s -w -X main.version=${version}" \
    -o "${out}/${file}" ./cmd/gs)
done

# 2. Skills: archivio della cartella skills/ (basta anche la sola LICENSE).
if [ ! -d "${skills_dir}" ]; then
  echo "cartella delle skills assente: ${skills_dir}" >&2
  exit 1
fi
rm -f "${out}/gs-skills.zip"
(cd "${skills_dir}" && zip -q -r -X "${out}/gs-skills.zip" .)

# 3. SHA256SUMS (formato sha256sum: "<hash>  <file>"), ordinato.
(cd "${out}" && sha256sum gs_* gs-skills.zip > SHA256SUMS)

# 4. index.json (formato fissato con la UI di GIT-172). ca_cert resta null:
#    lo riscrive l'entrypoint dell'immagine web se l'istanza ha una CA interna.
sum_of() { awk -v f="$1" '$2 == f { print $1 }' "${out}/SHA256SUMS"; }
size_of() { wc -c < "${out}/$1" | tr -d ' '; }

{
  printf '{"version":"%s","binaries":[' "${version}"
  first=1
  for p in ${platforms}; do
    os=${p%/*}
    arch=${p#*/}
    file="gs_${os}_${arch}"
    [ "${os}" = windows ] && file="${file}.exe"
    [ "${first}" = 1 ] || printf ','
    first=0
    printf '{"os":"%s","arch":"%s","file":"%s","url":"/downloads/%s","sha256":"%s","size":%s}' \
      "${os}" "${arch}" "${file}" "${file}" "$(sum_of "${file}")" "$(size_of "${file}")"
  done
  printf '],"checksums":"/downloads/SHA256SUMS",'
  printf '"skills":{"file":"gs-skills.zip","url":"/downloads/gs-skills.zip","sha256":"%s"},' "$(sum_of gs-skills.zip)"
  printf '"install":{"sh":"/install-gs.sh","ps1":"/install-gs.ps1"},"ca_cert":null}\n'
} > "${out}/index.json"

echo "distribuzione di gs ${version} in ${out}:"
cat "${out}/SHA256SUMS"
