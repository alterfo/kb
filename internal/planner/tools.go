package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/llm"
)

const (
	defaultBashTimeout = 5 * time.Minute
	maxToolOutput      = 64 * 1024
)

// tools implements the agent's tool set: shell execution plus file and
// search operations, all sandboxed to a single working directory. Every tool
// returns a string result for the model; a non-zero command exit is reported
// inside the result rather than as an error so the model can react to it.
type tools struct {
	workDir     string
	bashTimeout time.Duration
}

func newTools(workDir string) *tools {
	return &tools{workDir: workDir, bashTimeout: defaultBashTimeout}
}

func schema(props map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func (t *tools) specs() []llm.Tool {
	return []llm.Tool{
		llm.NewTool("bash",
			"Run a shell command in the project directory and return its combined output. Use for running tests, builds, git inspection, and any CLI. Do not start long-running servers.",
			schema(map[string]any{"command": strProp("Shell command to run. Example: 'go test ./...'")}, "command")),
		llm.NewTool("read_file",
			"Read a text file from the project and return its contents with line numbers.",
			schema(map[string]any{
				"path":   strProp("Path relative to the project root."),
				"offset": intProp("1-based line to start from (optional)."),
				"limit":  intProp("Maximum number of lines to return (optional)."),
			}, "path")),
		llm.NewTool("write_file",
			"Create a new file or overwrite an existing one with the given content.",
			schema(map[string]any{
				"path":    strProp("Path relative to the project root."),
				"content": strProp("Full file contents to write."),
			}, "path", "content")),
		llm.NewTool("edit_file",
			"Replace a single occurrence of old_string with new_string in a file. The old_string must match exactly once.",
			schema(map[string]any{
				"path":       strProp("Path relative to the project root."),
				"old_string": strProp("Exact text to replace."),
				"new_string": strProp("Replacement text."),
			}, "path", "old_string", "new_string")),
		llm.NewTool("glob",
			"Find files matching a glob pattern relative to the project root. Example pattern: '**/*_test.go'.",
			schema(map[string]any{"pattern": strProp("Glob pattern, e.g. 'src/**/*.go'.")}, "pattern")),
		llm.NewTool("grep",
			"Search file contents for a regular expression and return matches as path:line: text.",
			schema(map[string]any{"pattern": strProp("Regular expression to search for.")}, "pattern")),
		llm.NewTool("git",
			"Run a git command in the project directory (status, diff, log, branch, add, ...). Use this for inspection; the orchestrator handles commits.",
			schema(map[string]any{"args": strProp("Space-separated git arguments, e.g. 'status --short' or 'diff'.")}, "args")),
	}
}

func (t *tools) exec(ctx context.Context, name, argsJSON string) (string, error) {
	args, err := parseArgs(argsJSON)
	if err != nil {
		return "", err
	}
	switch name {
	case "bash":
		return t.bash(ctx, strArg(args, "command"))
	case "read_file":
		return t.readFile(args)
	case "write_file":
		return t.writeFile(args)
	case "edit_file":
		return t.editFile(args)
	case "glob":
		return t.glob(args)
	case "grep":
		return t.grep(args)
	case "git":
		return t.git(ctx, strArg(args, "args"))
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func parseArgs(jsonStr string) (map[string]any, error) {
	if strings.TrimSpace(jsonStr) == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return nil, fmt.Errorf("invalid tool arguments: %w", err)
	}
	return m, nil
}

func strArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func intArg(args map[string]any, key string, def int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return def
}

func (t *tools) resolve(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path not allowed: %s", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root: %s", rel)
	}
	return filepath.Join(t.workDir, clean), nil
}

func (t *tools) bash(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("empty command")
	}
	cctx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, t.bashTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(cctx, "sh", "-c", command)
	cmd.Dir = t.workDir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := truncate(buf.String(), maxToolOutput)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return out + fmt.Sprintf("\n[exit status %d]", exitErr.ExitCode()), nil
		}
		if cctx.Err() != nil {
			return out, fmt.Errorf("command %q: %w", command, cctx.Err())
		}
		return out, fmt.Errorf("command %q: %w", command, err)
	}
	return out, nil
}

func (t *tools) readFile(args map[string]any) (string, error) {
	path, err := t.resolve(strArg(args, "path"))
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	offset := intArg(args, "offset", 1)
	limit := intArg(args, "limit", 0)
	lines := strings.Split(string(b), "\n")
	return renderLines(path, lines, offset, limit), nil
}

func renderLines(path string, lines []string, offset, limit int) string {
	if offset < 1 {
		offset = 1
	}
	start := offset - 1
	if start > len(lines) {
		start = len(lines)
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d lines total)\n", path, len(lines))
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, lines[i])
	}
	return b.String()
}

func (t *tools) writeFile(args map[string]any) (string, error) {
	path, err := t.resolve(strArg(args, "path"))
	if err != nil {
		return "", err
	}
	content := strArg(args, "content")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create parent dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
}

func (t *tools) editFile(args map[string]any) (string, error) {
	path, err := t.resolve(strArg(args, "path"))
	if err != nil {
		return "", err
	}
	oldS := strArg(args, "old_string")
	newS := strArg(args, "new_string")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(b)
	n := strings.Count(content, oldS)
	if n == 0 {
		return "", fmt.Errorf("old_string not found in %s", path)
	}
	if n > 1 {
		return "", fmt.Errorf("old_string found %d times in %s; it must match exactly once", n, path)
	}
	replaced := strings.Replace(content, oldS, newS, 1)
	if err := os.WriteFile(path, []byte(replaced), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return fmt.Sprintf("replaced 1 occurrence in %s", path), nil
}

func (t *tools) glob(args map[string]any) (string, error) {
	pattern := strArg(args, "pattern")
	if pattern == "" {
		return "", fmt.Errorf("empty pattern")
	}
	if filepath.IsAbs(pattern) {
		return "", fmt.Errorf("absolute pattern not allowed: %s", pattern)
	}
	matches, err := filepath.Glob(filepath.Join(t.workDir, pattern))
	if err != nil {
		return "", fmt.Errorf("glob: %w", err)
	}
	rel := make([]string, 0, len(matches))
	for _, m := range matches {
		r, err := filepath.Rel(t.workDir, m)
		if err == nil {
			rel = append(rel, r)
		}
	}
	sort.Strings(rel)
	if len(rel) == 0 {
		return "no matches", nil
	}
	return strings.Join(rel, "\n"), nil
}

func (t *tools) grep(args map[string]any) (string, error) {
	pattern := strArg(args, "pattern")
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	var out []string
	_ = filepath.WalkDir(t.workDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(t.workDir, path)
		for i, line := range strings.Split(string(b), "\n") {
			if re.MatchString(line) {
				out = append(out, fmt.Sprintf("%s:%d: %s", rel, i+1, line))
			}
		}
		return nil
	})
	if len(out) == 0 {
		return "no matches", nil
	}
	return truncate(strings.Join(out, "\n"), maxToolOutput), nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".svn", ".hg", "node_modules", "vendor":
		return true
	}
	return false
}

func (t *tools) git(ctx context.Context, args string) (string, error) {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty git args")
	}
	cmd := exec.CommandContext(ctx, "git", fields...)
	cmd.Dir = t.workDir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := truncate(buf.String(), maxToolOutput)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return out + fmt.Sprintf("\n[exit status %d]", exitErr.ExitCode()), nil
		}
		return out, fmt.Errorf("git: %w", err)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... [truncated]"
}
