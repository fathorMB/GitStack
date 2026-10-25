#!/usr/bin/env bash
# Verifica H11 nel cluster k3d gia' installato dal job Chart.
set -euo pipefail
ns=default
kubectl get priorityclass gitstack-services gitstack-apps gitstack-ci-job >/dev/null
echo "OK: PriorityClass servizi, app e CI presenti"
services="$(kubectl -n "$ns" get deploy -l app.kubernetes.io/part-of=gitstack -o jsonpath='{range .items[*]}{.spec.template.spec.priorityClassName}{"\n"}{end}')"
if printf '%s\n' "$services" | grep -vx 'gitstack-services' >/dev/null; then
  echo "KO: un servizio GitStack non usa gitstack-services"; exit 1
fi
echo "OK: servizi con gitstack-services"
cat <<'EOF' | kubectl -n "$ns" apply -f -
apiVersion: v1
kind: Pod
metadata: {name: h11-app}
spec:
  priorityClassName: gitstack-apps
  restartPolicy: Never
  containers:
  - name: hold
    image: docker.io/busybox:1.36
    command: [sh, -c, 'sleep 300']
    resources: {requests: {cpu: 50m, memory: 32Mi}}
---
apiVersion: v1
kind: Pod
metadata: {name: h11-ci}
spec:
  priorityClassName: gitstack-ci-job
  restartPolicy: Never
  containers:
  - name: hold
    image: docker.io/busybox:1.36
    command: [sh, -c, 'sleep 300']
    resources: {requests: {cpu: 50m, memory: 32Mi}}
EOF
kubectl -n "$ns" wait --for=condition=Ready pod/h11-app --timeout=90s
kubectl -n "$ns" wait --for=condition=Ready pod/h11-ci --timeout=90s
echo "OK: pod app e CI schedulati"
kubectl -n "$ns" run h11-saturate --image=docker.io/busybox:1.36 --restart=Never --overrides='{"spec":{"priorityClassName":"gitstack-ci-job","containers":[{"name":"h11-saturate","image":"docker.io/busybox:1.36","command":["sleep","300"],"resources":{"requests":{"cpu":"1000","memory":"128Gi"}}}]}}'
sleep 5
phase="$(kubectl -n "$ns" get pod h11-saturate -o jsonpath='{.status.phase}')"
if [ "$phase" != Pending ]; then echo "KO: saturazione CI non Pending ($phase)"; exit 1; fi
echo "OK: job CI sotto pressione Pending"
if kubectl -n "$ns" get pod -l app.kubernetes.io/part-of=gitstack -o jsonpath='{range .items[*]}{.status.phase}{"\n"}{end}' | grep -vx Running >/dev/null; then
  echo "KO: la saturazione ha sfrattato un servizio"; exit 1
fi
echo "OK: servizi restano Running"
if [ "$(kubectl -n "$ns" get pod h11-app -o jsonpath='{.status.phase}')" != Running ]; then
  echo "KO: app non Running mentre CI cede"; exit 1
fi
echo "OK: app resta Running prima del job CI"
