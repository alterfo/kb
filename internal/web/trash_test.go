package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrash_ListRestoreEmpty(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/t1.md", doc("t1", "notes", "trash me content here"))
	te.index(t, "notes/t1.md")

	trashed, err := te.gov.Trash.SoftDelete("notes/t1.md")
	if err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/trash")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "notes/t1.md") {
		t.Errorf("trash page missing entry %q", trashed)
	}

	rr = postForm(t, te.server.Handler(), "/trash/restore", url.Values{"path": {strings.TrimPrefix(trashed, ".trash/")}})
	if !strings.Contains(rr.Body.String(), "restored") {
		t.Errorf("restore result missing: %q", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(te.root, "notes", "t1.md")); err != nil {
		t.Errorf("file not restored: %v", err)
	}

	if _, err := te.gov.Trash.SoftDelete("notes/t1.md"); err != nil {
		t.Fatalf("second SoftDelete: %v", err)
	}
	rr = postForm(t, te.server.Handler(), "/trash/empty", url.Values{})
	if !strings.Contains(rr.Body.String(), "trash emptied") {
		t.Errorf("empty result missing: %q", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(te.root, ".trash")); !os.IsNotExist(err) {
		t.Errorf("trash dir still exists after empty")
	}
}

func TestTrash_DegradesWithoutGovernance(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Deps{Root: root})
	rr := getPage(t, srv.Handler(), "/trash")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "governance is not configured") {
		t.Errorf("expected governance alert: %q", rr.Body.String())
	}
}
