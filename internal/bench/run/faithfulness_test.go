package run

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/llm"
)

type staticFaithfulnessJudge struct {
	score float64
	err   error
}

func (j *staticFaithfulnessJudge) Judge(ctx context.Context, answer string, chunks []ContextChunk) (float64, error) {
	return j.score, j.err
}

type faithfulChat struct {
	content string
	err     error
}

func (c *faithfulChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if c.err != nil {
		return llm.ChatResponse{}, c.err
	}
	return llm.ChatResponse{Content: c.content}, nil
}

func TestParseFaithfulness(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		err  bool
	}{
		{"YES", 1, false},
		{"yes.", 1, false},
		{"no", 0, false},
		{"No, not supported", 0, false},
		{"0.75", 0.75, false},
		{"1.5", 1, false},
		{"", 0, true},
		{"maybe", 0, true},
	}
	for _, c := range cases {
		got, err := parseFaithfulness(c.in)
		if c.err {
			if err == nil {
				t.Errorf("parseFaithfulness(%q) = %v, want error", c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("parseFaithfulness(%q) = %v, %v; want %v, nil", c.in, got, err, c.want)
		}
	}
}

func TestLLMFaithfulnessJudgeYes(t *testing.T) {
	chat := &faithfulChat{content: "YES"}
	judge := NewLLMFaithfulnessJudge(chat, "test-model")
	score, err := judge.Judge(context.Background(), "answer", []ContextChunk{{DocID: "d1", Text: "context"}})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if score != 1 {
		t.Fatalf("score = %v, want 1", score)
	}
}

func TestLLMFaithfulnessJudgeChatError(t *testing.T) {
	chat := &faithfulChat{err: context.DeadlineExceeded}
	judge := NewLLMFaithfulnessJudge(chat, "test-model")
	if _, err := judge.Judge(context.Background(), "answer", []ContextChunk{{Text: "context"}}); err == nil {
		t.Fatal("Judge = nil error, want chat error")
	}
}

func TestScoreWithJudgeAggregatesFaithfulness(t *testing.T) {
	submission := map[string]Answer{
		"q1": {
			QuestionID:    "q1",
			Answer:        "the cat runs",
			DocumentIDs:   []string{"d1"},
			ContextChunks: []ContextChunk{{DocID: "d1", Text: "the cat runs"}},
		},
	}
	gold := []corpus.Question{{ID: "q1", Type: "basic", ExpectedDocIDs: []string{"d1"}}}

	rep := ScoreWithJudge(context.Background(), submission, gold, &staticFaithfulnessJudge{score: 0.5})
	if rep.AvgFaithfulness != 0.5 {
		t.Fatalf("AvgFaithfulness = %v, want 0.5", rep.AvgFaithfulness)
	}
	if st := rep.Types["basic"]; st == nil || st.AvgFaithfulness != 0.5 {
		t.Fatalf("Types[basic] = %+v", rep.Types["basic"])
	}
}

func TestScoreWithJudgeSkipsErrors(t *testing.T) {
	submission := map[string]Answer{
		"q1": {
			QuestionID:    "q1",
			Answer:        "the cat runs",
			DocumentIDs:   []string{"d1"},
			ContextChunks: []ContextChunk{{DocID: "d1", Text: "the cat runs"}},
		},
	}
	gold := []corpus.Question{{ID: "q1", Type: "basic", ExpectedDocIDs: []string{"d1"}}}

	rep := ScoreWithJudge(context.Background(), submission, gold, &staticFaithfulnessJudge{err: context.DeadlineExceeded})
	if rep.AvgFaithfulness != 0 {
		t.Fatalf("AvgFaithfulness = %v, want 0 when judge errors", rep.AvgFaithfulness)
	}
}
