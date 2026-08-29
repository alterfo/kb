package weeek

import (
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/connector"
)

func buildDocument(sourceName, webBase string, t apiTask) connector.Document {
	id := strconv.Itoa(t.ID)

	assignees := make([]string, len(t.Responsible))
	for i, uid := range t.Responsible {
		assignees[i] = strconv.Itoa(uid)
	}

	return connector.Document{
		ID:         id,
		Source:     sourceName,
		Kind:       "task",
		Title:      t.Title,
		URL:        webBase + "/tm/tasks/" + id,
		Body:       t.Description,
		Visibility: "public",
		Frontmatter: map[string]any{
			"queue":    t.BoardName,
			"key":      id,
			"status":   t.ColumnName,
			"assignee": strings.Join(assignees, ","),
			"updated":  t.UpdatedAt,
		},
	}
}
