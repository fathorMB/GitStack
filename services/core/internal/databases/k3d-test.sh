#!/usr/bin/env bash
# Prova del controller dei database (GIT-551, ADR 0003) su un k3d usa-e-getta: crea il cluster, lancia i test
# d'integrazione di core che lo usano (contesto di sicurezza, TLS, NetworkPolicy, eliminazione) e lo elimina.
# Servono docker, k3d e go; Postgres di core parte con testcontainers; l'immagine postgres:17-alpine viene
# scaricata da Docker Hub dal cluster. Il cluster ha l'admin: il RBAC del chart e' provato da GIT-565.
#   services/core/internal/databases/k3d-test.sh
set -euo pipefail

cluster=${GITSTACK_K3D_CLUSTER:-gs-db-test}
k3d_bin=${K3D_BIN:-k3d}
root=$(cd "$(dirname "$0")/../../../.." && pwd)
k3s_image=${K3S_IMAGE:-"rancher/k3s:$(tr -d '[:space:]' < "$root/deploy/k3s-version" | tr '+' '-')"}
work=$(mktemp -d)
created=false

cleanup() {
  if "$created"; then "$k3d_bin" cluster delete "$cluster" >/dev/null 2>&1 || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

"$k3d_bin" version >/dev/null 2>&1 || { echo "KO: manca k3d"; exit 1; }
if "$k3d_bin" cluster list -o json | grep -q "\"$cluster\""; then echo "KO: cluster $cluster gia esistente"; exit 1; fi
"$k3d_bin" cluster create "$cluster" --image "$k3s_image" --wait --timeout "${K3D_TIMEOUT:-300s}" \
  --kubeconfig-update-default=false --kubeconfig-switch-context=false >/dev/null
created=true
"$k3d_bin" kubeconfig get "$cluster" >"$work/kubeconfig"
export GITSTACK_TEST_KUBECONFIG="$work/kubeconfig"
cd "$root/services/core"
# Mirata ai soli test su k3d (direttiva del quaderno del repo): niente pacchetti interi, niente ./...
go test -tags integration -count=1 -timeout 25m -run 'TestDatabases_(ContestoDiSicurezza|ConnessioneTls|Isolamento|Eliminazione)SuK3d' ./internal/databases/
