{{/*
Common labels for all resources.
*/}}
{{- define "agora.labels" -}}
app.kubernetes.io/name: agora
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{/*
Per-component resource names. Using Release.Name keeps the same name we used
under the kustomize layout when installed as `helm install agora ...`.
*/}}
{{- define "agora.backend.fullname" -}}
{{ .Release.Name }}-backend
{{- end -}}

{{- define "agora.frontend.fullname" -}}
{{ .Release.Name }}-frontend
{{- end -}}

{{- define "agora.postgres.fullname" -}}
{{ .Release.Name }}-postgres
{{- end -}}

{{/*
DATABASE_URL pieced together from the postgres service + Secret values.
The $(VAR) syntax is resolved by the kubelet from the container's env, so
POSTGRES_USER / POSTGRES_PASSWORD / POSTGRES_DB must also be loaded into env
on the same container (see envFrom on the backend Deployment).
*/}}
{{- define "agora.databaseUrl" -}}
postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@{{ include "agora.postgres.fullname" . }}:5432/$(POSTGRES_DB)?sslmode=disable
{{- end -}}
