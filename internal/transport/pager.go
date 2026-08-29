package transport

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type Pager interface {
	NextRequest(prevReq *http.Request, resp *http.Response, body []byte) (*http.Request, error)
}

func cloneWithQuery(req *http.Request, mutate func(q url.Values)) *http.Request {
	next := req.Clone(req.Context())
	u := *req.URL
	q := u.Query()
	mutate(q)
	u.RawQuery = q.Encode()
	next.URL = &u
	return next
}

type LinkHeaderPager struct{}

func parseLinkHeader(v string) map[string]string {
	out := make(map[string]string)
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		urlPart := strings.TrimSpace(segs[0])
		if !strings.HasPrefix(urlPart, "<") || !strings.HasSuffix(urlPart, ">") {
			continue
		}
		link := urlPart[1 : len(urlPart)-1]
		for _, param := range segs[1:] {
			param = strings.TrimSpace(param)
			kv := strings.SplitN(param, "=", 2)
			if len(kv) != 2 || strings.TrimSpace(kv[0]) != "rel" {
				continue
			}
			rel := strings.Trim(strings.TrimSpace(kv[1]), `"`)
			out[rel] = link
		}
	}
	return out
}

func (LinkHeaderPager) NextRequest(prevReq *http.Request, resp *http.Response, body []byte) (*http.Request, error) {
	links := parseLinkHeader(resp.Header.Get("Link"))
	next, ok := links["next"]
	if !ok || next == "" {
		return nil, nil
	}
	nextURL, err := url.Parse(next)
	if err != nil {
		return nil, err
	}
	req := prevReq.Clone(prevReq.Context())
	req.URL = nextURL
	return req, nil
}

type NextPageHeaderPager struct {
	Header string
	Param  string
}

func (p NextPageHeaderPager) NextRequest(prevReq *http.Request, resp *http.Response, body []byte) (*http.Request, error) {
	v := resp.Header.Get(p.Header)
	if v == "" {
		return nil, nil
	}
	return cloneWithQuery(prevReq, func(q url.Values) {
		q.Set(p.Param, v)
	}), nil
}

type CursorFieldPager struct {
	Path  string
	Param string
}

func (p CursorFieldPager) NextRequest(prevReq *http.Request, resp *http.Response, body []byte) (*http.Request, error) {
	v := gjson.GetBytes(body, p.Path)
	if !v.Exists() || v.String() == "" {
		return nil, nil
	}
	return cloneWithQuery(prevReq, func(q url.Values) {
		q.Set(p.Param, v.String())
	}), nil
}

type NextLinkPager struct {
	Path string
}

func (p NextLinkPager) NextRequest(prevReq *http.Request, resp *http.Response, body []byte) (*http.Request, error) {
	v := gjson.GetBytes(body, p.Path)
	if !v.Exists() || v.String() == "" {
		return nil, nil
	}
	nextURL, err := url.Parse(v.String())
	if err != nil {
		return nil, err
	}
	req := prevReq.Clone(prevReq.Context())
	req.URL = prevReq.URL.ResolveReference(nextURL)
	return req, nil
}

type OffsetPager struct {
	Param     string
	PageSize  int
	CountPath string
}

func (p OffsetPager) NextRequest(prevReq *http.Request, resp *http.Response, body []byte) (*http.Request, error) {
	count := int(gjson.GetBytes(body, p.CountPath).Int())
	if count < p.PageSize {
		return nil, nil
	}
	cur, _ := strconv.Atoi(prevReq.URL.Query().Get(p.Param))
	next := cur + p.PageSize
	if next >= count {
		return nil, nil
	}
	return cloneWithQuery(prevReq, func(q url.Values) {
		q.Set(p.Param, strconv.Itoa(next))
	}), nil
}

type TimeWindowPager struct {
	Param  string
	Layout string
	Step   time.Duration
	Until  time.Time
	NowFn  func() time.Time
}

func (p TimeWindowPager) now() time.Time {
	if p.NowFn != nil {
		return p.NowFn()
	}
	return time.Now()
}

func (p TimeWindowPager) NextRequest(prevReq *http.Request, resp *http.Response, body []byte) (*http.Request, error) {
	raw := prevReq.URL.Query().Get(p.Param)
	if raw == "" {
		return nil, nil
	}
	cur, err := time.Parse(p.Layout, raw)
	if err != nil {
		return nil, err
	}
	next := cur.Add(p.Step)
	until := p.Until
	if until.IsZero() {
		until = p.now()
	}
	if !next.Before(until) {
		return nil, nil
	}
	return cloneWithQuery(prevReq, func(q url.Values) {
		q.Set(p.Param, next.Format(p.Layout))
	}), nil
}
