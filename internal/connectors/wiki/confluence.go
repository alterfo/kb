package wiki

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/transport"
)

type apiConfluenceContent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Space struct {
		Key string `json:"key"`
	} `json:"space"`
	Version struct {
		Number int       `json:"number"`
		When   time.Time `json:"when"`
	} `json:"version"`
	Ancestors []struct {
		Title string `json:"title"`
	} `json:"ancestors"`
	Body struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

type apiConfluenceSearchResponse struct {
	Results []apiConfluenceContent `json:"results"`
}

func (c *Connector) fetchConfluence(ctx context.Context, since time.Time, out chan<- connector.Document, count *int, maxUpdated *time.Time) error {
	cql := "type=page"
	if c.space != "" {
		cql += ` and space="` + c.space + `"`
	}
	if !since.IsZero() {
		cql += ` and lastmodified >= "` + since.UTC().Format("2006-01-02 15:04") + `"`
	}

	q := url.Values{}
	q.Set("cql", cql)
	q.Set("expand", "body.storage,space,version,ancestors")
	q.Set("limit", "25")
	req, err := c.newRequest(ctx, http.MethodGet, c.apiBase+"/rest/api/content/search?"+q.Encode())
	if err != nil {
		return err
	}

	pager := transport.NextLinkPager{Path: "_links.next"}
	return c.client.Paginate(ctx, req, pager, func(resp *http.Response, body []byte) error {
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}
		var page apiConfluenceSearchResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		for _, res := range page.Results {
			d := buildConfluenceDocument(c.name, c.webBase, res)
			if err := send(ctx, out, d); err != nil {
				return err
			}
			*count++
			if res.Version.When.After(*maxUpdated) {
				*maxUpdated = res.Version.When
			}
		}
		return nil
	})
}
