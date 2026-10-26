#!/usr/bin/env bash
# Prova del controller dei deploy (GIT-343) su un k3d usa-e-getta: crea il cluster, lancia i test
# d'integrazione di core che lo usano (deploy, salute, ritorno automatico, riavvio, rollback, scala
# con traffico continuo) e lo elimina. Servono docker, k3d e go; Postgres parte con testcontainers.
#   services/core/internal/apps/k3d-test.sh
set -euo pipefail

cluster=${GITSTACK_K3D_CLUSTER:-gs-apps-test}
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
"$k3d_bin" cluster create "$cluster" --image "$k3s_image" --wait --timeout 300s \
  --kubeconfig-update-default=false --kubeconfig-switch-context=false >/dev/null
created=true
"$k3d_bin" kubeconfig get "$cluster" >"$work/kubeconfig"

cd "$root/services/core"
GITSTACK_TEST_KUBECONFIG="$work/kubeconfig" go test -tags integration -count=1 -timeout 25m \
  -run 'TestApps_DeployPermessiESenzaController|TestApps_ControllerSuK3d|TestApps_SegretiKubernetesERollback|TestApps_IsolamentoSuK3d|TestApps_BarriereInUscitaSuK3d|TestApps_NomiInterniSuK3d' -v ./internal/httpserver/
echo "OK: controller, segreti, isolamento e nomi interni delle app provati su k3d"
