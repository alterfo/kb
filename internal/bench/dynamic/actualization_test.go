package dynamic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFromActualizationControlStaleIsForeignCorrections(t *testing.T) {
	qs := []actualizationQuestion{
		{Question: "q1", BeforeAnswer: "15 марта 2026", AfterAnswer: "20 июня 2026", ExpectedBefore: "15 марта 2026", ExpectedAfter: "20 июня 2026", Affected: true, TargetDocID: "roadmap"},
		{Question: "q2", BeforeAnswer: "Новосибирск", AfterAnswer: "Новосибирск и 20 июня 2026", ExpectedBefore: "Новосибирск", ExpectedAfter: "Новосибирск", TargetDocID: "office"},
		{Question: "q3", BeforeAnswer: "Новосибирск", AfterAnswer: "Новосибирск", ExpectedBefore: "Новосибирск", ExpectedAfter: "Новосибирск", TargetDocID: "office"},
	}
	cases, before, after, err := FromActualization(qs)
	if err != nil {
		t.Fatal(err)
	}
	if !cases[1].Control || len(cases[1].Stale) != 1 || cases[1].Stale[0] != "20 июня 2026" {
		t.Fatalf("control stale: %+v", cases[1])
	}
	r := Score(cases, before, after)
	if r.Affected != 1 || r.Control != 2 {
		t.Fatalf("%+v", r)
	}
	if !approx(r.Adoption, 1) || !approx(r.Stability, 0.5) {
		t.Fatalf("adoption %v stability %v", r.Adoption, r.Stability)
	}
}

func TestLoadActualization(t *testing.T) {
	p := filepath.Join(t.TempDir(), "run.json")
	body := `{"questions":[{"question":"q","before_answer":"a1","after_answer":"b1","expected_before":"a1","expected_after":"b1","affected":true,"target_doc_id":"d"}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, _, _, err := LoadActualization(p)
	if err != nil || len(cases) != 1 {
		t.Fatalf("%v %v", cases, err)
	}
	if _, _, _, err := LoadActualization(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("want error")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("{"), 0o644)
	if _, _, _, err := LoadActualization(bad); err == nil {
		t.Fatal("want decode error")
	}
}

func TestFromActualizationRejectsEmpty(t *testing.T) {
	if _, _, _, err := FromActualization(nil); err == nil {
		t.Fatal("want error for no questions")
	}
	p := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(p, []byte(`{"questions":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := LoadActualization(p); err == nil {
		t.Fatal("want error for empty run.json")
	}
}
