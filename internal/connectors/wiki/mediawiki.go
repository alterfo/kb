package wiki

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/transport"
)

type apiRecentChange struct {
	Type      string    `json:"type"`
	Ns        int       `json:"ns"`
	Title     string    `json:"title"`
	PageID    int       `json:"pageid"`
	RevID     int       `json:"revid"`
	Timestamp time.Time `json:"timestamp"`
}

type apiRecentChangesResponse struct {
	Query struct {
		RecentChanges []apiRecentChange `json:"recentchanges"`
	} `json:"query"`
}

type apiParseResponse struct {
	Parse *struct {
		Wikitext struct {
			Content string `json:"*"`
		} `json:"wikitext"`
	} `json:"parse"`
}

func (c *Connector) fetchMediaWiki(ctx context.Context, since time.Time, out chan<- connector.Document, count *int, maxUpdated *time.Time) error {
	q := url.Values{}
	q.Set("action", "query")
	q.Set("list", "recentchanges")
	q.Set("format", "json")
	q.Set("rcprop", "title|ids|timestamp")
	q.Set("rcnamespace", c.namespace)
	q.Set("rclimit", "50")
	if since.IsZero() {
		// Initial sync: the newest window only, bounded by rclimit.
		q.Set("rcdir", "older")
	} else {
		// Incremental sync: enumerate forward from (exclusive of) the
		// cursor so each sync fetches only the changes after it and never
		// re-fetches the boundary change.
		q.Set("rcdir", "newer")
		q.Set("rcstart", since.UTC().Format(time.RFC3339))
	}
	req, err := c.newRequest(ctx, http.MethodGet, c.apiBase+"?"+q.Encode())
	if err != nil {
		return err
	}

	var changes []apiRecentChange
	pager := transport.CursorFieldPager{Path: "continue.rccontinue", Param: "rccontinue"}
	err = c.client.Paginate(ctx, req, pager, func(resp *http.Response, body []byte) error {
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}
		var page apiRecentChangesResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		changes = append(changes, page.Query.RecentChanges...)
		return nil
	})
	if err != nil {
		return err
	}

	seen := map[int]bool{}
	for _, rc := range changes {
		if rc.Type != "new" && rc.Type != "edit" {
			continue
		}
		if seen[rc.PageID] {
			continue
		}
		seen[rc.PageID] = true

		content, ok, err := c.fetchMediaWikiContent(ctx, rc.PageID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}

		d := buildMediaWikiDocument(c.name, c.wikiName, c.webBase, rc, content)
		if err := send(ctx, out, d); err != nil {
			return err
		}
		*count++
		if rc.Timestamp.After(*maxUpdated) {
			*maxUpdated = rc.Timestamp
		}
	}
	return nil
}

func (c *Connector) fetchMediaWikiContent(ctx context.Context, pageID int) (string, bool, error) {
	q := url.Values{}
	q.Set("action", "parse")
	q.Set("format", "json")
	q.Set("prop", "wikitext")
	q.Set("pageid", strconv.Itoa(pageID))
	req, err := c.newRequest(ctx, http.MethodGet, c.apiBase+"?"+q.Encode())
	if err != nil {
		return "", false, err
	}
	resp, err := c.do(ctx, req)
	if err != nil {
		return "", false, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", false, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, nil
	}
	var page apiParseResponse
	if err := json.Unmarshal(body, &page); err != nil || page.Parse == nil {
		return "", false, nil
	}
	return page.Parse.Wikitext.Content, true, nil
}
