package yandex

import (
	"github.com/alterfo/kb/internal/connector"
)

func buildDocument(sourceName, webBase, queue string, it apiIssue) connector.Document {
	assignee := ""
	if it.Assignee != nil {
		assignee = it.Assignee.Display
	}
	return connector.Document{
		ID:         it.Key,
		Source:     sourceName,
		Kind:       "issue",
		Title:      it.Summary,
		URL:        webBase + "/" + it.Key,
		UpdatedAt:  it.updated(),
		Body:       it.Description,
		Visibility: "public",
		Frontmatter: map[string]any{
			"queue":    queue,
			"key":      it.Key,
			"status":   it.Status.Display,
			"assignee": assignee,
			"updated":  it.UpdatedAt,
		},
	}
}
