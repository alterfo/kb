package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/render"
)

const promptDocMaxChars = 8_000

const mergeSystemPrompt = `You are a knowledge-base editor. You are given several notes about the same topic. ` +
	`Merge them into one up-to-date document: remove repetition and outdated claims (trust the newer text on ` +
	`conflicts), keep ALL unique facts, decisions, dates, names and links. Respond with a strict JSON object ` +
	`{"title": "...", "content": "..."} and nothing else; content is ready-to-use markdown.`

const compressSystemPrompt = `You are a knowledge-base editor. Compress the document, keeping all facts, ` +
	`decisions, numbers, dates, names and links; remove filler, repetition and wordiness. Target size is at ` +
	`most one third of the original. Respond with a strict JSON object {"content": "..."} and nothing else; ` +
	`content is ready-to-use markdown.`

// Indexer is the subset of *engine.Indexer governance needs to keep the
// vector/BM25/graph index in sync with trash/restore/rewrite operations.
type Indexer interface {
	RemoveDocument(ctx context.Context, path string) error
	AddOrUpdateDocument(ctx context.Context, path string) error
}

// ChatClient runs a single chat completion. Satisfied by *llm.Client.
type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

// Governance ties the mechanical Scan/Trash primitives to the live index
// (so a trash/restore/rewrite stays consistent with search) and an LLM (for
// merge/compress proposals only — see ApplyRewrite for why no LLM text
// reaches the corpus without it).
type Governance struct {
	Root           string
	Trash          *Trash
	Indexer        Indexer
	Chat           ChatClient
	Model          string
	RewriteSources map[string]bool
}

func New(root string, indexer Indexer, chat ChatClient, model string) *Governance {
	return &Governance{
		Root:           root,
		Trash:          NewTrash(root),
		Indexer:        indexer,
		Chat:           chat,
		Model:          model,
		RewriteSources: map[string]bool{"notes": true},
	}
}

// Proposal is an LLM-authored merge/compress candidate. Nothing has been
// written to the corpus yet — see ApplyRewrite.
type Proposal struct {
	Kind         string // "merge" | "compress"
	Paths        []string
	Primary      string
	Content      string
	OriginalSize int
	NewSize      int
}

// ApplyResult is the outcome of one action passed to Apply.
type ApplyResult struct {
	OK       bool
	Action   string
	Detail   string
	Proposal *Proposal
}

// Apply executes selected cleanup actions, one result per action. Actions
// are encoded as "trash:<path>", "merge:<path>;<path>[;...]" or
// "compress:<path>" — paths never contain ";"/":" prefixes, they are
// root-relative markdown paths (matches how a dashboard form would submit
// checkbox selections). "trash" executes immediately (mechanical,
// recoverable via Trash.Restore); "merge"/"compress" only produce an LLM
// Proposal — the corpus stays untouched until the caller confirms it via
// ApplyRewrite. Never panics: every failure becomes an ApplyResult with
// OK=false so one bad item doesn't abort the rest.
func (g *Governance) Apply(ctx context.Context, actions []string) []ApplyResult {
	results := make([]ApplyResult, 0, len(actions))
	for _, action := range actions {
		kind, payload, _ := strings.Cut(action, ":")
		switch {
		case kind == "trash" && payload != "":
			detail, err := g.doTrash(ctx, payload)
			results = append(results, resultFor(action, detail, err))
		case kind == "merge" && payload != "":
			proposal, err := g.ProposeMerge(ctx, strings.Split(payload, ";"))
			results = append(results, proposalResult(action, proposal, err))
		case kind == "compress" && payload != "":
			proposal, err := g.ProposeCompress(ctx, payload)
			results = append(results, proposalResult(action, proposal, err))
		default:
			results = append(results, ApplyResult{OK: false, Action: action, Detail: fmt.Sprintf("unknown action: %q", action)})
		}
	}
	return results
}

func resultFor(action, detail string, err error) ApplyResult {
	if err != nil {
		return ApplyResult{OK: false, Action: action, Detail: err.Error()}
	}
	return ApplyResult{OK: true, Action: action, Detail: detail}
}

func proposalResult(action string, p *Proposal, err error) ApplyResult {
	if err != nil {
		return ApplyResult{OK: false, Action: action, Detail: err.Error()}
	}
	return ApplyResult{
		OK: true, Action: action, Proposal: p,
		Detail: fmt.Sprintf("proposal ready: %s", strings.Join(p.Paths, " + ")),
	}
}

func (g *Governance) doTrash(ctx context.Context, relPath string) (string, error) {
	if g.Indexer != nil {
		if err := g.Indexer.RemoveDocument(ctx, relPath); err != nil {
			return "", fmt.Errorf("governance: trash %s: remove from index: %w", relPath, err)
		}
	}
	trashed, err := g.Trash.SoftDelete(relPath)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s -> trash (%s)", relPath, trashed), nil
}

// ProposeMerge asks the LLM for a merged version of paths. Read-only: the
// result is a Proposal for the manual-review step, the corpus is not
// touched.
func (g *Governance) ProposeMerge(ctx context.Context, paths []string) (*Proposal, error) {
	paths = trimAll(paths)
	if len(paths) < 2 {
		return nil, fmt.Errorf("governance: merge requires at least two files")
	}
	for _, p := range paths {
		if err := g.requireRewritable(p); err != nil {
			return nil, err
		}
	}
	if g.Chat == nil {
		return nil, fmt.Errorf("governance: merge %s: no chat client configured", strings.Join(paths, ", "))
	}

	docs := make([]string, len(paths))
	originalSize := 0
	for i, p := range paths {
		text, err := g.readDocument(p)
		if err != nil {
			return nil, err
		}
		docs[i] = text
		originalSize += len(text)
	}

	var b strings.Builder
	for i, p := range paths {
		fmt.Fprintf(&b, "### Document %d — %s\n\n%s\n\n", i+1, p, clip(docs[i], promptDocMaxChars))
	}
	resp, err := g.Chat.Chat(ctx, llm.ChatRequest{
		Model: g.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: mergeSystemPrompt},
			{Role: "user", Content: b.String()},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("governance: merge %s: %w", strings.Join(paths, ", "), err)
	}
	parsed, err := parseLLMJSON(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("governance: merge %s: %w", strings.Join(paths, ", "), err)
	}
	content, _ := parsed["content"].(string)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("governance: merge %s: empty content from LLM", strings.Join(paths, ", "))
	}
	if title, ok := parsed["title"].(string); ok && strings.TrimSpace(title) != "" && !strings.HasPrefix(strings.TrimSpace(content), "#") {
		content = fmt.Sprintf("# %s\n\n%s", strings.TrimSpace(title), content)
	}

	primary, err := g.primaryPath(paths)
	if err != nil {
		return nil, err
	}
	return &Proposal{
		Kind: "merge", Paths: paths, Primary: primary, Content: content,
		OriginalSize: originalSize, NewSize: len(content),
	}, nil
}

// ProposeCompress asks the LLM for a compressed version of one document.
// Read-only, see ProposeMerge.
func (g *Governance) ProposeCompress(ctx context.Context, relPath string) (*Proposal, error) {
	relPath = strings.TrimSpace(relPath)
	if err := g.requireRewritable(relPath); err != nil {
		return nil, err
	}
	if g.Chat == nil {
		return nil, fmt.Errorf("governance: compress %s: no chat client configured", relPath)
	}
	text, err := g.readDocument(relPath)
	if err != nil {
		return nil, err
	}

	prompt := fmt.Sprintf("### %s\n\n%s", relPath, clip(text, promptDocMaxChars))
	resp, err := g.Chat.Chat(ctx, llm.ChatRequest{
		Model: g.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: compressSystemPrompt},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("governance: compress %s: %w", relPath, err)
	}
	parsed, err := parseLLMJSON(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("governance: compress %s: %w", relPath, err)
	}
	content, _ := parsed["content"].(string)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("governance: compress %s: empty content from LLM", relPath)
	}

	return &Proposal{
		Kind: "compress", Paths: []string{relPath}, Primary: relPath, Content: content,
		OriginalSize: len(text), NewSize: len(content),
	}, nil
}

// ApplyRewrite is the confirm step for a merge/compress Proposal: it
// re-validates paths (untrusted round-trip through a review UI), trashes
// the originals, writes content at the primary (freshest) path and
// re-indexes it. This is the ONLY place LLM-produced text is ever written
// to the corpus — callers must only invoke it after a human has seen (and
// possibly edited) the proposal. If writing or indexing fails, every
// trashed original is restored so the corpus is never left with the
// originals gone and the rewrite missing or unindexed.
func (g *Governance) ApplyRewrite(ctx context.Context, paths []string, content string) (string, error) {
	paths = trimAll(paths)
	if len(paths) == 0 {
		return "", fmt.Errorf("governance: apply rewrite: no paths given")
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("governance: apply rewrite: empty content")
	}
	for _, p := range paths {
		if err := g.requireRewritable(p); err != nil {
			return "", err
		}
		if _, err := g.readDocument(p); err != nil {
			return "", err
		}
	}
	primary, err := g.primaryPath(paths)
	if err != nil {
		return "", err
	}
	full, err := resolveWithin(g.Root, primary)
	if err != nil {
		return "", err
	}
	primaryData, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("governance: apply rewrite: read %s: %w", primary, err)
	}
	writeData := []byte(content)
	if doc, parseErr := render.Parse(primaryData); parseErr == nil {
		doc.Body = content
		doc.UpdatedAt = time.Now()
		rendered, err := render.Render(doc)
		if err != nil {
			return "", fmt.Errorf("governance: apply rewrite: render %s: %w", primary, err)
		}
		writeData = rendered
	}

	var trashed []string
	for _, p := range paths {
		if g.Indexer != nil {
			if err := g.Indexer.RemoveDocument(ctx, p); err != nil {
				g.rollback(ctx, trashed)
				return "", fmt.Errorf("governance: apply rewrite: remove %s from index: %w", p, err)
			}
		}
		dest, err := g.Trash.SoftDelete(p)
		if err != nil {
			g.rollback(ctx, trashed)
			return "", fmt.Errorf("governance: apply rewrite: trash %s: %w", p, err)
		}
		trashed = append(trashed, dest)
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		g.rollback(ctx, trashed)
		return "", fmt.Errorf("governance: apply rewrite: %w", err)
	}
	if err := os.WriteFile(full, writeData, 0o644); err != nil {
		g.rollback(ctx, trashed)
		return "", fmt.Errorf("governance: apply rewrite: write %s: %w", primary, err)
	}

	if g.Indexer != nil {
		if err := g.Indexer.AddOrUpdateDocument(ctx, primary); err != nil {
			_ = os.Remove(full)
			g.rollback(ctx, trashed)
			return "", fmt.Errorf("governance: apply rewrite: index %s: %w", primary, err)
		}
	}

	if len(paths) > 1 {
		return fmt.Sprintf("merged %d file(s) -> %s (originals in trash)", len(paths), primary), nil
	}
	return fmt.Sprintf("%s: overwritten with reviewed version (original in trash)", primary), nil
}

// rollback restores every trashed path (best-effort: one restore failure
// doesn't stop the rest) and re-indexes it, undoing a partially-applied
// ApplyRewrite.
func (g *Governance) rollback(ctx context.Context, trashedPaths []string) {
	for _, tp := range trashedPaths {
		restored, err := g.Trash.Restore(tp)
		if err != nil {
			continue
		}
		if g.Indexer != nil {
			_ = g.Indexer.AddOrUpdateDocument(ctx, restored)
		}
	}
}

func (g *Governance) rewriteSources() map[string]bool {
	if g.RewriteSources != nil {
		return g.RewriteSources
	}
	return map[string]bool{"notes": true}
}

func (g *Governance) requireRewritable(relPath string) error {
	source := engine.InferSource(relPath)
	if !g.rewriteSources()[source] {
		return fmt.Errorf("governance: %s: only %s can be rewritten — every other source is overwritten by sync", relPath, joinSorted(g.rewriteSources()))
	}
	return nil
}

func (g *Governance) readDocument(relPath string) (string, error) {
	full, err := resolveWithin(g.Root, relPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, relPath)
		}
		return "", fmt.Errorf("governance: read %s: %w", relPath, err)
	}
	return string(data), nil
}

func (g *Governance) primaryPath(paths []string) (string, error) {
	var best string
	var bestMod time.Time
	for _, p := range paths {
		full, err := resolveWithin(g.Root, p)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(full)
		if err != nil {
			return "", fmt.Errorf("governance: %s: %w", p, ErrNotFound)
		}
		if best == "" || info.ModTime().After(bestMod) {
			best, bestMod = p, info.ModTime()
		}
	}
	return best, nil
}

func trimAll(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinSorted(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func clip(text string, limit int) string {
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return string(r[:limit]) + "\n\n[...text truncated for prompt...]"
}

func parseLLMJSON(raw string) (map[string]any, error) {
	text := strings.TrimSpace(raw)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end <= start {
		return nil, fmt.Errorf("LLM returned a non-JSON response: %.200s", text)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text[start:end+1]), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse LLM JSON: %w", err)
	}
	return parsed, nil
}
