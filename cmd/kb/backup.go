package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/store/sqlite"
)

func runBackupCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("backup", flag.ContinueOnError)
	fset.SetOutput(stderr)
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if fset.NArg() > 1 {
		fmt.Fprintln(stderr, "backup: expected at most one destination path")
		return 2
	}

	dest := fset.Arg(0)
	if dest == "" {
		dest = filepath.Join(env.PersistDir, "backups", "kb-"+time.Now().UTC().Format("20060102T150405Z")+".db")
	}

	src := filepath.Join(env.PersistDir, "kb.db")
	if _, err := os.Stat(src); os.IsNotExist(err) {
		fmt.Fprintf(stderr, "backup: no index yet at %s (run kb sync or kb reindex)\n", src)
		return 1
	} else if err != nil {
		fmt.Fprintf(stderr, "backup: inspect source %s: %v\n", src, err)
		return 1
	}

	db, err := sqlite.Open(context.Background(), src)
	if err != nil {
		fmt.Fprintf(stderr, "backup: opening db: %v\n", err)
		return 1
	}
	defer db.Close()

	if err := db.BackupTo(context.Background(), dest); err != nil {
		fmt.Fprintf(stderr, "backup: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "backup: wrote %s\n", dest)
	return 0
}
