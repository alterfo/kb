package verify

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/alterfo/kb/internal/llm"
)

type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

type Contradiction struct {
	ChunkA string `json:"chunk_a"`
	ChunkB string `json:"chunk_b"`
	Reason string `json:"reason"`
}

type ContradictionReport struct {
	Contradictions []Contradiction
}

func (r ContradictionReport) HasContradictions() bool {
	return len(r.Contradictions) > 0
}

const contradictionSystemPrompt = `You look for explicit factual contradictions between the provided excerpts. ` +
	`Respond with a JSON array of objects with fields chunk_a, chunk_b and reason, ` +
	`using the excerpt labels exactly as given. Return an empty array if there are no contradictions. ` +
	`No prose, no markdown fences.`

type ContradictionDetector struct {
	chat  ChatClient
	model string
}

func NewContradictionDetector(chat ChatClient, model string) *ContradictionDetector {
	return &ContradictionDetector{chat: chat, model: model}
}

func (d *ContradictionDetector) Detect(ctx context.Context, query string, chunks []Chunk) (ContradictionReport, error) {
	rep := ContradictionReport{}
	if d.chat == nil || len(chunks) < 2 {
		return rep, nil
	}
	resp, err := d.chat.Chat(ctx, llm.ChatRequest{
		Model: d.model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: contradictionSystemPrompt},
			{Role: "user", Content: buildContradictionPrompt(query, chunks)},
		},
	})
	if err != nil {
		return rep, err
	}
	contradictions, err := parseContradictions(resp.Content)
	if err != nil {
		return rep, err
	}
	rep.Contradictions = contradictions
	return rep, nil
}

func buildContradictionPrompt(query string, chunks []Chunk) string {
	var b strings.Builder
	if strings.TrimSpace(query) != "" {
		b.WriteString("Question: " + query + "\n\n")
	}
	b.WriteString("Excerpts:\n")
	for _, c := range chunks {
		label := c.ChunkID
		if label == "" {
			label = c.FileName
		}
		b.WriteString("- [" + label + "] " + c.Text + "\n")
	}
	return b.String()
}

func parseContradictions(content string) ([]Contradiction, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimPrefix(content, "json")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	var out []Contradiction
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, err
	}
	clean := out[:0]
	for _, c := range out {
		c.ChunkA = strings.TrimSpace(c.ChunkA)
		c.ChunkB = strings.TrimSpace(c.ChunkB)
		c.Reason = strings.TrimSpace(c.Reason)
		if c.ChunkA == "" && c.ChunkB == "" {
			continue
		}
		clean = append(clean, c)
	}
	return clean, nil
}
