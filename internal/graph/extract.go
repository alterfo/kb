package graph

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/llm"
)

// ChatClient runs a single chat completion. Satisfied by *llm.Client.
type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

type RawEntity struct {
	Name        string
	Type        string
	Description string
}

type RawRelation struct {
	Source          string
	Target          string
	Type            string
	Description     string
	ValidFrom       *time.Time
	ValidTo         *time.Time
	NoConflictClose bool
}

type Extraction struct {
	Entities  []RawEntity
	Relations []RawRelation
}

const extractionSystemPrompt = `You extract a knowledge graph from a text chunk. ` +
	`Respond with JSON: {"entities":[{"name":"","type":"","description":""}],"relations":[{"source":"","target":"","type":"","description":""}]}. ` +
	`"source"/"target" must reference entity names listed in "entities". No prose, no markdown fences.`

const gleaningPrompt = `Some entities or relations may have been missed. ` +
	`Respond with the same JSON schema containing ONLY the additional entities/relations not already listed. ` +
	`Use empty arrays if there is nothing to add.`

// Extractor runs per-chunk LLM entity/relation extraction. Fail-open: on any
// transport error or unparseable response it returns a zero Extraction and a
// nil error, so callers never need special-case handling.
type Extractor struct {
	Chat     ChatClient
	Model    string
	Gleaning bool
}

func NewExtractor(chat ChatClient, model string) *Extractor {
	return &Extractor{Chat: chat, Model: model}
}

func (e *Extractor) ExtractChunk(ctx context.Context, text string) (Extraction, error) {
	if e == nil || e.Chat == nil || strings.TrimSpace(text) == "" {
		return Extraction{}, nil
	}

	messages := []llm.ChatMessage{
		{Role: "system", Content: extractionSystemPrompt},
		{Role: "user", Content: text},
	}
	resp, err := e.Chat.Chat(ctx, llm.ChatRequest{Model: e.Model, Messages: messages})
	if err != nil {
		return Extraction{}, nil
	}
	result, ok := parseExtraction(resp.Content)
	if !ok {
		return Extraction{}, nil
	}

	if e.Gleaning {
		messages = append(messages,
			llm.ChatMessage{Role: "assistant", Content: resp.Content},
			llm.ChatMessage{Role: "user", Content: gleaningPrompt},
		)
		resp2, err := e.Chat.Chat(ctx, llm.ChatRequest{Model: e.Model, Messages: messages})
		if err == nil {
			if extra, ok := parseExtraction(resp2.Content); ok {
				result.Entities = append(result.Entities, extra.Entities...)
				result.Relations = append(result.Relations, extra.Relations...)
			}
		}
	}

	return result, nil
}

type extractionJSON struct {
	Entities []struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
	} `json:"entities"`
	Relations []struct {
		Source      string `json:"source"`
		Target      string `json:"target"`
		Type        string `json:"type"`
		Description string `json:"description"`
		ValidFrom   string `json:"valid_from"`
		ValidTo     string `json:"valid_to"`
	} `json:"relations"`
}

func parseExtraction(content string) (Extraction, bool) {
	content = stripCodeFence(content)
	content = stripTrailingCommas(content)

	var parsed extractionJSON
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return Extraction{}, false
	}

	out := Extraction{}
	for _, e := range parsed.Entities {
		out.Entities = append(out.Entities, RawEntity{Name: e.Name, Type: e.Type, Description: e.Description})
	}
	for _, r := range parsed.Relations {
		out.Relations = append(out.Relations, RawRelation{
			Source:      r.Source,
			Target:      r.Target,
			Type:        r.Type,
			Description: r.Description,
			ValidFrom:   parseTimeField(r.ValidFrom),
			ValidTo:     parseTimeField(r.ValidTo),
		})
	}
	return out, true
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

var trailingCommaRe = regexp.MustCompile(`,(\s*[}\]])`)

func stripTrailingCommas(s string) string {
	return trailingCommaRe.ReplaceAllString(s, "$1")
}

// parseTimeField parses an optional ISO date or RFC3339 timestamp from the
// extraction JSON. Empty or malformed values yield nil (fail-open).
func parseTimeField(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
