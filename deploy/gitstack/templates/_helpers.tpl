{{/*
Nome base del rilascio (troncato a 63 caratteri, limite dei nomi k8s).
*/}}
{{- define "gitstack.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Nome di un componente: "<release>-<componente>", es. "gitstack-gateway".
*/}}
{{- define "gitstack.componentName" -}}
{{- printf "%s-%s" (include "gitstack.fullname" .root) .component | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Label comuni a tutte le risorse del chart.
*/}}
{{- define "gitstack.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: gitstack
{{- end -}}

{{/*
Label comuni + selector di un componente. Uso: {{ include "gitstack.componentLabels" (dict "root" $ "component" "gateway") }}
*/}}
{{- define "gitstack.componentLabels" -}}
{{ include "gitstack.labels" .root }}
{{ include "gitstack.componentSelectorLabels" . }}
{{- end -}}

{{- define "gitstack.componentSelectorLabels" -}}
app.kubernetes.io/name: {{ .component }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
{{- end -}}

{{/*
Riferimento immagine completo di un componente: registry/repository:tag.
Uso: {{ include "gitstack.image" (dict "root" $ "component" .Values.gateway) }}
Risoluzione (registry più override per componente, criterio 7 di GIT-8):
- registry: component.image.registry, altrimenti global.image.registry
- tag: component.image.tag, altrimenti global.image.tag, altrimenti Chart.AppVersion
*/}}
{{- define "gitstack.image" -}}
{{- $comp := .component -}}
{{- $registry := $comp.image.registry | default .root.Values.global.image.registry -}}
{{- printf "%s/%s:%s" $registry $comp.image.repository (include "gitstack.imageTag" .) -}}
{{- end -}}

{{/*
Tag effettivo di un componente (stessa risoluzione di gitstack.image): quello
che legge `gitstack status` e che il gateway riporta in GET /v1/meta.
*/}}
{{- define "gitstack.imageTag" -}}
{{- $tag := .component.image.tag -}}
{{- if not $tag -}}
{{- $tag = .root.Values.global.image.tag -}}
{{- end -}}
{{- if not $tag -}}
{{- $tag = .root.Chart.AppVersion -}}
{{- end -}}
{{- $tag -}}
{{- end -}}

{{/*
Nome del Secret con la password Postgres usata da core, sia con il
Postgres bundle sia con quello esterno del cliente (D6):
- bundle (postgres.enabled=true): postgres.auth.existingSecret se
  impostato, altrimenti il Secret generato dal chart (chiave "password").
- esterno (postgres.enabled=false): postgres.external.existingSecret
  (obbligatorio in quel caso, chiave "password").
*/}}
{{- define "gitstack.postgres.secretName" -}}
{{- if .Values.postgres.enabled -}}
{{- .Values.postgres.auth.existingSecret | default (printf "%s-postgres" (include "gitstack.fullname" .)) -}}
{{- else -}}
{{- .Values.postgres.external.existingSecret -}}
{{- end -}}
{{- end -}}

{{/*
DSN Postgres usato da core (GITSTACK_CORE_DB_URL), come stringa Helm: la
password è iniettata a parte via env var (vedi templates/core/deployment.yaml)
per non finire in chiaro in un ConfigMap. Qui costruiamo la parte senza
password con un segnaposto "$(POSTGRES_PASSWORD)" espanso da Kubernetes
($(VAR) nei valori di env di un container, sintassi standard k8s).
*/}}
{{- define "gitstack.postgres.dsnTemplate" -}}
{{- if .Values.postgres.enabled -}}
{{- printf "postgres://%s:$(POSTGRES_PASSWORD)@%s-postgres:5432/%s?sslmode=disable" .Values.postgres.auth.username (include "gitstack.fullname" .) .Values.postgres.auth.database -}}
{{- else -}}
{{- printf "postgres://%s:$(POSTGRES_PASSWORD)@%s:%d/%s?%s" .Values.postgres.external.username .Values.postgres.external.host (.Values.postgres.external.port | int) .Values.postgres.external.database (include "gitstack.postgres.externalSSLQuery" .) -}}
{{- end -}}
{{- end -}}

{{/*
Parametri TLS del DSN del Postgres esterno (N8): sslmode e, se c'è un
Secret con la CA, sslrootcert sul file montato.
*/}}
{{- define "gitstack.postgres.externalSSLQuery" -}}
{{- $q := printf "sslmode=%s" .Values.postgres.external.sslMode -}}
{{- if .Values.postgres.external.caSecret -}}
{{- $q = printf "%s&sslrootcert=%s/ca.crt" $q (include "gitstack.postgres.caMountPath" .) -}}
{{- end -}}
{{- $q -}}
{{- end -}}

{{- define "gitstack.postgres.caMountPath" -}}
/etc/gitstack/postgres-ca
{{- end -}}

{{/*
URL NATS usato da core (GITSTACK_CORE_NATS_URL).
*/}}
{{- define "gitstack.nats.url" -}}
{{- printf "nats://$(NATS_USER):$(NATS_PASSWORD)@%s-nats:%d" (include "gitstack.fullname" .) (.Values.nats.service.clientPort | int) -}}
{{- end -}}

{{/*
Nome del Secret con i segreti di servizio (F9): una chiave per chiamante
(`gateway`, `core`, `git`). Generato dal chart (service-secrets.yaml) o dato con
serviceSecrets.existingSecret.
*/}}
{{- define "gitstack.serviceSecret.name" -}}
{{- .Values.serviceSecrets.existingSecret | default (printf "%s-service-secrets" (include "gitstack.fullname" .)) -}}
{{- end -}}

{{/*
Variabile GITSTACK_SERVICE_SECRET_<CHIAMANTE> dal Secret dei segreti di
servizio. Argomenti: dict "root" . "caller" "gateway|core|git".
*/}}
{{- define "gitstack.serviceSecret.env" -}}
- name: GITSTACK_SERVICE_SECRET_{{ upper .caller }}
  valueFrom:
    secretKeyRef:
      name: {{ include "gitstack.serviceSecret.name" .root }}
      key: {{ .caller }}
{{- end -}}

{{/*
URL core usato dal gateway (GITSTACK_CORE_URL).
*/}}
{{- define "gitstack.core.url" -}}
{{- printf "http://%s-core:%d" (include "gitstack.fullname" .) (.Values.core.service.port | int) -}}
{{- end -}}

{{/*
URL identity usato dal gateway (GITSTACK_IDENTITY_URL).
*/}}
{{- define "gitstack.identity.url" -}}
{{- printf "http://%s-identity:%d" (include "gitstack.fullname" .) (.Values.identity.service.port | int) -}}
{{- end -}}

{{/*
URL gateway usato da web (env GATEWAY_UPSTREAM dell'immagine nginx di GIT-7,
vedi web/deploy/nginx.conf.template): senza questo, il container usa il
default dell'immagine ("http://gateway:8080"), che non risolve nel cluster
(il Service si chiama "<release>-gateway", non "gateway").
FQDN completo (<service>.<namespace>.svc.cluster.local), non il nome breve
del Service: il `resolver` di nginx (usato da questo `proxy_pass` dinamico
per poter seguire un cambio di IP, vedi il commento nel template) fa query
DNS dirette e non applica la lista `search` di /etc/resolv.conf come fa
invece il resolver di Go (usato da GITSTACK_CORE_URL sopra) — il nome breve
darebbe NXDOMAIN. Riprodotto e verificato su k3d durante GIT-10.
*/}}
{{- define "gitstack.gateway.url" -}}
{{- printf "http://%s-gateway.%s.svc.cluster.local:%d" (include "gitstack.fullname" .) .Release.Namespace (.Values.gateway.service.port | int) -}}
{{- end -}}

{{/*
URL interno del servizio git usato da core (GITSTACK_GIT_URL).
*/}}
{{- define "gitstack.git.url" -}}
{{- printf "http://%s-git:%d" (include "gitstack.fullname" .) (.Values.git.service.port | int) -}}
{{- end -}}

{{/*
Nome del Secret con la chiave host SSH (chiave ssh_host_ed25519_key).
*/}}
{{- define "gitstack.git.sshHostKeySecret" -}}
{{- .Values.git.ssh.hostKey.existingSecret | default (printf "%s-git-ssh-host-key" (include "gitstack.fullname" .)) -}}
{{- end -}}

{{/*
SMTP facoltativo (M-06/F, C5): attivo se smtp.host o smtp.existingSecret sono
valorizzati; altrimenti core manda solo notifiche in-app. Stampa "true" o niente.
*/}}
{{- define "gitstack.smtp.enabled" -}}
{{- if or .Values.smtp.host .Values.smtp.existingSecret -}}true{{- end -}}
{{- end -}}

{{/*
Secret con la configurazione SMTP (chiavi host, port, security, username,
password, from): generato dal chart o dato con smtp.existingSecret.
*/}}
{{- define "gitstack.smtp.secretName" -}}
{{- .Values.smtp.existingSecret | default (printf "%s-smtp" (include "gitstack.fullname" .)) -}}
{{- end -}}

{{/*
Proxy aziendale e CA aziendale (N9). NO_PROXY: i valori dell'amministratore
più ciò che deve restare diretto dentro il cluster (nomi dei Service, domini
del cluster, CIDR di pod e servizi).
*/}}
{{- define "gitstack.proxy.noProxy" -}}
{{- $fn := include "gitstack.fullname" . -}}
{{- $items := list "localhost" "127.0.0.1" "::1" ".svc" ".cluster.local" -}}
{{- range $c := list "gateway" "core" "identity" "git" "web" "nats" "postgres" -}}
{{- $items = append $items (printf "%s-%s" $fn $c) -}}
{{- end -}}
{{- range $cidr := .Values.egress.clusterCIDRs -}}
{{- $items = append $items $cidr -}}
{{- end -}}
{{- with .Values.proxy.noProxy -}}
{{- $items = concat (list .) $items -}}
{{- end -}}
{{- join "," $items -}}
{{- end -}}

{{/*
Variabili d'ambiente del proxy e della CA aziendale per i servizi che escono
dal cluster (core: webhook, SMTP; git: mirror; identity: OIDC). Vuoto senza
proxy né CA. Uso: {{- include "gitstack.proxy.env" . | nindent 12 }}
*/}}
{{- define "gitstack.proxy.env" -}}
{{- if or .Values.proxy.http .Values.proxy.https }}
{{- $no := include "gitstack.proxy.noProxy" . }}
{{- with .Values.proxy.http }}
- name: HTTP_PROXY
  value: {{ . | quote }}
- name: http_proxy
  value: {{ . | quote }}
{{- end }}
{{- with .Values.proxy.https }}
- name: HTTPS_PROXY
  value: {{ . | quote }}
- name: https_proxy
  value: {{ . | quote }}
{{- end }}
- name: NO_PROXY
  value: {{ $no | quote }}
- name: no_proxy
  value: {{ $no | quote }}
{{- end }}
{{- if .Values.corporateCA.configMap }}
- name: SSL_CERT_DIR
  value: /etc/gitstack/corporate-ca
{{- end }}
{{- end -}}

{{/* Mount e volume della CA aziendale (ConfigMap con la chiave ca.crt). */}}
{{- define "gitstack.corporateCA.volumeMount" -}}
- name: corporate-ca
  mountPath: /etc/gitstack/corporate-ca/ca.crt
  subPath: ca.crt
  readOnly: true
{{- end -}}

{{- define "gitstack.corporateCA.volume" -}}
- name: corporate-ca
  configMap:
    name: {{ .Values.corporateCA.configMap | quote }}
    items:
      - key: ca.crt
        path: ca.crt
{{- end -}}

{{/*
Credenziali NATS (F8, ASVS V1.2.2): Secret <release>-nats-auth con le chiavi
`user` e `password`, generato una sola volta (nats-auth-secret.yaml) o dato
con nats.auth.existingSecret.
*/}}
{{- define "gitstack.nats.secretName" -}}
{{- .Values.nats.auth.existingSecret | default (printf "%s-nats-auth" (include "gitstack.fullname" .)) -}}
{{- end -}}

{{/*
NATS_USER e NATS_PASSWORD dal Secret: vanno prima delle variabili che
usano "$(NATS_USER)" (espansione di Kubernetes). Uso:
{{- include "gitstack.nats.authEnv" . | nindent 12 }}
*/}}
{{- define "gitstack.nats.authEnv" -}}
- name: NATS_USER
  valueFrom:
    secretKeyRef:
      name: {{ include "gitstack.nats.secretName" . }}
      key: user
- name: NATS_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "gitstack.nats.secretName" . }}
      key: password
{{- end -}}

{{/*
securityContext del pod (F8, V1.14.5): profilo seccomp RuntimeDefault sempre;
runAsUser/runAsNonRoot/fsGroup solo se il chiamante li dà.
Uso: {{ include "gitstack.podSecurityContext" (dict "runAsUser" 65532 "fsGroup" 65532) | nindent 8 }}
*/}}
{{- define "gitstack.podSecurityContext" -}}
seccompProfile:
  type: RuntimeDefault
{{- if .runAsUser }}
runAsNonRoot: true
runAsUser: {{ .runAsUser }}
{{- end }}
{{- if .fsGroup }}
fsGroup: {{ .fsGroup }}
{{- end }}
{{- end -}}

{{/*
securityContext del container: niente escalation, tutte le capability tolte
(`add` solo dove l'immagine ne ha bisogno, es. l'entrypoint di Postgres) e
filesystem radice in sola lettura dove il processo non scrive.
Uso: {{ include "gitstack.containerSecurityContext" (dict "readOnly" true) | nindent 12 }}
*/}}
{{- define "gitstack.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: {{ if .readOnly }}true{{ else }}false{{ end }}
capabilities:
  drop:
    - ALL
  {{- with .add }}
  add:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end -}}

{{/*
Arresto senza richieste perse (GIT-248). Traefik toglie l'endpoint di un pod
in terminazione con un ritardo: se il processo muore subito, le richieste in
volo danno 502/504. preStop con l'azione nativa `sleep` di Kubernetes (GA dalla
1.34, k3s del chart >= 1.36): non serve nessun binario nell'immagine, quindi
vale anche per il gateway (distroless, senza sleep né shell).
Uso (nel container, prima di resources): {{- include "gitstack.preStop" . | nindent 10 }}
*/}}
{{- define "gitstack.preStop" -}}
lifecycle:
  preStop:
    sleep:
      seconds: {{ .Values.global.shutdown.preStopSeconds | int }}
{{- end -}}
