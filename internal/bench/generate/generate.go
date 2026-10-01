package generate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alterfo/kb/internal/bench/corpus"
	runbench "github.com/alterfo/kb/internal/bench/run"
	"github.com/alterfo/kb/internal/llm"
)

const defaultLanguage = "ru"

const systemPrompt = `Ты — генератор русскоязычных фактологических вопросов для RAG-бенчмарка.
Составь ровно один вопрос по приведённому документу. Вопрос должен быть
single-doc: на него можно ответить, процитировав этот документ.
gold_answer обязан быть дословным фрагментом текста документа (с точностью до
регистра и пунктуации). answer_facts — 1-3 коротких факта, каждый из которых
подтверждается текстом документа.
Верни ТОЛЬКО JSON, без пояснений, в формате:
{"question":"...","question_type":"single-doc","gold_answer":"...","answer_facts":["...","..."]}`

const maxSeedExamples = 3

type generatedQuestion struct {
	Question     string   `json:"question"`
	QuestionType string   `json:"question_type"`
	GoldAnswer   string   `json:"gold_answer"`
	AnswerFacts  []string `json:"answer_facts"`
}

// Generate produces one single-doc question per document by asking the LLM
// for a question, gold answer and answer facts anchored to the document. A
// generated question is dropped when its gold answer is not found verbatim in
// the document body (anti-hallucination gate). Seed questions are used as
// few-shot examples; the first error is returned alongside the partial result.
func Generate(ctx context.Context, chat runbench.ChatClient, model string, docs []corpus.Doc, seed []corpus.Question) ([]corpus.Question, error) {
	examples := selectExamples(seed)
	out := make([]corpus.Question, 0, len(docs))
	var firstErr error
	for _, doc := range docs {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		q, err := generateOne(ctx, chat, model, doc, examples)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if q == nil {
			continue
		}
		out = append(out, *q)
	}
	return out, firstErr
}

func generateOne(ctx context.Context, chat runbench.ChatClient, model string, doc corpus.Doc, examples []corpus.Question) (*corpus.Question, error) {
	user := renderUserPrompt(doc, examples)
	resp, err := chat.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("generate %s: %w", doc.ID, err)
	}
	gen, err := parseGenerated(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("generate %s: %w", doc.ID, err)
	}
	q := corpus.Question{
		ID:             "qgen_" + doc.ID,
		Type:           "single-doc",
		SourceTypes:    []string{doc.SourceType},
		Text:           strings.TrimSpace(gen.Question),
		ExpectedDocIDs: []string{doc.ID},
		GoldAnswer:     strings.TrimSpace(gen.GoldAnswer),
		AnswerFacts:    groundedFacts(nonEmptyFacts(gen.AnswerFacts), doc.Body, defaultLanguage),
		Language:       defaultLanguage,
	}
	if !validQuestion(q, doc) {
		return nil, nil
	}
	return &q, nil
}

func validQuestion(q corpus.Question, doc corpus.Doc) bool {
	if q.Text == "" || q.GoldAnswer == "" {
		return false
	}
	if !runbench.GoldAnswerInText(doc.Body, q.GoldAnswer, defaultLanguage) {
		return false
	}
	return true
}

func nonEmptyFacts(facts []string) []string {
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		if s := strings.TrimSpace(f); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// groundedFacts drops facts not covered by docBody at the scorer's own
// facts-coverage threshold, so a hallucinated fact never reaches the
// generated question even when the gold answer itself passes the gate.
func groundedFacts(facts []string, docBody, lang string) []string {
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		if runbench.FactCoveredInText(docBody, f, lang) {
			out = append(out, f)
		}
	}
	return out
}

func selectExamples(seed []corpus.Question) []corpus.Question {
	out := make([]corpus.Question, 0, maxSeedExamples)
	for _, q := range seed {
		if len(out) >= maxSeedExamples {
			break
		}
		if strings.TrimSpace(q.Text) == "" || strings.TrimSpace(q.GoldAnswer) == "" {
			continue
		}
		out = append(out, q)
	}
	return out
}

func renderUserPrompt(doc corpus.Doc, examples []corpus.Question) string {
	var b strings.Builder
	if len(examples) > 0 {
		b.WriteString("Примеры вопроса и ответа (формат JSON):\n")
		for _, ex := range examples {
			data, _ := json.Marshal(generatedQuestion{
				Question:     ex.Text,
				QuestionType: ex.Type,
				GoldAnswer:   ex.GoldAnswer,
				AnswerFacts:  ex.AnswerFacts,
			})
			b.Write(data)
			b.WriteByte('\n')
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Документ:\n%s\n\n%s\n\nСгенерируй вопрос по этому документу.\n", doc.Title, doc.Body)
	return b.String()
}

func parseGenerated(content string) (generatedQuestion, error) {
	raw := strings.TrimSpace(stripCodeFence(content))
	var gen generatedQuestion
	if err := json.Unmarshal([]byte(raw), &gen); err != nil {
		return generatedQuestion{}, fmt.Errorf("parse generated JSON: %w", err)
	}
	return gen, nil
}

func stripCodeFence(content string) string {
	s := strings.TrimSpace(content)
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "```json") {
		s = strings.TrimSpace(s[len("```json"):])
	} else if strings.HasPrefix(lower, "```") {
		s = strings.TrimSpace(s[len("```"):])
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return s
}
