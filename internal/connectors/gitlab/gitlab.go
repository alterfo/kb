package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("gitlab", func() connector.Connector { return New() })
}

type Connector struct {
	name         string
	apiBase      string
	webBase      string
	token        string
	group        string
	projects     []string
	includeWiki  bool
	includeFiles bool
	client       *transport.Client
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "gitlab" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.group = strings.TrimSpace(cfg.Config["group"])
	c.projects = nil
	if v := strings.TrimSpace(cfg.Config["projects"]); v != "" {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				c.projects = append(c.projects, p)
			}
		}
	}
	if c.group == "" && len(c.projects) == 0 {
		return fmt.Errorf("gitlab: source %q: either config.group or config.projects is required", cfg.Name)
	}

	c.apiBase = defaultStr(cfg.Config["base_url"], "https://gitlab.com/api/v4")
	c.webBase = defaultStr(cfg.Config["web_base_url"], "https://gitlab.com")
	c.includeWiki = cfg.Config["include_wiki"] != "false"
	c.includeFiles = cfg.Config["include_files"] != "false"

	c.token = ""
	if envName, ok := cfg.Secrets["token"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.token = v
		}
	}

	if c.client == nil {
		c.client = transport.NewClient(transport.Config{})
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	st := parseCursorState(since.Value)
	fullReconcile := st.Since.IsZero()

	projects, err := c.resolveProjects(ctx)
	if err != nil {
		return since, connector.FetchInfo{}, err
	}

	count := 0
	maxUpdated := st.Since
	for _, project := range projects {
		if err := c.fetchIssues(ctx, project, st.Since, out, &count, &maxUpdated); err != nil {
			return since, connector.FetchInfo{}, fmt.Errorf("gitlab: issues %s: %w", project, err)
		}
		if err := c.fetchMergeRequests(ctx, project, st.Since, out, &count, &maxUpdated); err != nil {
			return since, connector.FetchInfo{}, fmt.Errorf("gitlab: merge requests %s: %w", project, err)
		}
		if c.includeWiki {
			if err := c.fetchWiki(ctx, project, out, &count); err != nil {
				return since, connector.FetchInfo{}, fmt.Errorf("gitlab: wiki %s: %w", project, err)
			}
		}
		if c.includeFiles {
			if err := c.fetchFiles(ctx, project, out, &count); err != nil {
				return since, connector.FetchInfo{}, fmt.Errorf("gitlab: files %s: %w", project, err)
			}
		}
	}

	// wiki and files are re-fetched in full on every run, so deletions in
	// those categories can be pruned even on incremental runs; issues and
	// merge requests stay incremental and must be preserved.
	var prunePrefixes []string
	for _, project := range projects {
		if c.includeWiki {
			prunePrefixes = append(prunePrefixes, project+":wiki:")
		}
		if c.includeFiles {
			prunePrefixes = append(prunePrefixes, project+":file:")
		}
	}

	newState := cursorState{Since: maxUpdated, Projects: projects}
	newCursor := connector.Cursor{Value: newState.encode()}
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: fullReconcile, PrunePrefixes: prunePrefixes}, nil
}

func (c *Connector) resolveProjects(ctx context.Context) ([]string, error) {
	if len(c.projects) > 0 {
		return c.projects, nil
	}

	req, err := c.newAPIRequest(ctx, http.MethodGet, c.apiBase+"/groups/"+url.PathEscape(c.group)+"/projects?per_page=100&include_subgroups=true&simple=true")
	if err != nil {
		return nil, err
	}

	var names []string
	err = c.paginate(ctx, req, transport.NextPageHeaderPager{Header: "X-Next-Page", Param: "page"}, func(resp *http.Response, body []byte) error {
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}
		var page []apiProject
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		for _, p := range page {
			names = append(names, p.PathWithNamespace)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

func (c *Connector) fetchIssues(ctx context.Context, project string, since time.Time, out chan<- connector.Document, count *int, maxUpdated *time.Time) error {
	q := "?order_by=updated_at&sort=asc&per_page=100"
	if !since.IsZero() {
		q += "&updated_after=" + url.QueryEscape(since.Add(-time.Second).UTC().Format(time.RFC3339))
	}
	req, err := c.newAPIRequest(ctx, http.MethodGet, c.apiBase+"/projects/"+encodeProjectID(project)+"/issues"+q)
	if err != nil {
		return err
	}

	return c.paginate(ctx, req, transport.NextPageHeaderPager{Header: "X-Next-Page", Param: "page"}, func(resp *http.Response, body []byte) error {
		if resp.StatusCode == http.StatusNotFound {
			return nil
		}
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}
		var page []apiIssue
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		for _, it := range page {
			d := buildIssueDocument(c.name, project, it)
			if err := send(ctx, out, d); err != nil {
				return err
			}
			*count++
			if it.UpdatedAt.After(*maxUpdated) {
				*maxUpdated = it.UpdatedAt
			}
		}
		return nil
	})
}

func (c *Connector) fetchMergeRequests(ctx context.Context, project string, since time.Time, out chan<- connector.Document, count *int, maxUpdated *time.Time) error {
	q := "?order_by=updated_at&sort=asc&per_page=100"
	if !since.IsZero() {
		q += "&updated_after=" + url.QueryEscape(since.Add(-time.Second).UTC().Format(time.RFC3339))
	}
	req, err := c.newAPIRequest(ctx, http.MethodGet, c.apiBase+"/projects/"+encodeProjectID(project)+"/merge_requests"+q)
	if err != nil {
		return err
	}

	return c.paginate(ctx, req, transport.NextPageHeaderPager{Header: "X-Next-Page", Param: "page"}, func(resp *http.Response, body []byte) error {
		if resp.StatusCode == http.StatusNotFound {
			return nil
		}
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}
		var page []apiMergeRequest
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		for _, mr := range page {
			d := buildMergeRequestDocument(c.name, project, mr)
			if err := send(ctx, out, d); err != nil {
				return err
			}
			*count++
			if mr.UpdatedAt.After(*maxUpdated) {
				*maxUpdated = mr.UpdatedAt
			}
		}
		return nil
	})
}

func (c *Connector) fetchWiki(ctx context.Context, project string, out chan<- connector.Document, count *int) error {
	req, err := c.newAPIRequest(ctx, http.MethodGet, c.apiBase+"/projects/"+encodeProjectID(project)+"/wikis?with_content=1")
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, req)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return &statusError{resp.StatusCode}
	}

	var pages []apiWikiPage
	if err := json.Unmarshal(body, &pages); err != nil {
		return err
	}
	for _, p := range pages {
		d := connector.Document{
			ID:         project + ":wiki:" + p.Slug,
			Source:     c.name,
			Kind:       "wiki",
			Title:      p.Title,
			URL:        c.webBase + "/" + project + "/-/wikis/" + p.Slug,
			Visibility: "public",
			Body:       p.Content,
			Frontmatter: map[string]any{
				"project": project,
				"page":    p.Slug,
			},
		}
		if err := send(ctx, out, d); err != nil {
			return err
		}
		*count++
	}
	return nil
}

func (c *Connector) fetchFiles(ctx context.Context, project string, out chan<- connector.Document, count *int) error {
	req, err := c.newAPIRequest(ctx, http.MethodGet, c.apiBase+"/projects/"+encodeProjectID(project)+"/repository/tree?recursive=true&per_page=100")
	if err != nil {
		return err
	}
	return c.paginate(ctx, req, transport.NextPageHeaderPager{Header: "X-Next-Page", Param: "page"}, func(resp *http.Response, body []byte) error {
		if resp.StatusCode == http.StatusNotFound {
			return nil
		}
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}
		var entries []apiTreeEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			return err
		}
		for _, e := range entries {
			if e.Type != "blob" || !strings.HasSuffix(strings.ToLower(e.Name), ".md") {
				continue
			}
			fReq, err := c.newAPIRequest(ctx, http.MethodGet, c.apiBase+"/projects/"+encodeProjectID(project)+"/repository/files/"+url.PathEscape(e.Path)+"/raw?ref=HEAD")
			if err != nil {
				return err
			}
			fResp, err := c.do(ctx, fReq)
			if err != nil {
				return err
			}
			fBody, err := io.ReadAll(fResp.Body)
			fResp.Body.Close()
			if err != nil {
				return err
			}
			if fResp.StatusCode != http.StatusOK {
				continue
			}

			d := connector.Document{
				ID:         project + ":file:" + e.Path,
				Source:     c.name,
				Kind:       "file",
				Title:      e.Name,
				URL:        c.webBase + "/" + project + "/-/blob/HEAD/" + e.Path,
				Visibility: "public",
				Body:       string(fBody),
				Frontmatter: map[string]any{
					"project": project,
					"path":    e.Path,
				},
			}
			if err := send(ctx, out, d); err != nil {
				return err
			}
			*count++
		}
		return nil
	})
}

func send(ctx context.Context, out chan<- connector.Document, d connector.Document) error {
	select {
	case out <- d:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func encodeProjectID(project string) string {
	return url.PathEscape(project)
}

func (c *Connector) newAPIRequest(ctx context.Context, method, u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)
	return req, nil
}

func (c *Connector) setAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func (c *Connector) paginate(ctx context.Context, req *http.Request, pager transport.Pager, handle func(resp *http.Response, body []byte) error) error {
	for req != nil {
		resp, err := c.do(ctx, req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if err := handle(resp, body); err != nil {
			return err
		}
		next, err := pager.NextRequest(req, resp, body)
		if err != nil {
			return err
		}
		req = next
	}
	return nil
}

func (c *Connector) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	return c.client.Do(ctx, req.Clone(ctx))
}

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
