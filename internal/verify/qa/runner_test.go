package qa

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/llm"
)

type runnerFakeJudge struct {
	v   Verdict
	err error
}

func (f runnerFakeJudge) Judge(ctx context.Context, question, answer, expected string) (Verdict, error) {
	return f.v, f.err
}

func TestRunner_AggregatesPassedAndBySource(t *testing.T) {
	ask := func(ctx context.Context, question string) (Answer, error) {
		if question == "q2" {
			return Answer{}, errors.New("retrieve failed")
		}
		return Answer{Text: "expected answer", Sources: []string{"leon-ai", "leon-code"}}, nil
	}
	r := &Runner{
		Ask:   ask,
		Judge: runnerFakeJudge{v: Verdict{Passed: true, Reason: "match"}},
	}
	rep, err := r.Run(context.Background(), []QAPair{
		{ID: "1", Question: "q1", Expected: "expected"},
		{ID: "2", Question: "q2", Expected: "expected"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.SampleSize != 2 || rep.Asked != 1 || rep.Passed != 1 || rep.AskErrors != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.BySource["leon-ai"].Passed != 1 || rep.BySource["leon-ai"].Total != 1 {
		t.Fatalf("leon-ai metric = %+v", rep.BySource["leon-ai"])
	}
	if rep.BySource["leon-code"].Passed != 1 || rep.BySource["leon-code"].Total != 1 {
		t.Fatalf("leon-code metric = %+v", rep.BySource["leon-code"])
	}
}

func TestRunner_JudgeErrorFailsOpen(t *testing.T) {
	r := &Runner{
		Ask: func(ctx context.Context, question string) (Answer, error) {
			return Answer{Text: "expected answer tokens", Sources: []string{"leon-ai"}}, nil
		},
		Judge: runnerFakeJudge{err: errors.New("judge boom")},
	}
	rep, err := r.Run(context.Background(), []QAPair{{Question: "q", Expected: "expected answer tokens"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.JudgeErrors != 1 || rep.Passed != 1 {
		t.Fatalf("report = %+v, want judge error and overlap pass", rep)
	}
	if !strings.Contains(rep.Results[0].Reason, "overlap") {
		t.Fatalf("reason = %q", rep.Results[0].Reason)
	}
}

func TestRunner_CountsLLMJudgeFailuresAsJudgeErrors(t *testing.T) {
	r := &Runner{
		Ask: func(ctx context.Context, question string) (Answer, error) {
			return Answer{Text: "expected answer tokens", Sources: []string{"leon-ai"}}, nil
		},
		Judge: NewLLMJudge(judgeFakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) {
			return llm.ChatResponse{}, errors.New("judge down")
		}}, "test-model"),
	}
	rep, err := r.Run(context.Background(), []QAPair{{Question: "q", Expected: "expected answer tokens"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.JudgeErrors != 1 {
		t.Fatalf("JudgeErrors = %d, want 1", rep.JudgeErrors)
	}
	if rep.Passed != 1 {
		t.Fatalf("Passed = %d, want overlap fallback pass", rep.Passed)
	}
	if !strings.Contains(rep.Results[0].Reason, "overlap") {
		t.Fatalf("reason = %q, want overlap fallback", rep.Results[0].Reason)
	}
}

func TestRunner_ReportFormats(t *testing.T) {
	rep := Report{GeneratedAt: time.Time{}, SampleSize: 1, Asked: 1, Passed: 1, BySource: map[string]SourceMetric{"leon-ai": {Total: 1, Passed: 1}}}
	summary := rep.Summary()
	for _, want := range []string{"QA hit-rate: 1.000 (1/1)", "sample size: 1", "leon-ai: 1.000 (1/1)"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q missing %q", summary, want)
		}
	}
	data, err := rep.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(string(data), `"sample_size": 1`) {
		t.Fatalf("json = %s", data)
	}
}
