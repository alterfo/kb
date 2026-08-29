package qa

import (
	"context"
	"strings"
	"testing"
)

func TestRunner_OfflineOverlapFallbackScoresWithoutJudge(t *testing.T) {
	r := &Runner{
		Ask: func(ctx context.Context, question string) (Answer, error) {
			return Answer{Text: "the retriever module is maintained by Alice", Sources: []string{"sample.json"}}, nil
		},
	}
	pairs := []QAPair{{ID: "offline-1", Question: "who maintains the retriever module", Expected: "Alice maintains the retriever module"}}

	rep, err := r.Run(context.Background(), pairs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Asked != 1 || rep.Passed != 1 || rep.AskErrors != 0 || rep.JudgeErrors != 0 {
		t.Fatalf("report = %+v, want one asked/passed result with no errors", rep)
	}
	if len(rep.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(rep.Results))
	}
	res := rep.Results[0]
	if res.Overlap < DefaultOverlapThreshold {
		t.Fatalf("overlap = %.3f, want >= %.3f", res.Overlap, DefaultOverlapThreshold)
	}
	if !res.Passed {
		t.Fatal("overlap fallback should pass the overlapping answer")
	}
	if !strings.Contains(res.Reason, "overlap fallback") {
		t.Fatalf("reason = %q, want overlap fallback", res.Reason)
	}
}

func TestRunner_OfflineOverlapFallbackFailsUnrelatedAnswer(t *testing.T) {
	r := &Runner{
		Ask: func(ctx context.Context, question string) (Answer, error) {
			return Answer{Text: "unrelated tokens only"}, nil
		},
	}
	rep, err := r.Run(context.Background(), []QAPair{{ID: "offline-2", Question: "q", Expected: "expected answer tokens"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Asked != 1 || rep.Passed != 0 {
		t.Fatalf("report = %+v, want one asked and zero passed", rep)
	}
	if rep.Results[0].Passed {
		t.Fatal("unrelated answer must fail token-overlap scoring")
	}
}
