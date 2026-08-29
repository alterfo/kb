package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/render"
)

const (
	// EmptyBodyMaxChars is the body-length threshold (frontmatter stripped)
	// below which a document is offered for deletion as near-empty.
	EmptyBodyMaxChars = 80
	// CompressMinBytes is the file-size threshold above which a rewritable
	// document is offered for compression.
	CompressMinBytes = 12_000
)

// DocRecord identifies one scanned document.
type DocRecord struct {
	Path    string // root-relative, forward slashes
	Source  string
	Size    int64
	ModTime time.Time
}

// DuplicateGroup is a set of byte-identical (frontmatter-stripped) bodies;
// Keep is the newest, Trash the rest.
type DuplicateGroup struct {
	Keep  DocRecord
	Trash []DocRecord
}

// MergeGroup is 2+ files sharing a normalized name stem, candidates for a
// same-topic merge.
type MergeGroup struct {
	Stem  string
	Paths []string
}

// Plan is the result of a mechanical (no network, no LLM) cleanup Scan.
type Plan struct {
	Duplicates []DuplicateGroup
	Empty      []DocRecord
	Merge      []MergeGroup
	Compress   []DocRecord
}

// Counts summarizes Plan for display: duplicate files that would be
// trashed, empty files, merge groups, and compress candidates.
func (p Plan) Counts() (duplicates, empty, merge, compress int) {
	for _, g := range p.Duplicates {
		duplicates += len(g.Trash)
	}
	return duplicates, len(p.Empty), len(p.Merge), len(p.Compress)
}

// ScanOptions controls which source directories participate in a Scan.
type ScanOptions struct {
	// ExcludeFromDelete lists source names skipped for duplicate/empty
	// detection (e.g. generated reports — regenerated, trashing is
	// pointless).
	ExcludeFromDelete map[string]bool
	// RewriteSources lists source names eligible for merge/compress
	// candidate detection. Content rewriting is restricted to sources the
	// KB itself owns (default: "notes") — every other source is synced
	// from an external system and would be silently overwritten by the
	// next incremental sync.
	RewriteSources map[string]bool
}

func DefaultScanOptions() ScanOptions {
	return ScanOptions{
		ExcludeFromDelete: map[string]bool{"reports": true},
		RewriteSources:    map[string]bool{"notes": true},
	}
}

var (
	stemNoiseRe  = regexp.MustCompile(`(?i)(-\d+|[-_ ]\d{4}-\d{2}-\d{2}|[-_ ](copy|копия|final|draft|old|new|v\d+))+$`)
	stemSpacesRe = regexp.MustCompile(`[-_\s]+`)
)

func normalizedStem(relPath string) string {
	stem := strings.ToLower(strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath)))
	stem = stemNoiseRe.ReplaceAllString(stem, "")
	stem = stemSpacesRe.ReplaceAllString(stem, "-")
	return strings.Trim(stem, "-")
}

// Scan walks root for markdown documents (mirrors the Indexer's tree walk:
// hidden directories, e.g. .trash, are skipped) and builds a mechanical
// cleanup plan. Duplicate/empty detection covers every source except
// ExcludeFromDelete; merge/compress detection is further restricted to
// RewriteSources. Files already offered as an exact duplicate are excluded
// from merge/compress (the duplicate trash already removes them).
func Scan(root string, opt ScanOptions) (Plan, error) {
	if opt.ExcludeFromDelete == nil && opt.RewriteSources == nil {
		opt = DefaultScanOptions()
	}

	byHash := map[string][]DocRecord{}
	var empty []DocRecord
	byStem := map[string][]DocRecord{}
	var compressCandidates []DocRecord

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		source := engine.InferSource(rel)

		info, err := d.Info()
		if err != nil {
			return err
		}
		rec := DocRecord{Path: rel, Source: source, Size: info.Size(), ModTime: info.ModTime()}

		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil // unreadable file is not a cleanup candidate
		}
		body := string(data)
		if doc, parseErr := render.Parse(data); parseErr == nil {
			body = doc.Body
		}
		body = strings.TrimSpace(body)

		if !opt.ExcludeFromDelete[source] {
			if len(body) <= EmptyBodyMaxChars {
				empty = append(empty, rec)
			} else {
				sum := sha256.Sum256([]byte(body))
				digest := hex.EncodeToString(sum[:])
				byHash[digest] = append(byHash[digest], rec)
			}
		}

		if opt.RewriteSources[source] {
			if stem := normalizedStem(rel); stem != "" {
				byStem[stem] = append(byStem[stem], rec)
			}
			if rec.Size >= CompressMinBytes {
				compressCandidates = append(compressCandidates, rec)
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return Plan{}, err
	}

	duplicates, dupTrashPaths := buildDuplicates(byHash)
	merge := buildMergeGroups(byStem, dupTrashPaths)
	compress := buildCompressCandidates(compressCandidates, dupTrashPaths, merge)

	return Plan{Duplicates: duplicates, Empty: empty, Merge: merge, Compress: compress}, nil
}

func buildDuplicates(byHash map[string][]DocRecord) ([]DuplicateGroup, map[string]bool) {
	dupTrashPaths := map[string]bool{}
	var duplicates []DuplicateGroup
	for _, recs := range byHash {
		if len(recs) < 2 {
			continue
		}
		sort.SliceStable(recs, func(i, j int) bool { return recs[i].ModTime.After(recs[j].ModTime) })
		keep, rest := recs[0], recs[1:]
		for _, r := range rest {
			dupTrashPaths[r.Path] = true
		}
		duplicates = append(duplicates, DuplicateGroup{Keep: keep, Trash: rest})
	}
	sort.Slice(duplicates, func(i, j int) bool { return duplicates[i].Keep.Path < duplicates[j].Keep.Path })
	return duplicates, dupTrashPaths
}

func buildMergeGroups(byStem map[string][]DocRecord, dupTrashPaths map[string]bool) []MergeGroup {
	var merge []MergeGroup
	for stem, recs := range byStem {
		var paths []string
		for _, r := range recs {
			if dupTrashPaths[r.Path] {
				continue // already going away as an exact duplicate
			}
			paths = append(paths, r.Path)
		}
		if len(paths) < 2 {
			continue
		}
		sort.Strings(paths)
		merge = append(merge, MergeGroup{Stem: stem, Paths: paths})
	}
	sort.Slice(merge, func(i, j int) bool { return merge[i].Stem < merge[j].Stem })
	return merge
}

func buildCompressCandidates(candidates []DocRecord, dupTrashPaths map[string]bool, merge []MergeGroup) []DocRecord {
	merged := map[string]bool{}
	for _, g := range merge {
		for _, p := range g.Paths {
			merged[p] = true
		}
	}
	var compress []DocRecord
	for _, r := range candidates {
		if dupTrashPaths[r.Path] || merged[r.Path] {
			continue // merge already rewrites it, or it's going to the trash
		}
		compress = append(compress, r)
	}
	sort.Slice(compress, func(i, j int) bool { return compress[i].Path < compress[j].Path })
	return compress
}
