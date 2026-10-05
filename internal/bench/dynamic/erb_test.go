package dynamic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadERB(t *testing.T) {
	dir := t.TempDir()
	qs := writeFile(t, dir, "q.jsonl", `{"question_id":"qst_1","question_type":"conflicting_info","question":"How many?","language":"en"}
{"question_id":"qst_2","question_type":"basic","question":"Other","language":"en"}
{"question_id":"qst_3","question_type":"conflicting_info","question":"Unlabelled","language":"en"}
`)
	st := writeFile(t, dir, "s.json", `{"qst_1":{"current":["30%"],"stale":["20%"]}}`)
	cases, err := LoadERB(qs, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 {
		t.Fatalf("cases: %+v", cases)
	}
	c := cases[0]
	if c.ID != "qst_1" || c.Language != "en" || c.Question != "How many?" || c.Control || c.Current[0] != "30%" || c.Stale[0] != "20%" {
		t.Fatalf("case: %+v", c)
	}
}

func TestLoadERBRejectsSpecWithoutQuestion(t *testing.T) {
	dir := t.TempDir()
	qs := writeFile(t, dir, "q.jsonl", `{"question_id":"qst_1","question":"q","language":"en"}`)
	st := writeFile(t, dir, "s.json", `{"qst_1":{"current":["a"],"stale":["b"]},"qst_9":{"current":["x"],"stale":["y"]},"qst_8":{"current":["x"],"stale":["y"]}}`)
	_, err := LoadERB(qs, st)
	if err == nil {
		t.Fatal("want error for stale spec entries with no question")
	}
	for _, id := range []string{"qst_8", "qst_9"} {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("error %q does not name %s", err, id)
		}
	}
}

func TestLoadERBErrors(t *testing.T) {
	dir := t.TempDir()
	qs := writeFile(t, dir, "q.jsonl", `{"question_id":"qst_1","question":"q","language":"en"}`)
	if _, err := LoadERB(qs, filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("want error for missing stale file")
	}
	bad := writeFile(t, dir, "bad.json", "{")
	if _, err := LoadERB(qs, bad); err == nil {
		t.Fatal("want decode error")
	}
	empty := writeFile(t, dir, "empty.json", `{"zzz":{"current":["a"],"stale":["b"]}}`)
	if _, err := LoadERB(qs, empty); err == nil {
		t.Fatal("want error when nothing matches")
	}
	if _, err := LoadERB(filepath.Join(dir, "none.jsonl"), empty); err == nil {
		t.Fatal("want error for missing questions")
	}
}

func TestScoreERBStyleNoBefore(t *testing.T) {
	cases := []Case{{ID: "a", Language: "en", Current: []string{"30%"}, Stale: []string{"20%"}}}
	r := Score(cases, nil, map[string]string{"a": "The target is 30% (earlier it was 20%)."})
	if !approx(r.CurrencyRate, 1) || r.AdoptionEligible != 0 || !approx(r.DCS, 1) {
		t.Fatalf("%+v", r)
	}
	r = Score(cases, nil, map[string]string{"a": "It was 20% and is now 30%."})
	if !approx(r.HedgeRate, 1) || !approx(r.DCS, 0) {
		t.Fatalf("%+v", r)
	}
}
