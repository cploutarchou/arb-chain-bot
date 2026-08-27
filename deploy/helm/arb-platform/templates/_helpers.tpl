{{/* Common naming / label helpers */}}
{{- define "arb.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "arb.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "arb.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "arb.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
app.kubernetes.io/name: {{ include "arb.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: arb-platform
arb.io/environment: {{ .Values.global.environment }}
{{- end -}}

{{- define "arb.selectorLabels" -}}
app.kubernetes.io/name: {{ include "arb.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "arb.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "arb.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/* image reference with digest-first promotion */}}
{{- define "arb.image" -}}
{{- $img := .img -}}
{{- $reg := default .root.Values.global.imageRegistry $img.registry -}}
{{- $tag := default .root.Chart.AppVersion $img.tag -}}
{{- if $img.digest -}}
{{ printf "%s/%s@%s" $reg $img.repository $img.digest }}
{{- else -}}
{{ printf "%s/%s:%s" $reg $img.repository $tag }}
{{- end -}}
{{- end -}}

{{- define "arb.secretName" -}}
{{ include "arb.fullname" . }}-secrets
{{- end -}}

{{- define "arb.arbdName" -}}
{{ include "arb.fullname" . }}-arbd
{{- end -}}

{{- define "arb.webName" -}}
{{ include "arb.fullname" . }}-web
{{- end -}}

{{/* Guard: refuse any mode that is not RECORD/PAPER/REPLAY. */}}
{{- define "arb.mode" -}}
{{- $m := upper (toString .Values.arbd.mode) -}}
{{- if not (has $m (list "RECORD" "PAPER" "REPLAY")) -}}
{{- fail (printf "arbd.mode=%q is not allowed; LIVE execution is disabled in every environment by code" $m) -}}
{{- end -}}
{{- $m -}}
{{- end -}}
