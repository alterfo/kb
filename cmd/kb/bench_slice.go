package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alterfo/kb/internal/bench/corpus"
	runbench "github.com/alterfo/kb/internal/bench/run"
)

func runBenchSliceCmd(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench slice", flag.ContinueOnError)
	fset.SetOutput(stderr)
	corpusDir := fset.String("corpus", "", "source corpus root (directory tree of .txt docs)")
	questionsPath := fset.String("questions", "", "questions JSONL path")
	typesCSV := fset.String("types", "", "comma-separated question types to include (empty = all)")
	outCorpus := fset.String("out-corpus", "slice-corpus", "output corpus directory")
	outQuestions := fset.String("out-questions", "slice-questions.jsonl", "output questions JSONL path")
	limitPerType := fset.Int("limit-per-type", 0, "max questions to keep per type (0 = unlimited)")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if *corpusDir == "" || *questionsPath == "" {
		fmt.Fprintln(stderr, "bench slice: -corpus and -questions are required")
		return 2
	}

	questions, qwarns, err := corpus.LoadQuestions(*questionsPath)
	if err != nil {
		fmt.Fprintf(stderr, "bench slice: %v\n", err)
		return 1
	}
	for _, w := range qwarns {
		fmt.Fprintf(stdout, "bench slice: questions warning: %s\n", w)
	}

	questions = runbench.FilterQuestions(questions, csvSet(*typesCSV), 0)
	if *limitPerType > 0 {
		questions = limitQuestionsPerType(questions, *limitPerType)
	}

	if err := corpus.WriteQuestions(*outQuestions, questions); err != nil {
		fmt.Fprintf(stderr, "bench slice: %v\n", err)
		return 1
	}

	if len(questions) == 0 {
		fmt.Fprintln(stdout, "bench slice: no questions matched the filters")
		if err := os.MkdirAll(*outCorpus, 0o755); err != nil {
			fmt.Fprintf(stderr, "bench slice: create output corpus: %v\n", err)
			return 1
		}
		return 0
	}

	wanted := map[string]struct{}{}
	for _, q := range questions {
		for _, id := range q.ExpectedDocIDs {
			if id != "" {
				wanted[id] = struct{}{}
			}
		}
	}

	found := map[string]string{}
	err = filepath.WalkDir(*corpusDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".txt") {
			return nil
		}
		id, ok := corpus.DocIDFromFileName(path)
		if !ok {
			return nil
		}
		if _, keep := wanted[id]; !keep {
			return nil
		}
		rel, relErr := filepath.Rel(*corpusDir, path)
		if relErr != nil {
			rel = path
		}
		found[id] = rel
		return nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "bench slice: walk corpus: %v\n", err)
		return 1
	}

	copied := 0
	var missing []string
	for id := range wanted {
		rel, ok := found[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		if err := copyFile(filepath.Join(*corpusDir, rel), filepath.Join(*outCorpus, rel)); err != nil {
			fmt.Fprintf(stderr, "bench slice: copy %s: %v\n", rel, err)
			return 1
		}
		copied++
	}
	sort.Strings(missing)
	for _, id := range missing {
		fmt.Fprintf(stdout, "bench slice: missing document %s\n", id)
	}
	fmt.Fprintf(stdout, "bench slice: copied %d documents to %s\n", copied, *outCorpus)
	fmt.Fprintf(stdout, "bench slice: wrote %d questions to %s\n", len(questions), *outQuestions)
	return 0
}

func limitQuestionsPerType(qs []corpus.Question, limit int) []corpus.Question {
	if limit <= 0 {
		return qs
	}
	counts := map[string]int{}
	out := make([]corpus.Question, 0, len(qs))
	for _, q := range qs {
		if counts[q.Type] >= limit {
			continue
		}
		counts[q.Type]++
		out = append(out, q)
	}
	return out
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
