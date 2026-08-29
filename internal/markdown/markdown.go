package markdown

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var inlineSpaceRE = regexp.MustCompile(`\s+`)

// Parse parses an HTML document or fragment.
func Parse(body []byte) (*html.Node, error) {
	return html.Parse(bytes.NewReader(body))
}

// Render converts an HTML node subtree to markdown. Relative links and image
// sources are resolved against base when non-nil.
func Render(n *html.Node, base *url.URL) string {
	return strings.TrimSpace(renderBlocks(n, base))
}

// NormalizeSpace collapses internal whitespace runs and trims the result.
func NormalizeSpace(s string) string {
	return strings.TrimSpace(inlineSpaceRE.ReplaceAllString(s, " "))
}

// TextContent returns the concatenated text of an HTML node subtree.
func TextContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func resolveRef(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	return u.String()
}

func renderBlocks(n *html.Node, base *url.URL) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(renderBlock(c, base))
	}
	return sb.String()
}

func renderBlock(n *html.Node, base *url.URL) string {
	switch n.Type {
	case html.TextNode:
		text := inlineSpaceRE.ReplaceAllString(n.Data, " ")
		if strings.TrimSpace(text) == "" {
			return ""
		}
		return text + "\n\n"
	case html.ElementNode:
		switch strings.ToLower(n.Data) {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			level := int(n.Data[1] - '0')
			return strings.Repeat("#", level) + " " + NormalizeSpace(renderInline(n, base)) + "\n\n"
		case "p":
			return NormalizeSpace(renderInline(n, base)) + "\n\n"
		case "ul":
			return renderList(n, base, false) + "\n"
		case "ol":
			return renderList(n, base, true) + "\n"
		case "blockquote":
			return "> " + NormalizeSpace(renderInline(n, base)) + "\n\n"
		case "pre":
			return "```\n" + strings.Trim(TextContent(n), "\n") + "\n```\n\n"
		case "br":
			return "\n"
		case "hr":
			return "---\n\n"
		default:
			return renderBlocks(n, base)
		}
	}
	return ""
}

func renderList(n *html.Node, base *url.URL, ordered bool) string {
	var sb strings.Builder
	idx := 1
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || !strings.EqualFold(c.Data, "li") {
			continue
		}
		content := NormalizeSpace(renderInline(c, base))
		if ordered {
			fmt.Fprintf(&sb, "%d. %s\n", idx, content)
			idx++
		} else {
			fmt.Fprintf(&sb, "- %s\n", content)
		}
	}
	return sb.String()
}

func renderInline(n *html.Node, base *url.URL) string {
	switch n.Type {
	case html.TextNode:
		return inlineSpaceRE.ReplaceAllString(n.Data, " ")
	case html.ElementNode:
		switch strings.ToLower(n.Data) {
		case "a":
			return "[" + renderInlineChildren(n, base) + "](" + resolveRef(base, attr(n, "href")) + ")"
		case "strong", "b":
			return "**" + renderInlineChildren(n, base) + "**"
		case "em", "i":
			return "*" + renderInlineChildren(n, base) + "*"
		case "code":
			return "`" + strings.TrimSpace(TextContent(n)) + "`"
		case "img":
			alt := strings.TrimSpace(attr(n, "alt"))
			src := resolveRef(base, attr(n, "src"))
			return "![" + alt + "](" + src + ")"
		case "br":
			return "\n"
		default:
			return renderInlineChildren(n, base)
		}
	}
	return ""
}

func renderInlineChildren(n *html.Node, base *url.URL) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(renderInline(c, base))
	}
	return sb.String()
}
