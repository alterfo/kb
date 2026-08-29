package chunk

import (
	"strings"
	"sync"

	"github.com/neurosnap/sentences"
	"github.com/neurosnap/sentences/english"
)

var (
	tokenizerOnce sync.Once
	tokenizer     *sentences.DefaultSentenceTokenizer
	tokenizerErr  error
)

func sentenceTokenizer() (*sentences.DefaultSentenceTokenizer, error) {
	tokenizerOnce.Do(func() {
		tokenizer, tokenizerErr = english.NewSentenceTokenizer(nil)
	})
	return tokenizer, tokenizerErr
}

func splitSentences(text string) ([]string, error) {
	tok, err := sentenceTokenizer()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range tok.Tokenize(text) {
		trimmed := strings.TrimSpace(s.Text)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out, nil
}

type TextChunker struct {
	Size    int
	Overlap int
}

func NewTextChunker(size, overlap int) *TextChunker {
	if size <= 0 {
		size = 512
	}
	if overlap < 0 || overlap >= size {
		overlap = 0
	}
	return &TextChunker{Size: size, Overlap: overlap}
}

func (c *TextChunker) Chunk(text string) ([]Chunk, error) {
	sents, err := splitSentences(text)
	if err != nil {
		return nil, err
	}
	if len(sents) == 0 {
		return nil, nil
	}

	type sized struct {
		text   string
		tokens int
	}
	items := make([]sized, len(sents))
	for i, s := range sents {
		items[i] = sized{text: s, tokens: EstimateTokens(s)}
	}

	var chunks []Chunk
	var cur []sized
	curTokens := 0

	flush := func() {
		if len(cur) == 0 {
			return
		}
		parts := make([]string, len(cur))
		for i, s := range cur {
			parts[i] = s.text
		}
		text := strings.Join(parts, " ")
		chunks = append(chunks, Chunk{
			Text:       text,
			Index:      len(chunks),
			TokenCount: EstimateTokens(text),
		})
	}

	for _, it := range items {
		if curTokens > 0 && curTokens+it.tokens > c.Size {
			flush()

			if c.Overlap > 0 {
				var overlap []sized
				overlapTokens := 0
				for i := len(cur) - 1; i >= 0; i-- {
					overlapTokens += cur[i].tokens
					overlap = append([]sized{cur[i]}, overlap...)
					if overlapTokens >= c.Overlap {
						break
					}
				}
				cur = overlap
				curTokens = overlapTokens
			} else {
				cur = nil
				curTokens = 0
			}
		}
		cur = append(cur, it)
		curTokens += it.tokens
	}
	flush()

	return chunks, nil
}
