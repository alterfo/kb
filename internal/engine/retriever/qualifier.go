package retriever

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

const qualifierSystemPrompt = `You extract structured metadata qualifiers from an enterprise knowledge-base question. ` +
	`qualifier-extract: identify filters that narrow which documents match - source system names (slack, gmail, jira, ` +
	`linear, github, confluence, google_drive, hubspot, fireflies), exact attribute values, fields with a small set of ` +
	`allowed values, date ranges and numeric thresholds. Respond with JSON only, no markdown fences, using this shape: ` +
	`{"sources":["slack"],"metadata":{"region":"us-east"},"in":{"status":["open","closed"]},` +
	`"time_range":{"field":"last_updated","from":"2026-01-01","to":"2026-03-31"},` +
	`"numeric":[{"field":"rps","op":">","value":500}]}. ` +
	`Omit keys with no qualifiers. When the question has none, respond {}.`

var qualifierOps = map[string]vector.Op{
	"<":  vector.OpLt,
	"<=": vector.OpLe,
	">":  vector.OpGt,
	">=": vector.OpGe,
	"==": vector.OpEq,
}

// ExtractQualifiers asks the LLM once for the structured qualifiers of
// question and converts them into a vector.Filter. It fails open to
// (zero, false) on any error or when no qualifier is found.
func ExtractQualifiers(ctx context.Context, chat ChatClient, model, question string) (vector.Filter, bool) {
	if chat == nil || strings.TrimSpace(question) == "" {
		return vector.Filter{}, false
	}
	resp, err := chat.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: qualifierSystemPrompt},
			{Role: "user", Content: question},
		},
	})
	if err != nil {
		return vector.Filter{}, false
	}
	return parseQualifiers(resp.Content)
}

type qualifierPayload struct {
	Sources   []string            `json:"sources"`
	Metadata  map[string]string   `json:"metadata"`
	In        map[string][]string `json:"in"`
	TimeRange *struct {
		Field string `json:"field"`
		From  string `json:"from"`
		To    string `json:"to"`
	} `json:"time_range"`
	Numeric []struct {
		Field string  `json:"field"`
		Op    string  `json:"op"`
		Value float64 `json:"value"`
	} `json:"numeric"`
}

func parseQualifiers(content string) (vector.Filter, bool) {
	var p qualifierPayload
	if err := json.Unmarshal([]byte(stripCodeFence(content)), &p); err != nil {
		return vector.Filter{}, false
	}

	f := vector.Filter{
		Sources:  cleanStrings(p.Sources),
		Metadata: cleanStringMap(p.Metadata),
		In:       cleanInMap(p.In),
	}
	if tr := parseQualifierTimeRange(p.TimeRange); tr != nil {
		f.TimeRange = tr
	}
	for _, n := range p.Numeric {
		op, ok := qualifierOps[n.Op]
		if !ok || strings.TrimSpace(n.Field) == "" {
			continue
		}
		f.Numeric = append(f.Numeric, vector.NumericCond{Field: strings.TrimSpace(n.Field), Op: op, Value: n.Value})
	}
	return f, !isEmptyFilter(f)
}

func parseQualifierTimeRange(tr *struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}) *vector.TimeRange {
	if tr == nil || strings.TrimSpace(tr.Field) == "" {
		return nil
	}
	out := &vector.TimeRange{Field: strings.TrimSpace(tr.Field)}
	if from, _, ok := parseQualifierDate(tr.From); ok {
		out.From = &from
	}
	if to, dateOnly, ok := parseQualifierDate(tr.To); ok {
		if dateOnly {
			to = time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), to.Location())
		}
		out.To = &to
	}
	if out.From == nil && out.To == nil {
		return nil
	}
	return out
}

func parseQualifierDate(s string) (time.Time, bool, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false, false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, layout == "2006-01-02", true
		}
	}
	return time.Time{}, false, false
}

func cleanStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func cleanStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if k = strings.TrimSpace(k); k != "" && v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanInMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, list := range in {
		if k = strings.TrimSpace(k); k == "" {
			continue
		}
		if vals := cleanStrings(list); len(vals) > 0 {
			out[k] = vals
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isEmptyFilter(f vector.Filter) bool {
	return len(f.Sources) == 0 &&
		len(f.Metadata) == 0 &&
		len(f.In) == 0 &&
		f.TimeRange == nil &&
		len(f.Numeric) == 0
}
