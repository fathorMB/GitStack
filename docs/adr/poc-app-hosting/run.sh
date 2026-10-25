#!/usr/bin/env bash
set -euo pipefail

cluster=${K3D_CLUSTER:-gitstack-app-poc}
namespace=gitstack-app-poc
port=${APP_HTTPS_PORT:-18443}
k3d_bin=${K3D_BIN:-k3d}
kubectl_bin=${KUBECTL_BIN:-kubectl}
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
work=$(mktemp -d)
created=false

cleanup() {
  rm -rf "$work"
  if "$created"; then "$k3d_bin" cluster delete "$cluster" >/dev/null; fi
}
trap cleanup EXIT

for tool in openssl curl; do command -v "$tool" >/dev/null || { echo "KO: manca $tool"; exit 1; }; done
"$kubectl_bin" version --client >/dev/null 2>&1 || { echo "KO: manca kubectl"; exit 1; }
"$k3d_bin" version >/dev/null 2>&1 || { echo "KO: manca k3d"; exit 1; }
if "$k3d_bin" cluster list -o json | grep -q "\"$cluster\""; then echo "KO: cluster $cluster gia esistente"; exit 1; fi

"$k3d_bin" cluster create "$cluster" --image rancher/k3s:v1.32.5-k3s1 --port "$port:443@loadbalancer" --wait --kubeconfig-update-default=false --kubeconfig-switch-context=false >/dev/null
created=true
"$k3d_bin" kubeconfig write "$cluster" > "$work/kubeconfig"
kubectl=("$kubectl_bin" --kubeconfig "$work/kubeconfig")
deadline=$((SECONDS + 120))
until "${kubectl[@]}" get crd ingressroutes.traefik.io >/dev/null 2>&1 && "${kubectl[@]}" get crd middlewares.traefik.io >/dev/null 2>&1; do
  if [ "$SECONDS" -ge "$deadline" ]; then echo 'KO: CRD Traefik non pronte entro 120 s'; exit 1; fi
  sleep 2
done

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=GitStack App POC CA' -keyout "$work/ca.key" -out "$work/ca.crt" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -subj '/CN=*.apps.test' -keyout "$work/tls.key" -out "$work/tls.csr" >/dev/null 2>&1
printf 'subjectAltName=DNS:*.apps.test\n' > "$work/ext.cnf"
openssl x509 -req -days 1 -in "$work/tls.csr" -CA "$work/ca.crt" -CAkey "$work/ca.key" -CAcreateserial -extfile "$work/ext.cnf" -out "$work/tls.crt" >/dev/null 2>&1

"${kubectl[@]}" apply -f "$root/app.yaml" >/dev/null
"${kubectl[@]}" -n "$namespace" create secret tls app-wildcard-tls --cert="$work/tls.crt" --key="$work/tls.key" >/dev/null
"${kubectl[@]}" -n "$namespace" rollout status deployment/forward-auth --timeout=120s >/dev/null
"${kubectl[@]}" -n "$namespace" rollout status deployment/app --timeout=120s >/dev/null

body=$(curl --silent --show-error --fail --cacert "$work/ca.crt" --resolve "app.apps.test:$port:127.0.0.1" "https://app.apps.test:$port/")
case "$body" in *'X-Gitstack-User: alice'*|*'X-GitStack-User: alice'*) echo 'OK: TLS CA interna e forward-auth autorizzato';; *) echo 'KO: intestazione utente assente'; exit 1;; esac
code=$(curl --silent --output /dev/null --write-out '%{http_code}' --cacert "$work/ca.crt" --resolve "deny.apps.test:$port:127.0.0.1" "https://deny.apps.test:$port/")
if [ "$code" = 401 ]; then echo 'OK: forward-auth rifiuta senza sessione'; else echo "KO: atteso 401, trovato $code"; exit 1; fi
echo 'ESITO: OK'
