package legaleval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type QAPair struct {
	Question             string   `json:"question"`
	ExpectedArticles     []string `json:"expected_articles"`
	ExpectedPlenumPoints []string `json:"expected_plenum_points"`
	Justification        string   `json:"justification"`
}

func ParseQAPairs(r io.Reader) ([]QAPair, error) {
	var pairs []QAPair
	if err := json.NewDecoder(r).Decode(&pairs); err != nil {
		return nil, fmt.Errorf("legaleval: parse qa pairs: %w", err)
	}
	for i, p := range pairs {
		if strings.TrimSpace(p.Question) == "" {
			return nil, fmt.Errorf("legaleval: qa pair %d: empty question", i)
		}
	}
	return pairs, nil
}

func LoadQAPairs(path string) ([]QAPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("legaleval: open %s: %w", path, err)
	}
	defer f.Close()
	return ParseQAPairs(f)
}
