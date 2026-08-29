package web

import (
	"embed"
	"html/template"
	"strings"
)

//go:embed templates static
var assets embed.FS

var tmplFuncs = template.FuncMap{
	"join": strings.Join,
}

func parseTemplates() (*template.Template, error) {
	return template.New("web").Funcs(tmplFuncs).ParseFS(assets, "templates/*.html")
}
