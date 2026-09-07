package plepatch

import (
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/gguf"
)

func validFile() *gguf.File {
	return &gguf.File{Metadata: []gguf.KV{
		{Key: "general.architecture", Value: gguf.Str("qwen4exp")},
		{Key: "qwen4exp.ple.ngram_size", Value: gguf.Uint32(3)},
		{Key: "qwen4exp.ple.heads_per_ngram", Value: gguf.Uint32(8)},
		{Key: "qwen4exp.ple.layer_multipliers", Value: gguf.Uint64Array([]uint64{23703573157769, 20109073645365, 8052911324071})},
		{Key: "qwen4exp.ple.head_offsets", Value: gguf.Uint64Array([]uint64{
			0, 20000003, 40000026, 60000059, 80000106, 100000165, 120000228, 140000297,
			160000374, 180000455, 200000548, 220000655, 240000802, 260000955, 280001114, 300001275,
		})},
		{Key: "qwen4exp.ple.head_vocab_sizes", Value: gguf.Uint64Array([]uint64{
			20000003, 20000023, 20000033, 20000047, 20000059, 20000063, 20000069, 20000077,
			20000081, 20000093, 20000107, 20000147, 20000153, 20000159, 20000161, 20000171,
		})},
		{Key: "qwen4exp.ple.eos_token_id", Value: gguf.Uint32(248044)},
	}}
}

func loadValid(t *testing.T) *Constants {
	t.Helper()
	c, err := LoadConstants(validFile())
	if err != nil {
		t.Fatalf("LoadConstants: %v", err)
	}
	return c
}

func TestRowsForTokenMatchesCppGolden(t *testing.T) {
	c := loadValid(t)
	data, err := os.ReadFile("testdata/golden.txt")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		head, rowsPart, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("bad golden line: %q", line)
		}
		fields := strings.Fields(head)
		if len(fields) != 3 {
			t.Fatalf("bad golden header: %q", head)
		}
		tok := parseTok(t, fields[0])
		p1 := parseTok(t, fields[1])
		p2 := parseTok(t, fields[2])

		expected := parseRows(t, rowsPart)
		if len(expected) != c.NHeads() {
			t.Fatalf("golden line %q has %d rows, want %d", line, len(expected), c.NHeads())
		}
		got := c.RowsForToken(tok, []int64{p2, p1})
		if !slices.Equal(got, expected) {
			t.Fatalf("hash mismatch for %d,%d,%d:\n got %v\nwant %v", tok, p1, p2, got, expected)
		}
		n++
	}
	if n < 10 {
		t.Fatalf("golden has %d windows, want >= 10", n)
	}
}

func TestEOSResetSemantics(t *testing.T) {
	c := loadValid(t)
	eos := c.EosTokenID

	a := c.RowsForToken(1234, []int64{999, eos})
	b := c.RowsForToken(1234, []int64{555, eos})
	if !slices.Equal(a, b) {
		t.Fatalf("eos at t-1 must cut the older t-2 context: %v != %v", a, b)
	}

	d := c.RowsForToken(1234, []int64{eos, 999})
	e := c.RowsForToken(1234, []int64{7, 999})
	if slices.Equal(d, e) {
		t.Fatalf("eos at t-2 should only affect the t-2 slot, not ctx[1]: %v == %v", d, e)
	}

	if got := c.RowsForToken(eos, []int64{2, 1}); len(got) != c.NHeads() {
		t.Fatalf("own eos token should still yield %d rows, got %d", c.NHeads(), len(got))
	}
}

func TestRowsInRange(t *testing.T) {
	c := loadValid(t)
	rng := rand.New(rand.NewPCG(0, 0))
	for i := 0; i < 2000; i++ {
		tok := rng.Int64N(248320)
		prev := make([]int64, c.NgramSize-1)
		for j := range prev {
			prev[j] = rng.Int64N(248320)
		}
		for _, row := range c.RowsForToken(tok, prev) {
			if row >= c.NRows() {
				t.Fatalf("row %d out of range [0,%d)", row, c.NRows())
			}
		}
	}
}

func TestRowsForSequence(t *testing.T) {
	c := loadValid(t)
	tokens := []int64{760, 6511, 314}
	got := c.RowsForSequence(tokens)
	if len(got) != len(tokens) {
		t.Fatalf("rows_for_sequence length = %d, want %d", len(got), len(tokens))
	}
	for i, rows := range got {
		if len(rows) != c.NHeads() {
			t.Fatalf("position %d has %d rows, want %d", i, len(rows), c.NHeads())
		}
	}
	wantFirst := c.RowsForToken(tokens[0], nil)
	if !slices.Equal(got[0], wantFirst) {
		t.Fatalf("position 0 rows mismatch")
	}
	wantSecond := c.RowsForToken(tokens[1], []int64{tokens[0]})
	if !slices.Equal(got[1], wantSecond) {
		t.Fatalf("position 1 rows mismatch")
	}
	wantThird := c.RowsForToken(tokens[2], []int64{tokens[0], tokens[1]})
	if !slices.Equal(got[2], wantThird) {
		t.Fatalf("position 2 rows mismatch")
	}
}

func TestLoadConstantsValidation(t *testing.T) {
	base := validFile()

	replace := func(key string, value gguf.Value) *gguf.File {
		kvs := append([]gguf.KV(nil), base.Metadata...)
		for i := range kvs {
			if kvs[i].Key == key {
				kvs[i].Value = value
			}
		}
		return &gguf.File{Metadata: kvs}
	}
	replace2 := func(key1 string, value1 gguf.Value, key2 string, value2 gguf.Value) *gguf.File {
		kvs := append([]gguf.KV(nil), base.Metadata...)
		for i := range kvs {
			switch kvs[i].Key {
			case key1:
				kvs[i].Value = value1
			case key2:
				kvs[i].Value = value2
			}
		}
		return &gguf.File{Metadata: kvs}
	}
	drop := func(key string) *gguf.File {
		var kvs []gguf.KV
		for _, kv := range base.Metadata {
			if kv.Key != key {
				kvs = append(kvs, kv)
			}
		}
		return &gguf.File{Metadata: kvs}
	}

	cases := []struct {
		name string
		file *gguf.File
	}{
		{"missing architecture", drop("general.architecture")},
		{"missing ngram_size", drop("qwen4exp.ple.ngram_size")},
		{"missing heads_per_ngram", drop("qwen4exp.ple.heads_per_ngram")},
		{"missing multipliers", drop("qwen4exp.ple.layer_multipliers")},
		{"missing offsets", drop("qwen4exp.ple.head_offsets")},
		{"missing vocab sizes", drop("qwen4exp.ple.head_vocab_sizes")},
		{"missing eos", drop("qwen4exp.ple.eos_token_id")},
		{"ngram_size too small", replace("qwen4exp.ple.ngram_size", gguf.Uint32(1))},
		{"heads_per_ngram zero", replace("qwen4exp.ple.heads_per_ngram", gguf.Uint32(0))},
		{"multipliers too short", replace("qwen4exp.ple.layer_multipliers", gguf.Uint64Array([]uint64{1, 2}))},
		{"offsets wrong length", replace("qwen4exp.ple.head_offsets", gguf.Uint64Array([]uint64{0, 20000003}))},
		{"vocab sizes wrong length", replace("qwen4exp.ple.head_vocab_sizes", gguf.Uint64Array([]uint64{20000003}))},
		{"offsets not prefix sum", replace("qwen4exp.ple.head_offsets", gguf.Uint64Array([]uint64{
			0, 20000003, 40000026, 60000059, 80000106, 100000165, 120000228, 140000297,
			160000374, 180000455, 200000548, 220000655, 240000802, 260000955, 280001114, 300001276,
		}))},
		{"negative array element", replace("qwen4exp.ple.head_offsets", gguf.Int64Array([]int64{
			0, 20000003, 40000026, 60000059, 80000106, 100000165, 120000228, 140000297,
			160000374, 180000455, 200000548, 220000655, 240000802, 260000955, 280001114, -1,
		}))},
		{"vocab size zero, offsets still a valid prefix sum", replace2(
			"qwen4exp.ple.head_vocab_sizes", gguf.Uint64Array([]uint64{
				0, 20000023, 20000033, 20000047, 20000059, 20000063, 20000069, 20000077,
				20000081, 20000093, 20000107, 20000147, 20000153, 20000159, 20000161, 20000171,
			}),
			"qwen4exp.ple.head_offsets", gguf.Uint64Array([]uint64{
				0, 0, 20000023, 40000056, 60000103, 80000162, 100000225, 120000294,
				140000371, 160000452, 180000545, 200000652, 220000799, 240000952, 260001111, 280001272,
			}),
		)},
	}

	for _, tc := range cases {
		if _, err := LoadConstants(tc.file); err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestLoadConstantsRejectsZeroVocabSizeMessage(t *testing.T) {
	base := validFile()
	kvs := append([]gguf.KV(nil), base.Metadata...)
	for i := range kvs {
		switch kvs[i].Key {
		case "qwen4exp.ple.head_vocab_sizes":
			kvs[i].Value = gguf.Uint64Array([]uint64{
				0, 20000023, 20000033, 20000047, 20000059, 20000063, 20000069, 20000077,
				20000081, 20000093, 20000107, 20000147, 20000153, 20000159, 20000161, 20000171,
			})
		case "qwen4exp.ple.head_offsets":
			kvs[i].Value = gguf.Uint64Array([]uint64{
				0, 0, 20000023, 40000056, 60000103, 80000162, 100000225, 120000294,
				140000371, 160000452, 180000545, 200000652, 220000799, 240000952, 260001111, 280001272,
			})
		}
	}
	_, err := LoadConstants(&gguf.File{Metadata: kvs})
	if err == nil {
		t.Fatalf("expected error for zero head_vocab_sizes entry")
	}
	if !strings.Contains(err.Error(), "head_vocab_sizes") {
		t.Fatalf("error %q does not name head_vocab_sizes", err)
	}
}

func TestLoadConstantsAcceptsScalarIntegerTypes(t *testing.T) {
	cases := []struct {
		name string
		kvs  []gguf.KV
	}{
		{"uint32 scalars", []gguf.KV{
			{Key: "general.architecture", Value: gguf.Str("arch")},
			{Key: "arch.ple.ngram_size", Value: gguf.Uint32(2)},
			{Key: "arch.ple.heads_per_ngram", Value: gguf.Uint32(1)},
			{Key: "arch.ple.layer_multipliers", Value: gguf.Uint32Array([]uint32{7, 11})},
			{Key: "arch.ple.head_offsets", Value: gguf.Uint32Array([]uint32{0})},
			{Key: "arch.ple.head_vocab_sizes", Value: gguf.Uint32Array([]uint32{100})},
			{Key: "arch.ple.eos_token_id", Value: gguf.Int64(5)},
		}},
		{"int64 arrays", []gguf.KV{
			{Key: "general.architecture", Value: gguf.Str("arch")},
			{Key: "arch.ple.ngram_size", Value: gguf.Int64(2)},
			{Key: "arch.ple.heads_per_ngram", Value: gguf.Int64(1)},
			{Key: "arch.ple.layer_multipliers", Value: gguf.Int64Array([]int64{7, 11})},
			{Key: "arch.ple.head_offsets", Value: gguf.Int64Array([]int64{0})},
			{Key: "arch.ple.head_vocab_sizes", Value: gguf.Int64Array([]int64{100})},
			{Key: "arch.ple.eos_token_id", Value: gguf.Uint64(5)},
		}},
	}

	for _, tc := range cases {
		c, err := LoadConstants(&gguf.File{Metadata: tc.kvs})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if c.NgramSize != 2 || c.HeadsPerNgram != 1 || c.NHeads() != 1 || c.NRows() != 100 {
			t.Fatalf("%s: unexpected constants %+v", tc.name, c)
		}
	}
}

func parseTok(t *testing.T, s string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("parse token %q: %v", s, err)
	}
	return v
}

func parseRows(t *testing.T, s string) []uint64 {
	t.Helper()
	fields := strings.Fields(s)
	out := make([]uint64, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			t.Fatalf("parse row %q: %v", f, err)
		}
		out[i] = v
	}
	return out
}
