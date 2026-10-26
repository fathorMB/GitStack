#!/usr/bin/env bash
# Test dell'hardening del chart Helm (F8, ASVS V1.2.2, V1.14.1, V1.14.5; GIT-234):
# NetworkPolicy default-deny con allow espliciti, securityContext di ogni
# container e credenziali NATS. Renderizza il chart con `helm template`
# (nessun cluster, nessuna rete).
# Uso: ./deploy/tests/chart_hardening_test.sh
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART="${HERE}/../gitstack"

command -v helm >/dev/null 2>&1 || { echo "ERRORE: helm non trovato nel PATH" >&2; exit 2; }

PASS=0
FAILN=0
OUT=""
ok() { PASS=$((PASS + 1)); }
bad() { FAILN=$((FAILN + 1)); printf 'FALLITO: %s\n' "$1"; }
expect_eq() { if [ "$2" = "$3" ]; then ok; else bad "$1 (atteso '$3', trovato '$2')"; fi; }
expect_has() { case "$2" in *"$3"*) ok ;; *) bad "$1: manca '$3'" ;; esac; }
expect_lacks() { case "$2" in *"$3"*) bad "$1: non doveva esserci '$3'" ;; *) ok ;; esac; }
# expect_line_has / expect_line_lacks NOME TESTO RIGA: la riga intera (senza rientro),
# non una sottostringa: GITSTACK_GIT_REGISTRY non combacia con GITSTACK_GIT_REGISTRY_*.
expect_line_has() { if printf '%s' "$2" | grep -Eq "^[[:space:]]*$3\$"; then ok; else bad "$1: manca la riga '$3'"; fi; }
expect_line_lacks() { if printf '%s' "$2" | grep -Eq "^[[:space:]]*$3\$"; then bad "$1: non doveva esserci la riga '$3'"; else ok; fi; }

render() { helm template gitstack "${CHART}" "$@" | tr -d '\r'; }
# count PATTERN: righe del rendering che combaciano (grep -E).
count() { printf '%s\n' "${OUT}" | grep -c -E "$1" || true; }
# doc KIND NAME: il documento YAML di una risorsa (il testo fra due '---').
doc() {
  printf '%s\n' "${OUT}" | awk -v kind="$1" -v name="$2" '
    BEGIN { RS = "\n---\n" }
    $0 ~ "(^|\n)kind: " kind "\n" && $0 ~ "\n  name: " name "\n" && !done { print; done = 1 }'
}

# ---------------------------------------------------------------------------
echo "== NetworkPolicy and securityContext are rendered"
OUT="$(render)" || { bad "helm template di default fallito"; exit 1; }

for n in default-deny allow-dns web gateway core identity git nats postgres; do
  d="$(doc NetworkPolicy "gitstack-${n}")"
  if [ -n "${d}" ]; then ok; else bad "NetworkPolicy gitstack-${n} non renderizzata"; fi
done
d="$(doc NetworkPolicy gitstack-default-deny)"
expect_has "default-deny" "${d}" "- Ingress"
expect_has "default-deny" "${d}" "- Egress"
expect_lacks "default-deny" "${d}" "ingress:"
expect_lacks "default-deny" "${d}" "egress:"

# Nessuna policy lascia entrare da "qualunque posto" senza porte, tranne SSH.
d="$(doc NetworkPolicy gitstack-nats)"
expect_has "nats ingress da core" "${d}" "app.kubernetes.io/name: core"
expect_has "nats ingress da git" "${d}" "app.kubernetes.io/name: git"
expect_lacks "nats non da gateway" "${d}" "app.kubernetes.io/name: gateway"
d="$(doc NetworkPolicy gitstack-postgres)"
expect_has "postgres da core" "${d}" "app.kubernetes.io/name: core"
expect_has "postgres da identity" "${d}" "app.kubernetes.io/name: identity"
expect_lacks "postgres non da gateway" "${d}" "app.kubernetes.io/name: gateway"
d="$(doc NetworkPolicy gitstack-identity)"
expect_lacks "identity non da web" "${d}" "app.kubernetes.io/name: web"
d="$(doc NetworkPolicy gitstack-core)"
expect_has "core: uscita verso nats" "${d}" "app.kubernetes.io/name: nats"

# securityContext: ogni container (init compresi) ha le stesse quattro
# garanzie; ogni pod ha il profilo seccomp.
IMAGES="$(count '^ +image: ')"
if [ "${IMAGES}" -ge 8 ]; then ok; else bad "troppo pochi container nel rendering (${IMAGES})"; fi
expect_eq "allowPrivilegeEscalation: false su ogni container" "$(count '^ +allowPrivilegeEscalation: false$')" "${IMAGES}"
expect_eq "drop ALL su ogni container" "$(count '^ +- ALL$')" "${IMAGES}"
expect_eq "readOnlyRootFilesystem dichiarato su ogni container" "$(count '^ +readOnlyRootFilesystem: (true|false)$')" "${IMAGES}"
expect_lacks "nessun container privilegiato" "${OUT}" "privileged: true"
PODS="$(count '^      securityContext:$')"
expect_eq "seccomp RuntimeDefault su ogni pod" "$(count '^ +type: RuntimeDefault$')" "${PODS}"
if [ "${PODS}" -ge 7 ]; then ok; else bad "pod con securityContext: ${PODS}, attesi almeno 7"; fi
if [ "$(count '^ +readOnlyRootFilesystem: true$')" -ge 5 ]; then ok; else bad "filesystem radice in sola lettura troppo raro"; fi
# I servizi Go girano come utente non root numerico.
for c in gateway core identity git web; do
  d="$(doc Deployment "gitstack-${c}")"
  expect_has "${c}: runAsNonRoot" "${d}" "runAsNonRoot: true"
done

# I pod di prova che si fingono core (CI k3d, e2e) devono ricevere anche
# allow-dns, che seleziona per part-of: senza quella label il DNS è chiuso.
REPO="$(cd "${HERE}/../.." && pwd)"
for f in .github/workflows/ci.yml deploy/test-vm/e2e/remote.sh; do
  probe_labels="$(grep -h 'app.kubernetes.io/name=core,' "${REPO}/${f}" | tr -d '')"
  expect_has "${f}: label di prova con part-of" "${probe_labels}" "app.kubernetes.io/part-of=gitstack"
done
expect_has "allow-dns seleziona per part-of" "$(doc NetworkPolicy gitstack-allow-dns)" "app.kubernetes.io/part-of: gitstack"

# Spenta, il chart non crea policy.
OUT="$(render --set networkPolicy.enabled=false)"
expect_eq "networkPolicy.enabled=false" "$(count '^kind: NetworkPolicy$')" "0"

# Con Postgres esterno: policy del pod client, uscita sulla porta del database.
OUT="$(render --set postgres.enabled=false --set postgres.external.host=db.example.com \
  --set postgres.external.database=gs --set postgres.external.username=gs \
  --set postgres.external.existingSecret=pg --set postgres.external.port=6432)"
d="$(doc NetworkPolicy gitstack-pgclient)"
expect_has "pgclient policy" "${d}" "port: 6432"
d="$(doc NetworkPolicy gitstack-core)"
expect_has "core: porta del database esterno" "${d}" "port: 6432"

# ---------------------------------------------------------------------------
echo "== NATS requires credentials"
OUT="$(render)" || { bad "helm template di default fallito"; exit 1; }

d="$(doc StatefulSet gitstack-nats)"
# $(NATS_USER) e $(NATS_PASSWORD) sono letterali: sintassi Kubernetes del manifest, non shell.
# shellcheck disable=SC2016
expect_has "nats --user" "${d}" '--user=$(NATS_USER)'
# shellcheck disable=SC2016
expect_has "nats --pass" "${d}" '--pass=$(NATS_PASSWORD)'
expect_has "nats env dal Secret" "${d}" "name: gitstack-nats-auth"

d="$(doc Secret gitstack-nats-auth)"
if [ -n "${d}" ]; then ok; else bad "Secret gitstack-nats-auth non renderizzato"; fi
expect_has "Secret NATS keep" "${d}" "helm.sh/resource-policy: keep"
expect_has "Secret NATS user" "${d}" "user: "
PW="$(printf '%s\n' "${d}" | sed -n 's/^  password: "\(.*\)"$/\1/p')"
if [ "${#PW}" -ge 32 ]; then ok; else bad "password NATS generata troppo corta (${#PW})"; fi

# Nessun client usa un URL senza credenziali.
expect_lacks "URL NATS senza credenziali" "${OUT}" "nats://gitstack-nats"
for c in core git; do
  d="$(doc Deployment "gitstack-${c}")"
  expect_has "${c}: NATS_USER" "${d}" "name: NATS_USER"
  expect_has "${c}: NATS_PASSWORD" "${d}" "name: NATS_PASSWORD"
  # $(NATS_USER) e $(NATS_PASSWORD) sono letterali: sintassi Kubernetes del manifest, non shell.
  # shellcheck disable=SC2016
  expect_has "${c}: URL con utente e password" "${d}" 'nats://$(NATS_USER):$(NATS_PASSWORD)@gitstack-nats:4222'
done
# Le variabili vanno dichiarate prima di essere espanse da $(VAR).
d="$(doc Deployment gitstack-core)"
first_user="$(printf '%s\n' "${d}" | grep -n 'name: NATS_USER' | head -n1 | cut -d: -f1)"
first_url="$(printf '%s\n' "${d}" | grep -n 'GITSTACK_CORE_NATS_URL' | head -n1 | cut -d: -f1)"
if [ -n "${first_user}" ] && [ -n "${first_url}" ] && [ "${first_user}" -lt "${first_url}" ]; then ok; else bad "core: NATS_USER deve precedere GITSTACK_CORE_NATS_URL"; fi
d="$(doc Deployment gitstack-git)"
first_user="$(printf '%s\n' "${d}" | grep -n 'name: NATS_USER' | head -n1 | cut -d: -f1)"
first_url="$(printf '%s\n' "${d}" | grep -n 'GITSTACK_GIT_NATS_URL' | head -n1 | cut -d: -f1)"
if [ -n "${first_user}" ] && [ -n "${first_url}" ] && [ "${first_user}" -lt "${first_url}" ]; then ok; else bad "git: NATS_USER deve precedere GITSTACK_GIT_NATS_URL"; fi

# Secret esistente: il chart non ne genera uno e i client usano quello.
OUT="$(render --set nats.auth.existingSecret=mio-nats)"
expect_eq "nessun Secret generato con existingSecret" "$(doc Secret gitstack-nats-auth)" ""
expect_has "client con il Secret dato" "$(doc Deployment gitstack-core)" "name: mio-nats"
expect_has "server con il Secret dato" "$(doc StatefulSet gitstack-nats)" "name: mio-nats"

# ---------------------------------------------------------------------------
echo "== One service secret per caller (F9)"
OUT="$(render)" || { bad "helm template di default fallito"; exit 1; }

d="$(doc Secret gitstack-service-secrets)"
if [ -n "${d}" ]; then ok; else bad "Secret gitstack-service-secrets non renderizzato"; fi
expect_has "service secrets keep" "${d}" "helm.sh/resource-policy: keep"
for k in gateway core git; do
  v="$(printf '%s\n' "${d}" | sed -n "s/^  ${k}: \"\(.*\)\"\$/\1/p")"
  if [ "${#v}" -ge 32 ]; then ok; else bad "segreto ${k} generato troppo corto (${#v})"; fi
done
# Il Secret condiviso storico non esiste più.
expect_eq "nessun Secret identity-service" "$(doc Secret gitstack-identity-service)" ""
expect_lacks "nessuna variabile condivisa storica" "${OUT}" "GITSTACK_IDENTITY_SERVICE_SECRET"

# Ogni servizio riceve solo le chiavi che gli servono: git non conosce quella
# del gateway (con cui si firma l'identità di un utente).
chk() {
  # chk SERVIZIO DEPLOYMENT CHIAVE yes|no
  if printf '%s\n' "$2" | grep -q "name: GITSTACK_SERVICE_SECRET_$3\$"; then
    if [ "$4" = yes ]; then ok; else bad "$1: $3 non doveva esserci"; fi
  else
    if [ "$4" = yes ]; then bad "$1: manca $3"; else ok; fi
  fi
}
for c in gateway core git identity; do
  d="$(doc Deployment "gitstack-${c}")"
  case "${c}" in
    gateway)  g=yes; o=no;  t=no ;;
    core)     g=yes; o=yes; t=yes ;;
    git)      g=no;  o=yes; t=yes ;;
    identity) g=yes; o=yes; t=yes ;;
  esac
  chk "${c}" "${d}" GATEWAY "${g}"
  chk "${c}" "${d}" CORE "${o}"
  chk "${c}" "${d}" GIT "${t}"
done
# gateway: solo la propria chiave (non conosce quelle di core e git)
d="$(doc Deployment gitstack-gateway)"
expect_has "gateway: chiave gateway dal Secret" "${d}" "name: gitstack-service-secrets"

# Il vecchio valore identity.serviceSecret.existingSecret (chart precedente a
# F9) non è più letto: nessun Deployment lo usa e NOTES.txt lo segnala. Il
# chart non si ferma, così un `gitstack upgrade` precedente a F9 funziona.
OUT="$(render --set identity.serviceSecret.existingSecret=vecchio)" || bad "il vecchio valore ferma helm template"
expect_lacks "il Secret vecchio non è usato" "${OUT}" "name: vecchio"
expect_has "Secret per chiamante generato lo stesso" "$(doc Secret gitstack-service-secrets)" "core: "

# Secret esistente: il chart non ne genera uno e i servizi usano quello.
OUT="$(render --set serviceSecrets.existingSecret=miei-segreti)"
expect_eq "nessun Secret generato con existingSecret" "$(doc Secret gitstack-service-secrets)" ""
for c in gateway core git identity; do
  expect_has "${c}: Secret dato" "$(doc Deployment "gitstack-${c}")" "name: miei-segreti"
done

# ---------------------------------------------------------------------------
# N3 (nodo singolo, installazione-e-deploy.md): con il volume degli allegati
# ReadWriteOnce il chart si ferma se core.replicaCount > 1; senza il volume più
# repliche sono ammesse. `render` passa l'output a tr e restituisce il codice di
# tr, quindi il codice di helm lo legge helm_rc.
echo "== N3: nodo singolo"
helm_rc() { helm template gitstack "${CHART}" "$@" >/dev/null 2>&1; }
if helm_rc --set core.replicaCount=2; then
  bad "nodo singolo: helm template non si ferma con core.replicaCount=2"
else
  ok
  expect_has "nodo singolo: messaggio del guard" "$(render --set core.replicaCount=2 2>&1)" \
    "core.attachments.enabled=true o ci.enabled=true richiedono core.replicaCount=1"
fi
# Il guard vale anche per i log CI (ci.enabled=true), pure senza allegati.
if helm_rc --set core.attachments.enabled=false --set ci.enabled=true --set core.replicaCount=2; then
  bad "nodo singolo: helm template non si ferma con ci.enabled=true e core.replicaCount=2"
else
  ok
  expect_has "nodo singolo: messaggio del guard con ci.enabled" \
    "$(render --set core.attachments.enabled=false --set ci.enabled=true --set core.replicaCount=2 2>&1)" \
    "ci.enabled=true richiedono core.replicaCount=1"
fi
OUT="$(render --set core.attachments.enabled=false --set core.replicaCount=3)" ||
  bad "nodo singolo: rendering con 3 repliche senza il volume degli allegati fallito"
expect_has "nodo singolo: più repliche senza il volume RWO" "$(doc Deployment gitstack-core)" "replicas: 3"

# Il PVC degli allegati è ReadWriteOnce e core gira con strategia Recreate.
OUT="$(render)"
expect_has "nodo singolo: PVC degli allegati ReadWriteOnce" \
  "$(doc PersistentVolumeClaim gitstack-attachments-data)" "- ReadWriteOnce"
d="$(doc Deployment gitstack-core)"
expect_has "nodo singolo: core con strategia Recreate" "${d}" "type: Recreate"
expect_has "nodo singolo: una sola replica di default" "${d}" "replicas: 1"
# Postgres e NATS sono a una sola replica: il chart non fa alta disponibilità
# (N3, dopo la v1).
expect_has "nodo singolo: Postgres con una sola replica" \
  "$(doc StatefulSet gitstack-postgres)" "replicas: 1"
expect_has "nodo singolo: NATS con una sola replica" \
  "$(doc StatefulSet gitstack-nats)" "replicas: 1"

echo "== GIT-286: registry OCI (/v2/)"
OUT="$(render --set ingress.enabled=true)" || { bad "helm template con ingress fallito"; exit 1; }
d="$(doc IngressRoute gitstack-registry)"
if [ -n "${d}" ]; then ok; else bad "IngressRoute gitstack-registry non renderizzata"; fi
expect_has "registry: /v2 al gateway" "${d}" "name: gitstack-gateway"
# shellcheck disable=SC2016 # i backtick sono la sintassi letterale della regola Traefik
expect_has "registry: prefisso /v2/" "${d}" 'PathPrefix(`/v2/`)'
expect_has "gateway: GITSTACK_REGISTRY_URL" "$(doc Deployment gitstack-gateway)" "GITSTACK_REGISTRY_URL"
expect_has "gateway: verso il servizio git" "$(doc NetworkPolicy gitstack-gateway)" "app.kubernetes.io/name: git"
expect_has "git: ingresso dal gateway" "$(doc NetworkPolicy gitstack-git)" "app.kubernetes.io/name: gateway"
d="$(doc Deployment gitstack-git)"
expect_line_lacks "git: registry acceso di default" "${d}" "- name: GITSTACK_GIT_REGISTRY"
expect_lacks "git: niente S3 di default" "${d}" "GITSTACK_GIT_REGISTRY_S3_BUCKET"
expect_lacks "git: niente ci-gateway di default" "${d}" "GITSTACK_GIT_CI_GATEWAY_URL"

OUT="$(render --set ingress.enabled=true --set git.registry.enabled=false)"
expect_eq "registry spento: nessuna IngressRoute" "$(count 'name: gitstack-registry$')" "0"
expect_lacks "registry spento: gateway senza GITSTACK_REGISTRY_URL" "$(doc Deployment gitstack-gateway)" "GITSTACK_REGISTRY_URL"
expect_line_has "registry spento: git lo spegne" "$(doc Deployment gitstack-git)" "- name: GITSTACK_GIT_REGISTRY"

OUT="$(render --set git.registry.s3.enabled=true --set git.registry.s3.bucket=img --set git.registry.s3.endpoint=http://minio:9000 \
  --set git.registry.s3.pathStyle=true --set git.registry.s3.existingSecret=s3keys \
  --set git.registry.ciGateway.url=http://gitstack-core:9090 --set git.registry.ciGateway.port=9090)"
d="$(doc Deployment gitstack-git)"
expect_has "S3: bucket" "${d}" 'value: "img"'
expect_has "S3: path style" "${d}" "GITSTACK_GIT_REGISTRY_S3_PATH_STYLE"
expect_has "S3: chiavi dal Secret" "${d}" 'name: "s3keys"'
expect_has "ci-gateway: URL" "${d}" "GITSTACK_GIT_CI_GATEWAY_URL"
expect_has "ci-gateway: porta aperta verso core" "$(doc NetworkPolicy gitstack-git)" "port: 9090"
if helm_rc --set git.registry.s3.enabled=true; then bad "S3 senza bucket: helm template doveva fermarsi"; else ok; fi

echo "== GIT-332: indirizzi del ci-gateway qualificati col namespace del rilascio"
OUT="$(helm template gitstack "${CHART}" --namespace prova-ns --set ci.enabled=true | tr -d '\r')" || { bad "helm template con ci.enabled fallito"; exit 1; }
d="$(doc Deployment gitstack-core)"
expect_has "ci: GATEWAY_URL con namespace" "${d}" 'value: "http://gitstack-core.prova-ns.svc:'
expect_has "ci: GATE_INTERNAL_ADDR con namespace" "${d}" 'value: "gitstack-core.prova-ns.svc:'

echo "== GIT-329: ci-gateway esposto ai runner esterni (spento per default)"
OUT="$(render --set ci.enabled=true --set ingress.tls.enabled=true --set ingress.tls.secretName=gitstack-tls)" || { bad "helm template con ci.enabled fallito"; exit 1; }
expect_eq "default: nessuna IngressRoute del ci-gateway" "$(count 'name: gitstack-ci-gateway$')" "0"
expect_lacks "default: core senza GITSTACK_CORE_CI_EXTERNAL_ADDR" "$(doc Deployment gitstack-core)" "GITSTACK_CORE_CI_EXTERNAL_ADDR"
expect_lacks "default: core senza porta ci-external" "$(doc Service gitstack-core)" "ci-external"
OUT="$(render --set ci.externalGateway.enabled=true --set ingress.tls.enabled=true --set ingress.tls.secretName=gitstack-tls)" || bad "helm template con externalGateway senza ci.enabled fallito"
expect_eq "externalGateway senza ci.enabled: niente IngressRoute" "$(count 'name: gitstack-ci-gateway$')" "0"
OUT="$(render --set ci.enabled=true --set ci.externalGateway.enabled=true --set ci.externalGateway.host=ci.example.test   --set ingress.tls.enabled=true --set ingress.tls.secretName=gitstack-tls)" || { bad "helm template con externalGateway fallito"; exit 1; }
d="$(doc IngressRoute gitstack-ci-gateway)"
if [ -n "${d}" ]; then ok; else bad "IngressRoute gitstack-ci-gateway non renderizzata"; fi
# shellcheck disable=SC2016 # i backtick sono la sintassi letterale della regola Traefik
expect_has "ci-gateway esterno: host dedicato" "${d}" 'Host(`ci.example.test`)'
expect_has "ci-gateway esterno: solo websecure" "${d}" "- websecure"
expect_has "ci-gateway esterno: TLS con il Secret dell'istanza" "${d}" "secretName: gitstack-tls"
expect_has "ci-gateway esterno: va a core, porta esterna" "${d}" "name: gitstack-core"
expect_has "ci-gateway esterno: porta 8082" "${d}" "port: 8082"
d="$(doc Deployment gitstack-core)"
expect_has "core: porta esterna" "${d}" "GITSTACK_CORE_CI_EXTERNAL_ADDR"
expect_has "core: URL pubblico" "${d}" 'value: "https://ci.example.test"'
expect_has "core: porta ci-external esposta" "$(doc Service gitstack-core)" "targetPort: ci-external"
expect_has "core: ingresso 8082 solo dal controller" "$(doc NetworkPolicy gitstack-core)" "port: 8082"
expect_lacks "pod dei job: la porta esterna resta chiusa" "$(doc NetworkPolicy ci-job-egress)" "8082"
OUT="$(render --set ci.enabled=true)" || { bad "helm template con ci.enabled fallito"; exit 1; }
expect_lacks "pod dei job: nessuna regola proxy di default" "$(doc NetworkPolicy ci-job-egress)" "10.0.0.5/32"
OUT="$(render --set ci.enabled=true --set 'ci.runner.proxyEgress[0].to[0].ipBlock.cidr=10.0.0.5/32' --set 'ci.runner.proxyEgress[0].ports[0].protocol=TCP' --set 'ci.runner.proxyEgress[0].ports[0].port=3128')" || { bad "helm template con proxyEgress fallito"; exit 1; }
d="$(doc NetworkPolicy ci-job-egress)"
expect_has "pod dei job: uscita verso il proxy" "${d}" "cidr: 10.0.0.5/32"
expect_has "pod dei job: porta del proxy" "${d}" "port: 3128"
expect_has "pod dei job: resta la regola generale" "${d}" "cidr: 0.0.0.0/0"
if helm_rc --set ci.enabled=true --set ci.externalGateway.enabled=true --set ingress.tls.enabled=true --set ingress.tls.secretName=t; then bad "externalGateway senza host: helm template doveva fermarsi"; else ok; fi
if helm_rc --set ci.enabled=true --set ci.externalGateway.enabled=true --set ci.externalGateway.host=ci.example.test; then bad "externalGateway senza TLS: helm template doveva fermarsi"; else ok; fi

echo "== GIT-285: immagine del runner, proxy e CA nei job"
OUT="$(helm template gitstack "${CHART}" --namespace prova-ns --set ci.enabled=true --set global.image.tag=sha-abc --set global.image.registry=reg.corp:5000 | tr -d '')" || { bad "helm template con ci.enabled fallito"; exit 1; }
d="$(doc Deployment gitstack-core)"
expect_has "runner: immagine di default dal registry e dal tag dei servizi" "${d}" 'value: "reg.corp:5000/fathormb/gitstack-ci-runner:sha-abc"'
expect_lacks "runner: senza CA aziendale nessun ConfigMap per i job" "${d}" "GITSTACK_CORE_CI_CA_CONFIGMAP"
OUT="$(helm template gitstack "${CHART}" --namespace prova-ns --set ci.enabled=true --set ci.runner.image=mirror/runner:1 | tr -d '')" || { bad "helm template con ci.runner.image fallito"; exit 1; }
expect_has "runner: ci.runner.image vince" "$(doc Deployment gitstack-core)" 'value: "mirror/runner:1"'
OUT="$(helm template gitstack "${CHART}" --namespace prova-ns --set ci.enabled=true --set proxy.https=http://proxy:3128 --set corporateCA.configMap=gitstack-corporate-ca | tr -d '')" || { bad "helm template con proxy e CA fallito"; exit 1; }
d="$(doc Deployment gitstack-core)"
expect_has "job: core conosce il ConfigMap della CA nel namespace dei job" "${d}" "GITSTACK_CORE_CI_CA_CONFIGMAP"
expect_has "job: core ha il proxy da passare ai pod" "${d}" "name: HTTPS_PROXY"
OUT="$(helm template gitstack "${CHART}" --namespace prova-ns | tr -d '')" || { bad "helm template di default fallito"; exit 1; }
expect_eq "CI spenta: nessuna PriorityClass gitstack-ci-job" "$(count 'metadata: {name: gitstack-ci-job}')" "0"
expect_eq "H11: PriorityClass dei servizi" "$(count 'metadata: {name: gitstack-services}')" "1"
expect_eq "H11: PriorityClass delle app" "$(count 'metadata: {name: gitstack-apps}')" "1"
expect_has "H11: core ha priorita servizi" "$(doc Deployment gitstack-core)" "priorityClassName: gitstack-services"
OUT="$(helm template gitstack "${CHART}" --namespace prova-ns --set ci.enabled=true | tr -d '')" || { bad "helm template con ci.enabled fallito"; exit 1; }
expect_eq "CI accesa: PriorityClass gitstack-ci-job" "$(count 'metadata: {name: gitstack-ci-job}')" "1"
expect_has "CI accesa: ResourceQuota dei posti" "$OUT" "name: ci-jobs"

echo "== GIT-383: core legge la CA interna (impronta per la guida dei tester)"
OUT="$(render)" || { bad "helm template di default fallito"; exit 1; }
d="$(doc Deployment gitstack-core)"
expect_lacks "CA: senza caConfigMap core non monta la CA" "${d}" "/etc/gitstack/ca"
expect_lacks "CA: senza caConfigMap nessun GITSTACK_CORE_CA_DIR" "${d}" "GITSTACK_CORE_CA_DIR"
expect_line_has "CA: senza HTTPS la modalita' e' none" "${d}" "value: none"
OUT="$(render --set ingress.tls.enabled=true --set ingress.tls.certResolver=le)" || { bad "helm template con letsencrypt fallito"; exit 1; }
d="$(doc Deployment gitstack-core)"
expect_lacks "CA: letsencrypt non monta la CA" "${d}" "name: ca"
expect_line_has "CA: letsencrypt e' custom" "${d}" "value: custom"
OUT="$(render --set ingress.tls.enabled=true --set ingress.tls.secretName=t --set ingress.tls.caConfigMap=gitstack-ca)" || { bad "helm template con caConfigMap fallito"; exit 1; }
d="$(doc Deployment gitstack-core)"
expect_line_has "CA: core monta la cartella intera" "${d}" "mountPath: /etc/gitstack/ca"
expect_line_has "CA: core legge GITSTACK_CORE_CA_DIR" "${d}" "value: /etc/gitstack/ca"
expect_has "CA: il ConfigMap e' quello dell'installer" "${d}" "name: gitstack-ca"
expect_line_has "CA: ConfigMap optional" "${d}" "optional: true"
expect_lacks "CA: niente subPath (un rinnovo arriva senza riavvio)" "${d}" "subPath"
expect_lacks "CA: con la CA interna nessun GITSTACK_CORE_CA_MODE" "${d}" "GITSTACK_CORE_CA_MODE"

echo
echo "Passati: ${PASS}  Falliti: ${FAILN}"
[ "${FAILN}" -eq 0 ]
