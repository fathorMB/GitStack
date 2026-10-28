#!/usr/bin/env bash
# Prova di Let's Encrypt per i domini propri delle app pubbliche (v3-c, GIT-459) con Pebble su k3d:
# un dominio con DNS verificato ottiene il certificato dal resolver di Traefik e risponde in HTTPS.
#
# Cosa prova: lo stesso resolver `letsencrypt` che l'installer scrive nella HelmChartConfig `traefik`
# (deploy/install.sh, write_traefik_manifest, con GITSTACK_ACME_CA_SERVER = Pebble) e una IngressRoute
# col `tls.certResolver` che costruisce core (apps.BuildDomainsRoute): emissione HTTP-01 sulla porta 80,
# poi certificato servito su 443 con l'emittente di Pebble. Non avvia core: la parte di core (stato
# del certificato, porta 80) è provata dai test Go di services/core/internal/apps.
#
# Servono docker, k3d, kubectl, curl e jq. Crea un cluster usa-e-getta e lo elimina.
#   deploy/tests/acme_pebble_k3d_test.sh
set -euo pipefail

cluster=${GITSTACK_K3D_CLUSTER:-gs-acme-test}
k3d_bin=${K3D_BIN:-k3d}
root=$(cd "$(dirname "$0")/../.." && pwd)
k3s_image=${K3S_IMAGE:-"rancher/k3s:$(tr -d '[:space:]' <"$root/deploy/k3s-version" | tr '+' '-')"}
pebble_ref=${PEBBLE_REF:-v2.7.0}
domain=${ACME_TEST_DOMAIN:-shop.acme-test.example}
work=$(mktemp -d)
created=false

cleanup() {
  if "$created"; then "$k3d_bin" cluster delete "$cluster" >/dev/null 2>&1 || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

ko() { echo "KO: $*"; exit 1; }
k() { kubectl --kubeconfig "$work/kubeconfig" "$@"; }

"$k3d_bin" version >/dev/null 2>&1 || ko "manca k3d"
if "$k3d_bin" cluster list -o json | grep -q "\"$cluster\""; then ko "cluster $cluster già esistente"; fi
"$k3d_bin" cluster create "$cluster" --image "$k3s_image" --wait --timeout 300s \
  --kubeconfig-update-default=false --kubeconfig-switch-context=false >/dev/null
created=true
"$k3d_bin" kubeconfig get "$cluster" >"$work/kubeconfig"

echo "attendo le CRD di Traefik"
for _ in $(seq 1 60); do
  if k get crd ingressroutes.traefik.io >/dev/null 2>&1; then break; fi
  sleep 3
done
k get crd ingressroutes.traefik.io >/dev/null 2>&1 || ko "CRD di Traefik assenti"
k wait --for=condition=Established crd/ingressroutes.traefik.io --timeout=60s >/dev/null

# --- Pebble (CA ACME di prova) e challtestsrv (DNS finto) in kube-system, accanto a Traefik --------
# Il certificato di Pebble ha tra i SAN `pebble`, e la CA di prova (minica) va data a Traefik con
# LEGO_CA_CERTIFICATES. Per questo caServer usa il nome corto `pebble` (stesso namespace di Traefik,
# si risolve col search domain del pod), non pebble.kube-system.svc, che non è nei SAN.
curl -fsSL "https://raw.githubusercontent.com/letsencrypt/pebble/${pebble_ref}/test/certs/pebble.minica.pem" -o "$work/minica.pem" || ko "download della CA di Pebble"
k -n kube-system create configmap pebble-ca --from-file=pebble.minica.pem="$work/minica.pem" >/dev/null
k apply -f - >/dev/null <<EOF
apiVersion: v1
kind: ConfigMap
metadata: {name: pebble-config, namespace: kube-system}
data:
  pebble-config.json: |
    {"pebble": {"listenAddress": "0.0.0.0:14000", "managementListenAddress": "0.0.0.0:15000",
      "certificate": "test/certs/localhost/cert.pem", "privateKey": "test/certs/localhost/key.pem",
      "httpPort": 80, "tlsPort": 443, "ocspResponderURL": "", "externalAccountBindingRequired": false}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: pebble, namespace: kube-system}
spec:
  replicas: 1
  selector: {matchLabels: {app: pebble}}
  template:
    metadata: {labels: {app: pebble}}
    spec:
      containers:
        - name: pebble
          image: ghcr.io/letsencrypt/pebble:${pebble_ref#v}
          args: ["-config", "/test/config/pebble-config.json", "-dnsserver", "challtestsrv.kube-system.svc:8053"]
          env: [{name: PEBBLE_VA_NOSLEEP, value: "1"}, {name: PEBBLE_WFE_NONCEREJECT, value: "0"}]
          volumeMounts: [{name: cfg, mountPath: /test/config}]
        - name: challtestsrv
          image: ghcr.io/letsencrypt/pebble-challtestsrv:${pebble_ref#v}
          args: ["-http01", "", "-https01", "", "-tlsalpn01", "", "-management", ":8055", "-dns01", ":8053"]
      volumes: [{name: cfg, configMap: {name: pebble-config}}]
---
apiVersion: v1
kind: Service
metadata: {name: pebble, namespace: kube-system}
spec: {selector: {app: pebble}, ports: [{name: acme, port: 14000}]}
---
apiVersion: v1
kind: Service
metadata: {name: challtestsrv, namespace: kube-system}
spec: {selector: {app: pebble}, ports: [{name: mgmt, port: 8055}, {name: dns, port: 8053, protocol: UDP}]}
EOF
k -n kube-system rollout status deployment/pebble --timeout=180s >/dev/null || ko "Pebble non parte"

# --- Resolver ACME di Traefik: quello dell'installer, con Pebble come server ACME -----------------
# Stessa forma di write_traefik_manifest (certificatesResolvers.letsencrypt.acme, HTTP-01 su `web`,
# archivio su volume persistente).
k apply -f - >/dev/null <<EOF
apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata: {name: traefik, namespace: kube-system}
spec:
  valuesContent: |-
    persistence: {enabled: true, path: /data, size: 128Mi}
    env:
      - {name: LEGO_CA_CERTIFICATES, value: /pebble/pebble.minica.pem}
    volumes:
      - {name: pebble-ca, mountPath: /pebble, type: configMap}
    certificatesResolvers:
      letsencrypt:
        acme:
          email: test@example.com
          caServer: https://pebble:14000/dir
          storage: /data/acme.json
          httpChallenge: {entryPoint: web}
EOF
echo "attendo che Traefik riparta col resolver"
sleep 10
k -n kube-system rollout status deployment/traefik --timeout=240s >/dev/null || ko "Traefik non riparte"

# --- App di prova e DNS finto: il dominio punta al Service di Traefik (porta 80 per la sfida) ------
tr_ip=$(k -n kube-system get svc traefik -o jsonpath='{.spec.clusterIP}')
[ -n "$tr_ip" ] || ko "Service traefik senza ClusterIP"
k create namespace app-acme >/dev/null
k -n app-acme apply -f - >/dev/null <<EOF
apiVersion: apps/v1
kind: Deployment
metadata: {name: app}
spec:
  replicas: 1
  selector: {matchLabels: {app: whoami}}
  template:
    metadata: {labels: {app: whoami}}
    spec:
      containers: [{name: whoami, image: docker.io/traefik/whoami:v1.10, ports: [{containerPort: 80}]}]
---
apiVersion: v1
kind: Service
metadata: {name: app}
spec: {selector: {app: whoami}, ports: [{port: 80}]}
---
# Come apps.BuildDomainsRoute: una regola per dominio, tls.certResolver = resolver condiviso.
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata: {name: app-domains}
spec:
  entryPoints: [websecure]
  routes: [{kind: Rule, match: "Host(\`${domain}\`)", services: [{name: app, port: 80}]}]
  tls: {certResolver: letsencrypt}
EOF
k -n app-acme rollout status deployment/app --timeout=180s >/dev/null || ko "l'app di prova non parte"
k -n kube-system run dnsset --restart=Never --image=docker.io/curlimages/curl:8.10.1 --command -- \
  curl -fsS -X POST http://challtestsrv.kube-system.svc:8055/add-a -d "{\"host\":\"${domain}\",\"addresses\":[\"${tr_ip}\"]}" >/dev/null
k -n kube-system wait --for=jsonpath='{.status.phase}'=Succeeded pod/dnsset --timeout=120s >/dev/null || ko "DNS finto non impostato"

# --- Attesa del certificato: emittente di Pebble sul nome del dominio, poi risposta HTTPS ---------
issuer=""
for _ in $(seq 1 40); do
  k -n kube-system delete pod probe --ignore-not-found >/dev/null
  k -n kube-system run probe --restart=Never --image=docker.io/alpine/openssl:latest --command -- sh -c \
    "echo | openssl s_client -connect traefik.kube-system.svc:443 -servername ${domain} 2>/dev/null | openssl x509 -noout -issuer -ext subjectAltName" >/dev/null
  k -n kube-system wait --for=jsonpath='{.status.phase}'=Succeeded pod/probe --timeout=60s >/dev/null 2>&1 || true
  issuer=$(k -n kube-system logs probe 2>/dev/null || true)
  case "$issuer" in *Pebble*"$domain"*) break ;; esac
  issuer=""
  sleep 6
done
if [ -z "$issuer" ]; then
  k -n kube-system logs deployment/traefik --tail=40 || true
  ko "nessun certificato di Pebble per $domain entro il tempo massimo"
fi
echo "ok: Traefik serve per $domain un certificato emesso da Pebble"

k -n kube-system delete pod probe --ignore-not-found >/dev/null
k -n kube-system run https --restart=Never --image=docker.io/curlimages/curl:8.10.1 --command -- \
  curl -fsS --cacert /dev/null -k --resolve "${domain}:443:${tr_ip}" "https://${domain}/" >/dev/null
k -n kube-system wait --for=jsonpath='{.status.phase}'=Succeeded pod/https --timeout=90s >/dev/null || ko "il dominio non risponde in HTTPS"
k -n kube-system logs pod/https | grep -qi '^Hostname:' || ko "la risposta HTTPS non è quella di whoami"
echo "ok: https://$domain risponde dall'app"

# ADR 0002: il Secret TLS non entra mai nel namespace dell'app.
if [ -n "$(k -n app-acme get secrets --field-selector type=kubernetes.io/tls -o name)" ]; then ko "Secret TLS nel namespace dell'app"; fi
echo "ok: nessun Secret TLS nel namespace dell'app"
