package mcp

import (
	"encoding/json"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/connector"
)

func extractText(contents []*sdk.ResourceContents) string {
	var parts []string
	for _, c := range contents {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func formatToolBody(t *sdk.Tool) string {
	var b strings.Builder
	b.WriteString(t.Description)
	if t.InputSchema != nil {
		if j, err := json.MarshalIndent(t.InputSchema, "", "  "); err == nil {
			b.WriteString("\n\n```json\n")
			b.Write(j)
			b.WriteString("\n```\n")
		}
	}
	return b.String()
}

func buildResourceDocument(sourceName string, r *sdk.Resource, body string) connector.Document {
	title := r.Title
	if title == "" {
		title = r.Name
	}
	if title == "" {
		title = r.URI
	}
	return connector.Document{
		ID:         sourceName + ":" + r.URI,
		Source:     sourceName,
		Kind:       "resource",
		Title:      title,
		URL:        r.URI,
		Body:       body,
		Visibility: "public",
		Frontmatter: map[string]any{
			"mcp_server":   sourceName,
			"resource_uri": r.URI,
			"mime_type":    r.MIMEType,
		},
	}
}

func buildToolDocument(sourceName string, t *sdk.Tool, body string) connector.Document {
	return connector.Document{
		ID:         sourceName + ":tool:" + t.Name,
		Source:     sourceName,
		Kind:       "tool",
		Title:      t.Name,
		Body:       body,
		Visibility: "public",
		Frontmatter: map[string]any{
			"mcp_server":   sourceName,
			"resource_uri": "tool:" + t.Name,
		},
	}
}
