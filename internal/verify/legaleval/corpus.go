package legaleval

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/importer/legalru"
)

type Redaction struct {
	Date time.Time
	FZ   string
}

type Article struct {
	ID         string
	File       string
	Number     string
	Title      string
	Body       string
	Redactions []Redaction
}

type Corpus struct {
	byID    map[string]*Article
	byFile  map[string][]string
	byChunk map[string]string
	asOf    time.Time
}

func NewCorpus(articles []Article) *Corpus {
	c := &Corpus{
		byID:    make(map[string]*Article, len(articles)),
		byFile:  make(map[string][]string),
		byChunk: make(map[string]string),
	}
	for i := range articles {
		a := articles[i]
		if _, dup := c.byID[a.ID]; dup {
			continue
		}
		c.byID[a.ID] = &a
		if a.File != "" {
			base := filepath.Base(a.File)
			c.byFile[base] = append(c.byFile[base], a.ID)
		}
		for _, r := range a.Redactions {
			if r.Date.After(c.asOf) {
				c.asOf = r.Date
			}
		}
	}
	for _, ids := range c.byFile {
		sort.Strings(ids)
	}
	return c
}

func (c *Corpus) AddChunk(chunkID, articleID string) {
	if chunkID != "" && articleID != "" {
		c.byChunk[chunkID] = articleID
	}
}

func (c *Corpus) AsOf() time.Time {
	return c.asOf
}

func (c *Corpus) Article(id string) (Article, bool) {
	a, ok := c.byID[id]
	if !ok {
		return Article{}, false
	}
	return *a, true
}

func (c *Corpus) CurrentAt(id string, t time.Time) bool {
	a, ok := c.byID[id]
	if !ok {
		return false
	}
	if len(a.Redactions) == 0 {
		return true
	}
	for _, r := range a.Redactions {
		if !r.Date.After(t) {
			return true
		}
	}
	return false
}

func (c *Corpus) Resolve(cit Citation) (string, bool) {
	if cit.ChunkID != "" {
		if id, ok := c.byChunk[cit.ChunkID]; ok {
			return id, true
		}
	}
	base := filepath.Base(cit.FileName)
	if base == "." || base == "/" || base == "" {
		base = filepath.Base(cit.FilePath)
	}
	ids := c.byFile[base]
	if len(ids) == 1 {
		return ids[0], true
	}
	return "", false
}

func LoadCorpus(files ...string) (*Corpus, error) {
	imp := legalru.New()
	var articles []Article
	for _, path := range files {
		docs, err := imp.Import(path)
		if err != nil {
			return nil, fmt.Errorf("legaleval: corpus %s: %w", path, err)
		}
		for _, d := range docs {
			articles = append(articles, articleFromDocument(path, d))
		}
	}
	return NewCorpus(articles), nil
}

func articleFromDocument(file string, d connector.Document) Article {
	a := Article{
		ID:     d.ID,
		File:   file,
		Number: frontmatterString(d.Frontmatter, "article_number"),
		Title:  frontmatterString(d.Frontmatter, "article_title"),
		Body:   d.Body,
	}
	reds, _ := d.Frontmatter["redactions"].([]string)
	for _, r := range reds {
		dateStr, fz, ok := strings.Cut(r, ":")
		if !ok {
			continue
		}
		date, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		a.Redactions = append(a.Redactions, Redaction{Date: date, FZ: fz})
	}
	return a
}

func frontmatterString(fm map[string]any, key string) string {
	v, ok := fm[key]
	if !ok {
		return ""
	}
	return fmt.Sprint(v)
}
