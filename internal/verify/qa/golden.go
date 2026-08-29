package qa

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alterfo/kb/internal/render"
)

type QAPair struct {
	ID       string `json:"id"`
	Source   string `json:"source,omitempty"`
	Question string `json:"question"`
	Expected string `json:"expected"`
}

func ParseQAPairs(r io.Reader) ([]QAPair, error) {
	var pairs []QAPair
	if err := json.NewDecoder(r).Decode(&pairs); err != nil {
		return nil, fmt.Errorf("qa: parse qa pairs: %w", err)
	}
	for i, p := range pairs {
		if strings.TrimSpace(p.Question) == "" {
			return nil, fmt.Errorf("qa: pair %d: empty question", i)
		}
	}
	return pairs, nil
}

func LoadQAPairs(path string) ([]QAPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("qa: open %s: %w", path, err)
	}
	defer f.Close()
	return ParseQAPairs(f)
}

func WriteQAPairs(path string, pairs []QAPair) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("qa: create pair dir: %w", err)
	}
	data, err := json.MarshalIndent(pairs, "", "  ")
	if err != nil {
		return fmt.Errorf("qa: encode pairs: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("qa: write pairs: %w", err)
	}
	return nil
}

func BuildGoldenFromRoot(root, source string) ([]QAPair, error) {
	var pairs []QAPair
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
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
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		doc, err := render.Parse(data)
		if err != nil {
			return nil
		}
		if doc.Kind != "issue" || frontmatterString(doc.Frontmatter["state"]) != "closed" {
			return nil
		}
		if source != "" && doc.Source != source {
			return nil
		}
		question := strings.TrimSpace(doc.Title)
		expected := strings.TrimSpace(doc.Body)
		if question == "" || expected == "" {
			return nil
		}
		pairs = append(pairs, QAPair{ID: doc.ID, Source: doc.Source, Question: question, Expected: expected})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("qa: build golden set: %w", err)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Source != pairs[j].Source {
			return pairs[i].Source < pairs[j].Source
		}
		return pairs[i].ID < pairs[j].ID
	})
	return pairs, nil
}

func frontmatterString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
