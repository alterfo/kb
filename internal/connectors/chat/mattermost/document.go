package mattermost

import (
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
)

func buildDocument(sourceName, webBase, team, channel string, p apiPost) connector.Document {
	fm := map[string]any{
		"channel": channel,
		"ts":      p.CreateAt,
		"user":    p.UserID,
	}
	if p.RootID != "" && p.RootID != p.ID {
		fm["thread"] = p.RootID
	}
	if p.EditAt != 0 {
		fm["edit_at"] = time.UnixMilli(p.EditAt).UTC().Format(time.RFC3339)
	}

	url := ""
	if team != "" {
		url = strings.TrimRight(webBase, "/") + "/" + team + "/pl/" + p.ID
	}

	return connector.Document{
		ID:          "mattermost:" + channel + ":" + p.ID,
		Source:      sourceName,
		Kind:        "message",
		Title:       messageTitle(p.Message),
		URL:         url,
		UpdatedAt:   time.UnixMilli(p.CreateAt).UTC(),
		Body:        p.Message,
		Visibility:  "public",
		Frontmatter: fm,
	}
}

func messageTitle(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	if len(text) > 80 {
		text = text[:80]
	}
	if text == "" {
		text = "Mattermost message"
	}
	return text
}
