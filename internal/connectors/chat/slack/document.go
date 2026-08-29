package slack

import (
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
)

func buildDocument(sourceName, channel string, m apiMessage) connector.Document {
	fm := map[string]any{
		"channel": channel,
		"ts":      m.Ts,
		"user":    m.User,
	}
	if m.ThreadTs != "" {
		fm["thread"] = m.ThreadTs
	}
	if m.Edited != nil && m.Edited.Ts != "" {
		fm["edit_at"] = parseSlackTs(m.Edited.Ts).UTC().Format(time.RFC3339)
	}

	return connector.Document{
		ID:          "slack:" + channel + ":" + m.Ts,
		Source:      sourceName,
		Kind:        "message",
		Title:       messageTitle(m.Text),
		URL:         permalink(channel, m.Ts),
		UpdatedAt:   parseSlackTs(m.Ts),
		Body:        m.Text,
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
		text = "Slack message"
	}
	return text
}

func permalink(channel, ts string) string {
	p := strings.Replace(ts, ".", "", 1)
	return "https://slack.com/archives/" + channel + "/p" + p
}

func parseSlackTs(ts string) time.Time {
	f, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return time.Time{}
	}
	sec := int64(f)
	nsec := int64((f - float64(sec)) * 1e9)
	return time.Unix(sec, nsec).UTC()
}

func compareTs(a, b string) int {
	if b == "" {
		return 1
	}
	af, _ := strconv.ParseFloat(a, 64)
	bf, _ := strconv.ParseFloat(b, 64)
	switch {
	case af > bf:
		return 1
	case af < bf:
		return -1
	default:
		return 0
	}
}
