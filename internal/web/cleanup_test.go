package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/llm"
)

func TestCleanup_ScanShowsEmptyDocAndApplyTrashes(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/empty.md", doc("empty", "notes", "tiny"))
	writeDoc(t, te.root, "notes/keep.md", doc("keep", "notes",
		"a sufficiently long body that will not be flagged as near-empty by the cleanup scanner because it exceeds the eighty character threshold comfortably"))

	rr := getPage(t, te.server.Handler(), "/cleanup")
	body := rr.Body.String()
	if !strings.Contains(body, "Near-empty documents") || !strings.Contains(body, "notes/empty.md") {
		t.Fatalf("cleanup plan missing empty doc: %q", body)
	}

	rr = postForm(t, te.server.Handler(), "/cleanup", url.Values{"action": {"trash:notes/empty.md"}})
	body = rr.Body.String()
	if !strings.Contains(body, "applied") {
		t.Errorf("apply result missing: %q", body)
	}
	if _, err := os.Stat(filepath.Join(te.root, "notes", "empty.md")); !os.IsNotExist(err) {
		t.Errorf("file not trashed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(te.root, ".trash", "notes", "empty.md")); err != nil {
		t.Errorf("file not in trash: %v", err)
	}
}

func TestCleanup_NoActionsSelected(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := postForm(t, te.server.Handler(), "/cleanup", url.Values{})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no actions selected") {
		t.Errorf("expected no-actions note")
	}
}

func TestCleanup_MergeProposalAndRewrite(t *testing.T) {
	chat := &fakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{Content: `{"title":"Merged","content":"merged content from llm"}`}, nil
	}}
	te := newTestEnv(t, chat)
	writeDoc(t, te.root, "notes/topic.md", doc("topic", "notes",
		"first topic body that is long enough to avoid the empty detection threshold and carries several sentences of factual content"))
	writeDoc(t, te.root, "notes/topic-final.md", doc("topic-final", "notes",
		"second topic body that is also long enough to avoid the empty detection threshold and carries several sentences of factual content"))

	rr := getPage(t, te.server.Handler(), "/cleanup")
	if !strings.Contains(rr.Body.String(), "Merge candidates") {
		t.Fatalf("merge candidates not shown: %q", rr.Body.String())
	}

	rr = postForm(t, te.server.Handler(), "/cleanup", url.Values{
		"action": {"merge:notes/topic.md;notes/topic-final.md"},
	})
	body := rr.Body.String()
	if !strings.Contains(body, "merged content from llm") {
		t.Fatalf("proposal not rendered: %q", body)
	}

	rr = postForm(t, te.server.Handler(), "/cleanup/rewrite", url.Values{
		"paths":   {"notes/topic.md;notes/topic-final.md"},
		"content": {"# Merged\n\nfinal merged content"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("rewrite status = %d", rr.Code)
	}
	merged := false
	for _, p := range []string{"notes/topic.md", "notes/topic-final.md"} {
		data, err := os.ReadFile(filepath.Join(te.root, filepath.FromSlash(p)))
		if err == nil && strings.Contains(string(data), "final merged content") {
			merged = true
		}
	}
	if !merged {
		t.Errorf("merged content not written to primary path")
	}
}

func TestCleanup_DegradesWithoutGovernance(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Deps{Root: root})
	rr := getPage(t, srv.Handler(), "/cleanup")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "governance is not configured") {
		t.Errorf("expected governance alert: %q", rr.Body.String())
	}
}
