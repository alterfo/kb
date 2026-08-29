package trello

import (
	"strings"

	"github.com/alterfo/kb/internal/connector"
)

func buildDocument(sourceName, boardID, listName string, card trelloCard) connector.Document {
	labels := make([]string, 0, len(card.Labels))
	for _, label := range card.Labels {
		if strings.TrimSpace(label.Name) != "" {
			labels = append(labels, label.Name)
		}
	}

	return connector.Document{
		ID:         boardID + "-" + card.ID,
		Source:     sourceName,
		Kind:       "trello_card",
		Title:      card.Name,
		URL:        card.ShortURL,
		Body:       card.Desc,
		Visibility: "public",
		Frontmatter: map[string]any{
			"list":   listName,
			"labels": strings.Join(labels, ","),
			"due":    card.Due,
			"closed": card.Closed,
		},
	}
}
