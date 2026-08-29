package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/sink"
)

type describeChat interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

type describeCandidate struct {
	rel string
	doc connector.Document
}

type describeResult struct {
	Scanned   int
	Skipped   int
	Generated int
	Written   int
	Failed    int
}

type describeDeps struct {
	Root   string
	Source string
	Model  string
	Batch  int
	Chat   describeChat
	Write  func(rel string, data []byte) error
	Index  func(ctx context.Context, rel string) error
}

func runDescribeCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("describe", flag.ContinueOnError)
	fset.SetOutput(stderr)
	source := fset.String("source", "", "only describe documents from this source")
	if err := fset.Parse(args); err != nil {
		return 2
	}

	bundle, err := newEngineBundle(env)
	if err != nil {
		fmt.Fprintf(stderr, "describe: opening db: %v\n", err)
		return 1
	}
	defer bundle.close()

	model := env.DescribeModel
	if model == "" {
		model = env.LLMModel
	}
	batch := env.DescribeBatch
	if batch <= 0 {
		batch = 10
	}

	deps := describeDeps{
		Root:   env.KBRoot,
		Source: *source,
		Model:  model,
		Batch:  batch,
		Chat:   bundle.chat,
		Write: func(rel string, data []byte) error {
			return sink.WritePath(env.KBRoot, rel, data)
		},
		Index: func(ctx context.Context, rel string) error {
			return bundle.indexer.AddOrUpdateDocument(ctx, rel)
		},
	}

	res, err := runDescribe(context.Background(), deps)
	if err != nil {
		fmt.Fprintf(stderr, "describe: %v\n", err)
		return 1
	}
	if err := bundle.bm25.Refresh(context.Background(), bundle.db, bundle.vector); err != nil {
		fmt.Fprintf(stderr, "describe: refresh bm25: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "describe: scanned=%d skipped=%d generated=%d written=%d failed=%d\n",
		res.Scanned, res.Skipped, res.Generated, res.Written, res.Failed)
	return 0
}

func runDescribe(ctx context.Context, deps describeDeps) (describeResult, error) {
	if deps.Batch <= 0 {
		deps.Batch = 1
	}
	candidates, skipped, collectErrs, err := collectDescribeCandidates(deps.Root, deps.Source)
	if err != nil {
		return describeResult{}, err
	}

	res := describeResult{Scanned: len(candidates), Skipped: skipped, Failed: collectErrs}
	for start := 0; start < len(candidates); start += deps.Batch {
		end := start + deps.Batch
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[start:end]
		bodies := make([]string, len(batch))
		for i, candidate := range batch {
			bodies[i] = candidate.doc.Body
		}

		summaries, chatErr := describeBatch(ctx, deps.Chat, deps.Model, bodies)
		for i, candidate := range batch {
			origDoc := candidate.doc
			summary := ""
			if chatErr == nil && i < len(summaries) {
				summary = strings.TrimSpace(summaries[i])
			}
			if summary == "" {
				summary = fallbackSummary(candidate.doc.Body)
			}
			if summary == "" {
				res.Failed++
				continue
			}

			candidate.doc.Summary = summary
			raw, err := render.Render(candidate.doc)
			if err != nil {
				res.Failed++
				continue
			}
			if deps.Write != nil {
				if err := deps.Write(candidate.rel, raw); err != nil {
					res.Failed++
					continue
				}
			}
			if deps.Index != nil {
				if err := deps.Index(ctx, candidate.rel); err != nil {
					if origRaw, origErr := render.Render(origDoc); origErr == nil && deps.Write != nil {
						_ = deps.Write(candidate.rel, origRaw)
					}
					res.Failed++
					continue
				}
			}
			res.Written++
		}
		if chatErr == nil {
			res.Generated += len(summaries)
		}
	}
	return res, nil
}

func collectDescribeCandidates(root, source string) ([]describeCandidate, int, int, error) {
	var candidates []describeCandidate
	skipped := 0
	collectErrs := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			collectErrs++
			return nil
		}
		doc, err := render.Parse(data)
		if err != nil {
			collectErrs++
			return nil
		}
		if source != "" && doc.Source != source {
			return nil
		}
		if doc.Summary != "" {
			skipped++
			return nil
		}
		candidates = append(candidates, describeCandidate{rel: rel, doc: doc})
		return nil
	})
	if err != nil {
		return nil, 0, 0, err
	}
	return candidates, skipped, collectErrs, nil
}

func describeBatch(ctx context.Context, chat describeChat, model string, bodies []string) ([]string, error) {
	if chat == nil {
		return nil, fmt.Errorf("describe: chat client is nil")
	}
	if len(bodies) == 0 {
		return nil, nil
	}

	var prompt strings.Builder
	prompt.WriteString("Summarize each numbered document in one concise sentence of at most 200 characters. Return only a JSON array with one string per document, in the same order.\n\n")
	for i, body := range bodies {
		fmt.Fprintf(&prompt, "[%d]\n%s\n\n", i+1, body)
	}

	resp, err := chat.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{{
			Role:    "user",
			Content: prompt.String(),
		}},
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return nil, fmt.Errorf("describe: empty chat response")
	}

	summaries, err := parseSummariesResponse(resp.Content)
	if err != nil {
		return nil, err
	}
	if len(summaries) != len(bodies) {
		return nil, fmt.Errorf("describe: expected %d summaries, got %d", len(bodies), len(summaries))
	}
	return summaries, nil
}

func parseSummariesResponse(content string) ([]string, error) {
	text := strings.TrimSpace(content)
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = strings.TrimSpace(text[newline+1:])
		}
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}
	if start := strings.Index(text, "["); start >= 0 {
		if end := strings.LastIndex(text, "]"); end > start {
			text = text[start : end+1]
		}
	}

	var summaries []string
	if err := json.Unmarshal([]byte(text), &summaries); err != nil {
		return nil, fmt.Errorf("describe: parse summaries: %w", err)
	}
	for i := range summaries {
		summaries[i] = strings.TrimSpace(summaries[i])
	}
	return summaries, nil
}

func fallbackSummary(body string) string {
	const maxRunes = 200
	text := strings.Join(strings.Fields(body), " ")
	if text == "" {
		return ""
	}
	for i, r := range text {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(text) || text[i+1] == ' ') {
			return truncateRunes(text[:i+1], maxRunes)
		}
	}
	return truncateRunes(text, maxRunes)
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return strings.TrimSpace(string(runes[:max]))
}
