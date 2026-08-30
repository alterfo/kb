package dragon

import (
	"reflect"
	"sort"
	"testing"
)

func TestFlattenTextIDs_Flat(t *testing.T) {
	ids, err := flattenTextIDs("[144]")
	if err != nil {
		t.Fatalf("flattenTextIDs: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"144"}) {
		t.Fatalf("ids = %v, want [144]", ids)
	}
}

func TestFlattenTextIDs_Nested(t *testing.T) {
	ids, err := flattenTextIDs("[[105], [109, 164]]")
	if err != nil {
		t.Fatalf("flattenTextIDs: %v", err)
	}
	sort.Strings(ids)
	want := []string{"105", "109", "164"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestFlattenTextIDs_Dedup(t *testing.T) {
	ids, err := flattenTextIDs("[[105], [105]]")
	if err != nil {
		t.Fatalf("flattenTextIDs: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"105"}) {
		t.Fatalf("ids = %v, want [105]", ids)
	}
}

func TestFlattenTextIDs_Empty(t *testing.T) {
	ids, err := flattenTextIDs("[]")
	if err != nil {
		t.Fatalf("flattenTextIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want empty", ids)
	}
}

func TestFlattenTextIDs_InvalidJSON(t *testing.T) {
	if _, err := flattenTextIDs("not json"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestScore_RetrievalHitAndAnswerContains(t *testing.T) {
	submission := map[string]SubmissionEntry{
		"110": {FoundIDs: []string{"144", "200"}, ModelAnswer: "Ответ: Новосибирск, потому что..."},
		"419": {FoundIDs: []string{"1", "2"}, ModelAnswer: "Совсем другой ответ."},
	}
	gold := []GoldQA{
		{ID: 0, PublicID: 110, TextIDs: "[144]", Question: "q1", Answer: "Новосибирск", Type: "simple"},
		{ID: 1, PublicID: 419, TextIDs: "[433]", Question: "q2", Answer: "Сериал «Ландыши»", Type: "simple"},
	}

	rep, err := Score(submission, gold)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if rep.Total != 2 || rep.Matched != 2 {
		t.Fatalf("Total/Matched = %d/%d, want 2/2", rep.Total, rep.Matched)
	}
	if rep.RetrievalHits != 1 {
		t.Fatalf("RetrievalHits = %d, want 1", rep.RetrievalHits)
	}
	if rep.AnswerContains != 1 {
		t.Fatalf("AnswerContains = %d, want 1", rep.AnswerContains)
	}
	st := rep.Types["simple"]
	if st == nil || st.Count != 2 || st.RetrievalHits != 1 || st.AnswerContains != 1 {
		t.Fatalf("Types[simple] = %+v", st)
	}
}

func TestScore_UnmatchedGoldSkipped(t *testing.T) {
	submission := map[string]SubmissionEntry{}
	gold := []GoldQA{{ID: 0, PublicID: 999, TextIDs: "[1]", Answer: "x", Type: "simple"}}

	rep, err := Score(submission, gold)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if rep.Total != 1 || rep.Matched != 0 {
		t.Fatalf("Total/Matched = %d/%d, want 1/0", rep.Total, rep.Matched)
	}
}

func TestScore_CaseInsensitiveAnswerMatch(t *testing.T) {
	submission := map[string]SubmissionEntry{
		"1": {FoundIDs: nil, ModelAnswer: "Ответ: НОВОСИБИРСК точно."},
	}
	gold := []GoldQA{{ID: 0, PublicID: 1, TextIDs: "[1]", Answer: "новосибирск", Type: "simple"}}

	rep, err := Score(submission, gold)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if rep.AnswerContains != 1 {
		t.Fatalf("AnswerContains = %d, want 1", rep.AnswerContains)
	}
}

func TestAnswerContainsGold_SetTypeAllItemsPresent(t *testing.T) {
	answer := "Комаров — педагог, музыкант, а также деятель культуры."
	if !answerContainsGold(answer, "['Педагог', 'Музыкант']") {
		t.Fatal("expected all set items to match")
	}
}

func TestAnswerContainsGold_SetTypeMissingItem(t *testing.T) {
	answer := "Комаров — педагог."
	if answerContainsGold(answer, "['Педагог', 'Музыкант']") {
		t.Fatal("expected match to fail when an item is missing")
	}
}

func TestAnswerContainsGold_SetTypeEmptyList(t *testing.T) {
	if answerContainsGold("что угодно", "[]") {
		t.Fatal("expected empty set list to never match")
	}
}

func TestScore_SetTypeUsesListMatching(t *testing.T) {
	submission := map[string]SubmissionEntry{
		"1": {ModelAnswer: "Ответ: Педагог и Музыкант."},
	}
	gold := []GoldQA{{ID: 0, PublicID: 1, TextIDs: "[1]", Answer: "['Педагог', 'Музыкант']", Type: "set"}}

	rep, err := Score(submission, gold)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if rep.AnswerContains != 1 {
		t.Fatalf("AnswerContains = %d, want 1", rep.AnswerContains)
	}
}

func TestScore_EmptyGoldAnswerNeverCounted(t *testing.T) {
	submission := map[string]SubmissionEntry{
		"1": {FoundIDs: nil, ModelAnswer: "что угодно"},
	}
	gold := []GoldQA{{ID: 0, PublicID: 1, TextIDs: "[1]", Answer: "  ", Type: "simple"}}

	rep, err := Score(submission, gold)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if rep.AnswerContains != 0 {
		t.Fatalf("AnswerContains = %d, want 0", rep.AnswerContains)
	}
}
