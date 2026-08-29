package web

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"

	"github.com/alterfo/kb/internal/connector"
)

func buildDocument(sourceName, pageURL, title, body string) connector.Document {
	u, err := url.Parse(pageURL)
	path := ""
	if err == nil {
		path = u.Path
	}
	if path == "" {
		path = pageURL
	}

	id := strings.Trim(path, "/")
	if id == "" {
		id = "index"
	}
	if err == nil && u.RawQuery != "" {
		sum := sha256.Sum256([]byte(pageURL))
		id += "-" + fmt.Sprintf("%x", sum[:4])
	}
	if strings.TrimSpace(title) == "" {
		title = path
	}
	if strings.TrimSpace(title) == "" {
		title = pageURL
	}

	return connector.Document{
		ID:         id,
		Source:     sourceName,
		Kind:       "doc_page",
		Title:      title,
		URL:        pageURL,
		Body:       body,
		Visibility: "public",
		Frontmatter: map[string]any{
			"path": path,
		},
	}
}
