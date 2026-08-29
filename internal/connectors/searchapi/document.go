package searchapi

import (
	"time"

	"github.com/tidwall/gjson"

	"github.com/alterfo/kb/internal/connector"
)

type fieldMap struct {
	ID        string
	Title     string
	URL       string
	UpdatedAt string
	Body      string
	Extra     map[string]string
}

func buildDocument(sourceName, kind, visibility string, fm fieldMap, timeLayout string, item gjson.Result) (connector.Document, time.Time, bool) {
	id := item.Get(fm.ID).String()
	if id == "" {
		return connector.Document{}, time.Time{}, false
	}

	updated := parseItemTime(item.Get(fm.UpdatedAt), timeLayout)

	frontmatter := map[string]any{}
	for name, path := range fm.Extra {
		v := item.Get(path)
		if v.Exists() {
			frontmatter[name] = v.Value()
		}
	}

	doc := connector.Document{
		ID:          id,
		Source:      sourceName,
		Kind:        kind,
		Title:       item.Get(fm.Title).String(),
		URL:         item.Get(fm.URL).String(),
		UpdatedAt:   updated,
		Body:        item.Get(fm.Body).String(),
		Visibility:  visibility,
		Frontmatter: frontmatter,
	}
	return doc, updated, true
}

func parseItemTime(v gjson.Result, layout string) time.Time {
	if !v.Exists() {
		return time.Time{}
	}
	raw := v.String()
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(layout, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}
