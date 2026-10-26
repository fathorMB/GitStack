#!/usr/bin/env bash
# Verifica H11 nel cluster k3d gia' installato dal job Chart.
set -euo pipefail
ns=default
node="$(kubectl get node -o jsonpath='{.items[0].metadata.name}')"
trap 'kubectl -n default delete pod -l h11-probe=1 --ignore-not-found --wait=false' EXIT
kubectl get priorityclass gitstack-services gitstack-apps gitstack-ci-job >/dev/null
echo "OK: PriorityClass servizi, app e CI presenti"
services="$(kubectl -n "$ns" get deploy,statefulset -l app.kubernetes.io/part-of=gitstack -o jsonpath='{range .items[*]}{.spec.template.spec.priorityClassName}{"\n"}{end}')"
if printf '%s\n' "$services" | grep -vx 'gitstack-services' >/dev/null; then
  echo "KO: un servizio GitStack non usa gitstack-services"; exit 1
fi
echo "OK: servizi con gitstack-services"
cpu() { awk 'BEGIN { v="'$1'"; if (v ~ /m$/) { sub(/m$/, "", v); print v } else print v*1000 }'; }
alloc="$(cpu "$(kubectl get node "$node" -o jsonpath='{.status.allocatable.cpu}')")"
used="$(kubectl get pods -A --field-selector "spec.nodeName=$node,status.phase!=Succeeded,status.phase!=Failed" -o jsonpath='{range .items[*].spec.containers[*]}{.resources.requests.cpu}{"\n"}{end}' | awk 'NF { if ($0 ~ /m$/) { sub(/m$/, ""); sum += $0 } else sum += $0*1000 } END { print sum+0 }')"
free=$((alloc-used)); echo "free=${free}m"
cat <<EOF | kubectl -n "$ns" apply -f -
apiVersion: v1
kind: Pod
metadata: {name: h11-app, labels: {h11-probe: "1"}}
spec:
  nodeSelector: {kubernetes.io/hostname: $node}
  priorityClassName: gitstack-apps
  restartPolicy: Never
  containers:
  - name: hold
    image: docker.io/busybox:1.36
    command: [sh, -c, 'sleep 300']
    resources: {requests: {cpu: 300m, memory: 32Mi}}
---
apiVersion: v1
kind: Pod
metadata: {name: h11-ci, labels: {h11-probe: "1"}}
spec:
  nodeSelector: {kubernetes.io/hostname: $node}
  priorityClassName: gitstack-ci-job
  restartPolicy: Never
  containers:
  - name: hold
    image: docker.io/busybox:1.36
    command: [sh, -c, 'sleep 300']
    resources: {requests: {cpu: ${free}m, memory: 32Mi}}
EOF
kubectl -n "$ns" wait --for=condition=Ready pod/h11-ci --timeout=90s
echo "OK: job CI occupa il nodo"
uids="$(kubectl -n "$ns" get pods -l app.kubernetes.io/part-of=gitstack -o jsonpath='{range .items[*]}{.metadata.uid}{"\n"}{end}')"
kubectl -n "$ns" wait --for=condition=Ready pod/h11-app --timeout=120s
if kubectl -n "$ns" get pod h11-ci -o jsonpath='{.metadata.deletionTimestamp}' 2>/dev/null | grep -q . || ! kubectl -n "$ns" get pod h11-ci >/dev/null 2>&1; then echo "OK: il job CI cede all'app"; else echo "KO: job CI non preemptato"; exit 1; fi
now="$(kubectl get pods -A --field-selector "spec.nodeName=$node,status.phase!=Succeeded,status.phase!=Failed" -o jsonpath='{range .items[*].spec.containers[*]}{.resources.requests.cpu}{"\n"}{end}' | awk 'NF { if ($0 ~ /m$/) { sub(/m$/, ""); sum += $0 } else sum += $0*1000 } END { print sum+0 }')"
kubectl -n "$ns" run h11-app-big --image=docker.io/busybox:1.36 --restart=Never --labels=h11-probe=1 --overrides="{\"spec\":{\"nodeSelector\":{\"kubernetes.io/hostname\":\"$node\"},\"priorityClassName\":\"gitstack-apps\",\"containers\":[{\"name\":\"h11-app-big\",\"image\":\"docker.io/busybox:1.36\",\"command\":[\"sleep\",\"300\"],\"resources\":{\"requests\":{\"cpu\":\"$((alloc-now+500))m\"}}}]}}"
sleep 30
if [ "$(kubectl -n "$ns" get pod h11-app-big -o jsonpath='{.status.phase}')" != Pending ]; then echo "KO: app grande non Pending"; exit 1; fi
if [ "$uids" != "$(kubectl -n "$ns" get pods -l app.kubernetes.io/part-of=gitstack -o jsonpath='{range .items[*]}{.metadata.uid}{"\n"}{end}')" ]; then echo "KO: servizio sostituito"; exit 1; fi
if [ "$(kubectl -n "$ns" get pod h11-app -o jsonpath='{.status.phase}')" != Running ]; then echo "KO: app non Running"; exit 1; fi
echo "OK: l'app non sfratta i servizi"
