{{- define "atlas.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "atlas.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "atlas.labels" -}}
{{ include "atlas.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "atlas.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "atlas.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- required "serviceAccount.name est requis quand serviceAccount.create=false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "atlas.secretName" -}}
{{- default (include "atlas.fullname" .) .Values.auth.oidc.existingSecret -}}
{{- end -}}

{{- define "atlas.exposed" -}}
{{- if or .Values.ingress.enabled .Values.httpRoute.enabled }}true{{ end -}}
{{- end -}}

{{/* Garde-fous de configuration, évalués par chaque rendu. */}}
{{- define "atlas.validate" -}}
{{- if and (eq .Values.auth.mode "none") (include "atlas.exposed" .) -}}
{{- fail "auth.mode=none est réservé au développement local : désactivez ingress.enabled et httpRoute.enabled, ou configurez auth.mode=oidc" -}}
{{- end -}}
{{- if eq .Values.auth.mode "oidc" -}}
{{- if not .Values.auth.oidc.clientID -}}
{{- fail "auth.oidc.clientID est requis avec auth.mode=oidc" -}}
{{- end -}}
{{- if not (or .Values.publicURL (include "atlas.publicURL" .)) -}}
{{- fail "auth.mode=oidc : activez ingress ou httpRoute, ou renseignez publicURL (URL de retour OIDC)" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* URL publique : sert d'URL de retour OIDC et d'origine autorisée. */}}
{{- define "atlas.publicURL" -}}
{{- if .Values.ingress.enabled -}}
{{- printf "%s://%s" (ternary "https" "http" .Values.ingress.tls) .Values.ingress.host -}}
{{- else if and .Values.httpRoute.enabled .Values.httpRoute.hostnames -}}
{{- printf "https://%s" (first .Values.httpRoute.hostnames) -}}
{{- end -}}
{{- end -}}
