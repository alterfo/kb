package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/llm"
)

type generateFakeChat struct {
	response string
	err      error
}

func (f generateFakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if f.err != nil {
		return llm.ChatResponse{}, f.err
	}
	return llm.ChatResponse{Content: f.response, FinishReason: "stop"}, nil
}

func writeCorpusDoc(t *testing.T, root, source, id, slug, content string) {
	t.Helper()
	dir := filepath.Join(root, source)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+"__"+slug+".txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBenchGenerateWritesQuestions(t *testing.T) {
	dir := t.TempDir()
	corpusDir := filepath.Join(dir, "corpus")
	writeCorpusDoc(t, corpusDir, "doka", "dsid_ru0000000001", "docker",
		"Что такое Docker\n\nDocker чаще всего применяется для развёртывания серверных приложений.")
	writeCorpusDoc(t, corpusDir, "doka", "dsid_ru0000000002", "nginx",
		"Nginx\n\nNginx разработан Игорем Сысоевым в 2004 году.")

	seedPath := filepath.Join(dir, "seed.jsonl")
	if err := corpus.WriteQuestions(seedPath, []corpus.Question{
		{ID: "s1", Type: "single-doc", Text: "Что такое микросервис?", GoldAnswer: "Отдельное приложение.", AnswerFacts: []string{"факт"}, Language: "ru"},
	}); err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(dir, "generated.jsonl")
	chat := generateFakeChat{response: `{"question":"Для чего чаще всего применяется Docker?","gold_answer":"для развёртывания серверных приложений","answer_facts":["Docker применяется для развёртывания серверных приложений."]}`}

	generated, warns, err := benchGenerate(context.Background(), chat, "test-model", corpusDir, seedPath, outPath, 1)
	if err != nil {
		t.Fatalf("benchGenerate: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
	if generated != 1 {
		t.Fatalf("generated = %d, want 1", generated)
	}

	qs, qwarns, err := corpus.LoadQuestions(outPath)
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}
	if len(qwarns) != 0 || len(qs) != 1 {
		t.Fatalf("questions = %d (warnings %v), want 1 clean", len(qs), qwarns)
	}
	if qs[0].ID != "qgen_dsid_ru0000000001" {
		t.Errorf("ID = %q", qs[0].ID)
	}
	if len(qs[0].ExpectedDocIDs) != 1 || qs[0].ExpectedDocIDs[0] != "dsid_ru0000000001" {
		t.Errorf("ExpectedDocIDs = %v", qs[0].ExpectedDocIDs)
	}
}

func TestBenchGenerateReturnsErrorOnChatFailure(t *testing.T) {
	dir := t.TempDir()
	corpusDir := filepath.Join(dir, "corpus")
	writeCorpusDoc(t, corpusDir, "doka", "dsid_ru0000000001", "docker", "Что такое Docker\n\nТекст про Docker.")

	chat := generateFakeChat{err: errors.New("chat down")}
	generated, _, err := benchGenerate(context.Background(), chat, "test-model", corpusDir, "", filepath.Join(dir, "out.jsonl"), 0)
	if err == nil {
		t.Fatal("benchGenerate err = nil, want chat error")
	}
	if generated != 0 {
		t.Errorf("generated = %d, want 0", generated)
	}
}

type generateSequencedChat struct {
	responses []string
	errs      []error
	calls     int
}

func (f *generateSequencedChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	idx := f.calls
	f.calls++
	if idx < len(f.errs) && f.errs[idx] != nil {
		return llm.ChatResponse{}, f.errs[idx]
	}
	return llm.ChatResponse{Content: f.responses[idx], FinishReason: "stop"}, nil
}

func TestBenchGenerateWritesPartialResultsOnLaterFailure(t *testing.T) {
	dir := t.TempDir()
	corpusDir := filepath.Join(dir, "corpus")
	writeCorpusDoc(t, corpusDir, "doka", "dsid_ru0000000001", "docker",
		"Что такое Docker\n\nDocker чаще всего применяется для развёртывания серверных приложений.")
	writeCorpusDoc(t, corpusDir, "doka", "dsid_ru0000000002", "nginx",
		"Nginx\n\nNginx разработан Игорем Сысоевым в 2004 году.")

	outPath := filepath.Join(dir, "generated.jsonl")
	chat := &generateSequencedChat{
		responses: []string{
			`{"question":"Для чего чаще всего применяется Docker?","gold_answer":"для развёртывания серверных приложений","answer_facts":["Docker применяется для развёртывания серверных приложений."]}`,
			"",
		},
		errs: []error{nil, errors.New("chat down on second doc")},
	}

	generated, _, err := benchGenerate(context.Background(), chat, "test-model", corpusDir, "", outPath, 0)
	if err == nil {
		t.Fatal("benchGenerate err = nil, want error from the second document's failed chat call")
	}
	if generated != 1 {
		t.Fatalf("generated = %d, want 1 (first document succeeded before the second failed)", generated)
	}

	if _, statErr := os.Stat(outPath); statErr != nil {
		t.Fatalf("output file was not written despite a partial result: %v", statErr)
	}
	qs, _, loadErr := corpus.LoadQuestions(outPath)
	if loadErr != nil {
		t.Fatalf("LoadQuestions: %v", loadErr)
	}
	if len(qs) != 1 {
		t.Fatalf("questions on disk = %d, want 1 (the CLI reports a partial count and that count must match the file)", len(qs))
	}
}

func TestRunBenchGenerateMissingCorpus(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBenchGenerateCmd(nil, config.Env{}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-corpus is required") {
		t.Errorf("stderr = %q, want -corpus requirement", stderr.String())
	}
}

func TestRunBenchCmdDispatchesGenerate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBenchCmd([]string{"generate"}, config.Env{}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-corpus is required") {
		t.Errorf("stderr = %q, want -corpus requirement", stderr.String())
	}
}
