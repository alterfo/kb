package mcp

import (
	"context"
	"os"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/render"
)

type getDocumentIn struct {
	Path string `json:"path" jsonschema:"KB_ROOT-relative path of the document, e.g. notes/my-note.md"`
}

type getDocumentOut struct {
	Path        string         `json:"path"`
	Title       string         `json:"title,omitempty"`
	Source      string         `json:"source,omitempty"`
	Body        string         `json:"body"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
}

func (s *Server) getDocument(_ context.Context, _ *sdk.CallToolRequest, in getDocumentIn) (*sdk.CallToolResult, getDocumentOut, error) {
	abs, err := resolveWithin(s.deps.Root, in.Path)
	if err != nil {
		return nil, getDocumentOut{}, err
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, getDocumentOut{}, err
	}

	doc, err := render.Parse(data)
	if err != nil {
		doc = connector.Document{Body: string(data)}
	}

	return nil, getDocumentOut{
		Path:        in.Path,
		Title:       doc.Title,
		Source:      doc.Source,
		Body:        doc.Body,
		Frontmatter: doc.Frontmatter,
	}, nil
}
