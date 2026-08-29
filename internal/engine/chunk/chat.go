package chunk

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

type ChatMessage struct {
	Text      string
	User      string
	ThreadID  string
	ParentID  string
	Timestamp time.Time
}

// speakerEntry is one participant of a multi-speaker chunk: the speaker's
// name and the timestamp of their first message in the chunk. The
// deterministic chat extraction phase uses it to attribute decision edges
// to the right participant instead of the thread's first author.
type speakerEntry struct {
	User string `json:"user"`
	TS   string `json:"ts"`
}

type ChatChunker struct {
	MaxGap time.Duration
}

func NewChatChunker(maxGap time.Duration) *ChatChunker {
	if maxGap <= 0 {
		maxGap = 24 * time.Hour
	}
	return &ChatChunker{MaxGap: maxGap}
}

func (c *ChatChunker) Chunk(messages []ChatMessage) []Chunk {
	if len(messages) == 0 {
		return nil
	}

	var chunks []Chunk
	var cur []ChatMessage

	flush := func() {
		if len(cur) == 0 {
			return
		}
		type msg struct {
			raw  string
			text string
			user string
		}
		var msgs []msg
		seen := map[string]time.Time{}
		for _, m := range cur {
			text := strings.TrimSpace(m.Text)
			if text == "" {
				continue
			}
			user := strings.TrimSpace(m.User)
			msgs = append(msgs, msg{raw: m.Text, text: text, user: user})
			if user != "" {
				if _, ok := seen[user]; !ok {
					seen[user] = m.Timestamp
				}
			}
		}
		multi := len(seen) > 1
		parts := make([]string, 0, len(msgs))
		for _, m := range msgs {
			if multi && m.user != "" {
				parts = append(parts, m.user+": "+m.text)
			} else {
				parts = append(parts, m.raw)
			}
		}
		text := strings.Join(parts, "\n\n")
		meta := map[string]string{"thread_id": cur[0].ThreadID}
		if multi {
			var speakers []speakerEntry
			for u, ts := range seen {
				entry := speakerEntry{User: u}
				if !ts.IsZero() {
					entry.TS = ts.UTC().Format(time.RFC3339)
				}
				speakers = append(speakers, entry)
			}
			sort.Slice(speakers, func(i, j int) bool { return speakers[i].User < speakers[j].User })
			raw, err := json.Marshal(speakers)
			if err == nil {
				meta["speakers"] = string(raw)
			}
		}
		chunks = append(chunks, Chunk{
			Text:       text,
			Index:      len(chunks),
			TokenCount: EstimateTokens(text),
			Metadata:   meta,
		})
	}

	for _, m := range messages {
		if len(cur) > 0 {
			prev := cur[len(cur)-1]
			threadChanged := m.ThreadID != prev.ThreadID
			gapExceeded := c.MaxGap > 0 && !m.Timestamp.IsZero() && !prev.Timestamp.IsZero() &&
				m.Timestamp.Sub(prev.Timestamp) > c.MaxGap
			if threadChanged || gapExceeded {
				flush()
				cur = nil
			}
		}
		cur = append(cur, m)
	}
	flush()

	return chunks
}
