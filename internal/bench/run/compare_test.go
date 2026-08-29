package run

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareOverlappingTypesAndLanguages(t *testing.T) {
	baseline := &Report{
		Total:        10,
		AbstainTotal: 2,
		Types: map[string]*TypeStat{
			"basic":  {Count: 5, AvgRecall: 0.60, Abstain: 1, Cited: 3},
			"answer": {Count: 5, AvgRecall: 0.40, Abstain: 1, Cited: 2},
		},
		Languages: map[string]*TypeStat{
			"ru": {Count: 5, AvgRecall: 0.70, Abstain: 1, Cited: 4},
			"en": {Count: 5, AvgRecall: 0.50, Abstain: 1, Cited: 1},
		},
	}
	candidate := &Report{
		Total:        10,
		AbstainTotal: 3,
		Types: map[string]*TypeStat{
			"basic":  {Count: 5, AvgRecall: 0.80, Abstain: 0, Cited: 4},
			"answer": {Count: 5, AvgRecall: 0.35, Abstain: 3, Cited: 2},
		},
		Languages: map[string]*TypeStat{
			"ru": {Count: 5, AvgRecall: 0.90, Abstain: 0, Cited: 5},
			"en": {Count: 5, AvgRecall: 0.40, Abstain: 3, Cited: 1},
		},
	}

	res := Compare(baseline, candidate)

	if res.TotalDelta != 0 {
		t.Errorf("TotalDelta = %d, want 0", res.TotalDelta)
	}
	if res.AbstainTotalDelta != 1 {
		t.Errorf("AbstainTotalDelta = %d, want 1", res.AbstainTotalDelta)
	}

	basic := res.Types["basic"]
	if math.Abs(basic.AvgRecall-0.20) > 1e-9 || basic.Abstain != -1 || basic.Cited != 1 {
		t.Errorf("basic delta = %+v, want recall=0.20 abstain=-1 cited=1", basic)
	}
	answer := res.Types["answer"]
	if math.Abs(answer.AvgRecall+0.05) > 1e-9 || answer.Abstain != 2 || answer.Cited != 0 {
		t.Errorf("answer delta = %+v, want recall=-0.05 abstain=2 cited=0", answer)
	}

	ru := res.Languages["ru"]
	if math.Abs(ru.AvgRecall-0.20) > 1e-9 || ru.Abstain != -1 || ru.Cited != 1 {
		t.Errorf("ru delta = %+v, want recall=0.20 abstain=-1 cited=1", ru)
	}
	en := res.Languages["en"]
	if math.Abs(en.AvgRecall+0.10) > 1e-9 || en.Abstain != 2 || en.Cited != 0 {
		t.Errorf("en delta = %+v, want recall=-0.10 abstain=2 cited=0", en)
	}
}

func TestCompareKeysPresentOnOnlyOneSide(t *testing.T) {
	baseline := &Report{
		Types:     map[string]*TypeStat{"basic": {Count: 1, AvgRecall: 0.5, Abstain: 1, Cited: 1}},
		Languages: map[string]*TypeStat{"ru": {Count: 1, AvgRecall: 0.5, Abstain: 1, Cited: 1}},
	}
	candidate := &Report{
		Types:     map[string]*TypeStat{"answer": {Count: 1, AvgRecall: 0.25, Abstain: 0, Cited: 0}},
		Languages: map[string]*TypeStat{"en": {Count: 1, AvgRecall: 0.25, Abstain: 0, Cited: 0}},
	}

	res := Compare(baseline, candidate)

	if got := res.Types["basic"]; got.AvgRecall != -0.5 || got.Abstain != -1 || got.Cited != -1 {
		t.Errorf("baseline-only type delta = %+v, want recall=-0.5 abstain=-1 cited=-1", got)
	}
	if got := res.Types["answer"]; got.AvgRecall != 0.25 || got.Abstain != 0 || got.Cited != 0 {
		t.Errorf("candidate-only type delta = %+v, want recall=0.25 abstain=0 cited=0", got)
	}
	if got := res.Languages["ru"]; got.AvgRecall != -0.5 {
		t.Errorf("baseline-only language recall = %v, want -0.5", got.AvgRecall)
	}
	if got := res.Languages["en"]; got.AvgRecall != 0.25 {
		t.Errorf("candidate-only language recall = %v, want 0.25", got.AvgRecall)
	}
}

func TestCompareZeroRecallEdge(t *testing.T) {
	baseline := &Report{
		Types: map[string]*TypeStat{
			"basic": {Count: 1, AvgRecall: 0, Abstain: 1, Cited: 0},
		},
	}
	candidate := &Report{
		Types: map[string]*TypeStat{
			"basic": {Count: 1, AvgRecall: 0, Abstain: 1, Cited: 0},
		},
	}

	res := Compare(baseline, candidate)
	d := res.Types["basic"]
	if d.AvgRecall != 0 || d.Abstain != 0 || d.Cited != 0 {
		t.Errorf("zero-recall delta = %+v, want all zero", d)
	}
}

func TestCompareNilReports(t *testing.T) {
	res := Compare(nil, nil)
	if res.TotalDelta != 0 || res.AbstainTotalDelta != 0 {
		t.Errorf("nil compare deltas = %+v, want zero", res)
	}
	if len(res.Types) != 0 || len(res.Languages) != 0 {
		t.Errorf("nil compare maps = %+v, want empty", res)
	}
}

func TestCompareResultString(t *testing.T) {
	res := CompareResult{
		TotalDelta:        0,
		AbstainTotalDelta: 1,
		Types: map[string]StatDelta{
			"basic": {AvgRecall: 0.20, Abstain: -1, Cited: 1},
		},
		Languages: map[string]StatDelta{
			"en": {AvgRecall: -0.10, Abstain: 2, Cited: 0},
			"ru": {AvgRecall: 0.20, Abstain: -1, Cited: 1},
		},
	}

	out := res.String()
	for _, want := range []string{
		"total=+0 abstain=+1",
		"languages:",
		"en",
		"recall=-0.100 abstain=+2 cited=+0",
		"ru",
		"recall=+0.200 abstain=-1 cited=+1",
		"types:",
		"basic",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("CompareResult.String() = %q, want it to contain %q", out, want)
		}
	}
	if strings.Index(out, "ru") < strings.Index(out, "en") {
		t.Errorf("language keys not sorted: %q", out)
	}
}

func TestSaveCompareResultRoundTrip(t *testing.T) {
	res := CompareResult{
		TotalDelta:        1,
		AbstainTotalDelta: -2,
		Types:             map[string]StatDelta{"basic": {AvgRecall: 0.1, Abstain: -1, Cited: 2}},
		Languages:         map[string]StatDelta{"ru": {AvgRecall: -0.2, Abstain: 1, Cited: -1}},
	}
	path := filepath.Join(t.TempDir(), "compare.json")
	if err := SaveCompareResult(path, &res); err != nil {
		t.Fatalf("SaveCompareResult: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read compare result: %v", err)
	}
	var got CompareResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal compare result: %v", err)
	}
	if got.TotalDelta != res.TotalDelta || got.AbstainTotalDelta != res.AbstainTotalDelta {
		t.Errorf("round trip totals = %+v", got)
	}
	if got.Types["basic"].Cited != 2 {
		t.Errorf("round trip types = %+v", got.Types)
	}
	if got.Languages["ru"].AvgRecall != -0.2 {
		t.Errorf("round trip languages = %+v", got.Languages)
	}
}

func TestLoadReportInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReport(path); err == nil {
		t.Error("LoadReport on invalid JSON should fail")
	}
	if _, err := LoadReport(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("LoadReport on missing file should fail")
	}
}
