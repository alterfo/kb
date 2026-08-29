package governance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/render"
)

type fakeIndexer struct {
	removed []string
	updated []string
	// removeErrOn/addErrOn, when non-empty, make Remove/AddOrUpdateDocument
	// fail for that exact path; addErr/removeErr, when set, make every call
	// fail.
	removeErrOn string
	addErrOn    string
	removeErr   error
	addErr      error
}

func (f *fakeIndexer) RemoveDocument(ctx context.Context, path string) error {
	f.removed = append(f.removed, path)
	if f.removeErr != nil {
		return f.removeErr
	}
	if f.removeErrOn != "" && f.removeErrOn == path {
		return errors.New("boom: remove")
	}
	return nil
}

func (f *fakeIndexer) AddOrUpdateDocument(ctx context.Context, path string) error {
	f.updated = append(f.updated, path)
	if f.addErr != nil {
		return f.addErr
	}
	if f.addErrOn != "" && f.addErrOn == path {
		return errors.New("boom: add")
	}
	return nil
}

type fakeChat struct {
	resp llm.ChatResponse
	err  error
}

func (f fakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return f.resp, f.err
}

type fakeTombstones struct {
	added map[string]string // sourceKey -> last id
	errOn string            // id that makes Add fail
}

func (f *fakeTombstones) Add(sourceKey, id string) error {
	if f.errOn != "" && f.errOn == id {
		return errors.New("boom: tombstone")
	}
	if f.added == nil {
		f.added = map[string]string{}
	}
	f.added[sourceKey] = id
	return nil
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func writeDoc(t *testing.T, root, rel string, d connector.Document) {
	t.Helper()
	data, err := render.Render(d)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	writeFile(t, root, rel, string(data))
}

func mustExist(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("expected %s to exist: %v", rel, err)
	}
}

func mustNotExist(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		t.Fatalf("expected %s to not exist", rel)
	}
}

func readRaw(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}
