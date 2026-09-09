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

{{/*
ARB_REPLAY_SESSION as set through arbd.extraEnv ("" when absent). REPLAY
without it is a boot-time refusal in internal/config; under --atomic
that shows up as a CrashLoopBackOff and a rolled-back release, so it is
caught here at render time instead.
*/}}
{{- define "arb.replaySession" -}}
{{- $s := "" -}}
{{- range .Values.arbd.extraEnv -}}
{{- if eq (toString .name) "ARB_REPLAY_SESSION" -}}{{- $s = toString .value -}}{{- end -}}
{{- end -}}
{{- $s -}}
{{- end -}}

{{/*
Remote key (under externalSecrets.remotePrefix) that ARB_DATABASE_URL is
projected from. A canary reads its own key; it never reads the primary's.
*/}}
{{- define "arb.databaseRemoteKey" -}}
{{- if .Values.canary.enabled -}}
{{- .Values.canary.databaseRemoteKey -}}
{{- else -}}
{{- .Values.externalSecrets.keys.databaseURL -}}
{{- end -}}
{{- end -}}

{{/*
Render-time assertions. Included from configmap-arbd.yaml so every
render (lint, template, install, upgrade) runs them; produces no output.
deploy/helm/test-canary-guards.sh proves that each refusal fires.
*/}}
{{- define "arb.guards" -}}
{{- $mode := include "arb.mode" . -}}
{{- if and (eq $mode "REPLAY") (eq (include "arb.replaySession" .) "") -}}
{{- fail "arbd.mode=REPLAY requires arbd.extraEnv[ARB_REPLAY_SESSION] with a closed recording session id; the binary refuses to start without one" -}}
{{- end -}}
{{- if gt (int .Values.arbd.replicas) 1 -}}
{{- fail (printf "arbd.replicas=%d is refused: the engine is a single writer per database (one live feed, one outbox); horizontal scale needs run-ownership sharding first" (int .Values.arbd.replicas)) -}}
{{- end -}}
{{- if not .Values.arbd.persistence.enabled -}}
{{- if .Values.migrate.enabled -}}
{{- fail "migrate.enabled must be false when arbd.persistence.enabled=false: the hook would run migrations against whatever DSN the secret holds while the process itself runs in memory" -}}
{{- end -}}
{{- range .Values.arbd.extraEnv -}}
{{- if eq (toString .name) "ARB_DATABASE_URL" -}}
{{- fail "ARB_DATABASE_URL must not be set through arbd.extraEnv while arbd.persistence.enabled=false; enable persistence and project the DSN from the secret store instead" -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if .Values.canary.enabled -}}
{{- if ne $mode "PAPER" -}}
{{- fail (printf "canary: arbd.mode=%s is refused; the canary runs PAPER (RECORD would open a recording session, REPLAY needs a session id and a database)" $mode) -}}
{{- end -}}
{{- range .Values.arbd.extraEnv -}}
{{- if eq (toString .name) "ARB_DATABASE_URL" -}}
{{- fail "canary: ARB_DATABASE_URL must not be set through arbd.extraEnv; a canary database comes from canary.databaseRemoteKey or persistence stays disabled" -}}
{{- end -}}
{{- end -}}
{{- if .Values.arbd.persistence.enabled -}}
{{- $k := toString (default "" .Values.canary.databaseRemoteKey) -}}
{{- if eq $k "" -}}
{{- fail "canary: arbd.persistence.enabled=true needs canary.databaseRemoteKey (a remote secret holding a DSN to a scratch database); the primary's database-url is never shared with a second engine" -}}
{{- end -}}
{{- if eq $k (toString .Values.externalSecrets.keys.databaseURL) -}}
{{- fail (printf "canary: canary.databaseRemoteKey=%q is the primary release's database key; a canary must never run against the production database" $k) -}}
{{- end -}}
{{- if not .Values.externalSecrets.enabled -}}
{{- fail "canary: with persistence enabled the DSN must be projected by this chart's ExternalSecret (externalSecrets.enabled=true) so the key reference can be checked" -}}
{{- end -}}
{{- end -}}
{{- if .Values.arbd.recordings.enabled -}}
{{- fail "canary: arbd.recordings.enabled must be false; the canary must not claim or write the recordings volume" -}}
{{- end -}}
{{- if .Values.ingress.enabled -}}
{{- $a := default (dict) .Values.ingress.annotations -}}
{{- $flag := toString (default "" (index $a "nginx.ingress.kubernetes.io/canary")) -}}
{{- if ne $flag "true" -}}
{{- fail "canary: ingress.annotations must mark the Ingress as a canary (nginx.ingress.kubernetes.io/canary: \"true\") or it would replace the primary's host rules" -}}
{{- end -}}
{{- $w := toString (default "0" (index $a "nginx.ingress.kubernetes.io/canary-weight")) -}}
{{- if ne $w "0" -}}
{{- fail (printf "canary: canary-weight=%s is refused; sessions, settings and paper state are separate from the primary's, so weighted traffic would split users across two instances. Use canary-by-header for opt-in traffic" $w) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
