#!/bin/sh
# Entrypoint dell'immagine web (GIT-171): scrive /tmp/gs-index.json, copia
# dell'indice di /downloads fatta in build. Se il chart ha montato il
# certificato pubblico della CA interna (/etc/gitstack/ca/ca.crt), l'indice
# lo annuncia ("ca_cert"); senza, resta null. Va in /tmp perche' l'utente
# nginx non scrive in /usr/share/nginx/html.
set -eu
src=/usr/share/gitstack/downloads-index.json
dst=/tmp/gs-index.json
if [ -s /etc/gitstack/ca/ca.crt ]; then
  sed 's#"ca_cert":null#"ca_cert":"/downloads/ca.crt"#' "$src" > "$dst"
else
  cp "$src" "$dst"
fi
