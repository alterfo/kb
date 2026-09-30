package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/bench/generate"
	runbench "github.com/alterfo/kb/internal/bench/run"
	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/llm"
)

func runBenchGenerateCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench generate", flag.ContinueOnError)
	fset.SetOutput(stderr)
	corpusDir := fset.String("corpus", "", "benchmark corpus root (directory tree of .txt/.json docs)")
	seedPath := fset.String("seed", "", "seed questions JSONL path for few-shot examples (optional)")
	out := fset.String("out", "generated-questions.jsonl", "generated questions JSONL output path")
	count := fset.Int("count", 0, "max questions to generate (one per document; 0 = all docs)")
	model := fset.String("model", env.LLMModel, "LLM model used for generation")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if *corpusDir == "" {
		fmt.Fprintln(stderr, "bench generate: -corpus is required")
		return 2
	}

	chat := llm.NewClient(llm.Config{
		BaseURL:           env.LLMBaseURL,
		NoProxyHosts:      env.NoProxy,
		DefaultEmbedModel: env.EmbedModel,
		RequestTimeout:    env.LLMTimeout,
		MaxTokens:         env.LLMMaxTokens,
		NoThink:           env.LLMNoThink,
		RedactPII:         env.PIIRedact,
	})

	generated, warnings, err := benchGenerate(context.Background(), chat, *model, *corpusDir, *seedPath, *out, *count)
	for _, w := range warnings {
		fmt.Fprintf(stdout, "bench generate: corpus warning: %s\n", w)
	}
	if err != nil {
		fmt.Fprintf(stderr, "bench generate: %v\n", err)
		if generated > 0 {
			fmt.Fprintf(stdout, "bench generate: partial: %d questions written to %s\n", generated, *out)
		}
		return 1
	}
	fmt.Fprintf(stdout, "bench generate: %d questions written to %s\n", generated, *out)
	return 0
}

func benchGenerate(ctx context.Context, chat runbench.ChatClient, model, corpusDir, seedPath, outPath string, count int) (int, []string, error) {
	docs, warns, err := corpus.LoadCorpus(corpusDir)
	if err != nil {
		return 0, warns, err
	}
	if count > 0 && count < len(docs) {
		docs = docs[:count]
	}

	var seed []corpus.Question
	if seedPath != "" {
		seed, _, err = corpus.LoadQuestions(seedPath)
		if err != nil {
			return 0, warns, err
		}
	}

	questions, err := generate.Generate(ctx, chat, model, docs, seed)
	if err != nil {
		return len(questions), warns, err
	}
	if err := corpus.WriteQuestions(outPath, questions); err != nil {
		return len(questions), warns, err
	}
	return len(questions), warns, nil
}
