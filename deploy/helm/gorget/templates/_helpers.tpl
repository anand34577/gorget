{{- define "gorget.name" -}}{{ .Chart.Name }}{{- end }}
{{- define "gorget.fullname" -}}{{ .Release.Name }}-{{ .Chart.Name }}{{- end }}
{{- define "gorget.labels" -}}
app.kubernetes.io/name: {{ include "gorget.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
{{- define "gorget.selector" -}}
app.kubernetes.io/name: {{ include "gorget.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
{{- define "gorget.masterSecret" -}}{{ if .Values.masterKey.existingSecret }}{{ .Values.masterKey.existingSecret }}{{ else }}{{ include "gorget.fullname" . }}-secrets{{ end }}{{- end }}
{{- define "gorget.dsnSecret" -}}{{ if .Values.database.existingSecret }}{{ .Values.database.existingSecret }}{{ else }}{{ include "gorget.fullname" . }}-secrets{{ end }}{{- end }}
{{- define "gorget.clustered" -}}{{ and (eq .Values.database.driver "postgres") (gt (int .Values.replicaCount) 1) }}{{- end }}
{{- define "gorget.scheme" -}}{{ if eq .Values.tls.mode "off" }}HTTP{{ else }}HTTPS{{ end }}{{- end }}
