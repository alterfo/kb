package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

func postJSON(t *testing.T, h http.Handler, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestAPIIngestPruneTombstoneRoundtrip(t *testing.T) {
	te := newTestEnv(t, &fakeChat{})
	h := te.server.Handler()
	ctx := context.Background()

	ingest := func(id string) {
		t.Helper()
		rr := postJSON(t, h, "/documents", connector.Document{
			ID: id, Source: "chat", Kind: "message", Title: id, Body: "hello " + id,
		})
		if rr.Code != http.StatusNoContent {
			t.Fatalf("POST /documents (%s) = %d, want 204 (body %s)", id, rr.Code, rr.Body.String())
		}
	}
	ingest("m1")
	ingest("m2")
	ingest("m3")
	rr := postJSON(t, h, "/documents", connector.Document{ID: "x1", Source: "notes", Body: "note"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("ingest notes/x1 = %d (body %s)", rr.Code, rr.Body.String())
	}

	prune := postJSON(t, h, "/documents/prune", map[string]any{"source": "chat", "seen": []string{"m2"}})
	if prune.Code != http.StatusNoContent {
		t.Fatalf("POST /documents/prune = %d, want 204 (body %s)", prune.Code, prune.Body.String())
	}

	chunks, err := te.vector.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range chunks {
		seen[c.RefDocID] = true
	}
	if seen["chat/m1"] || seen["chat/m3"] || !seen["chat/m2"] || !seen["notes/x1"] {
		t.Fatalf("after prune refs = %v, want chat/m2 and notes/x1 only", seen)
	}

	tomb := postJSON(t, h, "/documents/tombstone", map[string]any{"source": "chat", "id": "m2"})
	if tomb.Code != http.StatusNoContent {
		t.Fatalf("POST /documents/tombstone = %d, want 204 (body %s)", tomb.Code, tomb.Body.String())
	}
	chunks, err = te.vector.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 || chunks[0].RefDocID != "notes/x1" {
		t.Fatalf("after tombstone chunks = %+v, want notes/x1 only", chunks)
	}
}

func TestAPIIngestRejectsInvalidPayload(t *testing.T) {
	te := newTestEnv(t, &fakeChat{})
	h := te.server.Handler()

	rr := postJSON(t, h, "/documents", map[string]any{"source": "s"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing id should be 400, got %d (body %s)", rr.Code, rr.Body.String())
	}
}

func TestAPIPruneScopedToPrefixes(t *testing.T) {
	te := newTestEnv(t, &fakeChat{})
	h := te.server.Handler()
	ctx := context.Background()

	for _, id := range []string{"acme/widgets:contents:README.md", "acme/widgets:wiki:Home", "acme/widgets:issue:12"} {
		rr := postJSON(t, h, "/documents", connector.Document{
			ID: id, Source: "gh", Kind: "content", Title: id, Body: "body " + id,
		})
		if rr.Code != http.StatusNoContent {
			t.Fatalf("ingest %s = %d (body %s)", id, rr.Code, rr.Body.String())
		}
	}

	rr := postJSON(t, h, "/documents/prune", map[string]any{
		"source":   "gh",
		"seen":     []string{},
		"prefixes": []string{"acme/widgets:contents:", "acme/widgets:wiki:"},
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("POST /documents/prune = %d, want 204 (body %s)", rr.Code, rr.Body.String())
	}

	chunks, err := te.vector.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	refs := map[string]bool{}
	for _, c := range chunks {
		refs[c.RefDocID] = true
	}
	if len(refs) != 1 {
		t.Fatalf("after scoped prune refs = %v, want exactly one document", refs)
	}
	for ref := range refs {
		if !strings.Contains(ref, "issue_12") {
			t.Fatalf("after scoped prune ref = %q, want the issue document to survive", ref)
		}
	}
}
