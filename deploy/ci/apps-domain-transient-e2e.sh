#!/usr/bin/env bash
# GIT-559: prova riproducibile del transitorio fra l'helm upgrade con i valori delle app e l'emissione del
# certificato wildcard (gitstack upgrade --enable-apps --apps-domain applica il dominio DOPO upgrade.Run).
#
# Usa un cluster k3d usa-e-getta con il Traefik di k3s e questi riferimenti reali:
#   - i template del chart (tlsstore.yaml, ingress.yaml, ingress-apps-fallback.yaml, tls-copy-secret.yaml)
#     resi con helm (alpine/helm in Docker) prima (apps spente) e dopo (apps accese) l'upgrade;
#   - deploy/gitstack-tls.sh vero (ensure --apps-domain), con un shim `k3s kubectl` -> kubectl.
# Non installa i servizi di GitStack (core/gateway/...): la loro readiness (/readyz) non dipende dal Secret TLS
# (nessun probe legge il certificato), quindi qui si misura solo cio' che passa da Traefik: il certificato
# presentato per il nome di GitStack e per un nome di app, e il codice HTTP, prima / durante / dopo.
#
# Uso: deploy/ci/apps-domain-transient-e2e.sh        (richiede docker, k3d, kubectl, openssl, curl)
# Esce 0 solo se ogni asserzione e' vera; stampa una riga ok/ko per controllo.
set -uo pipefail
export MSYS_NO_PATHCONV=1

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLUSTER="${CLUSTER:-gs559}"
PORT="${PORT:-18443}"
HELM_IMAGE="${HELM_IMAGE:-alpine/helm:3.16.2}"
HOST_MAIN="gitstack.test"
APPS_DOMAIN="apps.example.lan"
HOST_APP="demo-repo-owner.${APPS_DOMAIN}"
WORK="$(mktemp -d)"
KO=0

ok() { printf 'ok  %s\n' "$*"; }
ko() { printf 'KO  %s\n' "$*"; KO=$((KO + 1)); }
cleanup() {
  [ -z "${LOOP_PID:-}" ] || kill "${LOOP_PID}" 2>/dev/null
  if [ "${KEEP_CLUSTER:-0}" != "1" ]; then k3d cluster delete "${CLUSTER}" >/dev/null 2>&1; fi
  rm -rf "${WORK}"
}
trap cleanup EXIT

for c in docker k3d kubectl openssl curl; do command -v "$c" >/dev/null || { echo "manca $c"; exit 2; }; done

# shim: lo script usa `k3s kubectl`.
mkdir -p "${WORK}/bin"
printf '#!/bin/sh\nshift\nexec kubectl "$@"\n' >"${WORK}/bin/k3s"
chmod +x "${WORK}/bin/k3s"
# gitstack-tls vuole root: fuori da Linux/root (per esempio Git Bash) si finge solo `id -u` = 0.
if [ "$(id -u)" != "0" ]; then
  printf '#!/bin/sh
if [ "$1" = "-u" ]; then echo 0; else exec /usr/bin/id "$@"; fi
' >"${WORK}/bin/id"
  chmod +x "${WORK}/bin/id"
fi
export PATH="${WORK}/bin:${PATH}"

echo "==> Cluster k3d ${CLUSTER} (porta ${PORT})"
k3d cluster delete "${CLUSTER}" >/dev/null 2>&1
k3d cluster create "${CLUSTER}" ${K3S_IMAGE:+--image "${K3S_IMAGE}"} -p "${PORT}:443@loadbalancer" --wait --timeout 400s >/dev/null || { echo "k3d cluster create fallito"; exit 2; }
KCFG="${WORK}/kubeconfig"
k3d kubeconfig get "${CLUSTER}" >"${KCFG}"
# kubectl.exe (Windows) vuole un percorso Windows; su Linux cygpath non esiste.
command -v cygpath >/dev/null 2>&1 && KCFG="$(cygpath -m "${KCFG}")"
export KUBECONFIG="${KCFG}"
for i in $(seq 1 240); do
  kubectl get crd tlsstores.traefik.io tlsoptions.traefik.io ingressroutes.traefik.io >/dev/null 2>&1 && break
  sleep 2
done
kubectl get crd tlsstores.traefik.io >/dev/null 2>&1 || { echo "CRD di Traefik assenti"; kubectl get crd 2>&1 | head -20; kubectl get pods -A 2>&1 | head -20; exit 2; }

render() { # $1 = true|false (apps.enabled)
  local extra=() show=(-s templates/tlsstore.yaml -s templates/ingress.yaml)
  # La route di riserva esiste solo con le app accese.
  [ "$1" = "true" ] && extra=(--set "apps.domain=${APPS_DOMAIN}") && show+=(-s templates/ingress-apps-fallback.yaml)
  docker run --rm -v "$(cd "${ROOT}" && pwd -W 2>/dev/null || pwd):/src:ro" "${HELM_IMAGE}" template gs /src/deploy/gitstack \
    --namespace default --set ingress.enabled=true --set ingress.tls.enabled=true --set ingress.tls.secretName=gs-tls \
    --set "ingress.host=${HOST_MAIN}" --set web.enabled=true --set "apps.enabled=$1" "${extra[@]}" \
    "${show[@]}"
}
WORKP="${WORK}"
command -v cygpath >/dev/null 2>&1 && WORKP="$(cygpath -m "${WORK}")" # openssl.exe vuole percorsi Windows
tls() { GITSTACK_TLS_DIR="${WORKP}/tls" "${ROOT}/deploy/gitstack-tls.sh" "$@"; }

# Cio' che Traefik presenta per un nome: SAN del certificato (vuoto se la connessione fallisce).
sans() { # $1 = nome
  echo | openssl s_client -connect "127.0.0.1:${PORT}" -servername "$1" 2>/dev/null |
    openssl x509 -noout -ext subjectAltName 2>/dev/null | tr -d ' \r\n'
}
code() { curl -sk -o /dev/null -m 5 -w '%{http_code}' --resolve "$1:${PORT}:127.0.0.1" "https://$1:${PORT}/" 2>/dev/null; }

echo "==> Stato iniziale: apps spente, certificato senza wildcard"
tls ensure --names "${HOST_MAIN}" --release gs --namespace default --kubeconfig "${KCFG}" || { echo "gitstack-tls ensure fallito"; exit 2; }
kubectl apply -f - >/dev/null <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata: {name: gs-web}
spec:
  replicas: 1
  selector: {matchLabels: {app: gs-web}}
  template:
    metadata: {labels: {app: gs-web}}
    spec:
      containers: [{name: w, image: traefik/whoami, args: ["--port=8080"], ports: [{containerPort: 8080}]}]
---
apiVersion: v1
kind: Service
metadata: {name: gs-web}
spec:
  selector: {app: gs-web}
  ports: [{port: 8080, targetPort: 8080}]
EOF
kubectl rollout status deploy/gs-web --timeout=240s >/dev/null || { echo "backend non pronto"; exit 2; }
render false | kubectl apply -f - >/dev/null || { echo "apply 'prima' fallito"; exit 2; }

wait_for() { # $1 = nome, $2 = testo atteso nei SAN
  for i in $(seq 1 60); do
    case "$(sans "$1")" in *"$2"*) return 0 ;; esac
    sleep 1
  done
  return 1
}
wait_for "${HOST_MAIN}" "DNS:${HOST_MAIN}" || { echo "Traefik non serve il certificato iniziale"; exit 2; }
BASE_CODE="$(code "${HOST_MAIN}")"
[ "${BASE_CODE}" = "200" ] && ok "prima: ${HOST_MAIN} risponde 200 con il certificato di GitStack" || ko "prima: ${HOST_MAIN} risponde ${BASE_CODE}"
case "$(sans "${HOST_MAIN}")" in *"*.${APPS_DOMAIN}"*) ko "prima: il wildcard c'e' gia'" ;; *) ok "prima: nessun SAN wildcard *.${APPS_DOMAIN}" ;; esac

# Sonda continua sul nome di GitStack per tutto il transitorio: una riga per campione (codice, SAN ok/ko).
(
  while :; do
    c="$(code "${HOST_MAIN}")"
    case "$(sans "${HOST_MAIN}")" in *"DNS:${HOST_MAIN}"*) s=cert-ok ;; *) s=cert-KO ;; esac
    printf '%s %s\n' "${c}" "${s}" >>"${WORK}/main.log"
    sleep 0.2
  done
) &
LOOP_PID=$!

echo "==> helm upgrade simulato: chart reso con apps accese (TLSStore nel namespace certs, copia del Secret, route di riserva)"
kubectl delete tlsstore default -n default >/dev/null 2>&1 # il chart toglie il vecchio TLSStore del namespace del release
kubectl create namespace gs-certs >/dev/null 2>&1
# tls-copy-secret.yaml: la copia nasce da lookup del Secret del release, quindi con il certificato di prima.
kubectl -n gs-certs create secret tls gs-tls --cert="${WORKP}/tls/server.crt" --key="${WORKP}/tls/server.key" >/dev/null
render true | kubectl apply -f - >/dev/null || ko "apply 'dopo' fallito"
sleep 5
echo "==> Intervallo fra helm upgrade e certificato wildcard"
INT_CODE="$(code "${HOST_MAIN}")"
[ "${INT_CODE}" = "200" ] && ok "intervallo: ${HOST_MAIN} risponde 200" || ko "intervallo: ${HOST_MAIN} risponde ${INT_CODE}"
APP_SANS="$(sans "${HOST_APP}")"
case "${APP_SANS}" in *"*.${APPS_DOMAIN}"*) ko "intervallo: il wildcard e' gia' presentato (non e' stato misurato il prima)" ;; *) ok "intervallo: ${HOST_APP} presenta il certificato di prima (${APP_SANS:-nessuno}), senza wildcard" ;; esac
APP_CODE="$(code "${HOST_APP}")"
echo "    ${HOST_APP}: HTTP ${APP_CODE} (route di riserva del chart, nessuna app ancora creata)"

echo "==> gitstack-tls ensure --apps-domain ${APPS_DOMAIN} (deploy/gitstack-tls.sh vero)"
T0="$(date +%s)"
tls ensure --apps-domain "${APPS_DOMAIN}" --release gs --namespace default --kubeconfig "${KCFG}" 2>&1 | sed 's/^/    /'
[ "${PIPESTATUS[0]}" = "0" ] && ok "gitstack-tls ensure --apps-domain: exit 0" || ko "gitstack-tls ensure --apps-domain fallito"
if wait_for "${HOST_APP}" "DNS:*.${APPS_DOMAIN}"; then ok "dopo: ${HOST_APP} presenta il wildcard *.${APPS_DOMAIN} dopo $(($(date +%s) - T0)) s"; else ko "dopo: il wildcard non viene presentato entro 60 s"; fi
case "$(sans "${HOST_MAIN}")" in *"DNS:${HOST_MAIN}"*) ok "dopo: ${HOST_MAIN} presenta ancora il suo nome" ;; *) ko "dopo: ${HOST_MAIN} senza il suo nome nei SAN" ;; esac
[ "$(code "${HOST_MAIN}")" = "200" ] && ok "dopo: ${HOST_MAIN} risponde 200" || ko "dopo: ${HOST_MAIN} non risponde 200"

sleep 1
kill "${LOOP_PID}" 2>/dev/null
LOOP_PID=""
TOT="$(wc -l <"${WORK}/main.log")"
BAD="$(grep -vc '^200 cert-ok$' "${WORK}/main.log")"
echo "==> Sonda continua su ${HOST_MAIN}: ${TOT} campioni, ${BAD} diversi da «200 cert-ok»"
sort "${WORK}/main.log" | uniq -c | sed 's/^/    /'
[ "${TOT}" -gt 10 ] && ok "la sonda ha raccolto ${TOT} campioni" || ko "troppi pochi campioni (${TOT})"
[ "${BAD}" = "0" ] && ok "nessun campione con codice o certificato errato durante tutto il transitorio" || ko "${BAD} campioni anomali durante il transitorio"

echo
[ "${KO}" = "0" ] && { echo "TUTTO OK"; exit 0; }
echo "${KO} controlli ko"
exit 1
