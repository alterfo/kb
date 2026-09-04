package plepatch

import (
	"fmt"

	"github.com/alterfo/kb/internal/gguf"
)

type Constants struct {
	NgramSize      int
	HeadsPerNgram  int
	Multipliers    []uint64
	HeadOffsets    []uint64
	HeadVocabSizes []uint64
	EosTokenID     int64
}

type HeadRange struct {
	Order     int
	FirstHead int
	HeadCount int
}

func (c *Constants) NHeads() int {
	return (c.NgramSize - 1) * c.HeadsPerNgram
}

func (c *Constants) NRows() uint64 {
	last := len(c.HeadOffsets) - 1
	return c.HeadOffsets[last] + c.HeadVocabSizes[last]
}

func (c *Constants) HeadOf(h int) (uint64, uint64) {
	return c.HeadOffsets[h], c.HeadVocabSizes[h]
}

func (c *Constants) HeadRanges() []HeadRange {
	out := make([]HeadRange, 0, c.NgramSize-1)
	for n := 2; n <= c.NgramSize; n++ {
		out = append(out, HeadRange{
			Order:     n,
			FirstHead: (n - 2) * c.HeadsPerNgram,
			HeadCount: c.HeadsPerNgram,
		})
	}
	return out
}

func LoadConstants(f *gguf.File) (*Constants, error) {
	archValue, ok := f.MetadataValue("general.architecture")
	if !ok {
		return nil, fmt.Errorf("plepatch: general.architecture missing; is this a GGUF model file?")
	}
	arch, err := archValue.AsString()
	if err != nil {
		return nil, fmt.Errorf("plepatch: general.architecture: %w", err)
	}

	getInt := func(suffix string) (int64, error) {
		key := arch + suffix
		v, ok := f.MetadataValue(key)
		if !ok {
			return 0, fmt.Errorf("plepatch: required metadata key missing: %s", key)
		}
		n, err := v.AsInt64()
		if err != nil {
			return 0, fmt.Errorf("plepatch: %s: %w", key, err)
		}
		return n, nil
	}
	getInts := func(suffix string) ([]uint64, error) {
		key := arch + suffix
		v, ok := f.MetadataValue(key)
		if !ok {
			return nil, fmt.Errorf("plepatch: required metadata key missing: %s", key)
		}
		out, err := intArray(v, key)
		if err != nil {
			return nil, err
		}
		return out, nil
	}

	ngramSize, err := getInt(".ple.ngram_size")
	if err != nil {
		return nil, err
	}
	headsPerNgram, err := getInt(".ple.heads_per_ngram")
	if err != nil {
		return nil, err
	}
	multipliers, err := getInts(".ple.layer_multipliers")
	if err != nil {
		return nil, err
	}
	headOffsets, err := getInts(".ple.head_offsets")
	if err != nil {
		return nil, err
	}
	headVocabSizes, err := getInts(".ple.head_vocab_sizes")
	if err != nil {
		return nil, err
	}
	eosTokenID, err := getInt(".ple.eos_token_id")
	if err != nil {
		return nil, err
	}

	if ngramSize < 2 {
		return nil, fmt.Errorf("plepatch: %s.ple.ngram_size=%d, want >= 2", arch, ngramSize)
	}
	if headsPerNgram < 1 {
		return nil, fmt.Errorf("plepatch: %s.ple.heads_per_ngram=%d, want >= 1", arch, headsPerNgram)
	}
	nHeads := int(ngramSize-1) * int(headsPerNgram)
	if int64(len(multipliers)) < ngramSize {
		return nil, fmt.Errorf("plepatch: layer_multipliers has %d entries, need ngram_size=%d", len(multipliers), ngramSize)
	}
	if len(headOffsets) != nHeads || len(headVocabSizes) != nHeads {
		return nil, fmt.Errorf("plepatch: head_offsets/head_vocab_sizes length != (ngram_size-1)*heads_per_ngram=%d", nHeads)
	}

	var expect uint64
	for h, off := range headOffsets {
		vsz := headVocabSizes[h]
		if off != expect {
			return nil, fmt.Errorf("plepatch: head_offsets[%d]=%d is not the prefix sum %d; heads must tile the table contiguously", h, off, expect)
		}
		expect = off + vsz
	}

	return &Constants{
		NgramSize:      int(ngramSize),
		HeadsPerNgram:  int(headsPerNgram),
		Multipliers:    multipliers,
		HeadOffsets:    headOffsets,
		HeadVocabSizes: headVocabSizes,
		EosTokenID:     eosTokenID,
	}, nil
}

func intArray(v gguf.Value, key string) ([]uint64, error) {
	arr, err := v.AsArray()
	if err != nil {
		return nil, fmt.Errorf("plepatch: %s: %w", key, err)
	}
	switch arr.Type {
	case gguf.TypeUint64:
		out := arr.Data.([]uint64)
		return out, nil
	case gguf.TypeInt64:
		src := arr.Data.([]int64)
		out := make([]uint64, len(src))
		for i, x := range src {
			if x < 0 {
				return nil, fmt.Errorf("plepatch: %s: negative array element %d", key, x)
			}
			out[i] = uint64(x)
		}
		return out, nil
	case gguf.TypeUint32:
		src := arr.Data.([]uint32)
		out := make([]uint64, len(src))
		for i, x := range src {
			out[i] = uint64(x)
		}
		return out, nil
	case gguf.TypeInt32:
		src := arr.Data.([]int32)
		out := make([]uint64, len(src))
		for i, x := range src {
			if x < 0 {
				return nil, fmt.Errorf("plepatch: %s: negative array element %d", key, x)
			}
			out[i] = uint64(x)
		}
		return out, nil
	case gguf.TypeUint16:
		src := arr.Data.([]uint16)
		out := make([]uint64, len(src))
		for i, x := range src {
			out[i] = uint64(x)
		}
		return out, nil
	case gguf.TypeInt16:
		src := arr.Data.([]int16)
		out := make([]uint64, len(src))
		for i, x := range src {
			if x < 0 {
				return nil, fmt.Errorf("plepatch: %s: negative array element %d", key, x)
			}
			out[i] = uint64(x)
		}
		return out, nil
	case gguf.TypeUint8:
		src := arr.Data.([]uint8)
		out := make([]uint64, len(src))
		for i, x := range src {
			out[i] = uint64(x)
		}
		return out, nil
	case gguf.TypeInt8:
		src := arr.Data.([]int8)
		out := make([]uint64, len(src))
		for i, x := range src {
			if x < 0 {
				return nil, fmt.Errorf("plepatch: %s: negative array element %d", key, x)
			}
			out[i] = uint64(x)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("plepatch: %s: array element type %d is not an integer", key, arr.Type)
	}
}

func MixedValue(ctx []uint64, n int, multipliers []uint64) uint64 {
	mixed := ctx[0] * multipliers[0]
	for j := 1; j < n; j++ {
		mixed ^= ctx[j] * multipliers[j]
	}
	return mixed
}

func (c *Constants) RowsForToken(tok int64, prev []int64) []uint64 {
	nGram := c.NgramSize
	ctx := make([]uint64, nGram)
	ctx[0] = uint64(tok)
	cut := false
	for s := 1; s < nGram; s++ {
		var t int64
		if cut || len(prev) < s {
			t = c.EosTokenID
		} else {
			t = prev[len(prev)-s]
		}
		if cut || t < 0 || t == c.EosTokenID {
			cut = true
			ctx[s] = uint64(c.EosTokenID)
		} else {
			ctx[s] = uint64(t)
		}
	}

	rows := make([]uint64, 0, c.NHeads())
	for n := 2; n <= nGram; n++ {
		mixed := MixedValue(ctx, n, c.Multipliers)
		base := (n - 2) * c.HeadsPerNgram
		for g := 0; g < c.HeadsPerNgram; g++ {
			h := base + g
			rows = append(rows, mixed%c.HeadVocabSizes[h]+c.HeadOffsets[h])
		}
	}
	return rows
}

func (c *Constants) RowsForSequence(tokens []int64) [][]uint64 {
	nPrev := c.NgramSize - 1
	res := make([][]uint64, len(tokens))
	for i, tok := range tokens {
		start := i - nPrev
		if start < 0 {
			start = 0
		}
		prev := tokens[start:i]
		res[i] = c.RowsForToken(tok, prev)
	}
	return res
}
