package governance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/render"
)

// Tombstones records source:id pairs a later incremental sync must not
// re-ingest. Satisfied by *state.TombstoneStore.
type Tombstones interface {
	Add(sourceKey, id string) error
}

// RetirementCandidate is a cited source classified as an upstream (synced,
// not KB-owned) document — it needs a Tombstones entry, not a bare trash,
// or the next incremental sync would just re-download it.
type RetirementCandidate struct {
	Path   string
	Source string
	ID     string
}

// RetirementPlan splits an approved answer's cited sources into retirement
// candidates for a manual-confirm step (see ApplyRetirement). Sources in
// RewriteSources are trashed outright; every other source is tombstoned
// instead. Anything whose path can't be resolved to root, that names
// ApprovedNote itself, or whose document id can't be recovered from
// frontmatter, is reported under Skipped rather than silently dropped.
type RetirementPlan struct {
	ApprovedNote string
	Notes        []string
	Upstream     []RetirementCandidate
	Skipped      []string
}

// ProposeRetirement is read-only: it classifies sourcePaths, it does not
// touch the corpus or the tombstone store.
func (g *Governance) ProposeRetirement(sourcePaths []string, approvedNote string) RetirementPlan {
	approvedNote = strings.TrimSpace(approvedNote)
	plan := RetirementPlan{ApprovedNote: approvedNote}
	seen := map[string]bool{}

	for _, raw := range sourcePaths {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		rel := g.relativize(raw)
		if rel == "" {
			if !seen[raw] {
				seen[raw] = true
				plan.Skipped = append(plan.Skipped, raw)
			}
			continue
		}
		if rel == approvedNote || seen[rel] {
			continue
		}
		seen[rel] = true

		source := engine.InferSource(rel)
		if g.rewriteSources()[source] {
			plan.Notes = append(plan.Notes, rel)
			continue
		}
		id, err := g.documentID(rel)
		if err != nil {
			plan.Skipped = append(plan.Skipped, rel)
			continue
		}
		plan.Upstream = append(plan.Upstream, RetirementCandidate{Path: rel, Source: source, ID: id})
	}
	return plan
}

// RetirementResult is the outcome of retiring one candidate.
type RetirementResult struct {
	OK     bool
	Path   string
	Detail string
}

// ApplyRetirement is the confirm step for RetirementPlan: it trashes the
// selected notes, and for upstream candidates removes the document from the
// index and the corpus, then records a tombstone so the next incremental
// sync does not re-ingest it. Nothing here runs until the caller has
// confirmed the selection (mirrors ApplyRewrite's manual-confirm rule). One
// bad item never aborts the rest.
func (g *Governance) ApplyRetirement(ctx context.Context, ts Tombstones, notes []string, upstream []RetirementCandidate) []RetirementResult {
	var results []RetirementResult
	for _, rel := range notes {
		detail, err := g.doTrash(ctx, rel)
		if err != nil {
			results = append(results, RetirementResult{OK: false, Path: rel, Detail: err.Error()})
			continue
		}
		results = append(results, RetirementResult{OK: true, Path: rel, Detail: detail})
	}
	for _, c := range upstream {
		if g.Indexer != nil {
			if err := g.Indexer.RemoveDocument(ctx, c.Path); err != nil {
				results = append(results, RetirementResult{OK: false, Path: c.Path, Detail: "remove from index: " + err.Error()})
				continue
			}
		}
		full, err := resolveWithin(g.Root, c.Path)
		if err != nil {
			results = append(results, RetirementResult{OK: false, Path: c.Path, Detail: err.Error()})
			continue
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			results = append(results, RetirementResult{OK: false, Path: c.Path, Detail: "remove file: " + err.Error()})
			continue
		}
		if err := ts.Add(c.Source, c.ID); err != nil {
			results = append(results, RetirementResult{OK: false, Path: c.Path, Detail: err.Error()})
			continue
		}
		results = append(results, RetirementResult{
			OK: true, Path: c.Path,
			Detail: fmt.Sprintf("%s -> removed + tombstone (%s:%s)", c.Path, c.Source, c.ID),
		})
	}
	return results
}

func (g *Governance) documentID(relPath string) (string, error) {
	full, err := resolveWithin(g.Root, relPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("governance: read %s: %w", relPath, err)
	}
	doc, err := render.Parse(data)
	if err != nil {
		return "", fmt.Errorf("governance: parse %s: %w", relPath, err)
	}
	if doc.ID == "" {
		return "", fmt.Errorf("governance: %s: missing id in frontmatter", relPath)
	}
	return doc.ID, nil
}

// relativize normalizes a source-path citation to root-relative form.
// Citations from search/GoT are already root-relative; this also accepts an
// absolute path, returning "" if it doesn't actually resolve inside Root.
func (g *Governance) relativize(p string) string {
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(p)
	}
	rootAbs, err := filepath.Abs(g.Root)
	if err != nil {
		return ""
	}
	pAbs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(rootAbs, pAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}
