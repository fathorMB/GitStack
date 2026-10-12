#!/usr/bin/env bash
# Changelog di una release (GIT-222, V4/V5c): i commit fra un tag e il
# precedente, raggruppati per tipo (conventional commits, o "Altro").
#
# Uso: scripts/release-changelog.sh <tag> [opzioni]
#   --prev TAG        tag precedente (default: l'ultimo tag v* raggiungibile da
#                     <tag>^, senza i tag di prova v*-test.* se <tag> non lo è)
#   --out FILE        scrive qui la sezione (default: stdout)
#   --prepend FILE    inserisce la sezione in testa a FILE (CHANGELOG.md),
#                     sotto il titolo; idempotente: se la sezione c'è già non
#                     la ripete
#
# Senza il tag precedente (primo rilascio) elenca tutta la storia fino a <tag>.
# Non serve git-cliff: l'unica dipendenza è git, quindi nessuno strumento da
# pinnare nella CI.
set -euo pipefail

usage() {
  sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

tag=""
prev=""
out=""
prepend=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --prev) prev="${2:?--prev vuole un tag}"; shift 2 ;;
    --out) out="${2:?--out vuole un file}"; shift 2 ;;
    --prepend) prepend="${2:?--prepend vuole un file}"; shift 2 ;;
    -h | --help) usage ;;
    -*) echo "opzione sconosciuta: $1" >&2; usage ;;
    *)
      [ -z "${tag}" ] || { echo "un solo tag, trovato anche: $1" >&2; usage; }
      tag="$1"
      shift
      ;;
  esac
done
[ -n "${tag}" ] || usage

semver_re='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
if ! printf '%s' "${tag}" | grep -Eq "${semver_re}"; then
  echo "tag non valido: ${tag} (atteso vX.Y.Z o vX.Y.Z-rc.N)" >&2
  exit 2
fi
git rev-parse -q --verify "refs/tags/${tag}^{commit}" >/dev/null \
  || { echo "il tag ${tag} non esiste in questo repository" >&2; exit 1; }

if [ -z "${prev}" ]; then
  excl=()
  case "${tag}" in
    *-test.*) ;;
    *) excl=(--exclude 'v*-test.*') ;;
  esac
  prev="$(git describe --tags --abbrev=0 --match 'v*' ${excl[@]+"${excl[@]}"} "${tag}^" 2>/dev/null || true)"
fi

if [ -n "${prev}" ]; then
  range="${prev}..${tag}"
  from="${prev}"
else
  range="${tag}"
  from="(inizio)"
fi

date="$(git log -1 --format=%cs "${tag}")"

declare -A order=([feat]=1 [fix]=2 [perf]=3 [refactor]=4 [docs]=5 [test]=6 [ci]=7 [build]=8 [chore]=9 [other]=10)
declare -A title=(
  [feat]="Novità" [fix]="Correzioni" [perf]="Prestazioni" [refactor]="Refactoring"
  [docs]="Documentazione" [test]="Test" [ci]="CI" [build]="Build" [chore]="Manutenzione" [other]="Altro"
)
declare -A body=()

# Niente merge: i commit veri sono già nell'intervallo.
while IFS=$'\t' read -r hash subject; do
  [ -n "${hash}" ] || continue
  kind="other"
  text="${subject}"
  if [[ "${subject}" =~ ^([a-z]+)(\([^\)]*\))?(!)?:[[:space:]]*(.*)$ ]]; then
    k="${BASH_REMATCH[1]}"
    if [ -n "${order[${k}]:-}" ] && [ "${k}" != "other" ]; then
      kind="${k}"
      scope="${BASH_REMATCH[2]}"
      text="${BASH_REMATCH[4]}"
      [ -z "${scope}" ] || text="${scope//[()]/}: ${text}"
    fi
  fi
  body[${kind}]+="- ${text} (\`${hash}\`)"$'\n'
done < <(git log --no-merges --format='%h%x09%s' "${range}")

section="## ${tag} - ${date}"$'\n\n'
section+="Commit fra ${from} e ${tag}."$'\n\n'
count=0
for k in feat fix perf refactor docs test ci build chore other; do
  [ -n "${body[${k}]:-}" ] || continue
  section+="### ${title[${k}]}"$'\n\n'"${body[${k}]}"$'\n'
  count=$((count + 1))
done
if [ "${count}" -eq 0 ]; then
  section+="Nessun commit."$'\n\n'
fi

if [ -n "${out}" ]; then
  printf '%s' "${section}" >"${out}"
elif [ -z "${prepend}" ]; then
  printf '%s' "${section}"
fi

if [ -n "${prepend}" ]; then
  if [ -f "${prepend}" ] && grep -q "^## ${tag} - " "${prepend}"; then
    echo "${prepend}: la sezione ${tag} c'è già, non la ripeto." >&2
    exit 0
  fi
  tmp="$(mktemp)"
  trap 'rm -f "${tmp}"' EXIT
  if [ -f "${prepend}" ] && head -n1 "${prepend}" | grep -q '^# '; then
    {
      head -n1 "${prepend}"
      printf '\n%s' "${section}"
      tail -n +2 "${prepend}" | sed '1{/^$/d}'
    } >"${tmp}"
  else
    {
      printf '# Changelog\n\n%s' "${section}"
      [ ! -f "${prepend}" ] || cat "${prepend}"
    } >"${tmp}"
  fi
  cat "${tmp}" >"${prepend}"
fi
