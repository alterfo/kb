package dragon

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/alterfo/kb/internal/bench/corpus"
)

type AskFunc func(ctx context.Context, q corpus.Question) (answer string, foundIDs []string)

func RunQuestions(ctx context.Context, questions []corpus.Question, concurrency int, ask AskFunc, onProgress func(done int)) map[string]SubmissionEntry {
	if concurrency <= 0 {
		concurrency = 1
	}
	entries := make([]SubmissionEntry, len(questions))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var completed int32
	for i, q := range questions {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, q corpus.Question) {
			defer wg.Done()
			defer func() { <-sem }()
			answer, foundIDs := ask(ctx, q)
			entries[i] = SubmissionEntry{FoundIDs: foundIDs, ModelAnswer: answer}
			if onProgress != nil {
				onProgress(int(atomic.AddInt32(&completed, 1)))
			}
		}(i, q)
	}
	wg.Wait()

	out := make(map[string]SubmissionEntry, len(questions))
	for i, q := range questions {
		out[q.ID] = entries[i]
	}
	return out
}
