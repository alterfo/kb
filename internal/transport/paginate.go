package transport

import (
	"context"
	"io"
	"net/http"
)

func (c *Client) Paginate(ctx context.Context, req *http.Request, pager Pager, handle func(resp *http.Response, body []byte) error) error {
	for req != nil {
		resp, err := c.Do(ctx, req)
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
