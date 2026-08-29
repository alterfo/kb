package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/sink"
)

type addNoteIn struct {
	Path    string `json:"path" jsonschema:"KB_ROOT-relative path for the note, e.g. notes/my-note.md; a bare filename is placed under notes/"`
	Title   string `json:"title,omitempty" jsonschema:"note title"`
	Content string `json:"content" jsonschema:"note body (markdown)"`
}

type addNoteOut struct {
	Path string `json:"path"`
	ID   string `json:"id"`
}

func (s *Server) addNote(ctx context.Context, _ *sdk.CallToolRequest, in addNoteIn) (*sdk.CallToolResult, addNoteOut, error) {
	if strings.TrimSpace(in.Content) == "" {
		return nil, addNoteOut{}, fmt.Errorf("mcp: add_note: content is required")
	}
	if strings.TrimSpace(in.Path) == "" {
		return nil, addNoteOut{}, fmt.Errorf("mcp: add_note: path is required")
	}

	relPath := in.Path
	if !strings.HasSuffix(relPath, ".md") {
		relPath += ".md"
	}

	// Reject traversal before deriving source/id from the path.
	if _, err := resolveWithin(s.deps.Root, relPath); err != nil {
		return nil, addNoteOut{}, err
	}
	relPath = filepath.ToSlash(relPath)

	source := engine.InferSource(relPath)
	if source == "" {
		source = "notes"
	}
	id := noteID(relPath, source)
	// A bare filename has no source directory yet; canonicalize it to the
	// source dir the file will actually land in.
	if !strings.Contains(relPath, "/") {
		relPath = source + "/" + id + ".md"
	}

	doc := connector.Document{
		ID:        id,
		Source:    source,
		Title:     in.Title,
		Body:      in.Content,
		UpdatedAt: time.Now(),
	}

	data, err := render.Render(doc)
	if err != nil {
		return nil, addNoteOut{}, fmt.Errorf("mcp: add_note: render: %w", err)
	}
	if err := sink.WritePath(s.deps.Root, relPath, data); err != nil {
		return nil, addNoteOut{}, fmt.Errorf("mcp: add_note: %w", err)
	}

	if s.deps.Indexer != nil {
		if err := s.deps.Indexer.AddOrUpdateDocument(ctx, relPath); err != nil {
			return nil, addNoteOut{}, fmt.Errorf("mcp: add_note: index: %w", err)
		}
	}
	s.refreshBM25(ctx)

	return nil, addNoteOut{Path: relPath, ID: id}, nil
}

// noteID derives the frontmatter id from a validated relative note path:
// the path minus its .md extension and the leading source directory
// (notes/approved/foo.md -> approved/foo). Keeping the sub-path in the id
// makes nested notes distinct, so notes/name.md is never overwritten by
// notes/sub/name.md.
func noteID(relPath, source string) string {
	id := strings.TrimSuffix(relPath, ".md")
	if source != "" && strings.HasPrefix(id, source+"/") {
		id = strings.TrimPrefix(id, source+"/")
	}
	return id
}
