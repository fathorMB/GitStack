#!/usr/bin/env bash
# Prova di GIT-425 su due k3d usa-e-getta: un'app di un repo PRIVATO con l'immagine nel registry del
# repo arriva a serving senza configurazioni a mano.
#
#  1. cluster SENZA registries.yaml  -> il pod cade con «x509: certificate signed by unknown authority»
#  2. cluster col registries.yaml di install.sh (CA interna per l'host del registry), core SENZA
#     credenziali                    -> «authorization failed / no basic auth credentials»
#  3. stesso cluster, core con PullCredentials -> la versione è «serving», l'app «running»
#
# Il registry è un registry:2 vero dietro un proxy TLS (CA di prova) che chiede al ci-gateway vero di
# core cosa può fare il token (TestApps_RegistryPrivatoSuK3d). Servono docker, k3d, openssl e go; Postgres
# parte con testcontainers. GITSTACK_TEST_CORE_IMAGE: immagine di core già in docker, con `apps-gate`.
#   GITSTACK_TEST_CORE_IMAGE=gitstack-core:t418 services/core/internal/apps/k3d-pull-test.sh
set -euo pipefail

k3d_bin=${K3D_BIN:-k3d}
root=$(cd "$(dirname "$0")/../../../.." && pwd)
k3s_image=${K3S_IMAGE:-"rancher/k3s:$(tr -d '[:space:]' < "$root/deploy/k3s-version" | tr '+' '-')"}
: "${GITSTACK_TEST_CORE_IMAGE:?imposta GITSTACK_TEST_CORE_IMAGE (immagine di core con apps-gate)}"
port=${PULL_PROXY_PORT:-5543}
host=host.k3d.internal
reg=gs-pull-registry
work=$(mktemp -d)
clusters=()

cleanup() {
  for c in "${clusters[@]:-}"; do [ -n "$c" ] && "$k3d_bin" cluster delete "$c" >/dev/null 2>&1 || true; done
  docker rm -f "$reg" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
export MSYS_NO_PATHCONV=1

# CA di prova e certificato del proxy (SAN = host con cui i nodi lo raggiungono).
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$work/ca.key" -out "$work/ca.crt" -days 2 -subj "/CN=GitStack prova CA" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout "$work/server.key" -out "$work/server.csr" -subj "/CN=$host" >/dev/null 2>&1
printf 'subjectAltName=DNS:%s\n' "$host" >"$work/san.cnf"
openssl x509 -req -in "$work/server.csr" -CA "$work/ca.crt" -CAkey "$work/ca.key" -CAcreateserial -out "$work/server.crt" -days 2 -extfile "$work/san.cnf" >/dev/null 2>&1

# registry:2 vero con l'immagine dell'app (nginx senza privilegi, porta 8080).
docker rm -f "$reg" >/dev/null 2>&1 || true
docker run -d --name "$reg" -p 127.0.0.1:5000:5000 registry:2 >/dev/null
docker pull -q nginxinc/nginx-unprivileged:alpine >/dev/null
docker tag nginxinc/nginx-unprivileged:alpine localhost:5000/alice/handbook/e2eimg:v1
for i in $(seq 1 30); do curl -sf http://127.0.0.1:5000/v2/ >/dev/null && break; sleep 1; done
docker push -q localhost:5000/alice/handbook/e2eimg:v1 >/dev/null

# registries.yaml come lo scrive install.sh con TLS internal (stessa funzione dell'installer).
(
  export GITSTACK_INSTALL_LIB=1
  set --
  # shellcheck disable=SC1091
  . "$root/deploy/install.sh"
  set +e +o pipefail
  GITSTACK_TLS_MODE=internal PUBLIC_HOST="$host:$port" TLS_DIR=/etc/gitstack/tls
  HOST_ARGS=()
  render_registries_yaml >"$work/registries.yaml"
)
echo "--- registries.yaml generato da install.sh ---"
cat "$work/registries.yaml"
echo "----------------------------------------------"

run_scenario() { # nome cluster kubeconfig
  local scenario=$1 cluster=$2
  (
    cd "$root/services/core"
    GITSTACK_TEST_KUBECONFIG="$work/kubeconfig-$cluster" GITSTACK_TEST_CORE_IMAGE \
      GITSTACK_TEST_PULL_PROXY_ADDR="$host:$port" GITSTACK_TEST_PULL_UPSTREAM=http://127.0.0.1:5000 \
      GITSTACK_TEST_PULL_CERT_DIR="$work" GITSTACK_TEST_PULL_IMAGE=alice/handbook/e2eimg:v1 \
      GITSTACK_TEST_PULL_SCENARIO="$scenario" \
      go test -tags integration -count=1 -timeout 15m -run TestApps_RegistryPrivatoSuK3d -v ./internal/httpserver/
  )
}

mk_cluster() { # nome [extra args...]
  local cluster=$1
  shift
  "$k3d_bin" cluster create "$cluster" --image "$k3s_image" --wait --timeout 300s \
    --kubeconfig-update-default=false --kubeconfig-switch-context=false "$@" >/dev/null
  clusters+=("$cluster")
  "$k3d_bin" image import "$GITSTACK_TEST_CORE_IMAGE" -c "$cluster" >/dev/null
  "$k3d_bin" kubeconfig get "$cluster" >"$work/kubeconfig-$cluster"
}

echo "=== 1. senza registries.yaml (controllo negativo: containerd non conosce la CA) ==="
mk_cluster gs-pull-a
run_scenario x509 gs-pull-a
"$k3d_bin" cluster delete gs-pull-a >/dev/null 2>&1 || true

echo "=== 2. con registries.yaml, senza credenziali (controllo negativo) ==="
mk_cluster gs-pull-b --registry-config "$work/registries.yaml" --volume "$work/ca.crt:/etc/gitstack/tls/ca.crt@server:0"
run_scenario auth gs-pull-b

echo "=== 3. con registries.yaml e credenziali create da core ==="
run_scenario ok gs-pull-b
echo "OK: app di un repo privato con immagine del registry del repo, a serving senza configurazioni a mano"
