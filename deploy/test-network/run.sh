#!/usr/bin/env bash
# Prova ripetibile di N9a (GIT-206): install.sh in una rete aziendale chiusa.
#
#   deploy/test-network/run.sh all        # seed + verify + down
#   deploy/test-network/run.sh seed       # popola il registry di prova
#   deploy/test-network/run.sh verify     # la prova vera, senza uscita diretta
#   deploy/test-network/run.sh down       # toglie container, reti e volumi
#
# Che cosa costruisce (tutto in Docker, sull'host che ha Internet):
#   gs-corp-net   rete SENZA uscita (docker network create --internal)
#   gs-squid      proxy squid: l'unica strada verso Internet; in "verify" nega
#                 i registry delle immagini (ghcr.io, docker.io, ...)
#   gs-registry   registry:2, il mirror delle immagini, sulla rete chiusa
#   gs-node       Ubuntu 24.04 con systemd, solo sulla rete chiusa: qui gira
#                 install.sh come su un server vero (k3s dentro il container)
#
# "seed": installa una volta passando dal proxy aperto, raccoglie le immagini
# che k3s ha davvero scaricato e le copia nel registry. "verify": un nodo nuovo
# installa con --http-proxy e --registry-mirror, col proxy che nega i registry:
# le immagini possono arrivare solo dal mirror. Esito: righe "OK"/"KO" e codice
# di uscita; il log di squid e quello del registry sono le prove.
#
# Richiede Docker (motore Linux), curl, go (per costruire gitstack). Prima
# esecuzione: qualche minuto per le immagini; poi circa 10 minuti per giro.
set -euo pipefail
export MSYS_NO_PATHCONV=1

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"
# Con MSYS_NO_PATHCONV i programmi Windows (docker, go) non vedono /e/...:
# i percorsi locali passati a loro si convertono qui.
winpath() {
  if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi
}
NET=gs-corp-net
SQUID=gs-squid
REGISTRY=gs-registry
NODE=gs-node
NODE_IMAGE=gitstack-test-node:24.04
SQUID_IMAGE="${SQUID_IMAGE:-ubuntu/squid:latest}"
REGISTRY_IMAGE="${REGISTRY_IMAGE:-registry:2}"
REGISTRY_HOST_PORT="${REGISTRY_HOST_PORT:-5001}"
PROXY_URL="http://${SQUID}:3128"
MIRROR_URL="http://${REGISTRY}:5000"
# Tag delle immagini di GitStack: quelle pubblicate di main (il ramo di prova
# non è pubblicato). Di default lo sha di origin/main.
IMAGE_TAG="${GITSTACK_TEST_IMAGE_TAG:-sha-$(cd "${ROOT}" && { git rev-parse origin/main 2>/dev/null || git rev-parse HEAD; })}"
STATE="${GITSTACK_TEST_STATE:-${TMPDIR:-/tmp}/gitstack-test-network}"

PASS=0
FAILN=0
log() { printf '==> %s\n' "$*"; }
ok() { PASS=$((PASS + 1)); printf 'OK   %s\n' "$*"; }
ko() { FAILN=$((FAILN + 1)); printf 'KO   %s\n' "$*"; }
need() { command -v "$1" >/dev/null 2>&1 || { echo "manca '$1'" >&2; exit 2; }; }

node() { docker exec "${NODE}" bash -c "$*"; }

down() {
  docker rm -f "${NODE}" "${SQUID}" "${REGISTRY}" >/dev/null 2>&1 || true
  docker network rm "${NET}" >/dev/null 2>&1 || true
  docker volume rm -f gs-node-rancher gs-node-kubelet gs-node-containerd >/dev/null 2>&1 || true
}

net_up() {
  docker network inspect "${NET}" >/dev/null 2>&1 || docker network create --internal "${NET}" >/dev/null
}

# start_squid aperto|chiuso
start_squid() {
  local mode="$1" deny=""
  if [ "${mode}" = "chiuso" ]; then
    deny="http_access deny registries"
  fi
  mkdir -p "${STATE}"
  sed "s#@DENY@#${deny}#" "${HERE}/squid.conf.tmpl" >"${STATE}/squid.conf"
  docker rm -f "${SQUID}" >/dev/null 2>&1 || true
  docker create --name "${SQUID}" --network bridge "${SQUID_IMAGE}" >/dev/null
  docker cp "$(winpath "${STATE}/squid.conf")" "${SQUID}:/etc/squid/squid.conf"
  docker network connect --alias "${SQUID}" "${NET}" "${SQUID}"
  docker start "${SQUID}" >/dev/null
  local i
  for i in $(seq 1 30); do
    if docker exec "${SQUID}" squidclient -h 127.0.0.1 -p 3128 mgr:info >/dev/null 2>&1 \
      || docker logs "${SQUID}" 2>&1 | grep -qi "Accepting HTTP Socket connections"; then
      return 0
    fi
    sleep 1
  done
  docker logs "${SQUID}" 2>&1 | tail -20 >&2
  echo "squid non parte" >&2
  return 1
}

start_registry() {
  docker volume create gs-registry-data >/dev/null
  docker rm -f "${REGISTRY}" >/dev/null 2>&1 || true
  # Anche sulla rete normale, solo per pubblicare la porta all'host (seed).
  docker create --name "${REGISTRY}" --network bridge -p "127.0.0.1:${REGISTRY_HOST_PORT}:5000" \
    -v gs-registry-data:/var/lib/registry "${REGISTRY_IMAGE}" >/dev/null
  docker network connect --alias "${REGISTRY}" "${NET}" "${REGISTRY}"
  docker start "${REGISTRY}" >/dev/null
  local i
  for i in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${REGISTRY_HOST_PORT}/v2/" >/dev/null; then return 0; fi
    sleep 1
  done
  echo "il registry non risponde" >&2
  return 1
}

build_admin() {
  mkdir -p "${STATE}"
  (cd "${ROOT}/admin" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$(winpath "${STATE}/gitstack")" ./cmd/gitstack)
  sha256sum "${STATE}/gitstack" | awk '{print $1}' >"${STATE}/gitstack.sha256"
}

start_node() {
  docker rm -f "${NODE}" >/dev/null 2>&1 || true
  docker volume rm -f gs-node-rancher gs-node-kubelet gs-node-containerd >/dev/null 2>&1 || true
  docker volume create gs-node-rancher >/dev/null
  docker volume create gs-node-kubelet >/dev/null
  docker volume create gs-node-containerd >/dev/null
  # Solo sulla rete chiusa: nessuna strada diretta verso Internet.
  docker run -d --name "${NODE}" --hostname gitstack-node --network "${NET}" \
    --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
    -v gs-node-rancher:/var/lib/rancher -v gs-node-kubelet:/var/lib/kubelet \
    -v gs-node-containerd:/var/lib/containerd \
    "${NODE_IMAGE}" >/dev/null
  local i
  for i in $(seq 1 60); do
    if [ "$(docker exec "${NODE}" systemctl is-system-running 2>/dev/null || true)" = "running" ] \
      || [ "$(docker exec "${NODE}" systemctl is-system-running 2>/dev/null || true)" = "degraded" ]; then
      break
    fi
    sleep 1
  done
  # Una rete --internal non ha una default route e k3s si rifiuta di partire
  # senza: se ne dà una verso il gateway della rete, che non inoltra niente
  # (il controllo "nessuna uscita diretta" di verify lo dimostra).
  local gw
  gw="$(docker network inspect "${NET}" -f '{{(index .IPAM.Config 0).Gateway}}')"
  docker exec "${NODE}" ip route replace default via "${gw}"
  # Il codice dell'installer, come sul server: la cartella deploy/.
  docker exec "${NODE}" mkdir -p /opt/gitstack
  docker cp "$(winpath "${ROOT}/deploy")" "${NODE}:/opt/gitstack/deploy"
  docker cp "$(winpath "${STATE}/gitstack")" "${NODE}:/opt/gitstack/gitstack"
}

node_ip() {
  docker inspect -f "{{(index .NetworkSettings.Networks \"${NET}\").IPAddress}}" "${NODE}"
}

run_install() {
  local ip
  ip="$(node_ip)"
  docker exec "${NODE}" bash /opt/gitstack/deploy/install.sh \
    --chart-dir /opt/gitstack/deploy/gitstack --image-tag "${IMAGE_TAG}" --host "${ip}" \
    --admin-binary /opt/gitstack/gitstack --admin-sha256 "$(cat "${STATE}/gitstack.sha256")" \
    "$@"
}

prepare() {
  need docker
  need curl
  need go
  if ! docker image inspect "${NODE_IMAGE}" >/dev/null 2>&1; then
    log "Costruisco ${NODE_IMAGE}"
    docker build -t "${NODE_IMAGE}" -f "$(winpath "${HERE}/Dockerfile.node")" "$(winpath "${HERE}")"
  fi
  docker pull "${SQUID_IMAGE}" >/dev/null
  docker pull "${REGISTRY_IMAGE}" >/dev/null
  build_admin
}

# Copia nel registry le immagini che k3s ha scaricato nel nodo di "seed".
seed_registry() {
  local refs ref path n=0
  refs="$(node 'k3s ctr -n k8s.io images ls -q' | grep -v '^sha256:' | grep -v '@' | sort -u)"
  [ -n "${refs}" ] || { echo "nessuna immagine nel nodo di seed" >&2; return 1; }
  while IFS= read -r ref; do
    [ -n "${ref}" ] || continue
    # docker.io/rancher/x:1 -> rancher/x:1; ghcr.io/o/n:t -> o/n:t (containerd
    # chiede al mirror il nome senza l'host di origine; ns= lo dice a parte).
    path="${ref#*/}"
    log "mirror <- ${ref}"
    docker pull -q "${ref}" >/dev/null
    docker tag "${ref}" "127.0.0.1:${REGISTRY_HOST_PORT}/${path}"
    docker push -q "127.0.0.1:${REGISTRY_HOST_PORT}/${path}" >/dev/null
    n=$((n + 1))
  done <<<"${refs}"
  log "Immagini copiate nel mirror: ${n}"
}

wait_pods_ready() {
  local i
  for i in $(seq 1 60); do
    if node 'KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl get pods -n default --no-headers 2>/dev/null | awk "{print \$3}" | grep -qv -e Running -e Completed' ; then
      sleep 5
    else
      return 0
    fi
  done
  return 1
}

cmd_seed() {
  prepare
  net_up
  start_squid aperto
  start_registry
  start_node
  log "Seed: installazione col proxy aperto, senza mirror"
  run_install --http-proxy "${PROXY_URL}" --no-proxy "${REGISTRY}"
  seed_registry
  docker rm -f "${NODE}" >/dev/null 2>&1 || true
}

cmd_verify() {
  prepare
  net_up
  start_squid chiuso
  docker inspect "${REGISTRY}" >/dev/null 2>&1 || start_registry
  start_node

  log "1. La rete è davvero chiusa"
  if node 'curl -sS --noproxy "*" -m 8 -o /dev/null https://get.k3s.io' >/dev/null 2>&1; then
    ko "il nodo raggiunge Internet senza proxy: la prova non vale"
    return 1
  fi
  ok "nessuna uscita diretta dal nodo"
  local code
  code="$(node "curl -s -o /dev/null -m 15 -w '%{http_code}' -x ${PROXY_URL} https://get.k3s.io" || true)"
  [ "${code}" = "200" ] && ok "get.k3s.io raggiungibile solo attraverso il proxy" || ko "get.k3s.io via proxy: HTTP ${code}"
  code="$(node "curl -s -o /dev/null -m 15 -w '%{http_code}' -x ${PROXY_URL} https://ghcr.io/v2/" 2>/dev/null || true)"
  case "${code}" in
    200|401) ko "il proxy lascia passare ghcr.io: le immagini potrebbero non venire dal mirror" ;;
    *) ok "il proxy nega i registry (ghcr.io: ${code:-nessuna risposta})" ;;
  esac

  log "2. Il preflight dice cosa non risponde (mirror sbagliato: nessuna modifica)"
  local out rc=0
  out="$(run_install --http-proxy "${PROXY_URL}" --registry-mirror http://gs-nowhere:5000 2>&1)" || rc=$?
  if [ "${rc}" -ne 0 ] && printf '%s' "${out}" | grep -q "mirror delle immagini" && printf '%s' "${out}" | grep -q "non risponde"; then
    ok "preflight: mirror irraggiungibile indicato, installazione ferma (rc=${rc})"
  else
    ko "preflight non ha fermato un mirror irraggiungibile (rc=${rc}): ${out}"
  fi
  if node 'command -v k3s' >/dev/null 2>&1; then
    ko "k3s installato nonostante il preflight fallito"
  else
    ok "nessuna modifica al sistema dopo il preflight fallito"
  fi

  log "3. Installazione: proxy e mirror, nessuna uscita diretta"
  : >"${STATE}/install.log"
  if run_install --http-proxy "${PROXY_URL}" --no-proxy "${REGISTRY}" --registry-mirror "${MIRROR_URL}" \
    2>&1 | tee "${STATE}/install.log" | tail -30; then
    :
  fi
  if grep -q "GitStack installato" "${STATE}/install.log"; then
    ok "install.sh completato"
  else
    ko "install.sh non è arrivato in fondo (log: ${STATE}/install.log)"
    return 1
  fi
  if wait_pods_ready; then ok "tutti i pod Running/Completed"; else ko "pod non pronti"; fi
  local ip
  ip="$(node_ip)"
  code="$(node "curl -s -o /dev/null -m 15 -w '%{http_code}' --cacert /etc/gitstack/tls/ca.crt https://${ip}/api/healthz" || true)"
  [ "${code}" = "200" ] && ok "https://${ip}/api/healthz: 200" || ko "healthz: HTTP ${code}"

  log "4. Da dove è arrivato ogni byte"
  docker exec "${SQUID}" cat /var/log/squid/access.log >"${STATE}/squid-access.log" 2>/dev/null || true
  local host
  for host in get.k3s.io github.com get.helm.sh; do
    if grep -q "${host}" "${STATE}/squid-access.log"; then ok "scaricato attraverso il proxy: ${host}"; else ko "nessuna richiesta a ${host} nel log del proxy"; fi
  done
  if grep -E "TCP_DENIED" "${STATE}/squid-access.log" | grep -qE "ghcr.io|docker.io|registry.k8s.io|quay.io"; then
    ko "k3s ha provato a scaricare immagini dai registry originali (negate dal proxy)"
  else
    ok "nessuna richiesta di immagini ai registry originali"
  fi
  local pulls
  pulls="$(docker logs "${REGISTRY}" 2>&1 | grep -c 'GET /v2/.*/manifests/' || true)"
  if [ "${pulls}" -gt 0 ]; then ok "il mirror ha servito ${pulls} manifest"; else ko "il mirror non ha servito niente"; fi
  node 'cat /etc/rancher/k3s/registries.yaml' >"${STATE}/registries.yaml"
  grep -q '"\*"' "${STATE}/registries.yaml" && ok "registries.yaml con endpoint \"*\"" || ko "registries.yaml senza endpoint \"*\""
  node 'cat /etc/systemd/system/k3s.service.d/10-gitstack-network.conf' | grep -q "HTTP_PROXY=${PROXY_URL}" \
    && ok "drop-in di proxy per k3s" || ko "drop-in di proxy mancante"
  node 'KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl -n default get deploy gitstack-core -o jsonpath="{.spec.template.spec.containers[0].env}"' \
    | grep -q "${PROXY_URL}" && ok "i pod di core hanno HTTP_PROXY" || ko "core senza HTTP_PROXY (chart senza proxy.*? vedi GIT-207)"

  log "5. gitstack config set network dopo l'installazione"
  if node 'gitstack config set network --no-proxy gs-registry,extra.corp.test' 2>&1 | tee "${STATE}/config-set.log" | tail -15; then
    :
  fi
  if grep -q "Rete aziendale aggiornata" "${STATE}/config-set.log"; then ok "config set network applicato"; else ko "config set network fallito"; fi
  node 'grep -q extra.corp.test /etc/systemd/system/k3s.service.d/10-gitstack-network.conf' && ok "no_proxy aggiornato nel drop-in" || ko "drop-in non aggiornato"
  node 'grep -q extra.corp.test /etc/gitstack/config.yaml' && ok "config.yaml aggiornato" || ko "config.yaml non aggiornato"
  code="$(node "curl -s -o /dev/null -m 15 -w '%{http_code}' --cacert /etc/gitstack/tls/ca.crt https://${ip}/api/healthz" || true)"
  [ "${code}" = "200" ] && ok "healthz dopo il riavvio controllato: 200" || ko "healthz dopo config set network: HTTP ${code}"

  printf '\ntest-network: %s ok, %s falliti\n' "${PASS}" "${FAILN}"
  [ "${FAILN}" -eq 0 ]
}

case "${1:-}" in
  seed) cmd_seed ;;
  verify) cmd_verify ;;
  all)
    trap down EXIT
    cmd_seed
    cmd_verify
    ;;
  down) down ;;
  *)
    echo "uso: $0 all|seed|verify|down" >&2
    exit 2
    ;;
esac
