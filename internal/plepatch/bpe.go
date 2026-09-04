package plepatch

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/alterfo/kb/internal/gguf"
)

const (
	gpt2PrePattern  = `'s|'t|'re|'ve|'m|'ll|'d| ?\p{L}+| ?\p{N}+| ?[^\s\p{L}\p{N}]+|\s+`
	qwen2PrePattern = `(?i:'s|'t|'re|'ve|'m|'ll|'d)|[^\r\n\p{L}\p{N}]?[\p{L}\p{M}]+|\p{N}| ?[^\s\p{L}\p{M}\p{N}]+[\r\n]*|\s*[\r\n]+|\s+`
)

var (
	gpt2PreRe  = regexp.MustCompile(gpt2PrePattern)
	qwen2PreRe = regexp.MustCompile(qwen2PrePattern)
)

type Tokenizer struct {
	byteFallback   bool
	addPrefixSpace bool
	decoder        [256]rune
	encoder        map[rune]byte
	tokens         []string
	tokToID        map[string]int
	bpeRanks       map[[2]string]int
	pattern        *regexp.Regexp
	cache          map[string][]string
}

func bytesToUnicode() [256]rune {
	bs := make([]int, 0, 256)
	for b := '!'; b <= '~'; b++ {
		bs = append(bs, int(b))
	}
	for b := 0xa1; b <= 0xac; b++ {
		bs = append(bs, b)
	}
	for b := 0xae; b <= 0xff; b++ {
		bs = append(bs, b)
	}
	cs := append([]int(nil), bs...)
	inBase := make(map[int]bool, len(bs))
	for _, b := range bs {
		inBase[b] = true
	}
	n := 0
	for b := 0; b < 256; b++ {
		if !inBase[b] {
			bs = append(bs, b)
			cs = append(cs, 256+n)
			n++
		}
	}
	var table [256]rune
	for i, b := range bs {
		table[b] = rune(cs[i])
	}
	return table
}

func NewTokenizer(tokens, merges []string, byteFallback, addPrefixSpace bool, pre string) (*Tokenizer, error) {
	t := &Tokenizer{
		byteFallback:   byteFallback,
		addPrefixSpace: addPrefixSpace,
		decoder:        bytesToUnicode(),
		encoder:        make(map[rune]byte, 256),
		tokens:         append([]string(nil), tokens...),
		tokToID:        make(map[string]int, len(tokens)),
		bpeRanks:       make(map[[2]string]int, len(merges)),
		cache:          make(map[string][]string),
	}
	for b, r := range t.decoder {
		t.encoder[r] = byte(b)
	}
	for i, tok := range t.tokens {
		t.tokToID[tok] = i
	}
	for rank, m := range merges {
		a, b, ok := strings.Cut(m, " ")
		if !ok {
			return nil, fmt.Errorf("plepatch: invalid merge %q: missing space separator", m)
		}
		t.bpeRanks[[2]string{a, b}] = rank
	}
	switch pre {
	case "gpt2", "bpe":
		t.pattern = gpt2PreRe
	default:
		t.pattern = qwen2PreRe
	}
	return t, nil
}

func LoadTokenizer(f *gguf.File) (*Tokenizer, error) {
	tokensValue, ok := f.MetadataValue("tokenizer.ggml.tokens")
	if !ok {
		return nil, fmt.Errorf("plepatch: tokenizer.ggml.tokens missing from GGUF")
	}
	arr, err := tokensValue.AsArray()
	if err != nil {
		return nil, fmt.Errorf("plepatch: tokenizer.ggml.tokens: %w", err)
	}
	tokens, err := arr.AsStrings()
	if err != nil {
		return nil, fmt.Errorf("plepatch: tokenizer.ggml.tokens: %w", err)
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("plepatch: tokenizer.ggml.tokens is empty")
	}

	var merges []string
	if mv, ok := f.MetadataValue("tokenizer.ggml.merges"); ok {
		ma, err := mv.AsArray()
		if err != nil {
			return nil, fmt.Errorf("plepatch: tokenizer.ggml.merges: %w", err)
		}
		merges, err = ma.AsStrings()
		if err != nil {
			return nil, fmt.Errorf("plepatch: tokenizer.ggml.merges: %w", err)
		}
	}

	byteFallback := false
	if bv, ok := f.MetadataValue("tokenizer.ggml.byte_fallback"); ok {
		byteFallback, err = bv.AsBool()
		if err != nil {
			return nil, fmt.Errorf("plepatch: tokenizer.ggml.byte_fallback: %w", err)
		}
	}

	addPrefixSpace := false
	if av, ok := f.MetadataValue("tokenizer.ggml.add_prefix_space"); ok {
		addPrefixSpace, err = av.AsBool()
		if err != nil {
			return nil, fmt.Errorf("plepatch: tokenizer.ggml.add_prefix_space: %w", err)
		}
	}

	pre := "qwen2"
	if pv, ok := f.MetadataValue("tokenizer.ggml.pre"); ok {
		pre, err = pv.AsString()
		if err != nil {
			return nil, fmt.Errorf("plepatch: tokenizer.ggml.pre: %w", err)
		}
	}

	return NewTokenizer(tokens, merges, byteFallback, addPrefixSpace, pre)
}

func (t *Tokenizer) byteEncode(text string) string {
	var sb strings.Builder
	sb.Grow(len(text))
	for _, b := range []byte(text) {
		sb.WriteRune(t.decoder[b])
	}
	return sb.String()
}

func (t *Tokenizer) bpe(text string) []string {
	if syms, ok := t.cache[text]; ok {
		return syms
	}
	word := t.byteEncode(text)
	symbols := make([]string, 0, len(word))
	for _, r := range word {
		symbols = append(symbols, string(r))
	}
	if len(symbols) == 1 {
		t.cache[text] = symbols
		return symbols
	}
	for {
		seen := make(map[[2]string]bool)
		for i := 0; i < len(symbols)-1; i++ {
			seen[[2]string{symbols[i], symbols[i+1]}] = true
		}
		bestRank := -1
		var best [2]string
		found := false
		for p := range seen {
			if r, ok := t.bpeRanks[p]; ok && (!found || r < bestRank) {
				best = p
				bestRank = r
				found = true
			}
		}
		if !found {
			break
		}
		first, second := best[0], best[1]
		merged := make([]string, 0, len(symbols))
		i := 0
		for i < len(symbols) {
			if i < len(symbols)-1 && symbols[i] == first && symbols[i+1] == second {
				merged = append(merged, first+second)
				i += 2
			} else {
				merged = append(merged, symbols[i])
				i++
			}
		}
		symbols = merged
		if len(symbols) == 1 {
			break
		}
	}
	t.cache[text] = symbols
	return symbols
}

func (t *Tokenizer) Encode(text string) []int {
	var ids []int
	for _, piece := range t.pattern.FindAllString(text, -1) {
		for _, sym := range t.bpe(piece) {
			tid, ok := t.tokToID[sym]
			if !ok && t.byteFallback {
				for _, b := range []byte(sym) {
					if fid, ok2 := t.tokToID[string(t.decoder[b])]; ok2 {
						ids = append(ids, fid)
					}
				}
				continue
			}
			if ok {
				ids = append(ids, tid)
			}
		}
	}
	return ids
}
