#!/usr/bin/env bash
# shellcheck disable=SC2015,SC2016,SC2034
# Prova ripetibile di N9a (GIT-206): install.sh in una rete aziendale chiusa.
#
#   deploy/test-network/run.sh all        # seed + verify
#   deploy/test-network/run.sh seed       # popola il registry di prova
#   deploy/test-network/run.sh verify     # la prova vera, senza uscita diretta
#   deploy/test-network/run.sh down       # ferma squid e registry (i dati restano)
#
# Che cosa usa:
#   la VM di prova di deploy/test-vm (Ubuntu Server 24.04, Hyper-V, systemd e
#   cgroup v2 veri: k3s non parte in un container su Docker Desktop/WSL2, che è
#   cgroup v1), riportata al checkpoint `clean` prima di ogni giro;
#   gs-squid     proxy squid in Docker sull'host: l'unica strada verso
#                Internet; in "verify" nega i registry delle immagini (ghcr.io,
#                docker.io, registry.k8s.io, ...);
#   gs-registry  registry:2 in Docker sull'host, il mirror delle immagini.
# La VM non ha uscita diretta: regole iptables in OUTPUT lasciano passare solo
# il proxy e il mirror dell'host (porte 3128 e 5000), il cluster e il loopback.
# Gli indirizzi sono IP (nessun DNS verso l'esterno).
#
# "seed": installa una volta col proxy aperto, raccoglie le immagini che k3s ha
# davvero scaricato e le copia nel registry. "verify": VM nuova, installa con
# --http-proxy e --registry-mirror, col proxy che nega i registry: le immagini
# possono arrivare solo dal mirror. Esito: righe OK/KO e codice di uscita; il
# log di squid e quello del registry sono le prove.
#
# Richiede: Hyper-V con la VM di test (deploy/test-vm/README.md), PowerShell
# come amministratore (reset-vm.ps1), Docker (motore Linux), ssh, curl, go.
# Variabili: GITSTACK_TEST_VM_NAME, GITSTACK_TEST_VM_KEY (chiave privata SSH),
# GITSTACK_TEST_HOST_IP (IP dell'host visto dalla VM), GITSTACK_TEST_NO_RESET=1
# (non ripristina il checkpoint: solo per mettere a punto lo script).
set -euo pipefail
export MSYS_NO_PATHCONV=1

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"
winpath() {
  if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi
}
SQUID=gs-squid
REGISTRY=gs-registry
SQUID_IMAGE="${SQUID_IMAGE:-ubuntu/squid:latest}"
REGISTRY_IMAGE="${REGISTRY_IMAGE:-registry:2}"
REGISTRY_HOST_PORT=5000
PROXY_PORT=3128
VM_NAME="${GITSTACK_TEST_VM_NAME:-gitstack-test-vm}"
VM_USER="${GITSTACK_TEST_VM_USER:-gitstack}"
VM_KEY="${GITSTACK_TEST_VM_KEY:-${HOME}/.ssh/gitstack_vm}"
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

ps_run() {
  powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$1"
}

HOST_IP=""
VM_IP=""

host_ip() {
  if [ -n "${GITSTACK_TEST_HOST_IP:-}" ]; then HOST_IP="${GITSTACK_TEST_HOST_IP}"; return 0; fi
  HOST_IP="$(ps_run "(Get-NetIPAddress -AddressFamily IPv4 | Where-Object InterfaceAlias -like '*Default Switch*' | Select-Object -First 1).IPAddress" | tr -d '\r\n ')"
  [ -n "${HOST_IP}" ] || { echo "IP dell'host sullo switch della VM non trovato (GITSTACK_TEST_HOST_IP)" >&2; return 1; }
}

vm_ssh() {
  ssh -i "${VM_KEY}" -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "${VM_USER}@${VM_IP}" "$@"
}

# vm CMD: eseguito come root sulla VM.
vm() {
  vm_ssh "sudo -n bash -c $(printf '%q' "$*")"
}

# IP della VM: MAC -> cache ARP dell'host (la cloud image non ha il demone KVP).
find_vm_ip() {
  local i out
  for i in $(seq 1 60); do
    out="$(ps_run "\$m=(Get-VMNetworkAdapter -VMName '${VM_NAME}').MacAddress; \$m=(\$m -replace '(..)(?=.)','\$1-'); Get-NetNeighbor -AddressFamily IPv4 | Where-Object { \$_.LinkLayerAddress -eq \$m -and \$_.State -ne 'Unreachable' } | ForEach-Object { \$_.IPAddress }" | tr -d '\r')"
    for VM_IP in ${out}; do
      if vm_ssh true >/dev/null 2>&1; then return 0; fi
    done
    sleep 5
  done
  echo "IP della VM non trovato" >&2
  return 1
}

reset_vm() {
  if [ "${GITSTACK_TEST_NO_RESET:-0}" != "1" ]; then
    log "Ripristino la VM '${VM_NAME}' al checkpoint clean (deploy/test-vm/reset-vm.ps1)"
    powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$(winpath "${ROOT}/deploy/test-vm/reset-vm.ps1")" \
      -VmName "${VM_NAME}" -VmUser "${VM_USER}" -SshPrivateKeyPath "$(winpath "${VM_KEY}")" >"${STATE}/reset-vm.log" 2>&1 \
      || { cat "${STATE}/reset-vm.log" >&2; return 1; }
  fi
  find_vm_ip
  log "VM: ${VM_IP}, host: ${HOST_IP}"
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
  docker create --name "${SQUID}" -p "0.0.0.0:${PROXY_PORT}:3128" "${SQUID_IMAGE}" >/dev/null
  docker cp "$(winpath "${STATE}/squid.conf")" "${SQUID}:/etc/squid/squid.conf"
  docker start "${SQUID}" >/dev/null
  local i
  for i in $(seq 1 30); do
    if docker logs "${SQUID}" 2>&1 | grep -qi "Accepting HTTP Socket connections"; then return 0; fi
    sleep 1
  done
  docker logs "${SQUID}" 2>&1 | tail -20 >&2
  echo "squid non parte" >&2
  return 1
}

start_registry() {
  docker volume create gs-registry-data >/dev/null
  docker rm -f "${REGISTRY}" >/dev/null 2>&1 || true
  docker run -d --name "${REGISTRY}" -p "0.0.0.0:${REGISTRY_HOST_PORT}:5000" \
    -v gs-registry-data:/var/lib/registry "${REGISTRY_IMAGE}" >/dev/null
  local i
  for i in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${REGISTRY_HOST_PORT}/v2/" >/dev/null 2>&1; then return 0; fi
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

# Chiude l'uscita diretta della VM: solo loopback, cluster, risposte e il
# proxy/mirror dell'host.
close_egress() {
  vm "
    set -e
    iptables -N GS-EGRESS 2>/dev/null || iptables -F GS-EGRESS
    iptables -A GS-EGRESS -o lo -j ACCEPT
    iptables -A GS-EGRESS -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    iptables -A GS-EGRESS -p tcp --sport 22 -j ACCEPT
    iptables -A GS-EGRESS -d 10.42.0.0/15 -j ACCEPT
    iptables -A GS-EGRESS -d ${VM_IP}/32 -j ACCEPT
    iptables -A GS-EGRESS -p udp --dport 67:68 -j ACCEPT
    iptables -A GS-EGRESS -d ${HOST_IP}/32 -p tcp -m multiport --dports ${PROXY_PORT},${REGISTRY_HOST_PORT} -j ACCEPT
    iptables -A GS-EGRESS -j REJECT
    iptables -C OUTPUT -j GS-EGRESS 2>/dev/null || iptables -I OUTPUT 1 -j GS-EGRESS
  "
}

prepare_vm() {
  # Il codice dell'installer, come sul server: la cartella deploy/ e il
  # binario gitstack costruito da questo ramo.
  tar -C "${ROOT}" -cf - deploy | vm_ssh "sudo -n rm -rf /opt/gitstack && sudo -n mkdir -p /opt/gitstack && sudo -n tar -C /opt/gitstack -xf -"
  vm_ssh "cat | sudo -n tee /opt/gitstack/gitstack >/dev/null && sudo -n chmod 0755 /opt/gitstack/gitstack" <"${STATE}/gitstack"
  close_egress
}

run_install() {
  vm "bash /opt/gitstack/deploy/install.sh \
    --chart-dir /opt/gitstack/deploy/gitstack --image-tag ${IMAGE_TAG} --host ${VM_IP} \
    --admin-binary /opt/gitstack/gitstack --admin-sha256 $(cat "${STATE}/gitstack.sha256") $*"
}

prepare() {
  need docker
  need curl
  need go
  need ssh
  need powershell.exe
  mkdir -p "${STATE}"
  docker pull "${SQUID_IMAGE}" >/dev/null
  docker pull "${REGISTRY_IMAGE}" >/dev/null
  build_admin
  host_ip
}

# Copia nel registry le immagini che k3s ha scaricato nel nodo di "seed".
seed_registry() {
  local refs ref path n=0
  refs="$(vm 'k3s ctr -n k8s.io images ls -q' | tr -d '\r' | grep -v '^sha256:' | grep -v '@' | sort -u)"
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
    if vm 'KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl get pods -n default --no-headers 2>/dev/null | awk "{print \$3}" | grep -qv -e Running -e Completed'; then
      sleep 5
    else
      return 0
    fi
  done
  return 1
}

cmd_seed() {
  prepare
  start_squid aperto
  start_registry
  reset_vm
  prepare_vm
  log "Seed: installazione col proxy aperto, senza mirror"
  run_install --http-proxy "http://${HOST_IP}:${PROXY_PORT}" --no-proxy "${HOST_IP}"
  seed_registry
}

cmd_verify() {
  prepare
  start_squid chiuso
  docker inspect "${REGISTRY}" >/dev/null 2>&1 || start_registry
  local proxy="http://${HOST_IP}:${PROXY_PORT}" mirror="http://${HOST_IP}:${REGISTRY_HOST_PORT}"
  reset_vm
  prepare_vm

  log "1. La rete è davvero chiusa"
  local code
  if vm 'curl -sS --noproxy "*" -m 8 -o /dev/null https://get.k3s.io' >/dev/null 2>&1; then
    ko "la VM raggiunge Internet senza proxy: la prova non vale"
    return 1
  fi
  ok "nessuna uscita diretta dalla VM"
  code="$(vm "curl -s -o /dev/null -m 20 -w '%{http_code}' -x ${proxy} https://get.k3s.io" || true)"
  [ "${code}" = "200" ] && ok "get.k3s.io raggiungibile solo attraverso il proxy" || ko "get.k3s.io via proxy: HTTP ${code}"
  code="$(vm "curl -s -o /dev/null -m 20 -w '%{http_code}' -x ${proxy} https://ghcr.io/v2/" 2>/dev/null || true)"
  case "${code}" in
    200|401) ko "il proxy lascia passare ghcr.io: le immagini potrebbero non venire dal mirror" ;;
    *) ok "il proxy nega i registry (ghcr.io: HTTP ${code:-nessuna risposta})" ;;
  esac

  log "2. Il preflight dice cosa non risponde (mirror sbagliato: nessuna modifica)"
  local out rc=0
  out="$(run_install --http-proxy "${proxy}" --registry-mirror "http://${HOST_IP}:5999" 2>&1)" || rc=$?
  if [ "${rc}" -ne 0 ] && printf '%s' "${out}" | grep -q "mirror delle immagini" && printf '%s' "${out}" | grep -qE "non risponde|risponde HTTP"; then
    ok "preflight: mirror irraggiungibile indicato, installazione ferma (rc=${rc})"
  else
    ko "preflight non ha fermato un mirror irraggiungibile (rc=${rc}): ${out}"
  fi
  if vm 'test -d /etc/gitstack'; then
    ko "il preflight fallito ha lasciato /etc/gitstack"
  else
    ok "nessuna modifica al sistema dopo il preflight fallito"
  fi

  log "3. Installazione: proxy e mirror, nessuna uscita diretta"
  # Le richieste di prima (le mie prove 1 e 2) non contano: si legge il log del
  # proxy solo da qui in poi.
  local log_from
  log_from="$(docker exec "${SQUID}" wc -l /var/log/squid/access.log | awk '{print $1}')"
  : >"${STATE}/install.log"
  run_install --http-proxy "${proxy}" --no-proxy "${HOST_IP}" --registry-mirror "${mirror}" 2>&1 | tee "${STATE}/install.log" | tail -30 || true
  if grep -q "GitStack installato" "${STATE}/install.log"; then
    ok "install.sh completato"
  else
    ko "install.sh non è arrivato in fondo (log: ${STATE}/install.log)"
    return 1
  fi
  if wait_pods_ready; then ok "tutti i pod Running/Completed"; else ko "pod non pronti"; fi
  code="$(vm "curl -s -o /dev/null -m 15 -w '%{http_code}' --cacert /etc/gitstack/tls/ca.crt https://${VM_IP}/api/healthz" || true)"
  [ "${code}" = "200" ] && ok "https://${VM_IP}/api/healthz: 200" || ko "healthz: HTTP ${code}"

  log "4. Da dove è arrivato ogni byte"
  docker exec "${SQUID}" tail -n "+$((log_from + 1))" /var/log/squid/access.log >"${STATE}/squid-access.log" 2>/dev/null || true
  # Nessuna richiesta del kubelet (porta 10250 dell'IP del nodo) al proxy.
  if grep -q "CONNECT ${VM_IP}:10250" "${STATE}/squid-access.log"; then
    ko "il traffico verso il kubelet passa dal proxy: l'IP del nodo manca in NO_PROXY"
  else
    ok "nessun traffico interno del nodo passa dal proxy"
  fi
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
  vm 'cat /etc/rancher/k3s/registries.yaml' >"${STATE}/registries.yaml"
  grep -q '"\*"' "${STATE}/registries.yaml" && ok "registries.yaml con endpoint \"*\"" || ko "registries.yaml senza endpoint \"*\""
  vm 'cat /etc/systemd/system/k3s.service.d/10-gitstack-network.conf' | grep -q "HTTP_PROXY=${proxy}" \
    && ok "drop-in di proxy per k3s" || ko "drop-in di proxy mancante"
  if grep -q '^proxy:' "${ROOT}/deploy/gitstack/values.yaml"; then
    vm 'KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl -n default get deploy gitstack-core -o jsonpath="{.spec.template.spec.containers[0].env}"' \
      | grep -q "${proxy}" && ok "i pod di core hanno HTTP_PROXY" || ko "core senza HTTP_PROXY"
  else
    log "Il chart di questo ramo non ha i valori proxy.* (GIT-207, N9b): HTTP_PROXY nei pod non verificato"
  fi

  log "5. gitstack config set network dopo l'installazione"
  vm "gitstack config set network --no-proxy ${HOST_IP},extra.corp.test" 2>&1 | tee "${STATE}/config-set.log" | tail -15 || true
  if grep -q "Rete aziendale aggiornata" "${STATE}/config-set.log"; then ok "config set network applicato"; else ko "config set network fallito"; fi
  vm 'grep -q extra.corp.test /etc/systemd/system/k3s.service.d/10-gitstack-network.conf' && ok "no_proxy aggiornato nel drop-in" || ko "drop-in non aggiornato"
  vm 'grep -q extra.corp.test /etc/gitstack/config.yaml' && ok "config.yaml aggiornato" || ko "config.yaml non aggiornato"
  code="$(vm "curl -s -o /dev/null -m 15 -w '%{http_code}' --cacert /etc/gitstack/tls/ca.crt https://${VM_IP}/api/healthz" || true)"
  [ "${code}" = "200" ] && ok "healthz dopo il riavvio controllato: 200" || ko "healthz dopo config set network: HTTP ${code}"

  printf '\ntest-network: %s ok, %s falliti\n' "${PASS}" "${FAILN}"
  [ "${FAILN}" -eq 0 ]
}

down() {
  docker rm -f "${SQUID}" "${REGISTRY}" >/dev/null 2>&1 || true
}

case "${1:-}" in
  seed) cmd_seed ;;
  verify) cmd_verify ;;
  all)
    cmd_seed
    cmd_verify
    ;;
  down) down ;;
  *)
    echo "uso: $0 all|seed|verify|down" >&2
    exit 2
    ;;
esac
