package server

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html.tmpl
var adminTemplatesFS embed.FS

var statusOrder = map[string]int{"draft": 0, "tested": 1, "approved": 2, "released": 3, "closed": 4}

var templateFuncs = template.FuncMap{
	"slice": func(items ...string) []string { return items },
	"statusAtOrPast": func(current, step string) bool {
		return statusOrder[current] >= statusOrder[step]
	},
}

func parseTemplates() *template.Template {
	return template.Must(template.New("").Funcs(templateFuncs).ParseFS(adminTemplatesFS, "templates/*.html.tmpl"))
}
