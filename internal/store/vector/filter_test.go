package vector

import (
	"testing"
	"time"
)

func meta(m map[string]string) map[string]string { return m }

func TestFilter_SourcesAndMetadataRegression(t *testing.T) {
	f := Filter{
		Sources:  []string{"github", "linear"},
		Metadata: map[string]string{"project": "X"},
	}
	if !f.Matches("github", meta(map[string]string{"project": "X"})) {
		t.Error("expected match on source+metadata")
	}
	if f.Matches("slack", meta(map[string]string{"project": "X"})) {
		t.Error("source mismatch should not match")
	}
	if f.Matches("github", meta(map[string]string{"project": "Y"})) {
		t.Error("metadata mismatch should not match")
	}
	if f.Matches("github", meta(map[string]string{})) {
		t.Error("missing metadata key should not match")
	}
}

func TestFilter_Empty(t *testing.T) {
	var f Filter
	if !f.Matches("anything", nil) {
		t.Error("empty filter must match everything")
	}
}

func TestFilter_In(t *testing.T) {
	f := Filter{In: map[string][]string{"status": {"published", "draft"}}}
	if !f.Matches("notes", meta(map[string]string{"status": "draft"})) {
		t.Error("second IN value should match")
	}
	if f.Matches("notes", meta(map[string]string{"status": "archived"})) {
		t.Error("value outside IN list should not match")
	}
	if f.Matches("notes", meta(map[string]string{})) {
		t.Error("missing IN field should not match")
	}
}

func TestFilter_Numeric(t *testing.T) {
	tests := []struct {
		name string
		cond NumericCond
		val  string
		want bool
	}{
		{"lt hit", NumericCond{Field: "rps", Op: OpLt, Value: 100}, "42", true},
		{"lt miss equal", NumericCond{Field: "rps", Op: OpLt, Value: 100}, "100", false},
		{"le hit equal", NumericCond{Field: "rps", Op: OpLe, Value: 100}, "100", true},
		{"gt hit", NumericCond{Field: "rps", Op: OpGt, Value: 10}, "11", true},
		{"ge hit equal", NumericCond{Field: "rps", Op: OpGe, Value: 10}, "10", true},
		{"eq hit", NumericCond{Field: "rps", Op: OpEq, Value: 7}, "7", true},
		{"eq miss", NumericCond{Field: "rps", Op: OpEq, Value: 7}, "8", false},
		{"float value", NumericCond{Field: "score", Op: OpGe, Value: 3.14}, "3.14", true},
		{"unparseable", NumericCond{Field: "rps", Op: OpLt, Value: 100}, "high", false},
		{"empty missing field", NumericCond{Field: "rps", Op: OpLt, Value: 100}, "", false},
		{"unknown op fails closed", NumericCond{Field: "rps", Op: "?", Value: 1}, "1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Filter{Numeric: []NumericCond{tt.cond}}
			got := f.Matches("s", meta(map[string]string{tt.cond.Field: tt.val}))
			if got != tt.want {
				t.Errorf("Matches(%q) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestFilter_NumericMultipleAllMustHold(t *testing.T) {
	f := Filter{Numeric: []NumericCond{
		{Field: "rps", Op: OpGe, Value: 50},
		{Field: "budget", Op: OpLt, Value: 1000},
	}}
	md := map[string]string{"rps": "80", "budget": "900"}
	if !f.Matches("s", md) {
		t.Error("both conditions hold, want match")
	}
	md["budget"] = "1200"
	if f.Matches("s", md) {
		t.Error("second condition violated, want no match")
	}
}

func TestFilter_TimeRange(t *testing.T) {
	d := func(s string) *time.Time {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return &tm
	}
	from := d("2026-01-01T00:00:00Z")
	to := d("2026-03-31T23:59:59Z")

	tests := []struct {
		name string
		tr   TimeRange
		val  string
		want bool
	}{
		{"inside range", TimeRange{Field: "date", From: from, To: to}, "2026-02-03", true},
		{"before range", TimeRange{Field: "date", From: from, To: to}, "2025-12-31", false},
		{"after range", TimeRange{Field: "date", From: from, To: to}, "2026-04-01", false},
		{"from boundary inclusive", TimeRange{Field: "date", From: from, To: to}, "2026-01-01T00:00:00Z", true},
		{"to boundary inclusive", TimeRange{Field: "date", From: from, To: to}, "2026-03-31T23:59:59Z", true},
		{"only from", TimeRange{Field: "date", From: from}, "2026-06-01", true},
		{"only from miss", TimeRange{Field: "date", From: from}, "2025-06-01", false},
		{"only to", TimeRange{Field: "date", To: to}, "2025-06-01", true},
		{"only to miss", TimeRange{Field: "date", To: to}, "2026-06-01", false},
		{"date-only layout", TimeRange{Field: "date", From: from, To: to}, "2026-02-03", true},
		{"go default layout", TimeRange{Field: "date", From: from, To: to}, "2026-02-03 10:20:30 +0000 UTC", true},
		{"unparseable", TimeRange{Field: "date", From: from, To: to}, "whenever", false},
		{"missing field", TimeRange{Field: "date", From: from, To: to}, "", false},
		{"numeric timestamp rejected", TimeRange{Field: "date", From: from, To: to}, "1774728005", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Filter{TimeRange: &tt.tr}
			got := f.Matches("s", meta(map[string]string{tt.tr.Field: tt.val}))
			if got != tt.want {
				t.Errorf("Matches(%q) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestFilter_CombinedAND(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	f := Filter{
		Sources:   []string{"jira"},
		Metadata:  map[string]string{"region": "us-east"},
		In:        map[string][]string{"priority": {"p0", "p1"}},
		TimeRange: &TimeRange{Field: "last_updated", From: &from, To: &to},
		Numeric:   []NumericCond{{Field: "rps", Op: OpGt, Value: 500}},
	}
	ok := meta(map[string]string{
		"region":       "us-east",
		"priority":     "p0",
		"last_updated": "2026-05-17",
		"rps":          "800",
	})
	if !f.Matches("jira", ok) {
		t.Error("all conditions hold, want match")
	}
	bad := meta(map[string]string{
		"region":       "eu-west",
		"priority":     "p0",
		"last_updated": "2026-05-17",
		"rps":          "800",
	})
	if f.Matches("jira", bad) {
		t.Error("one violated condition, want no match")
	}
	if f.Matches("slack", ok) {
		t.Error("wrong source, want no match")
	}
}

func TestMergeAND_Empty(t *testing.T) {
	f := MergeAND(Filter{}, Filter{})
	if !f.Matches("any", nil) {
		t.Error("merge of empty filters must match everything")
	}
}

func TestMergeAND_Sources(t *testing.T) {
	a := Filter{Sources: []string{"slack", "jira"}}
	b := Filter{Sources: []string{"jira", "gmail"}}

	got := MergeAND(a, b)
	if !got.Matches("jira", nil) {
		t.Error("intersection should keep jira")
	}
	if got.Matches("slack", nil) || got.Matches("gmail", nil) {
		t.Error("intersection should drop slack/gmail")
	}

	got = MergeAND(a, Filter{})
	for _, s := range []string{"slack", "jira"} {
		if !got.Matches(s, nil) {
			t.Errorf("empty side should keep %s", s)
		}
	}

	disjoint := MergeAND(Filter{Sources: []string{"slack"}}, Filter{Sources: []string{"jira"}})
	if disjoint.Matches("slack", nil) || disjoint.Matches("jira", nil) {
		t.Error("disjoint sources should match nothing")
	}
}

func TestMergeAND_MetadataAndIn(t *testing.T) {
	a := Filter{
		Metadata: map[string]string{"region": "us-east", "team": "core"},
		In:       map[string][]string{"status": {"open", "closed"}},
	}
	b := Filter{
		Metadata: map[string]string{"region": "eu-west"},
		In:       map[string][]string{"status": {"closed"}, "tier": {"gold"}},
	}
	got := MergeAND(a, b)

	md := map[string]string{"region": "eu-west", "team": "core", "status": "closed", "tier": "gold"}
	if !got.Matches("s", md) {
		t.Error("merged metadata/in should match combined doc")
	}
	md["status"] = "open"
	if got.Matches("s", md) {
		t.Error("In intersection should drop open")
	}
	md["region"] = "us-east"
	if got.Matches("s", md) {
		t.Error("b metadata should win on conflict")
	}
}

func TestMergeAND_TimeRangeAndNumeric(t *testing.T) {
	f1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	to1 := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	to2 := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

	a := Filter{
		TimeRange: &TimeRange{Field: "date", From: &f1, To: &to1},
		Numeric:   []NumericCond{{Field: "rps", Op: OpGt, Value: 100}},
	}
	b := Filter{
		TimeRange: &TimeRange{Field: "date", From: &f2, To: &to2},
		Numeric:   []NumericCond{{Field: "budget", Op: OpLt, Value: 50}},
	}
	got := MergeAND(a, b)
	md := map[string]string{"date": "2026-05-01", "rps": "200", "budget": "40"}
	if !got.Matches("s", md) {
		t.Error("inner window and both numeric conds should match")
	}
	md["date"] = "2026-01-15"
	if got.Matches("s", md) {
		t.Error("before tightened From should not match")
	}
	md["date"] = "2026-07-01"
	if got.Matches("s", md) {
		t.Error("after tightened To should not match")
	}
}
