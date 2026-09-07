package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/config"
)

func writeSliceFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeSliceQuestions(t *testing.T, path string, qs []corpus.Question) string {
	t.Helper()
	if err := corpus.WriteQuestions(path, qs); err != nil {
		t.Fatalf("write questions: %v", err)
	}
	return path
}

func sliceCorpusFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSliceFile(t, root, filepath.Join("wiki", "general", "dsid_docA__semantic.txt"), "Doc A\n\nBody A\n")
	writeSliceFile(t, root, filepath.Join("github", "repo-x", "dsid_docB__notes.txt"), "Doc B\n\nBody B\n")
	writeSliceFile(t, root, filepath.Join("wiki", "general", "dsid_docC__unused.txt"), "Doc C\n\nBody C\n")
	return root
}

func TestBenchSliceCopiesReferencedDocsPreservingLayout(t *testing.T) {
	src := sliceCorpusFixture(t)
	outCorpus := filepath.Join(t.TempDir(), "out-corpus")
	outQuestions := filepath.Join(t.TempDir(), "out.jsonl")
	questions := writeSliceQuestions(t, filepath.Join(t.TempDir(), "questions.jsonl"), []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", ExpectedDocIDs: []string{"dsid_docA"}},
		{ID: "q2", Type: "constrained", Text: "two?", ExpectedDocIDs: []string{"dsid_docB", "dsid_docA"}},
	})

	var stdout, stderr bytes.Buffer
	code := runBenchSliceCmd([]string{
		"-corpus", src,
		"-questions", questions,
		"-out-corpus", outCorpus,
		"-out-questions", outQuestions,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{
		filepath.Join("wiki", "general", "dsid_docA__semantic.txt"),
		filepath.Join("github", "repo-x", "dsid_docB__notes.txt"),
	} {
		if _, err := os.Stat(filepath.Join(outCorpus, want)); err != nil {
			t.Errorf("expected copied file %s: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outCorpus, "wiki", "general", "dsid_docC__unused.txt")); err == nil {
		t.Error("unreferenced docC was copied, want absent")
	}
	if !strings.Contains(stdout.String(), "copied 2 documents") {
		t.Errorf("stdout = %q, want copied count", stdout.String())
	}
	if strings.Contains(stdout.String(), "missing document") {
		t.Errorf("stdout = %q, want no missing documents", stdout.String())
	}

	got, warns, err := corpus.LoadQuestions(outQuestions)
	if err != nil {
		t.Fatalf("LoadQuestions(out): %v", err)
	}
	if len(warns) != 0 || len(got) != 2 {
		t.Fatalf("out questions = %d (warns=%v), want 2", len(got), warns)
	}
}

func TestBenchSliceTypesFilterRespected(t *testing.T) {
	src := t.TempDir()
	writeSliceFile(t, src, filepath.Join("wiki", "dsid_basic__b.txt"), "B\n\nb\n")
	writeSliceFile(t, src, filepath.Join("github", "dsid_con__c.txt"), "C\n\nc\n")
	questions := writeSliceQuestions(t, filepath.Join(t.TempDir(), "questions.jsonl"), []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", ExpectedDocIDs: []string{"dsid_basic"}},
		{ID: "q2", Type: "constrained", Text: "two?", ExpectedDocIDs: []string{"dsid_con"}},
	})
	outCorpus := filepath.Join(t.TempDir(), "out-corpus")
	outQuestions := filepath.Join(t.TempDir(), "out.jsonl")

	var stdout, stderr bytes.Buffer
	code := runBenchSliceCmd([]string{
		"-corpus", src,
		"-questions", questions,
		"-types", "basic",
		"-out-corpus", outCorpus,
		"-out-questions", outQuestions,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outCorpus, "wiki", "dsid_basic__b.txt")); err != nil {
		t.Errorf("basic doc not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outCorpus, "github", "dsid_con__c.txt")); err == nil {
		t.Error("constrained doc copied despite -types basic, want absent")
	}
	got, _, err := corpus.LoadQuestions(outQuestions)
	if err != nil {
		t.Fatalf("LoadQuestions(out): %v", err)
	}
	if len(got) != 1 || got[0].Type != "basic" {
		t.Fatalf("out questions = %+v, want one basic question", got)
	}
}

func TestBenchSliceMissingDocReported(t *testing.T) {
	src := t.TempDir()
	questions := writeSliceQuestions(t, filepath.Join(t.TempDir(), "questions.jsonl"), []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", ExpectedDocIDs: []string{"dsid_missing"}},
	})
	outCorpus := filepath.Join(t.TempDir(), "out-corpus")
	outQuestions := filepath.Join(t.TempDir(), "out.jsonl")

	var stdout, stderr bytes.Buffer
	code := runBenchSliceCmd([]string{
		"-corpus", src,
		"-questions", questions,
		"-out-corpus", outCorpus,
		"-out-questions", outQuestions,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "missing document dsid_missing") {
		t.Errorf("stdout = %q, want missing document warning", stdout.String())
	}
	if !strings.Contains(stdout.String(), "copied 0 documents") {
		t.Errorf("stdout = %q, want copied 0 documents", stdout.String())
	}
	got, _, err := corpus.LoadQuestions(outQuestions)
	if err != nil {
		t.Fatalf("LoadQuestions(out): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("out questions = %d, want 1", len(got))
	}
}

func TestBenchSliceNoMatchingQuestions(t *testing.T) {
	src := sliceCorpusFixture(t)
	questions := writeSliceQuestions(t, filepath.Join(t.TempDir(), "questions.jsonl"), []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", ExpectedDocIDs: []string{"dsid_docA"}},
	})
	outCorpus := filepath.Join(t.TempDir(), "out-corpus")
	outQuestions := filepath.Join(t.TempDir(), "out.jsonl")

	var stdout, stderr bytes.Buffer
	code := runBenchSliceCmd([]string{
		"-corpus", src,
		"-questions", questions,
		"-types", "constrained",
		"-out-corpus", outCorpus,
		"-out-questions", outQuestions,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no questions matched") {
		t.Errorf("stdout = %q, want no-questions warning", stdout.String())
	}
	data, err := os.ReadFile(outQuestions)
	if err != nil {
		t.Fatalf("read out questions: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("out questions size = %d, want empty", len(data))
	}
	entries, err := os.ReadDir(outCorpus)
	if err != nil {
		t.Fatalf("read out corpus: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("out corpus entries = %d, want 0", len(entries))
	}
}

func TestBenchSliceLimitPerType(t *testing.T) {
	src := t.TempDir()
	for _, id := range []string{"dsid_a1", "dsid_a2", "dsid_a3", "dsid_b1", "dsid_b2"} {
		writeSliceFile(t, src, filepath.Join("wiki", id+"__d.txt"), "T\n\nt\n")
	}
	questions := writeSliceQuestions(t, filepath.Join(t.TempDir(), "questions.jsonl"), []corpus.Question{
		{ID: "q1", Type: "basic", Text: "1?", ExpectedDocIDs: []string{"dsid_a1"}},
		{ID: "q2", Type: "basic", Text: "2?", ExpectedDocIDs: []string{"dsid_a2"}},
		{ID: "q3", Type: "basic", Text: "3?", ExpectedDocIDs: []string{"dsid_a3"}},
		{ID: "q4", Type: "constrained", Text: "4?", ExpectedDocIDs: []string{"dsid_b1"}},
		{ID: "q5", Type: "constrained", Text: "5?", ExpectedDocIDs: []string{"dsid_b2"}},
	})
	outCorpus := filepath.Join(t.TempDir(), "out-corpus")
	outQuestions := filepath.Join(t.TempDir(), "out.jsonl")

	var stdout, stderr bytes.Buffer
	code := runBenchSliceCmd([]string{
		"-corpus", src,
		"-questions", questions,
		"-limit-per-type", "2",
		"-out-corpus", outCorpus,
		"-out-questions", outQuestions,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	got, _, err := corpus.LoadQuestions(outQuestions)
	if err != nil {
		t.Fatalf("LoadQuestions(out): %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("out questions = %d, want 4", len(got))
	}
	wantIDs := map[string]bool{"q1": true, "q2": true, "q4": true, "q5": true}
	for _, q := range got {
		if !wantIDs[q.ID] {
			t.Errorf("unexpected question %q kept", q.ID)
		}
	}
	if !strings.Contains(stdout.String(), "copied 4 documents") {
		t.Errorf("stdout = %q, want copied 4 documents", stdout.String())
	}
}

func TestBenchSliceRequiresCorpusAndQuestions(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBenchCmd([]string{"slice"}, config.Env{}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-corpus and -questions are required") {
		t.Errorf("stderr = %q, want required error", stderr.String())
	}
}

func TestBenchSliceDispatchesViaRunBenchCmd(t *testing.T) {
	src := sliceCorpusFixture(t)
	questions := writeSliceQuestions(t, filepath.Join(t.TempDir(), "questions.jsonl"), []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", ExpectedDocIDs: []string{"dsid_docA"}},
	})
	outCorpus := filepath.Join(t.TempDir(), "out-corpus")
	outQuestions := filepath.Join(t.TempDir(), "out.jsonl")

	var stdout, stderr bytes.Buffer
	code := runBenchCmd([]string{
		"slice",
		"-corpus", src,
		"-questions", questions,
		"-out-corpus", outCorpus,
		"-out-questions", outQuestions,
	}, config.Env{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "copied 1 documents") {
		t.Errorf("stdout = %q, want copied 1 documents", stdout.String())
	}
}

func TestBenchSliceClearsPreExistingOutputCorpus(t *testing.T) {
	src := sliceCorpusFixture(t)
	questions := writeSliceQuestions(t, filepath.Join(t.TempDir(), "questions.jsonl"), []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", ExpectedDocIDs: []string{"dsid_docA"}},
	})
	outCorpus := filepath.Join(t.TempDir(), "out-corpus")
	stale := writeSliceFile(t, outCorpus, filepath.Join("wiki", "general", "dsid_stale__x.txt"), "stale")

	var stdout, stderr bytes.Buffer
	code := runBenchSliceCmd([]string{
		"-corpus", src,
		"-questions", questions,
		"-out-corpus", outCorpus,
		"-out-questions", filepath.Join(t.TempDir(), "out.jsonl"),
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale output file still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outCorpus, "wiki", "general", "dsid_docA__semantic.txt")); err != nil {
		t.Fatalf("expected referenced file after clear: %v", err)
	}
}

func TestBenchSliceRejectsOutputEqualToSource(t *testing.T) {
	src := sliceCorpusFixture(t)
	questions := writeSliceQuestions(t, filepath.Join(t.TempDir(), "questions.jsonl"), []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", ExpectedDocIDs: []string{"dsid_docA"}},
	})

	var stdout, stderr bytes.Buffer
	code := runBenchSliceCmd([]string{
		"-corpus", src,
		"-questions", questions,
		"-out-corpus", src,
		"-out-questions", filepath.Join(t.TempDir(), "out.jsonl"),
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "must not be the source corpus") {
		t.Fatalf("stderr = %q, want overlap rejection", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(src, "wiki", "general", "dsid_docA__semantic.txt")); err != nil {
		t.Fatalf("source corpus was damaged: %v", err)
	}
}

func TestBenchSliceMissingCorpusDoesNotClearOutput(t *testing.T) {
	outCorpus := filepath.Join(t.TempDir(), "out-corpus")
	sentinel := writeSliceFile(t, outCorpus, "sentinel.txt", "keep")
	questions := writeSliceQuestions(t, filepath.Join(t.TempDir(), "questions.jsonl"), []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", ExpectedDocIDs: []string{"dsid_docA"}},
	})

	var stdout, stderr bytes.Buffer
	code := runBenchSliceCmd([]string{
		"-corpus", filepath.Join(t.TempDir(), "missing-corpus"),
		"-questions", questions,
		"-out-corpus", outCorpus,
		"-out-questions", filepath.Join(t.TempDir(), "out.jsonl"),
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "source corpus") {
		t.Fatalf("stderr = %q, want source corpus error", stderr.String())
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("pre-existing output was cleared despite missing source: %v", err)
	}
}

func TestValidateOutputDestination(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateOutputDestination(cwd, cwd); err == nil {
		t.Error("destination equal to cwd was accepted")
	}
	if err := validateOutputDestination(filepath.Dir(cwd), cwd); err == nil {
		t.Error("destination containing cwd was accepted")
	}
	if err := validateOutputDestination(string(os.PathSeparator), cwd); err == nil {
		t.Error("filesystem root was accepted")
	}
	if err := validateOutputDestination(filepath.Join(cwd, "out"), cwd); err != nil {
		t.Fatalf("destination inside cwd was rejected: %v", err)
	}
}

func TestValidateSourceOutputOverlapRejectsSameDir(t *testing.T) {
	src := t.TempDir()
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSourceOutputOverlap(src, src, info); err == nil {
		t.Fatal("same source and destination directory was accepted")
	}
}

func TestValidateSourceOutputOverlapRejectsParent(t *testing.T) {
	src := t.TempDir()
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSourceOutputOverlap(src, filepath.Join(src, "out"), info); err == nil {
		t.Fatal("destination inside source was accepted")
	}
	if err := validateSourceOutputOverlap(filepath.Join(src, "child"), src, info); err == nil {
		t.Fatal("destination containing source was accepted")
	}
}
