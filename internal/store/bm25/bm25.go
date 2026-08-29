package bm25

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/alterfo/kb/internal/store/vector"
)

type CorpusVersioner interface {
	CorpusVersion(ctx context.Context) (int, error)
}

type ChunkLister interface {
	AllForBM25(ctx context.Context) ([]vector.Chunk, error)
}

const (
	k1 = 1.2
	b  = 0.75
)

var tokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

func Tokenize(text string) []string {
	matches := tokenRe.FindAllString(strings.ToLower(text), -1)
	return matches
}

type ScoredID struct {
	ID    string
	Score float64
}

type docEntry struct {
	terms  map[string]int
	length int
	chunk  vector.Chunk
}

type Index struct {
	mu            sync.RWMutex
	docs          map[string]docEntry
	df            map[string]int
	avgDocLen     float64
	n             int
	corpusVersion int
	built         bool
}

func New() *Index {
	return &Index{}
}

func (idx *Index) NeedsRebuild(corpusVersion int) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return !idx.built || idx.corpusVersion != corpusVersion
}

func (idx *Index) Rebuild(chunks []vector.Chunk, corpusVersion int) {
	docs := make(map[string]docEntry, len(chunks))
	df := make(map[string]int)
	totalLen := 0

	for _, c := range chunks {
		tokens := Tokenize(c.Text)
		terms := make(map[string]int, len(tokens))
		for _, tok := range tokens {
			terms[tok]++
		}
		docs[c.ID] = docEntry{terms: terms, length: len(tokens), chunk: c}
		totalLen += len(tokens)
		for tok := range terms {
			df[tok]++
		}
	}

	avg := 0.0
	if len(docs) > 0 {
		avg = float64(totalLen) / float64(len(docs))
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.docs = docs
	idx.df = df
	idx.avgDocLen = avg
	idx.n = len(docs)
	idx.corpusVersion = corpusVersion
	idx.built = true
}

func (idx *Index) Refresh(ctx context.Context, versioner CorpusVersioner, chunks ChunkLister) error {
	version, err := versioner.CorpusVersion(ctx)
	if err != nil {
		return err
	}
	if !idx.NeedsRebuild(version) {
		return nil
	}
	all, err := chunks.AllForBM25(ctx)
	if err != nil {
		return err
	}
	idx.Rebuild(all, version)
	return nil
}

// Chunk returns the full chunk indexed under id, as passed to the most
// recent Rebuild. Used by the retriever to resolve BM25-only hits that
// never appeared in a dense query result.
func (idx *Index) Chunk(id string) (vector.Chunk, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	entry, ok := idx.docs[id]
	if !ok {
		return vector.Chunk{}, false
	}
	return entry.chunk, true
}

func (idx *Index) Search(query string, k int) []ScoredID {
	if k <= 0 {
		return nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if !idx.built || idx.n == 0 {
		return nil
	}

	queryTerms := Tokenize(query)
	if len(queryTerms) == 0 {
		return nil
	}

	scores := make(map[string]float64)
	for id, doc := range idx.docs {
		var score float64
		for _, term := range queryTerms {
			tf, ok := doc.terms[term]
			if !ok {
				continue
			}
			df := idx.df[term]
			idf := math.Log(1 + (float64(idx.n)-float64(df)+0.5)/(float64(df)+0.5))
			denom := float64(tf) + k1*(1-b+b*float64(doc.length)/idx.avgDocLen)
			score += idf * (float64(tf) * (k1 + 1)) / denom
		}
		if score > 0 {
			scores[id] = score
		}
	}

	results := make([]ScoredID, 0, len(scores))
	for id, score := range scores {
		results = append(results, ScoredID{ID: id, Score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > k {
		results = results[:k]
	}
	return results
}
