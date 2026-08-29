package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("github", func() connector.Connector { return New() })
}

type Connector struct {
	name            string
	apiBase         string
	webBase         string
	rawBase         string
	token           string
	org             string
	repos           []string
	includeWiki     bool
	includeContents bool
	client          *transport.Client

	now                 func() time.Time
	sleep               func(ctx context.Context, d time.Duration) error
	maxRateLimitRetries int
}

func New() *Connector {
	return &Connector{
		now:                 time.Now,
		sleep:               defaultSleep,
		maxRateLimitRetries: 3,
	}
}

func (c *Connector) Type() string { return "github" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.org = strings.TrimSpace(cfg.Config["org"])
	c.repos = nil
	if v := strings.TrimSpace(cfg.Config["repos"]); v != "" {
		for _, r := range strings.Split(v, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				c.repos = append(c.repos, r)
			}
		}
	}
	if c.org == "" && len(c.repos) == 0 {
		return fmt.Errorf("github: source %q: either config.org or config.repos is required", cfg.Name)
	}

	c.apiBase = defaultStr(cfg.Config["base_url"], "https://api.github.com")
	c.webBase = defaultStr(cfg.Config["web_base_url"], "https://github.com")
	c.rawBase = defaultStr(cfg.Config["raw_base_url"], "https://raw.githubusercontent.com")
	c.includeWiki = cfg.Config["include_wiki"] != "false"
	c.includeContents = cfg.Config["include_contents"] != "false"

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

	repos, reposETag, err := c.resolveRepos(ctx, st)
	if err != nil {
		return since, connector.FetchInfo{}, err
	}

	count := 0
	maxUpdated := st.Since
	for _, repo := range repos {
		if err := c.fetchIssues(ctx, repo, st.Since, out, &count, &maxUpdated); err != nil {
			return since, connector.FetchInfo{}, fmt.Errorf("github: issues %s: %w", repo, err)
		}
		if c.includeContents {
			if err := c.fetchContents(ctx, repo, out, &count); err != nil {
				return since, connector.FetchInfo{}, fmt.Errorf("github: contents %s: %w", repo, err)
			}
		}
		if c.includeWiki {
			if err := c.fetchWiki(ctx, repo, out, &count); err != nil {
				return since, connector.FetchInfo{}, fmt.Errorf("github: wiki %s: %w", repo, err)
			}
		}
	}

	// contents and wiki are re-fetched in full on every run, so deletions
	// in those categories can be pruned even on incremental runs; issues
	// stay incremental and must be preserved.
	var prunePrefixes []string
	for _, repo := range repos {
		if c.includeContents {
			prunePrefixes = append(prunePrefixes, repo+":contents:")
		}
		if c.includeWiki {
			prunePrefixes = append(prunePrefixes, repo+":wiki:")
		}
	}

	newState := cursorState{Since: maxUpdated, Repos: repos, ReposETag: reposETag}
	newCursor := connector.Cursor{Value: newState.encode()}
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: fullReconcile, PrunePrefixes: prunePrefixes}, nil
}

func (c *Connector) resolveRepos(ctx context.Context, st cursorState) ([]string, string, error) {
	if len(c.repos) > 0 {
		return c.repos, "", nil
	}

	orgReposURL := c.apiBase + "/orgs/" + url.PathEscape(c.org) + "/repos?per_page=100"
	if c.token == "" {
		orgReposURL += "&type=public"
	}
	req, err := c.newAPIRequest(ctx, http.MethodGet, orgReposURL)
	if err != nil {
		return nil, "", err
	}
	if st.ReposETag != "" {
		req.Header.Set("If-None-Match", st.ReposETag)
	}

	var names []string
	var newETag string
	notModified := false
	first := true

	err = c.paginate(ctx, req, transport.LinkHeaderPager{}, func(resp *http.Response, body []byte) error {
		if first {
			first = false
			if resp.StatusCode == http.StatusNotModified {
				notModified = true
				return nil
			}
			if et := resp.Header.Get("ETag"); et != "" {
				newETag = et
			}
		}
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}
		var page []apiRepo
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		for _, r := range page {
			names = append(names, r.FullName)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if notModified {
		return st.Repos, st.ReposETag, nil
	}
	return names, newETag, nil
}

func (c *Connector) fetchIssues(ctx context.Context, repoFullName string, since time.Time, out chan<- connector.Document, count *int, maxUpdated *time.Time) error {
	q := "?state=all&sort=updated&direction=asc&per_page=100"
	if !since.IsZero() {
		q += "&since=" + url.QueryEscape(since.Add(-time.Second).UTC().Format(time.RFC3339))
	}
	req, err := c.newAPIRequest(ctx, http.MethodGet, c.apiBase+"/repos/"+repoFullName+"/issues"+q)
	if err != nil {
		return err
	}

	return c.paginate(ctx, req, transport.LinkHeaderPager{}, func(resp *http.Response, body []byte) error {
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
			d := buildIssueDocument(c.name, repoFullName, it)
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

func (c *Connector) fetchContents(ctx context.Context, repoFullName string, out chan<- connector.Document, count *int) error {
	return c.walkContents(ctx, repoFullName, "", out, count)
}

// walkContents lists a repo directory via the contents API (paginated by
// Link headers) and recursively descends into subdirectories, so markdown
// files outside the repo root are indexed too.
func (c *Connector) walkContents(ctx context.Context, repoFullName, subpath string, out chan<- connector.Document, count *int) error {
	path := "/repos/" + repoFullName + "/contents"
	if subpath != "" {
		segs := strings.Split(subpath, "/")
		for i, s := range segs {
			segs[i] = url.PathEscape(s)
		}
		path += "/" + strings.Join(segs, "/")
	}
	req, err := c.newAPIRequest(ctx, http.MethodGet, c.apiBase+path)
	if err != nil {
		return err
	}
	return c.paginate(ctx, req, transport.LinkHeaderPager{}, func(resp *http.Response, body []byte) error {
		if resp.StatusCode == http.StatusNotFound {
			return nil
		}
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}
		var entries []apiContentEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			return err
		}
		for _, e := range entries {
			switch {
			case e.Type == "dir":
				if err := c.walkContents(ctx, repoFullName, e.Path, out, count); err != nil {
					return err
				}
			case e.Type == "file" && strings.HasSuffix(strings.ToLower(e.Name), ".md") && e.DownloadURL != "":
				if err := c.fetchContentFile(ctx, repoFullName, e, out, count); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (c *Connector) fetchContentFile(ctx context.Context, repoFullName string, e apiContentEntry, out chan<- connector.Document, count *int) error {
	fReq, err := http.NewRequestWithContext(ctx, http.MethodGet, e.DownloadURL, nil)
	if err != nil {
		return err
	}
	c.setAuth(fReq)
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
		return nil
	}

	d := connector.Document{
		ID:         repoFullName + ":contents:" + e.Path,
		Source:     c.name,
		Kind:       "content",
		Title:      e.Name,
		URL:        c.webBase + "/" + repoFullName + "/blob/HEAD/" + e.Path,
		Visibility: "public",
		Body:       string(fBody),
		Frontmatter: map[string]any{
			"repo": repoFullName,
			"path": e.Path,
		},
	}
	if err := send(ctx, out, d); err != nil {
		return err
	}
	*count++
	return nil
}

func (c *Connector) fetchWiki(ctx context.Context, repoFullName string, out chan<- connector.Document, count *int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.webBase+"/"+repoFullName+"/wiki", nil)
	if err != nil {
		return err
	}
	c.setAuth(req)
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

	seen := map[string]bool{}
	re := regexp.MustCompile(`href="/` + regexp.QuoteMeta(repoFullName) + `/wiki/([^"?#]+)"`)
	for _, m := range re.FindAllStringSubmatch(string(body), -1) {
		slug := m[1]
		if seen[slug] {
			continue
		}
		seen[slug] = true

		pReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.rawBase+"/wiki/"+repoFullName+"/"+slug+".md", nil)
		if err != nil {
			return err
		}
		c.setAuth(pReq)
		pResp, err := c.do(ctx, pReq)
		if err != nil {
			return err
		}
		pBody, err := io.ReadAll(pResp.Body)
		pResp.Body.Close()
		if err != nil {
			return err
		}
		if pResp.StatusCode != http.StatusOK {
			continue
		}

		d := connector.Document{
			ID:         repoFullName + ":wiki:" + slug,
			Source:     c.name,
			Kind:       "wiki",
			Title:      strings.ReplaceAll(slug, "-", " "),
			URL:        c.webBase + "/" + repoFullName + "/wiki/" + slug,
			Visibility: "public",
			Body:       string(pBody),
			Frontmatter: map[string]any{
				"repo": repoFullName,
				"page": slug,
			},
		}
		if err := send(ctx, out, d); err != nil {
			return err
		}
		*count++
	}
	return nil
}

func send(ctx context.Context, out chan<- connector.Document, d connector.Document) error {
	select {
	case out <- d:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Connector) newAPIRequest(ctx context.Context, method, u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
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
	for attempt := 0; ; attempt++ {
		resp, err := c.client.Do(ctx, req.Clone(ctx))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" && attempt < c.maxRateLimitRetries {
			wait := rateLimitWait(resp, c.now())
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		return resp, nil
	}
}

func rateLimitWait(resp *http.Response, now time.Time) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	if v := resp.Header.Get("X-RateLimit-Reset"); v != "" {
		if epoch, err := strconv.ParseInt(v, 10, 64); err == nil {
			if d := time.Unix(epoch, 0).Sub(now); d > 0 {
				return d
			}
		}
	}
	return time.Second
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
