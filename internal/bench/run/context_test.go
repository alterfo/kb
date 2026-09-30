package run

import (
	"math"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
)

func TestContextPrecisionRecallFullMatch(t *testing.T) {
	chunks := []ContextChunk{{DocID: "d1", Text: "the cat runs"}}
	facts := []string{"the cat runs"}
	if p := ContextPrecision(chunks, facts, "en"); p != 1 {
		t.Fatalf("precision = %v, want 1", p)
	}
	if r := ContextRecall(chunks, facts, "en"); r != 1 {
		t.Fatalf("recall = %v, want 1", r)
	}
}

func TestContextPrecisionRecallPartial(t *testing.T) {
	chunks := []ContextChunk{{DocID: "d1", Text: "the cat runs fast"}}
	facts := []string{"the cat runs"}
	if p := ContextPrecision(chunks, facts, "en"); math.Abs(p-2.0/3.0) > 1e-9 {
		t.Fatalf("precision = %v, want %.9f", p, 2.0/3.0)
	}
	if r := ContextRecall(chunks, facts, "en"); r != 1 {
		t.Fatalf("recall = %v, want 1", r)
	}
}

func TestContextPrecisionRecallZero(t *testing.T) {
	chunks := []ContextChunk{{DocID: "d1", Text: "dogs sleep"}}
	facts := []string{"the cat runs"}
	if p := ContextPrecision(chunks, facts, "en"); p != 0 {
		t.Fatalf("precision = %v, want 0", p)
	}
	if r := ContextRecall(chunks, facts, "en"); r != 0 {
		t.Fatalf("recall = %v, want 0", r)
	}
}

func TestContextRecallRussianInflection(t *testing.T) {
	chunks := []ContextChunk{{DocID: "d1", Text: "Кошка быстро бегает по улице."}}
	facts := []string{"Кошка бегает"}
	if r := ContextRecall(chunks, facts, "ru"); r != 1 {
		t.Fatalf("recall = %v, want 1", r)
	}
	p := ContextPrecision(chunks, facts, "ru")
	if p <= 0 || p >= 1 {
		t.Fatalf("precision = %v, want strictly between 0 and 1", p)
	}
}

func TestContextMetricsEmptyInputs(t *testing.T) {
	if p := ContextPrecision(nil, []string{"fact"}, "en"); p != 0 {
		t.Fatalf("precision with no chunks = %v, want 0", p)
	}
	if p := ContextPrecision([]ContextChunk{{Text: "context"}}, nil, "en"); p != 0 {
		t.Fatalf("precision with no facts = %v, want 0", p)
	}
	if r := ContextRecall(nil, []string{"fact"}, "en"); r != 0 {
		t.Fatalf("recall with no chunks = %v, want 0", r)
	}
	if r := ContextRecall([]ContextChunk{{Text: "context"}}, nil, "en"); r != 0 {
		t.Fatalf("recall with no facts = %v, want 0", r)
	}
}

func TestScoreAggregatesContextMetrics(t *testing.T) {
	submission := map[string]Answer{
		"q1": {
			QuestionID:    "q1",
			Answer:        "the cat runs",
			DocumentIDs:   []string{"d1"},
			ContextChunks: []ContextChunk{{DocID: "d1", Text: "the cat runs"}},
		},
	}
	gold := []corpus.Question{
		{ID: "q1", Type: "basic", AnswerFacts: []string{"the cat runs"}, ExpectedDocIDs: []string{"d1"}},
	}

	rep := Score(submission, gold)
	if rep.AvgContextPrecision != 1 || rep.AvgContextRecall != 1 {
		t.Fatalf("AvgContextPrecision/Recall = %v/%v, want 1/1", rep.AvgContextPrecision, rep.AvgContextRecall)
	}
	st := rep.Types["basic"]
	if st == nil || st.AvgContextPrecision != 1 || st.AvgContextRecall != 1 {
		t.Fatalf("Types[basic] = %+v", st)
	}
}

func TestScoreSkipsContextMetricsWithoutChunks(t *testing.T) {
	submission := map[string]Answer{
		"q1": {QuestionID: "q1", Answer: "the cat runs", DocumentIDs: []string{"d1"}},
	}
	gold := []corpus.Question{
		{ID: "q1", Type: "basic", AnswerFacts: []string{"the cat runs"}, ExpectedDocIDs: []string{"d1"}},
	}

	rep := Score(submission, gold)
	if rep.AvgContextPrecision != 0 || rep.AvgContextRecall != 0 {
		t.Fatalf("AvgContextPrecision/Recall = %v/%v, want 0/0", rep.AvgContextPrecision, rep.AvgContextRecall)
	}
}
