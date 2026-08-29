package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/transport"
)

type APISink struct {
	client  *transport.Client
	baseURL string
}

func NewAPISink(client *transport.Client, baseURL string) *APISink {
	return &APISink{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (s *APISink) Write(ctx context.Context, d connector.Document) error {
	body, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("apisink: marshal document: %w", err)
	}
	return s.post(ctx, "/documents", body)
}

type prunePayload struct {
	Source   string   `json:"source"`
	Seen     []string `json:"seen"`
	Prefixes []string `json:"prefixes,omitempty"`
}

func (s *APISink) Prune(ctx context.Context, sourceName string, seen map[string]struct{}, prefixes ...string) error {
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	body, err := json.Marshal(prunePayload{Source: sourceName, Seen: ids, Prefixes: prefixes})
	if err != nil {
		return fmt.Errorf("apisink: marshal prune: %w", err)
	}
	return s.post(ctx, "/documents/prune", body)
}

type tombstonePayload struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

func (s *APISink) Tombstone(ctx context.Context, sourceName, id string) error {
	body, err := json.Marshal(tombstonePayload{Source: sourceName, ID: id})
	if err != nil {
		return fmt.Errorf("apisink: marshal tombstone: %w", err)
	}
	return s.post(ctx, "/documents/tombstone", body)
}

func (s *APISink) post(ctx context.Context, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("apisink: new request %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("apisink: %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("apisink: %s: status %s", path, resp.Status)
	}
	return nil
}
