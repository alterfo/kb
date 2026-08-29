package blog

import (
	"regexp"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/markdown"
)

var pubDateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	time.RFC3339,
}

func parsePubDate(v string) time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}
	}
	for _, layout := range pubDateLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func bodyValue(item rssItem) string {
	if content := strings.TrimSpace(item.Content); content != "" {
		return content
	}
	return strings.TrimSpace(item.Description)
}

var htmlTagRE = regexp.MustCompile(`(?s)<\s*(/?)\s*[a-zA-Z][^>]*>`)

func toMarkdown(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || !htmlTagRE.MatchString(s) {
		return s
	}
	root, err := markdown.Parse([]byte(s))
	if err != nil {
		return s
	}
	return markdown.Render(root, nil)
}

func buildDocument(sourceName string, item rssItem) connector.Document {
	guid := strings.TrimSpace(item.GUID)
	link := strings.TrimSpace(item.Link)
	title := strings.TrimSpace(item.Title)

	id := guid
	if id == "" {
		id = link
	}
	if id == "" {
		id = title
	}

	fm := map[string]any{}
	if guid != "" {
		fm["guid"] = guid
	}
	published := strings.TrimSpace(item.PubDate)
	if published != "" {
		fm["published"] = published
	}

	return connector.Document{
		ID:          id,
		Source:      sourceName,
		Kind:        "blog_post",
		Title:       title,
		URL:         link,
		UpdatedAt:   parsePubDate(published),
		Body:        toMarkdown(bodyValue(item)),
		Visibility:  "public",
		Frontmatter: fm,
	}
}
