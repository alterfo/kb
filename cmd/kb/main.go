package main

import (
	"fmt"
	"os"

	"github.com/alterfo/kb/internal/config"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, usage())
		return 2
	}

	cmd := args[0]
	switch cmd {
	case "serve", "sync", "reindex", "doctor", "backup", "mcp", "describe", "verify", "bench", "bench-dragon", "bench-actualize", "config":
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s\n", cmd, usage())
		return 2
	}

	dotEnvPath := os.Getenv("KB_DOTENV")
	if dotEnvPath == "" {
		dotEnvPath = ".env"
	}
	lookup, _ := config.LookupWithDotEnv(dotEnvPath)
	env, err := config.LoadEnv(lookup)
	if err != nil {
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return 1
	}

	switch cmd {
	case "serve":
		return runServeCmd(args[1:], env, lookup, stdout, stderr)
	case "sync":
		return runSyncCmd(args[1:], env, stdout, stderr)
	case "reindex":
		return runReindexCmd(args[1:], env, stdout, stderr)
	case "doctor":
		return runDoctorCmd(args[1:], env, stdout, stderr)
	case "backup":
		return runBackupCmd(args[1:], env, stdout, stderr)
	case "mcp":
		return runMCPCmd(args[1:], env, lookup, stdout, stderr)
	case "describe":
		return runDescribeCmd(args[1:], env, stdout, stderr)
	case "verify":
		return runVerifyCmd(args[1:], env, stdout, stderr)
	case "bench":
		return runBenchCmd(args[1:], env, stdout, stderr)
	case "bench-dragon":
		return runBenchDragonCmd(args[1:], env, stdout, stderr)
	case "bench-actualize":
		return runBenchActualizeCmd(args[1:], env, stdout, stderr)
	case "config":
		return runConfigCmd(args[1:], env, lookup, stdout, stderr)
	}

	return 0
}

func usage() string {
	return "usage: kb <serve|sync|reindex|doctor|backup|mcp|describe|verify|bench|bench-dragon|bench-actualize|config>"
}
