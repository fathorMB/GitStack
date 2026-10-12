#!/usr/bin/env bash
# Test di scripts/release-changelog.sh (GIT-222): repository git temporaneo con
# due rilasci e un tag di prova, nessuna rete.
# Uso: ./deploy/tests/release_changelog_test.sh
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="${HERE}/../../scripts/release-changelog.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

PASS=0
FAILN=0
ok() { PASS=$((PASS + 1)); }
bad() { FAILN=$((FAILN + 1)); printf 'FALLITO: %s\n' "$1"; }
has() { case "$2" in *"$3"*) ok ;; *) bad "$1: manca '$3' in:
$2" ;; esac; }
lacks() { case "$2" in *"$3"*) bad "$1: non doveva esserci '$3'" ;; *) ok ;; esac; }

cd "${WORK}" || exit 1
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
git init -q -b main .
git config user.email t@example.com
git config user.name t
git config commit.gpgsign false
git config tag.gpgsign false
c() { echo "$1" >>f.txt; git add f.txt; git commit -q -m "$1"; }

c "feat(core): prima funzione"
c "fix: bug iniziale"
git tag v0.1.0
c "feat(web)!: nuova pagina"
c "fix(gateway): correzione header"
c "docs: guida"
c "messaggio senza tipo"
git tag v0.0.0-test.1
c "ci: pipeline di rilascio"
git tag v0.2.0-rc.1

# Intervallo fra due tag: solo i commit nuovi, raggruppati.
out="$("${SCRIPT}" v0.2.0-rc.1 --prev v0.1.0)"
has "titolo" "${out}" "## v0.2.0-rc.1 - "
has "feat" "${out}" "### Novità"
has "feat scope" "${out}" "web: nuova pagina"
has "fix" "${out}" "### Correzioni"
has "docs" "${out}" "### Documentazione"
has "ci" "${out}" "### CI"
has "altro" "${out}" "### Altro"
has "altro: testo" "${out}" "messaggio senza tipo"
lacks "commit del rilascio precedente" "${out}" "bug iniziale"

# Precedente automatico: il tag di prova non conta per un rilascio vero.
out="$("${SCRIPT}" v0.2.0-rc.1)"
has "prev automatico: da v0.1.0" "${out}" "Commit fra v0.1.0 e v0.2.0-rc.1"
has "prev automatico: contiene" "${out}" "nuova pagina"
lacks "prev automatico: non il rilascio vecchio" "${out}" "prima funzione"

# Un tag di prova invece usa il precedente reale.
out="$("${SCRIPT}" v0.0.0-test.1)"
has "tag di prova: da v0.1.0" "${out}" "Commit fra v0.1.0 e v0.0.0-test.1"

# Primo rilascio: tutta la storia.
out="$("${SCRIPT}" v0.1.0)"
has "primo rilascio" "${out}" "Commit fra (inizio) e v0.1.0"
has "primo rilascio: contenuto" "${out}" "prima funzione"

# --out e --prepend, idempotente.
"${SCRIPT}" v0.2.0-rc.1 --out "${WORK}/sezione.md"
has "--out" "$(cat "${WORK}/sezione.md")" "## v0.2.0-rc.1 - "
"${SCRIPT}" v0.1.0 --prepend "${WORK}/CHANGELOG.md"
has "prepend: file nuovo con titolo" "$(head -n1 "${WORK}/CHANGELOG.md")" "# Changelog"
"${SCRIPT}" v0.2.0-rc.1 --prepend "${WORK}/CHANGELOG.md"
chlog="$(cat "${WORK}/CHANGELOG.md")"
first="$(grep -n '^## ' "${WORK}/CHANGELOG.md" | head -n1)"
has "prepend: la versione nuova è in testa" "${first}" "v0.2.0-rc.1"
has "prepend: la vecchia resta" "${chlog}" "## v0.1.0 - "
"${SCRIPT}" v0.2.0-rc.1 --prepend "${WORK}/CHANGELOG.md" 2>/dev/null
n="$(grep -c '^## v0.2.0-rc.1 - ' "${WORK}/CHANGELOG.md")"
if [ "${n}" = "1" ]; then ok; else bad "prepend idempotente: ${n} sezioni"; fi

# Errori: tag non SemVer, tag inesistente.
if "${SCRIPT}" main >/dev/null 2>&1; then bad "tag non SemVer accettato"; else ok; fi
if "${SCRIPT}" v9.9.9 >/dev/null 2>&1; then bad "tag inesistente accettato"; else ok; fi

printf '\nrelease_changelog_test: %d ok, %d falliti\n' "${PASS}" "${FAILN}"
[ "${FAILN}" -eq 0 ]
