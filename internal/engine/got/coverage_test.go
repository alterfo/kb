package got

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

func chunk(fileName string, score float64) vector.ScoredChunk {
	return vector.ScoredChunk{Chunk: vector.Chunk{FileName: fileName, Text: "text"}, Score: score}
}

func TestDeterministicCoverageEmptyChunks(t *testing.T) {
	if got := deterministicCoverage(nil, 8); got != 0 {
		t.Fatalf("got %v, want 0", got)
	}
}

func TestDeterministicCoverageFullCountHighScore(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1), chunk("b.md", 1)}
	got := deterministicCoverage(chunks, 2)
	if got != 1 {
		t.Fatalf("got %v, want 1", got)
	}
}

func TestScoreCoverageHighDeterministicSkipsJudge(t *testing.T) {
	o := New(Config{Chat: fakeChat{err: errors.New("should not be called")}, CoverageHigh: 0.5, CoverageLow: 0.1})
	chunks := []vector.ScoredChunk{chunk("a.md", 1), chunk("b.md", 1)}
	res := o.scoreCoverage(context.Background(), "q", chunks)
	if !res.Covered {
		t.Fatalf("got %+v, want Covered=true", res)
	}
}

func TestScoreCoverageLowDeterministicSkipsJudge(t *testing.T) {
	o := New(Config{Chat: fakeChat{err: errors.New("should not be called")}, CoverageHigh: 0.9, CoverageLow: 0.3})
	res := o.scoreCoverage(context.Background(), "q", nil)
	if res.Covered {
		t.Fatalf("got %+v, want Covered=false", res)
	}
}

func TestScoreCoverageBorderlineAsksJudge(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: `{"covered":true,"score":0.9}`}}
	o := New(Config{Chat: chat, CoverageHigh: 0.95, CoverageLow: 0.05})
	chunks := []vector.ScoredChunk{chunk("a.md", 0.4)}
	res := o.scoreCoverage(context.Background(), "q", chunks)
	if !res.Covered || res.Score != 0.9 {
		t.Fatalf("got %+v, want judge result", res)
	}
}

func TestScoreCoverageBorderlineJudgeErrorFailsOpen(t *testing.T) {
	o := New(Config{Chat: fakeChat{err: errors.New("boom")}, CoverageHigh: 0.95, CoverageLow: 0.05})
	chunks := []vector.ScoredChunk{chunk("a.md", 0.4)}
	res := o.scoreCoverage(context.Background(), "q", chunks)
	if res.Covered {
		t.Fatalf("got %+v, want fail-open Covered=false", res)
	}
}

func TestScoreCoverageBorderlineInvalidJSONFailsOpen(t *testing.T) {
	o := New(Config{Chat: fakeChat{resp: llm.ChatResponse{Content: "not json"}}, CoverageHigh: 0.95, CoverageLow: 0.05})
	chunks := []vector.ScoredChunk{chunk("a.md", 0.4)}
	res := o.scoreCoverage(context.Background(), "q", chunks)
	if res.Covered {
		t.Fatalf("got %+v, want fail-open Covered=false", res)
	}
}

func TestParseCoverageJudgmentClampsScore(t *testing.T) {
	res, ok := parseCoverageJudgment(`{"covered":true,"score":5}`)
	if !ok || res.Score != 1 {
		t.Fatalf("got %+v ok=%v, want score clamped to 1", res, ok)
	}
	res, ok = parseCoverageJudgment(`{"covered":false,"score":-2}`)
	if !ok || res.Score != 0 {
		t.Fatalf("got %+v ok=%v, want score clamped to 0", res, ok)
	}
}
