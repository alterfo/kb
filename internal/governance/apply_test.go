package governance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/llm"
)

func TestApplyTrashSuccess(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "---\nid: a\n---\n\nbody")
	idx := &fakeIndexer{}
	g := New(root, idx, nil, "")

	results := g.Apply(context.Background(), []string{"trash:notes/a.md"})
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("results = %+v, want one OK result", results)
	}
	mustNotExist(t, root, "notes/a.md")
	mustExist(t, root, ".trash/notes/a.md")
	if len(idx.removed) != 1 || idx.removed[0] != "notes/a.md" {
		t.Fatalf("removed = %v, want [notes/a.md]", idx.removed)
	}
}

func TestApplyUnknownAction(t *testing.T) {
	g := New(t.TempDir(), &fakeIndexer{}, nil, "")
	results := g.Apply(context.Background(), []string{"bogus"})
	if len(results) != 1 || results[0].OK {
		t.Fatalf("results = %+v, want one failing result", results)
	}
}

func TestApplyTrashMissingFileFails(t *testing.T) {
	root := t.TempDir()
	g := New(root, &fakeIndexer{}, nil, "")
	results := g.Apply(context.Background(), []string{"trash:notes/missing.md"})
	if len(results) != 1 || results[0].OK {
		t.Fatalf("results = %+v, want one failing result", results)
	}
}

func TestApplyMergeProposalLeavesCorpusUntouched(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "---\nid: a\n---\n\nfirst")
	writeFile(t, root, "notes/b.md", "---\nid: b\n---\n\nsecond")
	chat := fakeChat{resp: llm.ChatResponse{Content: `{"title":"Merged","content":"combined text"}`}}
	g := New(root, &fakeIndexer{}, chat, "m")

	results := g.Apply(context.Background(), []string{"merge:notes/a.md;notes/b.md"})
	if len(results) != 1 || !results[0].OK || results[0].Proposal == nil {
		t.Fatalf("results = %+v, want one OK proposal", results)
	}
	p := results[0].Proposal
	if p.Kind != "merge" || !strings.Contains(p.Content, "combined text") {
		t.Fatalf("proposal = %+v", p)
	}
	// Corpus untouched: both originals still present, unmodified.
	mustExist(t, root, "notes/a.md")
	mustExist(t, root, "notes/b.md")
}

func TestApplyMergeRequiresRewritableSource(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "github/a.md", "---\nid: a\n---\n\nfirst")
	writeFile(t, root, "github/b.md", "---\nid: b\n---\n\nsecond")
	chat := fakeChat{resp: llm.ChatResponse{Content: `{"content":"x"}`}}
	g := New(root, &fakeIndexer{}, chat, "m")

	results := g.Apply(context.Background(), []string{"merge:github/a.md;github/b.md"})
	if len(results) != 1 || results[0].OK {
		t.Fatalf("results = %+v, want one failing result (github not rewritable)", results)
	}
}

func TestApplyCompressProposal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/big.md", "---\nid: a\n---\n\noriginal long text")
	chat := fakeChat{resp: llm.ChatResponse{Content: `{"content":"short text"}`}}
	g := New(root, &fakeIndexer{}, chat, "m")

	results := g.Apply(context.Background(), []string{"compress:notes/big.md"})
	if len(results) != 1 || !results[0].OK || results[0].Proposal == nil {
		t.Fatalf("results = %+v, want one OK proposal", results)
	}
	if results[0].Proposal.Content != "short text" {
		t.Fatalf("content = %q", results[0].Proposal.Content)
	}
}

func TestProposeMergeNoChatConfigured(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "---\nid: a\n---\n\nfirst")
	writeFile(t, root, "notes/b.md", "---\nid: b\n---\n\nsecond")
	g := New(root, &fakeIndexer{}, nil, "m")

	if _, err := g.ProposeMerge(context.Background(), []string{"notes/a.md", "notes/b.md"}); err == nil {
		t.Fatal("expected error when no chat client is configured")
	}
}

func TestProposeMergeRequiresTwoFiles(t *testing.T) {
	root := t.TempDir()
	g := New(root, &fakeIndexer{}, fakeChat{}, "m")
	if _, err := g.ProposeMerge(context.Background(), []string{"notes/a.md"}); err == nil {
		t.Fatal("expected error for a single-file merge")
	}
}

func TestProposeMergeRejectsNonJSONReply(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "---\nid: a\n---\n\nfirst")
	writeFile(t, root, "notes/b.md", "---\nid: b\n---\n\nsecond")
	chat := fakeChat{resp: llm.ChatResponse{Content: "not json at all"}}
	g := New(root, &fakeIndexer{}, chat, "m")

	if _, err := g.ProposeMerge(context.Background(), []string{"notes/a.md", "notes/b.md"}); err == nil {
		t.Fatal("expected error for a non-JSON LLM reply")
	}
}

func TestApplyRewriteMergeWritesPrimaryAndTrashesOriginals(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "---\nid: a\n---\n\nfirst")
	writeFile(t, root, "notes/b.md", "---\nid: b\n---\n\nsecond")
	now := time.Now()
	chtimes(t, root, "notes/a.md", now)
	chtimes(t, root, "notes/b.md", now.Add(time.Hour))
	idx := &fakeIndexer{}
	g := New(root, idx, fakeChat{}, "m")

	detail, err := g.ApplyRewrite(context.Background(), []string{"notes/a.md", "notes/b.md"}, "merged content")
	if err != nil {
		t.Fatalf("ApplyRewrite: %v", err)
	}
	if !strings.Contains(detail, "notes/b.md") {
		t.Fatalf("detail = %q, want primary notes/b.md mentioned", detail)
	}
	mustExist(t, root, "notes/b.md")
	primaryRaw := readRaw(t, root, "notes/b.md")
	if !strings.Contains(primaryRaw, "id: b") {
		t.Fatalf("primary frontmatter = %q, want preserved id", primaryRaw)
	}
	if !strings.Contains(primaryRaw, "merged content") {
		t.Fatalf("primary body = %q", primaryRaw)
	}
	mustNotExist(t, root, "notes/a.md")
	mustExist(t, root, ".trash/notes/a.md")

	if len(idx.removed) != 2 {
		t.Fatalf("removed = %v, want 2 calls", idx.removed)
	}
	found := false
	for _, p := range idx.updated {
		if p == "notes/b.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("updated = %v, want notes/b.md re-indexed", idx.updated)
	}
}

func TestApplyRewriteRollsBackOnIndexAddError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "---\nid: a\n---\n\nfirst")
	writeFile(t, root, "notes/b.md", "---\nid: b\n---\n\nsecond")
	idx := &fakeIndexer{addErr: errors.New("boom: always fails")}
	g := New(root, idx, fakeChat{}, "m")

	_, err := g.ApplyRewrite(context.Background(), []string{"notes/a.md", "notes/b.md"}, "merged content")
	if err == nil {
		t.Fatal("expected error")
	}
	// Both originals restored, no leftover rewrite.
	mustExist(t, root, "notes/a.md")
	mustExist(t, root, "notes/b.md")
	mustNotExist(t, root, ".trash/notes/a.md")
	mustNotExist(t, root, ".trash/notes/b.md")
}

func TestApplyRewriteRollsBackPartialTrashOnRemoveError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "---\nid: a\n---\n\nfirst")
	writeFile(t, root, "notes/b.md", "---\nid: b\n---\n\nsecond")
	idx := &fakeIndexer{removeErrOn: "notes/b.md"}
	g := New(root, idx, fakeChat{}, "m")

	_, err := g.ApplyRewrite(context.Background(), []string{"notes/a.md", "notes/b.md"}, "merged content")
	if err == nil {
		t.Fatal("expected error")
	}
	// a.md was trashed before b.md failed — must be restored, not left in trash.
	mustExist(t, root, "notes/a.md")
	mustNotExist(t, root, ".trash/notes/a.md")
	mustExist(t, root, "notes/b.md")
}

func TestApplyRewriteRequiresNonEmptyContent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "---\nid: a\n---\n\nfirst")
	g := New(root, &fakeIndexer{}, fakeChat{}, "m")
	if _, err := g.ApplyRewrite(context.Background(), []string{"notes/a.md"}, "   "); err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestApplyRewriteRequiresRewritableSource(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "github/a.md", "---\nid: a\n---\n\nfirst")
	g := New(root, &fakeIndexer{}, fakeChat{}, "m")
	if _, err := g.ApplyRewrite(context.Background(), []string{"github/a.md"}, "content"); err == nil {
		t.Fatal("expected error: github is not rewritable")
	}
	mustExist(t, root, "github/a.md")
}
