#!/usr/bin/env bash
# Misura a vuoto di CloudNativePG (alternativa scartata al punto 1 dell'ADR 0003) su un k3d nuovo:
# operatore da solo, poi operatore + un Cluster con 1 istanza. Servono internet (ghcr.io) e il
# manifest scaricato in $CNPG_MANIFEST. Stampa anche immagini e CRD. Ripulisce il cluster.
set -euo pipefail

cluster=${K3D_CLUSTER:-gitstack-cnpg-poc}
manifest=${CNPG_MANIFEST:?imposta CNPG_MANIFEST sul cnpg-<versione>.yaml}
k3d_bin=${K3D_BIN:-k3d}
kubectl_bin=${KUBECTL_BIN:-kubectl}
work=$(mktemp -d)
created=false
cleanup() {
  rc=$?
  rm -rf "$work"
  if "$created"; then "$k3d_bin" cluster delete "$cluster" >/dev/null 2>&1 || true; fi
  echo "exit: $rc"
}
trap cleanup EXIT

"$k3d_bin" cluster create "$cluster" --image rancher/k3s:v1.32.5-k3s1 --wait --kubeconfig-update-default=false --kubeconfig-switch-context=false >/dev/null
created=true
"$k3d_bin" kubeconfig get "$cluster" > "$work/kubeconfig"
k=("$kubectl_bin" --kubeconfig "$work/kubeconfig")

waittop() { # <ns> <selettore>
  local end=$((SECONDS + 240)) t=''
  while [ "$SECONDS" -lt "$end" ]; do
    t=$("${k[@]}" -n "$1" top pod -l "$2" --no-headers 2>/dev/null || true)
    [ -n "$t" ] && { echo "$t"; return 0; }
    sleep 5
  done
  echo 'misura non disponibile entro 240 s'
}

echo "manifest: $(basename "$manifest") $(wc -c < "$manifest") byte, CRD: $(grep -c '^kind: CustomResourceDefinition' "$manifest")"
t0=$SECONDS
"${k[@]}" apply --server-side -f "$manifest" >/dev/null
"${k[@]}" -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=240s >/dev/null
echo "operatore pronto in $((SECONDS - t0)) s"
sleep 60
echo "operatore a vuoto (CPU, RAM): $(waittop cnpg-system app.kubernetes.io/name=cloudnative-pg)"

"${k[@]}" create namespace gitstack-db-cnpg >/dev/null
"${k[@]}" apply -f - >/dev/null <<'EOF'
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: pg
  namespace: gitstack-db-cnpg
spec:
  instances: 1
  storage:
    size: 1Gi
    storageClass: local-path
EOF
t1=$SECONDS
"${k[@]}" -n gitstack-db-cnpg wait --for=condition=Ready cluster/pg --timeout=420s >/dev/null
echo "cluster 1 istanza pronto in $((SECONDS - t1)) s"
echo "immagine postgres usata: $("${k[@]}" -n gitstack-db-cnpg get pod pg-1 -o jsonpath='{.spec.containers[0].image}')"
echo "init container: $("${k[@]}" -n gitstack-db-cnpg get pod pg-1 -o jsonpath='{.spec.initContainers[*].image}')"
echo "uid del pod: $("${k[@]}" -n gitstack-db-cnpg get pod pg-1 -o jsonpath='{.spec.securityContext.runAsUser}')"
sleep 60
echo "istanza a vuoto (CPU, RAM): $(waittop gitstack-db-cnpg cnpg.io/cluster=pg)"
echo "operatore con un cluster (CPU, RAM): $(waittop cnpg-system app.kubernetes.io/name=cloudnative-pg)"
echo "ESITO: OK"
