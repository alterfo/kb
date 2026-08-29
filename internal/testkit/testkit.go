package testkit

import (
	"context"
	"hash/fnv"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/alterfo/kb/internal/llm"
)

const DefaultDim = 384

const (
	extractMarker    = "extract a knowledge graph from a text chunk"
	summaryMarker    = "summarize a cluster of related entities"
	decomposeMarker  = "break a user question into 2-5"
	synthesizeMarker = "answer a focused sub-question using only"
	aggregateMarker  = "combine sub-answers into one coherent"
	findGapsMarker   = "list what important information is still missing"
	coverageMarker   = "judge whether the given excerpts sufficiently answer"
	expandMarker     = "rewrite a search query into 3-5"
)

var tokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

var fileNameCitationRe = regexp.MustCompile(`\(([^)]*\.md)\)`)

type FakeEmbedder struct {
	Dim int
	Err error
}

func NewFakeEmbedder() FakeEmbedder {
	return FakeEmbedder{Dim: DefaultDim}
}

func (f FakeEmbedder) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	dim := f.Dim
	if dim <= 0 {
		dim = DefaultDim
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = embedText(text, dim)
	}
	return out, nil
}

type FakeChat struct {
	Responses map[string]string
	Fallback  string
	Err       error
}

func NewFakeChat() FakeChat {
	return FakeChat{Responses: DefaultResponses()}
}

func DefaultResponses() map[string]string {
	return map[string]string{
		extractMarker:   `{"entities":[{"name":"kb","type":"project","description":"a graph-based knowledge base"},{"name":"Alice","type":"person","description":"maintains the retriever module"}],"relations":[{"source":"Alice","target":"kb","type":"maintains","description":"Alice maintains the kb project"}]}`,
		summaryMarker:   `{"title":"kb and its maintainers","summary":"The kb project is maintained by Alice."}`,
		decomposeMarker: `[{"subquestion":"what is the kb project"},{"subquestion":"who maintains the retriever module"}]`,
		findGapsMarker:  `[]`,
		coverageMarker:  `{"covered":true,"score":1.0}`,
		expandMarker:    `["kb project","retriever module maintainer"]`,
	}
}

func (f FakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if f.Err != nil {
		return llm.ChatResponse{}, f.Err
	}
	content := f.respond(req)
	if content == "" {
		content = f.dynamic(req)
	}
	if content == "" {
		content = f.Fallback
	}
	return llm.ChatResponse{Content: content, FinishReason: "stop"}, nil
}

type responseKey struct {
	original string
	lower    string
}

func (f FakeChat) respond(req llm.ChatRequest) string {
	if len(f.Responses) == 0 {
		return ""
	}
	hay := strings.ToLower(messageHaystack(req.Messages))
	keys := make([]responseKey, 0, len(f.Responses))
	for key := range f.Responses {
		keys = append(keys, responseKey{original: key, lower: strings.ToLower(key)})
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i].lower) != len(keys[j].lower) {
			return len(keys[i].lower) > len(keys[j].lower)
		}
		return keys[i].lower < keys[j].lower
	})
	for _, key := range keys {
		if strings.Contains(hay, key.lower) {
			return f.Responses[key.original]
		}
	}
	return ""
}

func (f FakeChat) dynamic(req llm.ChatRequest) string {
	hay := strings.ToLower(messageHaystack(req.Messages))
	names := extractFileNames(req)
	switch {
	case strings.Contains(hay, synthesizeMarker):
		return citationsAnswer("The provided excerpts answer the sub-question.", names)
	case strings.Contains(hay, aggregateMarker):
		return citationsAnswer("The provided sources answer the question.", names)
	}
	return ""
}

func messageHaystack(messages []llm.ChatMessage) string {
	contents := make([]string, len(messages))
	for i, m := range messages {
		contents[i] = m.Content
	}
	return strings.Join(contents, "\n")
}

func extractFileNames(req llm.ChatRequest) []string {
	hay := messageHaystack(req.Messages)
	seen := make(map[string]bool)
	var names []string
	for _, m := range fileNameCitationRe.FindAllStringSubmatch(hay, -1) {
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func citationsAnswer(prefix string, names []string) string {
	if len(names) == 0 {
		return prefix
	}
	var b strings.Builder
	b.WriteString(prefix)
	for _, name := range names {
		b.WriteString(" (")
		b.WriteString(name)
		b.WriteString(")")
	}
	return b.String()
}

func embedText(text string, dim int) []float32 {
	vec := make([]float32, dim)
	for _, token := range tokenRe.FindAllString(strings.ToLower(text), -1) {
		h := fnvHash(token)
		idx := int(h % uint64(dim))
		val := float32(1)
		if h&(1<<63) != 0 {
			val = -1
		}
		vec[idx] += val
	}
	normalize(vec)
	return vec
}

func fnvHash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func normalize(v []float32) {
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	if sumSq == 0 {
		return
	}
	norm := math.Sqrt(sumSq)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
}
