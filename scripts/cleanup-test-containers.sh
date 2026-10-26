#!/usr/bin/env bash
# Rimuove i container di test orfani (label org.testcontainers=true e
# gitstack.test). Equivalente di cleanup-test-containers.ps1, per Linux/macOS e
# Git Bash. Di default toglie i fermi e quelli accesi da piu' di
# MAX_AGE_MINUTES (60); --all toglie tutto; --dry-run elenca soltanto.
set -euo pipefail
max_age="${MAX_AGE_MINUTES:-60}"; all=0; dry=0
for a in "$@"; do
  case "$a" in --all) all=1 ;; --dry-run) dry=1 ;; *) echo "uso: $0 [--all] [--dry-run]" >&2; exit 2 ;; esac
done
ids=$( { docker ps -a --filter label=org.testcontainers=true --format '{{.ID}}'
         docker ps -a --filter label=gitstack.test --format '{{.ID}}'; } | sort -u | grep . || true)
removed=0; total=0; now=$(date +%s)
for id in $ids; do
  total=$((total + 1))
  running=$(docker inspect --format '{{.State.Running}}' "$id")
  created=$(date -d "$(docker inspect --format '{{.Created}}' "$id")" +%s 2>/dev/null || echo "$now")
  age=$(( (now - created) / 60 ))
  if [ "$running" = true ] && [ "$all" = 0 ] && [ "$age" -lt "$max_age" ]; then
    echo "salto $id: acceso da $age min"; continue
  fi
  if [ "$dry" = 1 ]; then echo "rimuoverei $id"; continue; fi
  docker rm -f "$id" >/dev/null; echo "rimosso $id"; removed=$((removed + 1))
done
echo "container rimossi: $removed (su $total con etichetta di test)"
