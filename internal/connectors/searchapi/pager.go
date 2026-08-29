package searchapi

import (
	"net/http"
	"time"

	"github.com/alterfo/kb/internal/transport"
)

type nonePager struct{}

func (nonePager) NextRequest(prevReq *http.Request, resp *http.Response, body []byte) (*http.Request, error) {
	return nil, nil
}

type pagerConfig struct {
	Kind      string
	Header    string
	Param     string
	Path      string
	PageSize  int
	CountPath string
	Layout    string
	Step      time.Duration
	Until     time.Time
	NowFn     func() time.Time
}

func buildPager(cfg pagerConfig) transport.Pager {
	switch cfg.Kind {
	case "link_header":
		return transport.LinkHeaderPager{}
	case "next_page_header":
		return transport.NextPageHeaderPager{Header: cfg.Header, Param: cfg.Param}
	case "cursor_field":
		return transport.CursorFieldPager{Path: cfg.Path, Param: cfg.Param}
	case "next_link":
		return transport.NextLinkPager{Path: cfg.Path}
	case "offset":
		return transport.OffsetPager{Param: cfg.Param, PageSize: cfg.PageSize, CountPath: cfg.CountPath}
	case "time_window":
		return transport.TimeWindowPager{Param: cfg.Param, Layout: cfg.Layout, Step: cfg.Step, Until: cfg.Until, NowFn: cfg.NowFn}
	default:
		return nonePager{}
	}
}
