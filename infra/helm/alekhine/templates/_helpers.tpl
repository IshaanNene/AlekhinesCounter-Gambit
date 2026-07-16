{{/* Fully-qualified image reference, honouring an optional global registry. */}}
{{- define "acg.image" -}}
{{- if .registry -}}{{ .registry }}{{ .image }}{{- else -}}{{ .image }}{{- end -}}
{{- end -}}

{{/* Standard labels applied to every object, so kubectl selectors and Prometheus
     discovery have a consistent handle. */}}
{{- define "acg.labels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/part-of: alekhine
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
{{- end -}}
