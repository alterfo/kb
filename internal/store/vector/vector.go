package vector

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

var ErrDimMismatch = errors.New("vector: embedding dimension mismatch")

type Chunk struct {
	ID           string
	RefDocID     string
	Text         string
	FilePath     string
	FileName     string
	Source       string
	TokenCount   int
	ChunkIndex   int
	Embedding    []float32
	Metadata     map[string]string
	CreatedAt    string
	ValidTo      string
	Replaces     string
	SupersededBy string
}

type ScoredChunk struct {
	Chunk
	Score float64
}

// Filter selects chunks by source (virtual collection) and/or arbitrary
// frontmatter key/value pairs (e.g. project=X, space=Y). All conditions
// must match (AND). In allows OR within one field; TimeRange and Numeric
// constrain metadata values parsed as time/float. Conditions on missing or
// unparseable values never match.
type Filter struct {
	Sources   []string
	Metadata  map[string]string
	In        map[string][]string
	TimeRange *TimeRange
	Numeric   []NumericCond
}

type Op string

const (
	OpLt Op = "<"
	OpLe Op = "<="
	OpGt Op = ">"
	OpGe Op = ">="
	OpEq Op = "=="
)

type NumericCond struct {
	Field string
	Op    Op
	Value float64
}

type TimeRange struct {
	Field string
	From  *time.Time
	To    *time.Time
}

func (f Filter) Matches(source string, metadata map[string]string) bool {
	if len(f.Sources) > 0 {
		found := false
		for _, s := range f.Sources {
			if s == source {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for k, v := range f.Metadata {
		if metadata[k] != v {
			return false
		}
	}
	for field, allowed := range f.In {
		v, ok := metadata[field]
		if !ok {
			return false
		}
		hit := false
		for _, a := range allowed {
			if a == v {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if tr := f.TimeRange; tr != nil {
		ts, ok := parseMetaTime(metadata[tr.Field])
		if !ok {
			return false
		}
		if tr.From != nil && ts.Before(*tr.From) {
			return false
		}
		if tr.To != nil && ts.After(*tr.To) {
			return false
		}
	}
	for _, cond := range f.Numeric {
		n, ok := parseMetaFloat(metadata[cond.Field])
		if !ok || !compareNum(n, cond.Op, cond.Value) {
			return false
		}
	}
	return true
}

func compareNum(n float64, op Op, ref float64) bool {
	switch op {
	case OpLt:
		return n < ref
	case OpLe:
		return n <= ref
	case OpGt:
		return n > ref
	case OpGe:
		return n >= ref
	case OpEq:
		return n == ref
	default:
		return false
	}
}

func mergeSources(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	set := make(map[string]struct{}, len(b))
	for _, s := range b {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, s := range a {
		if _, ok := set[s]; ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{"\x00impossible"}
	}
	return out
}

// MergeAND combines two filters so that only chunks matching both qualify:
// sources intersect (empty intersection yields an impossible filter),
// metadata maps merge with b winning conflicts, In lists intersect per
// field, time ranges tighten to the inner window, numeric conditions
// concatenate.
func MergeAND(a, b Filter) Filter {
	out := Filter{
		Sources:  mergeSources(a.Sources, b.Sources),
		Metadata: map[string]string{},
	}
	for k, v := range a.Metadata {
		out.Metadata[k] = v
	}
	for k, v := range b.Metadata {
		out.Metadata[k] = v
	}
	if len(out.Metadata) == 0 {
		out.Metadata = nil
	}

	out.In = map[string][]string{}
	for field, list := range a.In {
		out.In[field] = append([]string(nil), list...)
	}
	for field, list := range b.In {
		existing, ok := out.In[field]
		if !ok {
			out.In[field] = append([]string(nil), list...)
			continue
		}
		set := make(map[string]struct{}, len(list))
		for _, v := range list {
			set[v] = struct{}{}
		}
		var inter []string
		for _, v := range existing {
			if _, ok := set[v]; ok {
				inter = append(inter, v)
			}
		}
		out.In[field] = inter
	}
	if len(out.In) == 0 {
		out.In = nil
	}

	switch {
	case a.TimeRange == nil:
		out.TimeRange = b.TimeRange
	case b.TimeRange == nil:
		out.TimeRange = a.TimeRange
	case a.TimeRange.Field != b.TimeRange.Field:
		out.Sources = []string{"\x00impossible"}
		return out
	default:
		tr := TimeRange{Field: a.TimeRange.Field, From: a.TimeRange.From, To: a.TimeRange.To}
		if tr.From == nil || (b.TimeRange.From != nil && b.TimeRange.From.After(*tr.From)) {
			tr.From = b.TimeRange.From
		}
		if tr.To == nil || (b.TimeRange.To != nil && b.TimeRange.To.Before(*tr.To)) {
			tr.To = b.TimeRange.To
		}
		out.TimeRange = &tr
	}

	out.Numeric = append(append([]NumericCond{}, a.Numeric...), b.Numeric...)
	return out
}

func parseMetaFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

var metaTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02",
	"2006-01-02 15:04:05.999999999 -0700 MST",
}

func parseMetaTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range metaTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

type Store interface {
	EnsureDim(ctx context.Context, dim int) error
	Upsert(ctx context.Context, chunks []Chunk) error
	ReplaceByDoc(ctx context.Context, docID string, chunks []Chunk) error
	DeleteByDoc(ctx context.Context, docID string) error
	SoftCloseByDoc(ctx context.Context, docID string) error
	SetSuperseded(ctx context.Context, chunkIDs []string, byRefDocID string) error
	ClearSupersededBy(ctx context.Context, refDocID string) error
	ClearSupersededOnDoc(ctx context.Context, docID string) error
	Query(ctx context.Context, vec []float32, k int, filter Filter) ([]ScoredChunk, error)
	AllForBM25(ctx context.Context) ([]Chunk, error)
	ChunksByDoc(ctx context.Context, docID string) ([]Chunk, error)
	DocHash(ctx context.Context, docID string) (string, bool, error)
	SetDocHash(ctx context.Context, docID, hash string) error
}
