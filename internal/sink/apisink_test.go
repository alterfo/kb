package sink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/transport"
)

func newTestClient() *transport.Client {
	return transport.NewClient(transport.Config{MaxRetries: 0})
}

func TestAPISinkWritePostsDocument(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody connector.Document
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewAPISink(newTestClient(), srv.URL)
	d := connector.Document{ID: "1", Source: "github-myorg", Title: "T"}
	if err := s.Write(context.Background(), d); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/documents" {
		t.Fatalf("path = %q, want /documents", gotPath)
	}
	if gotBody.ID != "1" || gotBody.Source != "github-myorg" {
		t.Fatalf("decoded body = %+v", gotBody)
	}
}

func TestAPISinkWriteErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := NewAPISink(newTestClient(), srv.URL)
	err := s.Write(context.Background(), connector.Document{ID: "1", Source: "s"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestAPISinkPrune(t *testing.T) {
	var gotBody prunePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/documents/prune" {
			t.Errorf("path = %q, want /documents/prune", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewAPISink(newTestClient(), srv.URL)
	seen := map[string]struct{}{"1": {}, "2": {}}
	if err := s.Prune(context.Background(), "github-myorg", seen, "repo:contents:", "repo:wiki:"); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if gotBody.Source != "github-myorg" {
		t.Fatalf("source = %q, want github-myorg", gotBody.Source)
	}
	if len(gotBody.Seen) != 2 {
		t.Fatalf("seen = %v, want 2 ids", gotBody.Seen)
	}
	if len(gotBody.Prefixes) != 2 || gotBody.Prefixes[0] != "repo:contents:" || gotBody.Prefixes[1] != "repo:wiki:" {
		t.Fatalf("prefixes = %v, want [repo:contents: repo:wiki:]", gotBody.Prefixes)
	}
}

func TestAPISinkTombstone(t *testing.T) {
	var gotBody tombstonePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/documents/tombstone" {
			t.Errorf("path = %q, want /documents/tombstone", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := NewAPISink(newTestClient(), srv.URL)
	if err := s.Tombstone(context.Background(), "github-myorg", "42"); err != nil {
		t.Fatalf("Tombstone: %v", err)
	}

	if gotBody.Source != "github-myorg" || gotBody.ID != "42" {
		t.Fatalf("decoded body = %+v", gotBody)
	}
}
