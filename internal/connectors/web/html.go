package web

import (
	"strings"

	"golang.org/x/net/html"

	"github.com/alterfo/kb/internal/markdown"
)

func parseHTML(body []byte) (*html.Node, error) {
	return markdown.Parse(body)
}

func findFirstTag(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, tag) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findFirstTag(c, tag); found != nil {
			return found
		}
	}
	return nil
}

// extractContent selects the element whose subtree is converted to markdown.
// It prefers the configured selector and falls back to article and then body;
// if none are present, the whole document is used.
func extractContent(root *html.Node, selector string) *html.Node {
	candidates := []string{selector, "article", "body"}
	seen := make(map[string]bool, len(candidates))
	for _, tag := range candidates {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		if node := findFirstTag(root, tag); node != nil {
			return node
		}
	}
	return root
}

func extractTitle(root *html.Node) string {
	if title := findFirstTag(root, "title"); title != nil {
		return markdown.NormalizeSpace(markdown.TextContent(title))
	}
	if h1 := findFirstTag(root, "h1"); h1 != nil {
		return markdown.NormalizeSpace(markdown.TextContent(h1))
	}
	return ""
}
