package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func mkResp(t *testing.T, header http.Header, body string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	for k, vs := range header {
		for _, v := range vs {
			rec.Header().Add(k, v)
		}
	}
	rec.WriteHeader(http.StatusOK)
	rec.WriteString(body)
	return rec.Result()
}

func TestLinkHeaderPager(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://api.example/items?page=1", nil)
	resp := mkResp(t, http.Header{"Link": {`<http://api.example/items?page=2>; rel="next", <http://api.example/items?page=9>; rel="last"`}}, "")

	next, err := (LinkHeaderPager{}).NextRequest(req, resp, nil)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if next == nil || next.URL.String() != "http://api.example/items?page=2" {
		t.Fatalf("next = %v, want page=2", next)
	}
}

func TestLinkHeaderPagerNoNext(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://api.example/items?page=9", nil)
	resp := mkResp(t, http.Header{"Link": {`<http://api.example/items?page=1>; rel="first"`}}, "")

	next, err := (LinkHeaderPager{}).NextRequest(req, resp, nil)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if next != nil {
		t.Fatalf("next = %v, want nil (no rel=next)", next)
	}
}

func TestNextPageHeaderPager(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://api.example/items", nil)
	pager := NextPageHeaderPager{Header: "X-Next-Page", Param: "page"}

	resp := mkResp(t, http.Header{"X-Next-Page": {"3"}}, "")
	next, err := pager.NextRequest(req, resp, nil)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if next == nil || next.URL.Query().Get("page") != "3" {
		t.Fatalf("next = %v, want page=3", next)
	}

	respDone := mkResp(t, http.Header{}, "")
	done, err := pager.NextRequest(req, respDone, nil)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if done != nil {
		t.Fatalf("expected nil when header absent, got %v", done)
	}
}

func TestCursorFieldPager(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://api.example/items", nil)
	pager := CursorFieldPager{Path: "next_cursor", Param: "cursor"}

	resp := mkResp(t, nil, `{"items":[],"next_cursor":"abc123"}`)
	next, err := pager.NextRequest(req, resp, []byte(`{"items":[],"next_cursor":"abc123"}`))
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if next == nil || next.URL.Query().Get("cursor") != "abc123" {
		t.Fatalf("next = %v, want cursor=abc123", next)
	}

	respDone := mkResp(t, nil, `{"items":[],"next_cursor":""}`)
	done, err := pager.NextRequest(req, respDone, []byte(`{"items":[],"next_cursor":""}`))
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if done != nil {
		t.Fatalf("expected nil for empty cursor, got %v", done)
	}
	_ = resp
}

func TestNextLinkPager(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://api.example/wiki/rest/api/content/search?cql=x", nil)
	pager := NextLinkPager{Path: "_links.next"}

	body := []byte(`{"_links":{"next":"/wiki/rest/api/content/search?cql=x&start=25"}}`)
	next, err := pager.NextRequest(req, mkResp(t, nil, string(body)), body)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if next == nil || next.URL.String() != "http://api.example/wiki/rest/api/content/search?cql=x&start=25" {
		t.Fatalf("next = %v, want resolved absolute next link", next)
	}

	bodyDone := []byte(`{"_links":{}}`)
	done, err := pager.NextRequest(req, mkResp(t, nil, string(bodyDone)), bodyDone)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if done != nil {
		t.Fatalf("expected nil when no next link, got %v", done)
	}
}

func TestOffsetPager(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://api.example/items?offset=0", nil)
	pager := OffsetPager{Param: "offset", PageSize: 50, CountPath: "count"}

	body := []byte(`{"count":100}`)
	next, err := pager.NextRequest(req, mkResp(t, nil, string(body)), body)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if next == nil || next.URL.Query().Get("offset") != "50" {
		t.Fatalf("next = %v, want offset=50", next)
	}

	bodyDone := []byte(`{"count":50}`)
	done, err := pager.NextRequest(req, mkResp(t, nil, string(bodyDone)), bodyDone)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if done != nil {
		t.Fatalf("expected nil when offset reaches count, got %v", done)
	}

	bodyShort := []byte(`{"count":10}`)
	shortDone, err := pager.NextRequest(req, mkResp(t, nil, string(bodyShort)), bodyShort)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if shortDone != nil {
		t.Fatalf("expected nil when page short of size, got %v", shortDone)
	}
}

func TestTimeWindowPager(t *testing.T) {
	const layout = "2006-01-02"
	now, _ := time.Parse(layout, "2026-08-19")
	req, _ := http.NewRequest(http.MethodGet, "http://api.example/items?since=2026-08-01", nil)
	pager := TimeWindowPager{
		Param:  "since",
		Layout: layout,
		Step:   24 * time.Hour,
		NowFn:  func() time.Time { return now },
	}

	next, err := pager.NextRequest(req, mkResp(t, nil, ""), nil)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if next == nil || next.URL.Query().Get("since") != "2026-08-02" {
		t.Fatalf("next = %v, want since=2026-08-02", next)
	}

	reqAtEnd, _ := http.NewRequest(http.MethodGet, "http://api.example/items?since=2026-08-19", nil)
	done, err := pager.NextRequest(reqAtEnd, mkResp(t, nil, ""), nil)
	if err != nil {
		t.Fatalf("NextRequest: %v", err)
	}
	if done != nil {
		t.Fatalf("expected nil at/after Until, got %v", done)
	}
}

func TestPaginateDrivesAllPages(t *testing.T) {
	pages := []string{"a", "b", "c"}
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if idx+1 < len(pages) {
			w.Header().Set("X-Next-Page", "x")
		}
		w.Write([]byte(pages[idx]))
		idx++
	}))
	defer srv.Close()

	c := testClient(t, srv)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	pager := NextPageHeaderPager{Header: "X-Next-Page", Param: "page"}

	var got []string
	err := c.Paginate(req.Context(), req, pager, func(resp *http.Response, body []byte) error {
		got = append(got, string(body))
		return nil
	})
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d pages, want 3: %v", len(got), got)
	}
}
