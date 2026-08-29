package kaiten

import (
	"strconv"

	"github.com/alterfo/kb/internal/connector"
)

func buildDocument(sourceName, webBase string, c apiCard) connector.Document {
	assignee := ""
	if c.Owner != nil {
		assignee = c.Owner.FullName
	}
	id := strconv.Itoa(c.ID)
	return connector.Document{
		ID:         id,
		Source:     sourceName,
		Kind:       "card",
		Title:      c.Title,
		URL:        webBase + "/card/" + id,
		UpdatedAt:  c.updated(),
		Body:       c.Description,
		Visibility: "public",
		Frontmatter: map[string]any{
			"queue":    c.Board.Title,
			"key":      id,
			"status":   c.Column.Title,
			"assignee": assignee,
			"updated":  c.UpdatedAt,
		},
	}
}
