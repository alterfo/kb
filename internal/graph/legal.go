package graph

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
)

// Chunk kind metadata values set by the indexer for legal documents.
const (
	KindLegalArticle     = "legal-article"
	KindLegalPlenum      = "legal-plenum"
	KindLegalPlenumPoint = "legal-plenum-point"
)

// Redaction is one amendment to a legal article: the federal law (FZ) and
// the date its redaction came into force. Mirrors the deterministic parse
// in internal/importer/legalru.
type Redaction struct {
	Date time.Time
	FZ   string
}

// LegalExtractor runs domain-specific LLM extraction for legal documents:
// articles of a code (terms, institutions, cross-references) and Plenum
// (Верховный Суд РФ) clarifications (which articles/parts are explained).
// Same fail-open contract as Extractor: on transport error, empty input or
// unparseable response it returns a zero Extraction and nil error.
type LegalExtractor struct {
	Chat  ChatClient
	Model string
}

func NewLegalExtractor(chat ChatClient, model string) *LegalExtractor {
	return &LegalExtractor{Chat: chat, Model: model}
}

const legalArticleSystemPrompt = `You extract a knowledge graph from a Russian legal article (статья кодекса РФ). ` +
	`Ignore amendment markers ("в редакции Федерального закона ...") — extract only the legal substance. ` +
	`Respond with JSON: {"entities":[{"name":"","type":"","description":""}],"relations":[{"source":"","target":"","type":"","description":"","valid_from":"","valid_to":""}]}. ` +
	`Entity types: "термин" (defined or used legal term), "институт" (legal institution), "орган" (state body), "статья" (referenced article). ` +
	`Relation types: "refers_to" (cross-reference to another article/code), "defines" (this article defines a term), "interprets" (this article interprets a term or institution). ` +
	`Relations may carry optional "valid_from"/"valid_to" (ISO date) fields. ` +
	`"source"/"target" must reference entity names listed in "entities". No prose, no markdown fences.`

const legalPlenumSystemPrompt = `You extract a knowledge graph from a point (пункт) of a Постановление Пленума Верховного Суда РФ. ` +
	`Extract which articles/parts (статьи/части) of the code this point clarifies. ` +
	`Respond with JSON: {"entities":[{"name":"","type":"","description":""}],"relations":[{"source":"","target":"","type":"","description":""}]}. ` +
	`Entity types: "пункт" (the resolution point), "статья" (the clarified article), "термин", "институт". ` +
	`Relation types: "interprets", "clarifies" — from the resolution point to the clarified article. ` +
	`Relations must NOT include valid_from/valid_to — a clarification has no expiry. ` +
	`"source"/"target" must reference entity names listed in "entities". No prose, no markdown fences.`

// ExtractArticle runs the legal-article extraction prompt on the article
// body. Fail-open: any error yields a zero Extraction.
func (e *LegalExtractor) ExtractArticle(ctx context.Context, text string) (Extraction, error) {
	return e.extract(ctx, legalArticleSystemPrompt, text)
}

// ExtractPlenum runs the Plenum-clarification extraction prompt on a
// resolution point. Fail-open: any error yields a zero Extraction.
func (e *LegalExtractor) ExtractPlenum(ctx context.Context, text string) (Extraction, error) {
	return e.extract(ctx, legalPlenumSystemPrompt, text)
}

func (e *LegalExtractor) extract(ctx context.Context, systemPrompt, text string) (Extraction, error) {
	if e == nil || e.Chat == nil || strings.TrimSpace(text) == "" {
		return Extraction{}, nil
	}
	resp, err := e.Chat.Chat(ctx, llm.ChatRequest{
		Model: e.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: text},
		},
	})
	if err != nil {
		return Extraction{}, nil
	}
	result, ok := parseExtraction(resp.Content)
	if !ok {
		return Extraction{}, nil
	}
	return result, nil
}

// BuildLegalArticleEntity deterministically creates the anchor entity of a
// legal article — the article node itself — for any legal-article chunk,
// with or without amendment history. It needs no LLM; the metadata is the
// frontmatter carried into chunk metadata by the indexer. Returns ok=false
// when the metadata does not describe a legal article.
func BuildLegalArticleEntity(metadata map[string]string) (graphstore.Entity, bool) {
	articleID := metadata["id"]
	if articleID == "" {
		articleID = reconstructArticleID(metadata)
	}
	if articleID == "" {
		return graphstore.Entity{}, false
	}
	return graphstore.Entity{
		ID:          EntityID(articleID, "legal-article"),
		Name:        articleDisplayName(metadata),
		Type:        "legal-article",
		Description: "Статья кодекса " + articleID,
	}, true
}

func LegalPlenumPointName(id string, metadata map[string]string) string {
	base := id
	if i := strings.LastIndex(id, "/"); i >= 0 {
		base = id[i+1:]
	}
	num := strings.TrimPrefix(base, "п")
	if num == base {
		num = ""
	}
	date := metadata["resolution_date"]
	if date != "" {
		if t, err := time.Parse("2006-01-02", date); err == nil {
			date = t.Format("02.01.2006")
		}
	}
	resNum := metadata["resolution_number"]
	switch {
	case num != "" && date != "" && resNum != "":
		return fmt.Sprintf("Пункт %s Постановления Пленума ВС РФ от %s N %s", num, date, resNum)
	case num != "":
		return "Пункт " + num
	default:
		return "Пункт " + id
	}
}

func BuildLegalPlenumPointEntity(metadata map[string]string) (graphstore.Entity, bool) {
	id := metadata["id"]
	if id == "" {
		return graphstore.Entity{}, false
	}
	return graphstore.Entity{
		ID:          EntityID(id, KindLegalPlenumPoint),
		Name:        LegalPlenumPointName(id, metadata),
		Type:        KindLegalPlenumPoint,
		Description: "Пункт постановления Пленума ВС РФ " + id,
	}, true
}

// BuildLegalArticleContribution deterministically creates the anchor nodes
// of a legal article with an amendment history — the article entity, one
// Action entity per amendment law, and one AMENDS edge per redaction with
// bi-temporal validity (Task 1): valid_from = the amendment date, valid_to
// = the next amendment's date, or nil for the current redaction. It needs
// no LLM; the metadata is the frontmatter carried into chunk metadata by
// the indexer. Returns ok=false when the metadata does not describe a legal
// article with redactions.
func BuildLegalArticleContribution(metadata map[string]string) ([]graphstore.Entity, []graphstore.Relation, bool) {
	article, ok := BuildLegalArticleEntity(metadata)
	if !ok {
		return nil, nil, false
	}
	redactions := ParseRedactionsMetadata(metadata["redactions"])
	if len(redactions) == 0 {
		return nil, nil, false
	}
	sort.SliceStable(redactions, func(i, j int) bool { return redactions[i].Date.Before(redactions[j].Date) })

	entities := make([]graphstore.Entity, 0, 1+len(redactions))
	entities = append(entities, article)
	relations := make([]graphstore.Relation, 0, len(redactions))
	articleID := metadata["id"]
	if articleID == "" {
		articleID = reconstructArticleID(metadata)
	}

	for i, r := range redactions {
		actionName := fmt.Sprintf("Федеральный закон от %s N %s", r.Date.Format("02.01.2006"), r.FZ)
		action := graphstore.Entity{
			ID:          EntityID(actionName, "legal-amendment"),
			Name:        actionName,
			Type:        "legal-amendment",
			Description: "Поправка к " + articleID,
		}
		entities = append(entities, action)

		validFrom := r.Date
		rel := graphstore.Relation{
			ID:          RelationID(action.ID, article.ID, "amends"),
			Src:         action.ID,
			Dst:         article.ID,
			Type:        "amends",
			Description: "Вносит редакцию от " + r.Date.Format("02.01.2006") + " N " + r.FZ,
			Weight:      1,
			ValidFrom:   &validFrom,
			CreatedAt:   time.Now(),
		}
		if i+1 < len(redactions) {
			next := redactions[i+1].Date
			rel.ValidTo = &next
		}
		relations = append(relations, rel)
	}
	return entities, relations, true
}

func articleDisplayName(metadata map[string]string) string {
	name := "Статья " + metadata["article_number"]
	if title := metadata["article_title"]; title != "" {
		name += ". " + title
	}
	return name
}

// reconstructArticleID rebuilds the canonical article id from frontmatter
// fields, mirroring internal/importer/legalru.Article.ID.
func reconstructArticleID(metadata map[string]string) string {
	code := metadata["code"]
	number := metadata["article_number"]
	if code == "" || number == "" {
		return ""
	}
	parts := []string{code}
	if p := metadata["part"]; p != "" {
		parts = append(parts, "ч"+p)
	}
	if s := metadata["section"]; s != "" {
		parts = append(parts, "р"+s)
	}
	if ch := metadata["chapter"]; ch != "" {
		parts = append(parts, "гл"+ch)
	}
	parts = append(parts, "ст"+number)
	return strings.Join(parts, "/")
}

// ParseRedactionsMetadata parses the frontmatter "redactions" value: Go's
// fmt.Sprint of a []string ("[2012-12-30:302-ФЗ 2015-03-08:42-ФЗ]") or a
// plain "date:FZ" list separated by whitespace. Malformed entries are
// skipped (fail-open).
func ParseRedactionsMetadata(v string) []Redaction {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return nil
	}
	out := make([]Redaction, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		dateStr, fz, ok := strings.Cut(f, ":")
		if !ok || fz == "" {
			continue
		}
		d, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		key := d.Format("2006-01-02") + ":" + fz
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, Redaction{Date: d, FZ: fz})
	}
	return out
}
