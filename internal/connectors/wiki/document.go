package wiki

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/connector"
)

func buildMediaWikiDocument(sourceName, wikiName, webBase string, rc apiRecentChange, content string) connector.Document {
	return connector.Document{
		ID:         wikiName + ":" + strconv.Itoa(rc.PageID),
		Source:     sourceName,
		Kind:       "page",
		Title:      rc.Title,
		URL:        webBase + "/wiki/" + url.PathEscape(strings.ReplaceAll(rc.Title, " ", "_")),
		UpdatedAt:  rc.Timestamp,
		Body:       content,
		Visibility: "public",
		Frontmatter: map[string]any{
			"wiki":      wikiName,
			"pageid":    rc.PageID,
			"namespace": rc.Ns,
			"revid":     rc.RevID,
		},
	}
}

func buildConfluenceDocument(sourceName, webBase string, res apiConfluenceContent) connector.Document {
	fm := map[string]any{
		"space":   res.Space.Key,
		"pageid":  res.ID,
		"version": res.Version.Number,
	}
	if len(res.Ancestors) > 0 {
		titles := make([]string, len(res.Ancestors))
		for i, a := range res.Ancestors {
			titles[i] = a.Title
		}
		fm["ancestors"] = titles
	}
	return connector.Document{
		ID:          "confluence:" + res.Space.Key + ":" + res.ID,
		Source:      sourceName,
		Kind:        "page",
		Title:       res.Title,
		URL:         webBase + res.Links.WebUI,
		UpdatedAt:   res.Version.When,
		Body:        res.Body.Storage.Value,
		Visibility:  "public",
		Frontmatter: fm,
	}
}
