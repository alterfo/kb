package plepatch

import (
	"slices"
	"testing"

	"github.com/alterfo/kb/internal/gguf"
)

func byteLevelVocab() []string {
	table := bytesToUnicode()
	toks := make([]string, 256)
	for b := 0; b < 256; b++ {
		toks[b] = string(table[b])
	}
	return toks
}

func utf8Bytes(text string) []int {
	raw := []byte(text)
	out := make([]int, len(raw))
	for i, b := range raw {
		out[i] = int(b)
	}
	return out
}

func TestBytesToUnicodeStandardTable(t *testing.T) {
	table := bytesToUnicode()
	if table[32] != 'Ġ' {
		t.Fatalf("byte 32 maps to %q, want 'Ġ'", table[32])
	}
	if table[0] != rune(0x100) {
		t.Fatalf("byte 0 maps to %q, want U+0100", table[0])
	}
	if table[33] != '!' {
		t.Fatalf("byte 33 maps to %q, want '!'", table[33])
	}
	if table[126] != '~' {
		t.Fatalf("byte 126 maps to %q, want '~'", table[126])
	}
	if table[160] != rune(0x142) {
		t.Fatalf("byte 160 maps to %q, want U+0142", table[160])
	}
	if table[255] != 'ÿ' {
		t.Fatalf("byte 255 maps to %q, want 'ÿ'", table[255])
	}
	seen := make(map[rune]bool, 256)
	for _, r := range table {
		if seen[r] {
			t.Fatalf("byte-to-unicode table is not a bijection: %q repeats", r)
		}
		seen[r] = true
	}
}

func TestByteLevelVocabIdentity(t *testing.T) {
	toks := append(byteLevelVocab(), "hello", "world", "France", "capital", "red")
	tok, err := NewTokenizer(toks, nil, false, false, "gpt2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	samples := []string{
		"",
		"The capital of France is",
		"Héllo 世界 naïve 🚀 42",
		"def f(x): return x**2 + 1",
		"  spaces\tand\ttabs  ",
		"\n",
		"'quoted' \"string\" (paren) [bracket]",
	}
	for _, s := range samples {
		got := tok.Encode(s)
		want := utf8Bytes(s)
		if !slices.Equal(got, want) {
			t.Fatalf("byte-level identity broken for %q:\n got %v\nwant %v", s, got, want)
		}
	}
}

func TestBPEMerges(t *testing.T) {
	toks := append(byteLevelVocab(), "ab", "abc", "cd")
	merges := []string{"a b", "ab c", "c d"}
	tok, err := NewTokenizer(toks, merges, false, false, "gpt2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}

	cases := []struct {
		in   string
		syms []string
		ids  []int
	}{
		{"abc", []string{"abc"}, []int{257}},
		{"abcd", []string{"abc", "d"}, []int{257, 100}},
		{"ab", []string{"ab"}, []int{256}},
	}
	for _, tc := range cases {
		if got := tok.bpe(tc.in); !slices.Equal(got, tc.syms) {
			t.Fatalf("bpe(%q) = %v, want %v", tc.in, got, tc.syms)
		}
		if got := tok.Encode(tc.in); !slices.Equal(got, tc.ids) {
			t.Fatalf("Encode(%q) = %v, want %v", tc.in, got, tc.ids)
		}
	}
}

func TestPrePatternSelection(t *testing.T) {
	gpt2, err := NewTokenizer(byteLevelVocab(), nil, false, false, "gpt2")
	if err != nil {
		t.Fatalf("gpt2: %v", err)
	}
	if gpt2.pattern != gpt2PreRe {
		t.Fatalf("pre=gpt2 did not select gpt2 pattern")
	}

	bpe, err := NewTokenizer(byteLevelVocab(), nil, false, false, "bpe")
	if err != nil {
		t.Fatalf("bpe: %v", err)
	}
	if bpe.pattern != gpt2PreRe {
		t.Fatalf("pre=bpe did not select gpt2 pattern")
	}

	qwen2, err := NewTokenizer(byteLevelVocab(), nil, false, false, "qwen2")
	if err != nil {
		t.Fatalf("qwen2: %v", err)
	}
	if qwen2.pattern != qwen2PreRe {
		t.Fatalf("pre=qwen2 did not select qwen2 pattern")
	}

	fallback, err := NewTokenizer(byteLevelVocab(), nil, false, false, "llama3")
	if err != nil {
		t.Fatalf("llama3: %v", err)
	}
	if fallback.pattern != qwen2PreRe {
		t.Fatalf("unknown pre did not fall back to qwen2 pattern")
	}
}

func TestQwen2PatternSegmentation(t *testing.T) {
	tok, err := NewTokenizer(byteLevelVocab(), nil, false, false, "qwen2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	got := tok.pattern.FindAllString("It's a test, 42!", -1)
	want := []string{"It", "'s", " a", " test", ",", " ", "4", "2", "!"}
	if !slices.Equal(got, want) {
		t.Fatalf("qwen2 segmentation = %q, want %q", got, want)
	}

	gpt2, err := NewTokenizer(byteLevelVocab(), nil, false, false, "gpt2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	got = gpt2.pattern.FindAllString("It's a test, 42!", -1)
	want = []string{"It", "'s", " a", " test", ",", " 42", "!"}
	if !slices.Equal(got, want) {
		t.Fatalf("gpt2 segmentation = %q, want %q", got, want)
	}
}

func TestByteFallback(t *testing.T) {
	toks := byteLevelVocab()
	merges := []string{"a b"}
	withFallback, err := NewTokenizer(toks, merges, true, false, "gpt2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	if got := withFallback.Encode("ab"); !slices.Equal(got, []int{97, 98}) {
		t.Fatalf("byte fallback Encode(%q) = %v, want [97 98]", "ab", got)
	}

	withoutFallback, err := NewTokenizer(toks, merges, false, false, "gpt2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	if got := withoutFallback.Encode("ab"); len(got) != 0 {
		t.Fatalf("without byte fallback Encode(%q) = %v, want empty", "ab", got)
	}
}

func TestByteFallbackDecomposesMultiByteMappedSymbol(t *testing.T) {
	toks := byteLevelVocab()
	merges := []string{"Ā ā"}
	tok, err := NewTokenizer(toks, merges, true, false, "gpt2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	text := string([]byte{0, 1})
	got := tok.Encode(text)
	want := []int{0, 1}
	if !slices.Equal(got, want) {
		t.Fatalf("byte fallback for multi-byte-mapped symbol Encode(%q) = %v, want %v", text, got, want)
	}
}

func TestAddPrefixSpaceAppliedInEncode(t *testing.T) {
	toks := byteLevelVocab()
	withPrefix, err := NewTokenizer(toks, nil, false, true, "gpt2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	withoutPrefix, err := NewTokenizer(toks, nil, false, false, "gpt2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	got := withPrefix.Encode("hello")
	want := utf8Bytes(" hello")
	if !slices.Equal(got, want) {
		t.Fatalf("add_prefix_space Encode(%q) = %v, want %v", "hello", got, want)
	}
	if slices.Equal(withoutPrefix.Encode("hello"), got) {
		t.Fatalf("add_prefix_space=true should not match add_prefix_space=false output")
	}
}

func TestTokenizerDeterminism(t *testing.T) {
	toks := append(byteLevelVocab(), "ab", "abc")
	tok, err := NewTokenizer(toks, []string{"a b", "ab c"}, false, false, "gpt2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	text := "abc abc abc"
	first := tok.Encode(text)
	for i := 0; i < 100; i++ {
		if got := tok.Encode(text); !slices.Equal(got, first) {
			t.Fatalf("encode not deterministic at iteration %d: %v != %v", i, got, first)
		}
	}
	if _, ok := tok.cache["abc"]; !ok {
		t.Fatalf("bpe cache not populated for repeated symbol")
	}
}

func TestLoadTokenizer(t *testing.T) {
	toks := append(byteLevelVocab(), "ab")
	f := &gguf.File{Metadata: []gguf.KV{
		{Key: "tokenizer.ggml.tokens", Value: gguf.StringArray(toks)},
		{Key: "tokenizer.ggml.merges", Value: gguf.StringArray([]string{"a b"})},
		{Key: "tokenizer.ggml.pre", Value: gguf.Str("gpt2")},
		{Key: "tokenizer.ggml.byte_fallback", Value: gguf.Bool(false)},
		{Key: "tokenizer.ggml.add_prefix_space", Value: gguf.Bool(true)},
	}}
	tok, err := LoadTokenizer(f)
	if err != nil {
		t.Fatalf("LoadTokenizer: %v", err)
	}
	if tok.addPrefixSpace != true {
		t.Fatalf("add_prefix_space not loaded")
	}
	if tok.byteFallback {
		t.Fatalf("byte_fallback should be false")
	}
	if tok.pattern != gpt2PreRe {
		t.Fatalf("pre=gpt2 did not select gpt2 pattern")
	}
	if r, ok := tok.bpeRanks[[2]string{"a", "b"}]; !ok || r != 0 {
		t.Fatalf("merge rank for (a,b) = %d ok=%v, want 0 true", r, ok)
	}
	if got := tok.Encode("ab"); !slices.Equal(got, []int{32, 256}) {
		t.Fatalf("Encode(%q) = %v, want [32 256] (add_prefix_space prepends a space)", "ab", got)
	}
}

func TestLoadTokenizerDefaults(t *testing.T) {
	f := &gguf.File{Metadata: []gguf.KV{
		{Key: "tokenizer.ggml.tokens", Value: gguf.StringArray(byteLevelVocab())},
	}}
	tok, err := LoadTokenizer(f)
	if err != nil {
		t.Fatalf("LoadTokenizer: %v", err)
	}
	if tok.pattern != qwen2PreRe {
		t.Fatalf("missing pre should default to qwen2 pattern")
	}
	if tok.byteFallback {
		t.Fatalf("missing byte_fallback should default to false")
	}
	if tok.addPrefixSpace {
		t.Fatalf("missing add_prefix_space should default to false")
	}
}

func TestLoadTokenizerErrors(t *testing.T) {
	if _, err := LoadTokenizer(&gguf.File{}); err == nil {
		t.Fatalf("missing tokens should error")
	}
	empty := &gguf.File{Metadata: []gguf.KV{
		{Key: "tokenizer.ggml.tokens", Value: gguf.StringArray(nil)},
	}}
	if _, err := LoadTokenizer(empty); err == nil {
		t.Fatalf("empty tokens should error")
	}
	wrongType := &gguf.File{Metadata: []gguf.KV{
		{Key: "tokenizer.ggml.tokens", Value: gguf.Str("not an array")},
	}}
	if _, err := LoadTokenizer(wrongType); err == nil {
		t.Fatalf("non-array tokens should error")
	}
}

func TestNewTokenizerInvalidMerge(t *testing.T) {
	if _, err := NewTokenizer(byteLevelVocab(), []string{"ab"}, false, false, "gpt2"); err == nil {
		t.Fatalf("merge without space separator should error")
	}
}
