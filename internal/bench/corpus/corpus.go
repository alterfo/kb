package corpus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
)

const maxLineSize = 10 * 1024 * 1024

type Doc struct {
	ID         string
	SourceType string
	RelPath    string
	FileName   string
	Title      string
	Body       string
	Meta       map[string]any
	Noise      bool
}

type Question struct {
	ID             string   `json:"question_id"`
	Type           string   `json:"question_type"`
	SourceTypes    []string `json:"source_types"`
	Text           string   `json:"question"`
	ExpectedDocIDs []string `json:"expected_doc_ids"`
	GoldAnswer     string   `json:"gold_answer"`
	AnswerFacts    []string `json:"answer_facts"`
	Language       string   `json:"language"`
}

var reservedMetaKeys = map[string]struct{}{
	"dataset_doc_uuid":       {},
	"title_field_name":       {},
	"content_field_names":    {},
	"dataset_noise_document": {},
}

func LoadQuestions(path string) ([]Question, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("corpus: open questions: %w", err)
	}
	defer f.Close()

	var (
		qs      []Question
		warns   []string
		scanner = bufio.NewScanner(f)
	)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\uFEFF"))
		if line == "" {
			continue
		}
		var q Question
		if err := json.Unmarshal([]byte(line), &q); err != nil {
			warns = append(warns, fmt.Sprintf("questions line %d: %v", lineNo, err))
			continue
		}
		if q.ID == "" || strings.TrimSpace(q.Text) == "" {
			warns = append(warns, fmt.Sprintf("questions line %d: missing question_id or question", lineNo))
			continue
		}
		qs = append(qs, q)
	}
	if err := scanner.Err(); err != nil {
		return nil, warns, fmt.Errorf("corpus: scan questions: %w", err)
	}
	if len(qs) == 0 {
		return nil, warns, fmt.Errorf("corpus: no questions loaded from %s", path)
	}
	return qs, warns, nil
}

func LoadCorpus(root string) ([]Doc, []string, error) {
	var (
		docs  []Doc
		warns []string
	)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".txt":
			doc, w := parseTXT(root, rel, path)
			if w != "" {
				warns = append(warns, w)
			}
			if doc != nil {
				docs = append(docs, *doc)
			}
		case ".json":
			doc, w := parseJSON(root, rel, path)
			if w != "" {
				warns = append(warns, w)
			}
			if doc != nil {
				docs = append(docs, *doc)
			}
		default:
			warns = append(warns, fmt.Sprintf("%s: unsupported extension, skipped", rel))
		}
		return nil
	})
	if err != nil {
		return nil, warns, fmt.Errorf("corpus: walk %s: %w", root, err)
	}
	if len(docs) == 0 {
		return nil, warns, fmt.Errorf("corpus: no documents found under %s", root)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].RelPath < docs[j].RelPath })
	return docs, warns, nil
}

func parseTXT(root, rel, path string) (*Doc, string) {
	if sourceType(rel) == "" {
		return nil, fmt.Sprintf("%s: file outside a source directory, skipped", rel)
	}
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	idx := strings.Index(name, "__")
	if idx <= 0 || !strings.HasPrefix(name[:idx], "dsid_") {
		return nil, fmt.Sprintf("%s: filename does not match {dsid__semantic}.txt, skipped", rel)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Sprintf("%s: read: %v", rel, err)
	}
	content := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	title := content
	body := ""
	if i := strings.Index(content, "\n"); i >= 0 {
		title = strings.TrimSpace(content[:i])
		body = strings.TrimLeft(content[i+1:], "\n")
		body = strings.TrimRight(body, "\n")
	} else {
		title = strings.TrimSpace(content)
	}
	if title == "" {
		return nil, fmt.Sprintf("%s: empty title, skipped", rel)
	}
	return &Doc{
		ID:         name[:idx],
		SourceType: sourceType(rel),
		RelPath:    rel,
		FileName:   base,
		Title:      title,
		Body:       body,
	}, ""
}

func parseJSON(root, rel, path string) (*Doc, string) {
	if sourceType(rel) == "" {
		return nil, fmt.Sprintf("%s: file outside a source directory, skipped", rel)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Sprintf("%s: read: %v", rel, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Sprintf("%s: invalid json: %v", rel, err)
	}
	id, _ := raw["dataset_doc_uuid"].(string)
	if id == "" {
		return nil, fmt.Sprintf("%s: missing dataset_doc_uuid, skipped", rel)
	}
	titleField, _ := raw["title_field_name"].(string)
	if titleField == "" {
		return nil, fmt.Sprintf("%s: missing title_field_name, skipped", rel)
	}
	titleVal, ok := raw[titleField]
	if !ok {
		return nil, fmt.Sprintf("%s: title field %q not found, skipped", rel, titleField)
	}
	fieldsRaw, ok := raw["content_field_names"].([]any)
	if !ok || len(fieldsRaw) == 0 {
		return nil, fmt.Sprintf("%s: missing content_field_names, skipped", rel)
	}
	var parts []string
	for _, fr := range fieldsRaw {
		field, ok := fr.(string)
		if !ok {
			return nil, fmt.Sprintf("%s: non-string content field name, skipped", rel)
		}
		v, ok := raw[field]
		if !ok {
			return nil, fmt.Sprintf("%s: content field %q not found, skipped", rel, field)
		}
		parts = append(parts, joinAny(v))
	}
	meta := make(map[string]any, len(raw))
	for k, v := range raw {
		if _, reserved := reservedMetaKeys[k]; reserved {
			continue
		}
		meta[k] = v
	}
	return &Doc{
		ID:         id,
		SourceType: sourceType(rel),
		RelPath:    rel,
		FileName:   filepath.Base(path),
		Title:      anyToString(titleVal),
		Body:       strings.Join(parts, "\n"),
		Meta:       meta,
		Noise:      truthy(raw["dataset_noise_document"]),
	}, ""
}

func sourceType(rel string) string {
	rel = filepath.ToSlash(rel)
	if i := strings.Index(rel, "/"); i > 0 {
		return rel[:i]
	}
	return ""
}

func joinAny(v any) string {
	switch t := v.(type) {
	case []any:
		items := make([]string, len(t))
		for i, item := range t {
			items[i] = anyToString(item)
		}
		return strings.Join(items, "\n")
	default:
		return anyToString(v)
	}
}

func anyToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case []any:
		return joinAny(t)
	default:
		return fmt.Sprint(t)
	}
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	default:
		return false
	}
}

var benchTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02",
	"2006-01-02 15:04:05.999999999 -0700 MST",
}

func benchTime(v any) time.Time {
	s, ok := v.(string)
	if !ok {
		return time.Time{}
	}
	for _, layout := range benchTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (d Doc) ToDocument() connector.Document {
	updatedAt := time.Time{}
	if v, ok := d.Meta["last_updated"]; ok {
		updatedAt = benchTime(v)
	}
	if updatedAt.IsZero() {
		if v, ok := d.Meta["created_at"]; ok {
			updatedAt = benchTime(v)
		}
	}
	return connector.Document{
		ID:          d.ID,
		Source:      d.SourceType,
		Title:       d.Title,
		UpdatedAt:   updatedAt,
		Body:        d.Body,
		Frontmatter: d.Meta,
	}
}
