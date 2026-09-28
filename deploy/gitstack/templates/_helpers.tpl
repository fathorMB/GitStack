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
{{- $root := .root -}}
{{- $comp := .component -}}
{{- $registry := $comp.image.registry | default $root.Values.global.image.registry -}}
{{- $tag := $comp.image.tag -}}
{{- if not $tag -}}
{{- $tag = $root.Values.global.image.tag -}}
{{- end -}}
{{- if not $tag -}}
{{- $tag = $root.Chart.AppVersion -}}
{{- end -}}
{{- printf "%s/%s:%s" $registry $comp.image.repository $tag -}}
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
{{- printf "postgres://%s:$(POSTGRES_PASSWORD)@%s:%d/%s?sslmode=%s" .Values.postgres.external.username .Values.postgres.external.host (.Values.postgres.external.port | int) .Values.postgres.external.database .Values.postgres.external.sslMode -}}
{{- end -}}
{{- end -}}

{{/*
URL NATS usato da core (GITSTACK_CORE_NATS_URL).
*/}}
{{- define "gitstack.nats.url" -}}
{{- printf "nats://%s-nats:%d" (include "gitstack.fullname" .) (.Values.nats.service.clientPort | int) -}}
{{- end -}}

{{/*
URL core usato dal gateway (GITSTACK_CORE_URL).
*/}}
{{- define "gitstack.core.url" -}}
{{- printf "http://%s-core:%d" (include "gitstack.fullname" .) (.Values.core.service.port | int) -}}
{{- end -}}
