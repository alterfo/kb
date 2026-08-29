package main

import (
	"os"
	"testing"
)

func TestRun_ServeFlagParseErrorExitsTwo(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening devnull: %v", err)
	}
	defer devNull.Close()

	code := run([]string{"serve", "--bogus"}, devNull, devNull)
	if code != 2 {
		t.Errorf("run(serve --bogus) = %d, want 2", code)
	}
}

func TestRun_SyncWithoutFlagsFails(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening devnull: %v", err)
	}
	defer devNull.Close()

	code := run([]string{"sync"}, devNull, devNull)
	if code != 2 {
		t.Errorf("run(sync) = %d, want 2", code)
	}
}

func TestRun_SyncAllWithMissingSourcesFileIsNoop(t *testing.T) {
	t.Setenv("KB_ROOT", t.TempDir())
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening devnull: %v", err)
	}
	defer devNull.Close()

	code := run([]string{"sync", "--all"}, devNull, devNull)
	if code != 0 {
		t.Errorf("run(sync --all) = %d, want 0", code)
	}
}

func TestRun_ReindexOnEmptyRootIsNoop(t *testing.T) {
	t.Setenv("KB_ROOT", t.TempDir())
	t.Setenv("PERSIST_DIR", t.TempDir())
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening devnull: %v", err)
	}
	defer devNull.Close()

	code := run([]string{"reindex"}, devNull, devNull)
	if code != 0 {
		t.Errorf("run(reindex) = %d, want 0", code)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening devnull: %v", err)
	}
	defer devNull.Close()

	code := run([]string{"bogus"}, devNull, devNull)
	if code != 2 {
		t.Errorf("run(bogus) = %d, want 2", code)
	}
}

func TestRun_NoArgs(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening devnull: %v", err)
	}
	defer devNull.Close()

	code := run(nil, devNull, devNull)
	if code != 2 {
		t.Errorf("run(nil) = %d, want 2", code)
	}
}
