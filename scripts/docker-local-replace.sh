#!/bin/sh
# Usato dai Dockerfile dei servizi (GIT-184): dentro la cartella di un modulo
# punta ogni modulo interno (pkg/*, client/go) alle sorgenti locali copiate
# nell'immagine, con un `replace` in go.mod. L'immagine usa cosi' il codice del
# commit, qualunque pseudo-versione dica il require. La lista dei moduli si
# ricava dalle cartelle (riga "module" del loro go.mod), non e' ricopiata.
#
#   docker-local-replace.sh <radice del repo nell'immagine>   (cwd = modulo)
set -eu
root="${1:?uso: docker-local-replace.sh <radice>}"
for gomod in "$root"/pkg/*/go.mod "$root"/client/go/go.mod; do
	[ -f "$gomod" ] || continue
	mod=$(sed -n 's/^module[[:space:]][[:space:]]*//p' "$gomod" | head -n 1)
	go mod edit -replace "${mod}=$(dirname "$gomod")"
done
