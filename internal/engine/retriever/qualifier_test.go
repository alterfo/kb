package retriever

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

type stubQualifierChat struct {
	response string
	err      error
}

func (s stubQualifierChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if s.err != nil {
		return llm.ChatResponse{}, s.err
	}
	return llm.ChatResponse{Content: s.response}, nil
}

func TestParseQualifiers_Full(t *testing.T) {
	payload := `{"sources":["jira","slack"],"metadata":{"region":"us-east"},` +
		`"in":{"status":["open","closed"]},` +
		`"time_range":{"field":"last_updated","from":"2026-01-01","to":"2026-03-31"},` +
		`"numeric":[{"field":"rps","op":">","value":500},{"field":"score","op":"<=","value":3.5}]}`

	f, ok := parseQualifiers(payload)
	if !ok {
		t.Fatal("parseQualifiers() ok = false, want true")
	}
	if len(f.Sources) != 2 || f.Sources[0] != "jira" {
		t.Errorf("Sources = %v", f.Sources)
	}
	if f.Metadata["region"] != "us-east" {
		t.Errorf("Metadata = %v", f.Metadata)
	}
	if len(f.In["status"]) != 2 {
		t.Errorf("In = %v", f.In)
	}
	if f.TimeRange == nil || f.TimeRange.Field != "last_updated" {
		t.Fatalf("TimeRange = %+v", f.TimeRange)
	}
	wantFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if f.TimeRange.From == nil || !f.TimeRange.From.Equal(wantFrom) {
		t.Errorf("From = %v, want %v", f.TimeRange.From, wantFrom)
	}
	if len(f.Numeric) != 2 || f.Numeric[0].Op != vector.OpGt || f.Numeric[0].Value != 500 {
		t.Errorf("Numeric = %+v", f.Numeric)
	}
	if f.Numeric[1].Op != vector.OpLe || f.Numeric[1].Value != 3.5 {
		t.Errorf("Numeric[1] = %+v", f.Numeric[1])
	}
}

func TestParseQualifiers_EmptyAndGarbage(t *testing.T) {
	if _, ok := parseQualifiers("{}"); ok {
		t.Error("empty payload should yield ok=false")
	}
	if _, ok := parseQualifiers("not json at all"); ok {
		t.Error("garbage should yield ok=false")
	}
	if _, ok := parseQualifiers(""); ok {
		t.Error("empty string should yield ok=false")
	}
}

func TestParseQualifiers_CodeFence(t *testing.T) {
	payload := "```json\n{\"metadata\":{\"team\":\"core\"}}\n```"
	f, ok := parseQualifiers(payload)
	if !ok || f.Metadata["team"] != "core" {
		t.Errorf("fenced payload not parsed: ok=%v f=%+v", ok, f)
	}
}

func TestParseQualifiers_UnknownOpDropped(t *testing.T) {
	f, ok := parseQualifiers(`{"numeric":[{"field":"rps","op":"~","value":5},{"field":"n","op":">=","value":2}]}`)
	if !ok {
		t.Fatal("expected ok=true with one valid condition")
	}
	if len(f.Numeric) != 1 || f.Numeric[0].Field != "n" {
		t.Errorf("Numeric = %+v, want only the >= cond on n", f.Numeric)
	}
}

func TestParseQualifiers_PartialDateRangeKept(t *testing.T) {
	f, ok := parseQualifiers(`{"time_range":{"field":"d","from":"whenever","to":"2026-02-01"}}`)
	if !ok {
		t.Fatal("expected ok=true with a partial range")
	}
	if f.TimeRange == nil || f.TimeRange.From != nil || f.TimeRange.To == nil {
		t.Fatalf("TimeRange = %+v, want From=nil, To set", f.TimeRange)
	}
}

func TestParseQualifiers_AllDatesBadDropped(t *testing.T) {
	f, ok := parseQualifiers(`{"time_range":{"field":"d","from":"whenever","to":"someday"}}`)
	if ok {
		t.Fatalf("expected ok=false when the only qualifier collapses, got %+v", f)
	}
	if !isEmptyFilter(f) {
		t.Errorf("filter = %+v, want empty", f)
	}
}

func TestParseQualifiers_RFC3339Bounds(t *testing.T) {
	f, ok := parseQualifiers(`{"time_range":{"field":"d","from":"2026-01-01T05:00:00Z"}}`)
	if !ok || f.TimeRange == nil {
		t.Fatal("expected TimeRange parsed from RFC3339")
	}
	want := time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC)
	if !f.TimeRange.From.Equal(want) {
		t.Errorf("From = %v, want %v", f.TimeRange.From, want)
	}
}

func TestParseQualifiers_DateOnlyUpperBoundEndOfDay(t *testing.T) {
	f, ok := parseQualifiers(`{"time_range":{"field":"d","from":"2026-01-01","to":"2026-03-31"}}`)
	if !ok || f.TimeRange == nil || f.TimeRange.To == nil {
		t.Fatalf("expected TimeRange with To set, got ok=%v tr=%+v", ok, f.TimeRange)
	}
	want := time.Date(2026, 3, 31, 23, 59, 59, int(time.Second-time.Nanosecond), time.UTC)
	if !f.TimeRange.To.Equal(want) {
		t.Errorf("To = %v, want end-of-day %v (inclusive date-only upper bound)", f.TimeRange.To, want)
	}
	if f.TimeRange.From == nil {
		t.Error("From = nil, want 2026-01-01 midnight")
	}
}

func TestExtractQualifiers(t *testing.T) {
	tests := []struct {
		name     string
		chat     ChatClient
		question string
		ok       bool
	}{
		{"nil chat", nil, "q", false},
		{"empty question", stubQualifierChat{response: "{}"}, "   ", false},
		{"chat error", stubQualifierChat{err: context.Canceled}, "q", false},
		{"no qualifiers", stubQualifierChat{response: "{}"}, "what is kb", false},
		{"with qualifiers", stubQualifierChat{response: `{"sources":["slack"]}`}, "open slack threads", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok := ExtractQualifiers(context.Background(), tt.chat, "model", tt.question)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v (filter=%+v)", ok, tt.ok, f)
			}
		})
	}
}

func TestQualifierPromptMarkerPresent(t *testing.T) {
	if !strings.Contains(qualifierSystemPrompt, "qualifier-extract:") {
		t.Error("system prompt must contain the qualifier-extract marker")
	}
}
