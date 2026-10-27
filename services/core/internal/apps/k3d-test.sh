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
# GIT-418: con GITSTACK_TEST_CORE_IMAGE (immagine di core gia' presente in docker) il cancello di rete
# dei pod delle app e' quello vero (`core apps-gate`): l'immagine entra nel cluster e arriva ai test.
if [ -n "${GITSTACK_TEST_CORE_IMAGE:-}" ]; then
  "$k3d_bin" image import "$GITSTACK_TEST_CORE_IMAGE" -c "$cluster" >/dev/null
  export GITSTACK_TEST_CORE_IMAGE
fi
"$k3d_bin" kubeconfig get "$cluster" >"$work/kubeconfig"

# GIT-424: il kubeconfig di k3d e' cluster-admin, quindi i test non provano mai i permessi del
# controller. Si applica l'RBAC vero del chart (apps-rbac.yaml, release "gitstack" nel namespace
# default) e si ricava un kubeconfig col token del ServiceAccount di core: il test
# TestApps_SenzaSegretiConRbacDelChartSuK3d fa girare il controller con quello.
sa=gitstack-core
helm template gitstack "$root/deploy/gitstack" --namespace default --show-only templates/apps-rbac.yaml \
  --set apps.enabled=true --set apps.domain=apps.example.com >"$work/apps-rbac.yaml"
kubectl --kubeconfig "$work/kubeconfig" -n default apply -f "$work/apps-rbac.yaml" >/dev/null
sa_token=$(kubectl --kubeconfig "$work/kubeconfig" -n default create token "$sa" --duration 2h)
cp "$work/kubeconfig" "$work/kubeconfig-core"
kubectl --kubeconfig "$work/kubeconfig-core" config set-credentials "sa-$sa" --token "$sa_token" >/dev/null
kubectl --kubeconfig "$work/kubeconfig-core" config set-context --current --user "sa-$sa" >/dev/null
export GITSTACK_TEST_KUBECONFIG_CORE="$work/kubeconfig-core"
export GITSTACK_TEST_CORE_SA="default/$sa"
export GITSTACK_TEST_WORKLOAD_ROLE=gitstack-app-workload
# Caso opposto: il ruolo modello non ha binding cluster-wide, quindi col token del SA (non
# dell'admin) delete sui secrets fuori dai namespace delle app resta vietato.
answer=$(kubectl --kubeconfig "$work/kubeconfig-core" auth can-i delete secrets -n kube-system 2>&1 || true)
if [ "$answer" != "no" ]; then echo "KO: il SA di core può cancellare secrets fuori dai namespace delle app (can-i: $answer)"; exit 1; fi
echo "ok: il SA di core non può cancellare secrets in kube-system"
# GIT-427: readPodMetrics (metrics.go) fa List su metrics.k8s.io/v1beta1 pods nel namespace
# dell'app. Il permesso sta nel ruolo modello, legato per namespace: si crea un namespace di prova
# col RoleBinding che crea il controller e si chiede a Kubernetes (can-i non richiede metrics-server).
mns=gitstack-app-metricsprobe
kubectl --kubeconfig "$work/kubeconfig" create namespace "$mns" >/dev/null
kubectl --kubeconfig "$work/kubeconfig" -n "$mns" create rolebinding probe-workload \
  --clusterrole=gitstack-app-workload --serviceaccount="default:$sa" >/dev/null
# Lo stdout resta separato dallo stderr: su un k3d appena creato il metrics-server non e' ancora
# Available e kubectl scrive i warning di discovery su stderr, la risposta resta yes/no.
answer=$(kubectl --kubeconfig "$work/kubeconfig" auth can-i list pods.metrics.k8s.io --as="system:serviceaccount:default:$sa" -n "$mns" 2>"$work/cani-metrics.err" || true)
if [ "$answer" = yes ]; then
  echo "ok: il SA di core può leggere le metriche dei pod nel namespace dell'app"
else
  echo "KO: il SA di core non può fare list su pods.metrics.k8s.io nel namespace dell'app (can-i: $answer; stderr: $(cat "$work/cani-metrics.err"))"; exit 1
fi
answer=$(kubectl --kubeconfig "$work/kubeconfig" auth can-i list pods.metrics.k8s.io --as="system:serviceaccount:default:$sa" -n kube-system 2>"$work/cani-metrics.err" || true)
if [ "$answer" = no ]; then
  echo "ok: il SA di core non può leggere le metriche dei pod fuori dai namespace delle app"
else
  echo "KO: il SA di core può leggere pods.metrics.k8s.io fuori dai namespace delle app (can-i: $answer; stderr: $(cat "$work/cani-metrics.err"))"; exit 1
fi
kubectl --kubeconfig "$work/kubeconfig" delete namespace "$mns" --wait=false >/dev/null

cd "$root/services/core"
GITSTACK_TEST_KUBECONFIG="$work/kubeconfig" go test -tags integration -count=1 -timeout 25m \
  -run 'TestApps_DeployPermessiESenzaController|TestApps_ControllerSuK3d|TestApps_SegretiKubernetesERollback|TestApps_IsolamentoSuK3d|TestApps_BarriereInUscitaSuK3d|TestApps_CancelloVeroSenzaPolicySuK3d|TestApps_NomiInterniSuK3d|TestApps_VolumiSuK3d|TestApps_SenzaSegretiConRbacDelChartSuK3d' -v ./internal/httpserver/
echo "OK: controller, segreti, isolamento, barriere in uscita, nomi interni e volumi delle app provati su k3d"
