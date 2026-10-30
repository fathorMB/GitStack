#!/usr/bin/env bash
# GIT-432: importa immagini in un cluster k3d e VERIFICA che siano nel nodo.
#
# `k3d image import` può uscire con 0 ("Successfully imported N image(s)")
# senza aver caricato niente (run CI 40480382095): i pod poi vanno in
# ImagePullBackOff o scaricano da un registry. Qui, dopo ogni import,
# si controlla dentro il nodo con crictl che ci siano tutte le immagini;
# se ne manca qualcuna si ripete (max 3 tentativi), poi si esce con 1
# stampando l'elenco delle immagini del nodo.
#
# Uso: scripts/k3d-import-verified.sh <cluster> <immagine>...
# Il nodo è k3d-<cluster>-server-0 (override: K3D_IMPORT_NODE).
# Variabili: K3D_IMPORT_ATTEMPTS (default 3), K3D_IMPORT_SLEEP (default 3).
set -euo pipefail

if [ "$#" -lt 2 ]; then
  echo "uso: $0 <cluster> <immagine>..." >&2
  exit 2
fi

cluster="$1"
shift
images=("$@")
node="${K3D_IMPORT_NODE:-k3d-${cluster}-server-0}"
max_attempts="${K3D_IMPORT_ATTEMPTS:-3}"
pause="${K3D_IMPORT_SLEEP:-3}"

# Nome completo come lo riporta crictl: senza registry -> docker.io/library/.
full_name() {
  local img="$1" first
  first="${img%%/*}"
  if [ "$first" = "$img" ]; then
    echo "docker.io/library/${img}"
  elif [[ "$first" == *.* || "$first" == *:* || "$first" == localhost ]]; then
    echo "$img"
  else
    echo "docker.io/${img}"
  fi
}

# Elenco "repo:tag" delle immagini presenti nel nodo.
node_images() {
  docker exec "$node" crictl images 2>/dev/null | awk 'NR>1 {print $1 ":" $2}'
}

missing=()
for attempt in $(seq 1 "$max_attempts"); do
  echo "Import delle immagini in ${cluster} (tentativo ${attempt}/${max_attempts})"
  if ! k3d image import "${images[@]}" -c "$cluster"; then
    echo "k3d image import è uscito con errore (tentativo ${attempt})" >&2
  fi
  present="$(node_images || true)"
  missing=()
  for img in "${images[@]}"; do
    want="$(full_name "$img")"
    if ! grep -Fxq -- "$want" <<<"$present"; then
      missing+=("$want")
    fi
  done
  if [ "${#missing[@]}" -eq 0 ]; then
    echo "OK: tutte le ${#images[@]} immagini sono nel nodo ${node}"
    exit 0
  fi
  echo "Mancano nel nodo dopo il tentativo ${attempt}: ${missing[*]}" >&2
  if [ "$attempt" -lt "$max_attempts" ]; then
    sleep "$pause"
  fi
done

echo "FALLITO: dopo ${max_attempts} tentativi mancano ancora: ${missing[*]}" >&2
echo "Immagini presenti nel nodo ${node}:" >&2
docker exec "$node" crictl images >&2 || true
exit 1
