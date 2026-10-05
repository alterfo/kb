package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchDynamicERB(t *testing.T) {
	dir := t.TempDir()
	w := func(n, b string) string {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	q := w("q.jsonl", `{"question_id":"qst_1","question_type":"conflicting_info","question":"How many?","language":"en"}`)
	s := w("s.json", `{"qst_1":{"current":["30%"],"stale":["20%"]}}`)
	a := w("a.jsonl", `{"question_id":"qst_1","answer":"The target is 30%.\n\nEarlier it was 20%."}`)
	var out, errb bytes.Buffer
	if code := runBenchDynamicCmd([]string{"-questions", q, "-stale", s, "-submission", a}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "currency=1.00") {
		t.Fatalf("out: %s", out.String())
	}
}

func TestBenchDynamicUsageAndErrors(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runBenchDynamicCmd(nil, &out, &errb); code != 2 {
		t.Fatalf("code %d", code)
	}
	if code := runBenchDynamicCmd([]string{"-actualization", "/nonexistent.json"}, &out, &errb); code != 1 {
		t.Fatalf("code %d", code)
	}
}
