package discord

import (
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
)

func messageTitle(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	if runes := []rune(text); len(runes) > 80 {
		text = string(runes[:80])
	}
	if text == "" {
		return "Discord message"
	}
	return text
}

func parseTimestamp(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func buildDocument(sourceName, guildID, webBase, channelID string, m apiMessage, threadTopic string) connector.Document {
	author := m.Author.Username
	if author == "" {
		author = m.Author.ID
	}

	fm := map[string]any{
		"channel": channelID,
		"ts":      m.Timestamp,
	}
	if author != "" {
		fm["user"] = author
	}
	if m.ReferencedMessage != nil && m.ReferencedMessage.ID != "" {
		fm["thread"] = m.ReferencedMessage.ID
		fm["parent_id"] = m.ReferencedMessage.ID
	}
	if threadTopic != "" {
		fm["thread_topic"] = threadTopic
	}
	if m.EditedTimestamp != nil && *m.EditedTimestamp != "" {
		if t := parseTimestamp(*m.EditedTimestamp); !t.IsZero() {
			fm["edited_at"] = t.Format(time.RFC3339)
		}
	}

	url := ""
	if guildID != "" {
		url = strings.TrimRight(webBase, "/") + "/channels/" + guildID + "/" + channelID + "/" + m.ID
	}

	return connector.Document{
		ID:          channelID + "-" + m.ID,
		Source:      sourceName,
		Kind:        "message",
		Title:       messageTitle(m.Content),
		URL:         url,
		UpdatedAt:   parseTimestamp(m.Timestamp),
		Body:        m.Content,
		Visibility:  "public",
		Frontmatter: fm,
	}
}
