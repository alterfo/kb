package telegram

import (
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
)

func buildDocument(sourceName string, msg apiMessage) connector.Document {
	user := ""
	if msg.From != nil {
		if msg.From.Username != "" {
			user = msg.From.Username
		} else {
			user = msg.From.FirstName
		}
	}

	channel := msg.Chat.Title
	if channel == "" {
		channel = msg.Chat.Username
	}

	fm := map[string]any{
		"chat":    msg.Chat.ID,
		"channel": channel,
		"ts":      msg.Date,
		"user":    user,
	}
	if msg.EditDate != 0 {
		fm["edit_at"] = time.Unix(msg.EditDate, 0).UTC().Format(time.RFC3339)
	}
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.MessageID != 0 {
		fm["thread"] = msg.ReplyToMessage.MessageID
	}

	url := ""
	if msg.Chat.Username != "" {
		url = "https://t.me/" + msg.Chat.Username + "/" + strconv.FormatInt(msg.MessageID, 10)
	}

	return connector.Document{
		ID:          "telegram:" + strconv.FormatInt(msg.Chat.ID, 10) + ":" + strconv.FormatInt(msg.MessageID, 10),
		Source:      sourceName,
		Kind:        "message",
		Title:       messageTitle(msg.Text),
		URL:         url,
		UpdatedAt:   time.Unix(msg.Date, 0).UTC(),
		Body:        msg.Text,
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
		text = "Telegram message"
	}
	return text
}
