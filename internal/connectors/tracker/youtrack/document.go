package youtrack

import (
	"github.com/alterfo/kb/internal/connector"
)

func buildDocument(sourceName, webBase string, it apiIssue) connector.Document {
	return connector.Document{
		ID:         it.IDReadable,
		Source:     sourceName,
		Kind:       "issue",
		Title:      it.Summary,
		URL:        webBase + "/issue/" + it.IDReadable,
		UpdatedAt:  it.updated(),
		Body:       it.Description,
		Visibility: "public",
		Frontmatter: map[string]any{
			"queue":    it.Project.ShortName,
			"key":      it.IDReadable,
			"status":   it.status(),
			"assignee": it.assignee(),
			"updated":  it.Updated,
		},
	}
}
