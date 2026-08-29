package importer

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

var testExtSeq atomic.Int64

func testExt(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, testExtSeq.Add(1))
}

type fakeImporter struct{ ext string }

func (f *fakeImporter) Ext() string { return f.ext }
func (f *fakeImporter) Import(path string) ([]connector.Document, error) {
	return []connector.Document{{ID: path}}, nil
}

func TestRegisterAndNew(t *testing.T) {
	ext := testExt(".regtest-a")
	Register(ext, func() FileImporter { return &fakeImporter{ext: ext} })

	imp, err := New(ext)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if imp.Ext() != ext {
		t.Fatalf("Ext() = %q, want %q", imp.Ext(), ext)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	ext := testExt(".regtest-b")
	Register(ext, func() FileImporter { return &fakeImporter{ext: ext} })

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	Register(ext, func() FileImporter { return &fakeImporter{ext: ext} })
}

func TestNewUnknownExtension(t *testing.T) {
	_, err := New(".regtest-does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown extension")
	}
}
