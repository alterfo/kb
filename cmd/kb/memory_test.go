package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alterfo/kb/internal/gguf"
)

func runMainForMemoryTest(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	stdoutPath := filepath.Join(dir, "stdout")
	stderrPath := filepath.Join(dir, "stderr")
	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open stdout: %v", err)
	}
	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		stdout.Close()
		t.Fatalf("open stderr: %v", err)
	}
	code := run(args, stdout, stderr)
	if err := stdout.Close(); err != nil {
		t.Fatalf("close stdout: %v", err)
	}
	if err := stderr.Close(); err != nil {
		t.Fatalf("close stderr: %v", err)
	}
	stdoutData, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderrData, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return code, string(stdoutData), string(stderrData)
}

func writeMemoryTestGGUF(t *testing.T, dir string) string {
	t.Helper()
	w := gguf.NewWriter()
	w.AddMetadata("general.architecture", gguf.Str("qwen4exp"))
	w.AddMetadata("general.name", gguf.Str("synthetic"))
	w.AddMetadata("qwen4exp.ple.ngram_size", gguf.Uint32(3))
	w.AddMetadata("qwen4exp.ple.heads_per_ngram", gguf.Uint32(1))
	w.AddMetadata("qwen4exp.ple.layer_multipliers", gguf.Uint64Array([]uint64{1, 2, 3}))
	w.AddMetadata("qwen4exp.ple.head_offsets", gguf.Uint64Array([]uint64{0, 100}))
	w.AddMetadata("qwen4exp.ple.head_vocab_sizes", gguf.Uint64Array([]uint64{100, 100}))
	w.AddMetadata("qwen4exp.ple.eos_token_id", gguf.Uint32(5))
	w.AddMetadata("tokenizer.ggml.tokens", gguf.StringArray([]string{"a"}))
	w.AddMetadata("tokenizer.ggml.pre", gguf.Str("gpt2"))

	const (
		rowDim      = 160
		nRows       = 200
		bytesPerRow = 170
	)
	table := make([]byte, nRows*bytesPerRow)
	w.AddTensor("per_layer_token_embd.weight", []uint64{rowDim, nRows}, 8, table)

	data, err := w.Bytes()
	if err != nil {
		t.Fatalf("gguf writer: %v", err)
	}
	path := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write synthetic gguf: %v", err)
	}
	return path
}

func writeMemoryTestKnowledge(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}
	return path
}

func TestRunMemoryCompileSmoke(t *testing.T) {
	dir := t.TempDir()
	ggufPath := writeMemoryTestGGUF(t, dir)
	knowledgePath := writeMemoryTestKnowledge(t, dir, "knowledge.json", `{"entries":[{"trigger":"a","op":"zero"}]}`)
	out := filepath.Join(dir, "model.gguf.plepatch")

	code, _, stderr := runMainForMemoryTest(t, "memory", "compile", "-gguf", ggufPath, "-knowledge", knowledgePath, "-out", out)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read patch: %v", err)
	}
	if string(data[:8]) != "PLEOVLY1" {
		t.Fatalf("patch magic = %q, want PLEOVLY1", data[:8])
	}
}

func TestRunMemoryCompileDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	ggufPath := writeMemoryTestGGUF(t, dir)
	knowledgePath := writeMemoryTestKnowledge(t, dir, "knowledge.json", `{"entries":[{"trigger":"a","op":"zero"}]}`)
	out := filepath.Join(dir, "model.gguf.plepatch")

	code, _, stderr := runMainForMemoryTest(t, "memory", "compile", "-gguf", ggufPath, "-knowledge", knowledgePath, "-out", out, "-dry-run")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("dry-run created %s", out)
	}
}

func TestRunMemoryCompileReportJSON(t *testing.T) {
	dir := t.TempDir()
	ggufPath := writeMemoryTestGGUF(t, dir)
	knowledgePath := writeMemoryTestKnowledge(t, dir, "knowledge.json", `{"entries":[{"trigger":"a","op":"zero"}]}`)
	out := filepath.Join(dir, "model.gguf.plepatch")
	reportPath := filepath.Join(dir, "report.json")

	code, _, stderr := runMainForMemoryTest(t, "memory", "compile", "-gguf", ggufPath, "-knowledge", knowledgePath, "-out", out, "-report", reportPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("report is not valid JSON: %q", data)
	}
	var report map[string]any
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if report["model"] != "synthetic" {
		t.Fatalf("report model = %v, want synthetic", report["model"])
	}
}

func TestRunMemoryCompileUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no subcommand", []string{"memory"}},
		{"unknown subcommand", []string{"memory", "bogus"}},
		{"missing both flags", []string{"memory", "compile"}},
		{"missing knowledge", []string{"memory", "compile", "-gguf", "model.gguf"}},
		{"missing gguf", []string{"memory", "compile", "-knowledge", "knowledge.json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, _ := runMainForMemoryTest(t, tc.args...)
			if code != 2 {
				t.Fatalf("code = %d, want 2", code)
			}
		})
	}
}

func TestRunMemoryCompileCorruptGGUF(t *testing.T) {
	dir := t.TempDir()
	ggufPath := filepath.Join(dir, "bad.gguf")
	if err := os.WriteFile(ggufPath, []byte("not a gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	knowledgePath := writeMemoryTestKnowledge(t, dir, "knowledge.json", `{"entries":[{"trigger":"a","op":"zero"}]}`)
	out := filepath.Join(dir, "model.gguf.plepatch")

	code, _, stderr := runMainForMemoryTest(t, "memory", "compile", "-gguf", ggufPath, "-knowledge", knowledgePath, "-out", out)
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr = %q", code, stderr)
	}
}
