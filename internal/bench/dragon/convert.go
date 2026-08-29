package dragon

import (
	"strconv"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/connector"
)

const (
	SourceType   = "dragon"
	QuestionType = "dragon"
	Language     = "ru"
)

func ToDocuments(texts []Text) []connector.Document {
	docs := make([]connector.Document, 0, len(texts))
	for _, t := range texts {
		docs = append(docs, connector.Document{
			ID:     strconv.Itoa(t.ID),
			Source: SourceType,
			Title:  "DRAGON #" + strconv.Itoa(t.ID),
			Body:   t.Text,
			Frontmatter: map[string]any{
				"language": Language,
			},
		})
	}
	return docs
}

func ToQuestions(questions []Question) []corpus.Question {
	qs := make([]corpus.Question, 0, len(questions))
	for _, q := range questions {
		qs = append(qs, corpus.Question{
			ID:       strconv.Itoa(q.ID),
			Type:     QuestionType,
			Text:     q.Question,
			Language: Language,
		})
	}
	return qs
}
