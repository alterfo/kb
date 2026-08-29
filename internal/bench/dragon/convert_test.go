package dragon

import (
	"testing"
)

func TestToDocuments(t *testing.T) {
	texts := []Text{
		{ID: 0, Text: "первый текст"},
		{ID: 41, Text: "сорок второй"},
	}
	docs := ToDocuments(texts)
	if len(docs) != 2 {
		t.Fatalf("len(docs) = %d, want 2", len(docs))
	}
	if docs[0].ID != "0" || docs[0].Source != SourceType || docs[0].Body != "первый текст" {
		t.Errorf("docs[0] = %+v", docs[0])
	}
	if docs[1].ID != "41" || docs[1].Body != "сорок второй" {
		t.Errorf("docs[1] = %+v", docs[1])
	}
	if lang, _ := docs[0].Frontmatter["language"].(string); lang != Language {
		t.Errorf("docs[0].Frontmatter[language] = %q, want %q", lang, Language)
	}
}

func TestToDocuments_Empty(t *testing.T) {
	if docs := ToDocuments(nil); len(docs) != 0 {
		t.Fatalf("ToDocuments(nil) = %v, want empty", docs)
	}
}

func TestToQuestions(t *testing.T) {
	questions := []Question{
		{ID: 0, Question: "Какой регион?"},
		{ID: 7, Question: "Кто победил?"},
	}
	qs := ToQuestions(questions)
	if len(qs) != 2 {
		t.Fatalf("len(qs) = %d, want 2", len(qs))
	}
	if qs[0].ID != "0" || qs[0].Text != "Какой регион?" || qs[0].Type != QuestionType || qs[0].Language != Language {
		t.Errorf("qs[0] = %+v", qs[0])
	}
	if qs[1].ID != "7" || qs[1].Text != "Кто победил?" {
		t.Errorf("qs[1] = %+v", qs[1])
	}
}

func TestToQuestions_Empty(t *testing.T) {
	if qs := ToQuestions(nil); len(qs) != 0 {
		t.Fatalf("ToQuestions(nil) = %v, want empty", qs)
	}
}
