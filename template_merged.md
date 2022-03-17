# Similar test failures

| Test Name | Prow Link |
| --------- | --------- |

{{- range $i, $d := .}}
{{- if gt (len $d) 1 }}
| {{$i}} | {{ range $i, $v := $d}}- {{ $v | emojiFromString }} {{ $v | styledURL "md"}}<br>{{end}} |
{{- end}}
{{- end }}

---

# Different

| Test Name | Prow Link |
| --------- | --------- |

{{- range $i, $d := .}}
{{- if eq (len $d) 1 }}
| {{$i}} | {{ ($d|first) | emojiFromString }} {{ $d | styledURL "md"}}|
{{- end}}
{{- end }}
