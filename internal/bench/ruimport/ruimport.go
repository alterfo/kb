package ruimport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	reFenceOpen   = regexp.MustCompile("^\\s*(```|~~~)")
	reImage       = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reLink        = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	reHTMLComment = regexp.MustCompile(`<!--.*?-->`)
	reHTMLTag     = regexp.MustCompile(`<[^>]+>`)
	reHeading     = regexp.MustCompile(`^#{1,6}\s*`)
	reBlockquote  = regexp.MustCompile(`^>\s?`)
	reListBullet  = regexp.MustCompile(`^\s*[-*+]\s+`)
	reListNum     = regexp.MustCompile(`^\s*\d+[.)]\s+`)
)

type Options struct {
	SourceType string
	Root       string
	OutDir     string
	Sections   []string
	Limit      int
}

type Result struct {
	Imported int
	Skipped  int
	Warnings []string
}

func Import(opts Options) (Result, error) {
	var res Result
	allowed := make(map[string]bool, len(opts.Sections))
	for _, s := range opts.Sections {
		allowed[s] = true
	}
	err := filepath.WalkDir(opts.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(opts.Root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		parts := strings.Split(rel, "/")
		if len(parts) != 3 || filepath.Base(path) != "index.md" {
			return nil
		}
		if len(allowed) > 0 && !allowed[parts[0]] {
			return nil
		}
		if opts.Limit > 0 && res.Imported >= opts.Limit {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: read: %v", rel, readErr))
			res.Skipped++
			return nil
		}
		title, body := frontmatterTitle(string(data))
		text := MarkdownToText(body)
		if strings.TrimSpace(title) == "" {
			title = firstHeading(body)
		}
		if strings.TrimSpace(title) == "" {
			title = sanitizeSlug(SlugFromSourcePath(rel))
		}
		if strings.TrimSpace(text) == "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: empty body, skipped", rel))
			res.Skipped++
			return nil
		}
		id := DocIDFromSourcePath(rel)
		slug := sanitizeSlug(SlugFromSourcePath(rel))
		if slug == "" {
			slug = id
		}
		if err := writeDoc(opts.OutDir, opts.SourceType, id, slug, title, text); err != nil {
			return err
		}
		res.Imported++
		return nil
	})
	if err != nil {
		return res, err
	}
	if res.Imported == 0 {
		return res, fmt.Errorf("ruimport: no documents imported from %s", opts.Root)
	}
	return res, nil
}

func DocIDFromSourcePath(rel string) string {
	sum := sha256.Sum256([]byte(filepath.ToSlash(rel)))
	return "dsid_ru" + hex.EncodeToString(sum[:])[:10]
}

func SlugFromSourcePath(rel string) string {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	dir := filepath.Dir(rel)
	if base == "index.md" {
		return filepath.Base(dir)
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func MarkdownToText(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		if reFenceOpen.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		line = reHTMLComment.ReplaceAllString(line, "")
		line = reImage.ReplaceAllString(line, "")
		line = reLink.ReplaceAllString(line, "$1")
		line = reHTMLTag.ReplaceAllString(line, "")
		line = reHeading.ReplaceAllString(line, "")
		line = reBlockquote.ReplaceAllString(line, "")
		line = reListBullet.ReplaceAllString(line, "")
		line = reListNum.ReplaceAllString(line, "")
		line = strings.ReplaceAll(line, "`", "")
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "__", "")
		line = strings.ReplaceAll(line, "*", "")
		line = strings.ReplaceAll(line, "_", "")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func frontmatterTitle(src string) (string, string) {
	meta, body, ok := splitFrontmatter(src)
	if !ok {
		return "", src
	}
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(meta), &fm); err != nil {
		return "", body
	}
	title, _ := fm["title"].(string)
	return strings.TrimSpace(title), body
}

func splitFrontmatter(src string) (string, string, bool) {
	lines := strings.Split(src, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return "", src, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", src, false
}

func firstHeading(src string) string {
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(t[2:])
		}
	}
	return ""
}

func sanitizeSlug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func writeDoc(outDir, sourceType, id, slug, title, body string) error {
	dir := filepath.Join(outDir, sourceType)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ruimport: create output dir: %w", err)
	}
	path := filepath.Join(dir, id+"__"+slug+".txt")
	content := title + "\n\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("ruimport: write %s: %w", path, err)
	}
	return nil
}
