package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/store/sqlite"
)

func TestRunBackupCmdWritesCopy(t *testing.T) {
	persist := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(persist, "kb.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	env := config.Defaults()
	env.PersistDir = persist
	dest := filepath.Join(t.TempDir(), "kb-backup.db")

	var stdout, stderr bytes.Buffer
	code := runBackupCmd([]string{dest}, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runBackupCmd = %d, stderr=%s, stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "backup: wrote "+dest) {
		t.Fatalf("stdout missing backup path:\n%s", stdout.String())
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("backup file was not created: %v", err)
	}

	copyDB, err := sqlite.Open(context.Background(), dest)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer copyDB.Close()
	if got, err := copyDB.IntegrityCheck(context.Background()); err != nil || got != "ok" {
		t.Fatalf("backup integrity = (%q,%v), want (ok,nil)", got, err)
	}
}

func TestRunBackupCmdMissingSourceFails(t *testing.T) {
	env := config.Defaults()
	env.PersistDir = t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runBackupCmd([]string{filepath.Join(t.TempDir(), "out.db")}, env, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runBackupCmd = %d, want 1; stderr=%s", code, stderr.String())
	}
}

func TestRunBackupCmdTooManyArgsFails(t *testing.T) {
	env := config.Defaults()

	var stdout, stderr bytes.Buffer
	code := runBackupCmd([]string{"a.db", "b.db"}, env, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("runBackupCmd = %d, want 2; stderr=%s", code, stderr.String())
	}
}
