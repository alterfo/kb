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

	if err := validateQuestionsOutput(*outQuestions, *questionsPath, *corpusDir); err != nil {
		fmt.Fprintf(stderr, "bench slice: %v\n", err)
		return 1
	}

	if len(questions) == 0 {
		if err := prepareOutputCorpus(*outCorpus, *corpusDir); err != nil {
			fmt.Fprintf(stderr, "bench slice: %v\n", err)
			return 1
		}
		if err := corpus.WriteQuestions(*outQuestions, questions); err != nil {
			fmt.Fprintf(stderr, "bench slice: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "bench slice: no questions matched the filters")
		return 0
	}

	if err := prepareOutputCorpus(*outCorpus, *corpusDir); err != nil {
		fmt.Fprintf(stderr, "bench slice: %v\n", err)
		return 1
	}
	if err := corpus.WriteQuestions(*outQuestions, questions); err != nil {
		fmt.Fprintf(stderr, "bench slice: %v\n", err)
		return 1
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

func validateQuestionsOutput(outQuestions, questionsPath, corpusDir string) error {
	srcAbs, err := filepath.Abs(questionsPath)
	if err != nil {
		return fmt.Errorf("resolve input questions: %w", err)
	}
	srcAbs = resolveExisting(srcAbs)

	outAbs, err := filepath.Abs(outQuestions)
	if err != nil {
		return fmt.Errorf("resolve output questions: %w", err)
	}
	outResolved := resolveExisting(outAbs)

	if filepath.Clean(srcAbs) == filepath.Clean(outResolved) {
		return fmt.Errorf("output questions must not be the input questions file: %s", outQuestions)
	}
	if srcInfo, statErr := os.Stat(srcAbs); statErr == nil {
		if outInfo, statOutErr := os.Stat(outResolved); statOutErr == nil && os.SameFile(srcInfo, outInfo) {
			return fmt.Errorf("output questions must not be the input questions file: %s", outQuestions)
		}
	}

	corpusAbs, err := filepath.Abs(corpusDir)
	if err != nil {
		return fmt.Errorf("resolve source corpus: %w", err)
	}
	corpusResolved := resolveExisting(corpusAbs)
	if pathsOverlap(corpusResolved, outResolved) {
		return fmt.Errorf("output questions must not be inside the source corpus: %s", outQuestions)
	}

	return nil
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
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if srcAbs == dstAbs {
		return fmt.Errorf("bench slice: source and destination are the same file: %s", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".slice-copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmp != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	tmp = nil
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func prepareOutputCorpus(outCorpus, corpusDir string) error {
	srcInfo, err := os.Stat(corpusDir)
	if err != nil {
		return fmt.Errorf("bench slice: source corpus: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("bench slice: source corpus is not a directory: %s", corpusDir)
	}
	srcAbs, err := filepath.Abs(corpusDir)
	if err != nil {
		return fmt.Errorf("bench slice: resolve source corpus: %w", err)
	}
	dstAbs, err := filepath.Abs(outCorpus)
	if err != nil {
		return fmt.Errorf("bench slice: resolve output corpus: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("bench slice: resolve working directory: %w", err)
	}
	srcAbs = resolveExisting(srcAbs)
	dstResolved := resolveExisting(dstAbs)
	cwdResolved := resolveExisting(cwd)
	if err := validateOutputDestination(dstResolved, cwdResolved); err != nil {
		return err
	}
	if err := validateSourceOutputOverlap(srcAbs, dstResolved, srcInfo); err != nil {
		return err
	}
	if pathsOverlap(srcAbs, dstResolved) {
		return fmt.Errorf("bench slice: output corpus must not be the source corpus or inside it")
	}
	dstInfo, err := os.Stat(dstAbs)
	switch {
	case err == nil:
		if !dstInfo.IsDir() {
			return fmt.Errorf("bench slice: output corpus is not a directory: %s", dstAbs)
		}
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("bench slice: inspect output corpus: %w", err)
	}
	if err := os.RemoveAll(dstAbs); err != nil {
		return fmt.Errorf("bench slice: clear output corpus: %w", err)
	}
	if err := os.MkdirAll(dstAbs, 0o755); err != nil {
		return fmt.Errorf("bench slice: create output corpus: %w", err)
	}
	return nil
}

func resolveExisting(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolvedParent, filepath.Base(path))
	}
	return path
}

func validateOutputDestination(dst, cwd string) error {
	if dst == cwd || pathWithin(cwd, dst) {
		return fmt.Errorf("bench slice: output corpus must not contain the working directory: %s", dst)
	}
	if dst == string(os.PathSeparator) {
		return fmt.Errorf("bench slice: output corpus must not be the filesystem root")
	}
	if _, err := os.Stat(cwd); err != nil {
		return fmt.Errorf("bench slice: inspect working directory: %w", err)
	}
	dstInfo, err := os.Stat(dst)
	switch {
	case err == nil:
		if sameFileOrAncestor(cwd, dstInfo) {
			return fmt.Errorf("bench slice: output corpus must not contain the working directory: %s", dst)
		}
	case os.IsNotExist(err):
		return nil
	default:
		return fmt.Errorf("bench slice: inspect output corpus: %w", err)
	}
	return nil
}

func validateSourceOutputOverlap(srcAbs, dstAbs string, srcInfo os.FileInfo) error {
	dstInfo, err := os.Stat(dstAbs)
	switch {
	case err == nil:
		if sameFileOrAncestor(dstAbs, srcInfo) || sameFileOrAncestor(srcAbs, dstInfo) {
			return fmt.Errorf("bench slice: output corpus must not be the source corpus or inside it")
		}
	case os.IsNotExist(err):
		if sameFileOrAncestor(dstAbs, srcInfo) {
			return fmt.Errorf("bench slice: output corpus must not be the source corpus or inside it")
		}
	default:
		return fmt.Errorf("bench slice: inspect output corpus: %w", err)
	}
	return nil
}

func sameFileOrAncestor(path string, target os.FileInfo) bool {
	p := filepath.Clean(path)
	for {
		info, err := os.Stat(p)
		if err == nil && os.SameFile(info, target) {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

func pathsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	return pathWithin(a, b) || pathWithin(b, a)
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
