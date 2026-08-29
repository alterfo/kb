package web

import (
	"html/template"
	"io"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

type safeHTMLRenderer struct {
	*html.Renderer
}

func (r *safeHTMLRenderer) RenderNode(w io.Writer, node ast.Node, entering bool) ast.WalkStatus {
	switch n := node.(type) {
	case *ast.Image:
		if !parser.IsSafeURL(n.Destination) {
			return ast.SkipChildren
		}
	}
	return r.Renderer.RenderNode(w, node, entering)
}

var mdRenderer = &safeHTMLRenderer{Renderer: html.NewRenderer(html.RendererOptions{
	Flags: html.CommonFlags | html.SkipHTML | html.Safelink,
})}

func renderMarkdown(src string) template.HTML {
	mdParser := parser.NewWithExtensions(parser.CommonExtensions)
	out := markdown.ToHTML([]byte(src), mdParser, mdRenderer)
	return template.HTML(out)
}
