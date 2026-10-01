package run

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
)

func TestScoreRetrievalHitAndAnswerContains(t *testing.T) {
	submission := map[string]Answer{
		"q1": {QuestionID: "q1", Answer: "The answer is alpha beta gamma.", DocumentIDs: []string{"dsid_d1", "dsid_d2"}},
		"q2": {QuestionID: "q2", Answer: "Completely different answer.", DocumentIDs: []string{"dsid_x"}},
	}
	gold := []corpus.Question{
		{ID: "q1", Type: "basic", ExpectedDocIDs: []string{"dsid_d1"}, GoldAnswer: "alpha beta"},
		{ID: "q2", Type: "basic", ExpectedDocIDs: []string{"dsid_y"}, GoldAnswer: "expected"},
	}

	rep := Score(submission, gold)
	if rep.Total != 2 || rep.Matched != 2 {
		t.Fatalf("Total/Matched = %d/%d, want 2/2", rep.Total, rep.Matched)
	}
	if rep.RetrievalHits != 1 {
		t.Fatalf("RetrievalHits = %d, want 1", rep.RetrievalHits)
	}
	if rep.AnswerContains != 1 {
		t.Fatalf("AnswerContains = %d, want 1", rep.AnswerContains)
	}
	st := rep.Types["basic"]
	if st == nil || st.Count != 2 || st.RetrievalHits != 1 || st.AnswerContains != 1 {
		t.Fatalf("Types[basic] = %+v", st)
	}
}

func TestScoreUnmatchedGoldSkipped(t *testing.T) {
	submission := map[string]Answer{}
	gold := []corpus.Question{{ID: "q999", Type: "basic", GoldAnswer: "x"}}

	rep := Score(submission, gold)
	if rep.Total != 1 || rep.Matched != 0 {
		t.Fatalf("Total/Matched = %d/%d, want 1/0", rep.Total, rep.Matched)
	}
}

func TestScoreFactsCoveragePartial(t *testing.T) {
	submission := map[string]Answer{
		"q1": {QuestionID: "q1", Answer: "The max file size is 10MiB and the request total is 50MiB.", DocumentIDs: []string{"dsid_d1"}},
	}
	gold := []corpus.Question{
		{
			ID:             "q1",
			Type:           "basic",
			ExpectedDocIDs: []string{"dsid_d1"},
			GoldAnswer:     "alpha beta",
			AnswerFacts: []string{
				"The maximum file size is 10 MiB.",
				"The total request size is 50 MiB.",
				"The default timeout is 30 seconds.",
			},
		},
	}

	rep := Score(submission, gold)
	if rep.AnswerContains != 0 {
		t.Fatalf("AnswerContains = %d, want 0 (gold phrase not contiguous)", rep.AnswerContains)
	}
	st := rep.Types["basic"]
	if st == nil {
		t.Fatal("Types[basic] missing")
	}
	if math.Abs(st.FactsCoverage-2.0/3.0) > 1e-9 {
		t.Fatalf("FactsCoverage = %v, want %.9f", st.FactsCoverage, 2.0/3.0)
	}
	if math.Abs(rep.AvgFactsCoverage-2.0/3.0) > 1e-9 {
		t.Fatalf("AvgFactsCoverage = %v, want %.9f", rep.AvgFactsCoverage, 2.0/3.0)
	}
}

func TestFactCoveredParaphrase(t *testing.T) {
	answer := "Based on the sources, the default file upload size limit (max_file_size) is 10MiB for multipart uploads."
	fact := "The default per file upload size limit (max_file_size) for multipart uploads on OpenAI-compatible endpoints is 10 MiB."
	if !factCovered(answer, fact, "en") {
		t.Fatal("expected paraphrased numeric fact to be covered")
	}
}

func TestFactCoveredMissingNumber(t *testing.T) {
	answer := "The default file upload size limit is small."
	fact := "The default per file upload size limit is 10 MiB."
	if factCovered(answer, fact, "en") {
		t.Fatal("expected fact with missing number to be uncovered")
	}
}

func TestFactCoveredEmptyAnswer(t *testing.T) {
	if factCovered("", "The default limit is 10 MiB.", "en") {
		t.Fatal("empty answer must never cover a fact")
	}
}

func TestScoreEmptyGoldAnswerNeverCounted(t *testing.T) {
	submission := map[string]Answer{
		"q1": {QuestionID: "q1", Answer: "anything"},
	}
	gold := []corpus.Question{{ID: "q1", Type: "basic", GoldAnswer: "  "}}

	rep := Score(submission, gold)
	if rep.AnswerContains != 0 {
		t.Fatalf("AnswerContains = %d, want 0", rep.AnswerContains)
	}
}

func TestAnswerContainsGoldCaseInsensitive(t *testing.T) {
	if !answerContainsGold("Alpha Beta Gamma", "alpha beta", "en") {
		t.Fatal("expected case-insensitive phrase match")
	}
}

func TestAnswerContainsGoldStemming(t *testing.T) {
	if !answerContainsGold("The cat is running quickly.", "run", "en") {
		t.Fatal("expected stemmed running to match gold run")
	}
}

func TestAnswerContainsGoldRejectsScatteredWords(t *testing.T) {
	if answerContainsGold("Alpha is first. Beta is second.", "alpha beta", "en") {
		t.Fatal("expected no match for scattered words")
	}
}

func TestAnswerContainsGoldSetTypeAllItemsPresent(t *testing.T) {
	if !answerContainsGold("Alpha beta gamma delta", "['alpha', 'gamma']", "en") {
		t.Fatal("expected all set items to match")
	}
}

func TestAnswerContainsGoldSetTypeMissingItem(t *testing.T) {
	if answerContainsGold("Alpha beta", "['alpha', 'gamma']", "en") {
		t.Fatal("expected match to fail when an item is missing")
	}
}

func TestAnswerContainsGoldSetTypeEmptyList(t *testing.T) {
	if answerContainsGold("anything", "[]", "en") {
		t.Fatal("expected empty set list to never match")
	}
}

func TestLoadSubmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "submission.jsonl")
	if err := writeAnswers(path, []Answer{
		{QuestionID: "q1", Answer: "one", DocumentIDs: []string{"d1"}},
		{QuestionID: "q2", Answer: "two"},
	}); err != nil {
		t.Fatalf("writeAnswers: %v", err)
	}

	submission, err := LoadSubmission(path)
	if err != nil {
		t.Fatalf("LoadSubmission: %v", err)
	}
	if len(submission) != 2 {
		t.Fatalf("submission length = %d, want 2", len(submission))
	}
	if submission["q1"].Answer != "one" || len(submission["q1"].DocumentIDs) != 1 {
		t.Fatalf("submission[q1] = %+v", submission["q1"])
	}
}

func TestLoadSubmissionRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "submission.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSubmission(path); err == nil {
		t.Fatal("LoadSubmission(empty) = nil error, want error")
	}
}

func TestLoadSubmissionRejectsOnlyBlankQuestionIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "submission.jsonl")
	if err := os.WriteFile(path, []byte(`{"question_id":"","answer":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSubmission(path); err == nil {
		t.Fatal("LoadSubmission(blank id) = nil error, want error")
	}
}

func TestAnswerContainsGoldRussianStemming(t *testing.T) {
	if !answerContainsGold("Кошка быстро бегает по улице.", "бегала", "ru") {
		t.Fatal("expected russian-stemmed бегает to match gold бегала")
	}
}

func TestAnswerContainsGoldRussianInflection(t *testing.T) {
	if !answerContainsGold("Он сейчас проживает в Израиле.", "Израиль", "ru") {
		t.Fatal("expected inflected form (Израиле) to match nominative gold (Израиль)")
	}
}

func TestAnswerContainsGoldRussianSetType(t *testing.T) {
	if !answerContainsGold("Россия обогнала США, Великобританию и Францию.", "['Великобритания', 'США', 'Франция']", "ru") {
		t.Fatal("expected russian set items to match across grammatical case")
	}
}

func TestFactCoveredRejectsNegatedAnswer(t *testing.T) {
	fact := "The default timeout is 30 seconds."
	answer := "The default timeout is not 30 seconds."
	if factCovered(answer, fact, "en") {
		t.Fatal("expected negated answer to not cover a non-negated fact")
	}
}

func TestFactCoveredRejectsNegatedAnswerRussian(t *testing.T) {
	fact := "В HTML нет тега с ролью application."
	answer := "В HTML есть тег с ролью application."
	if factCovered(answer, fact, "ru") {
		t.Fatal("expected opposite-polarity russian answer to not cover the fact")
	}
}

func TestFactCoveredSingleDigitMismatch(t *testing.T) {
	fact := "The default timeout is 5 seconds."
	answer := "The default timeout is 9 seconds."
	if factCovered(answer, fact, "en") {
		t.Fatal("expected a different single-digit number to not cover the fact")
	}
}

func TestFactCoveredSingleDigitMatch(t *testing.T) {
	fact := "The default timeout is 5 seconds."
	answer := "The default timeout is 5 seconds."
	if !factCovered(answer, fact, "en") {
		t.Fatal("expected matching single-digit number to cover the fact")
	}
}

func TestAnswerContainsGoldRejectsImmediateNegation(t *testing.T) {
	if answerContainsGold("The default timeout is not 30 seconds.", "30 seconds", "en") {
		t.Fatal("expected immediately-negated gold phrase to not match")
	}
}

func TestAnswerContainsGoldToleratesUnrelatedNegationElsewhere(t *testing.T) {
	if !answerContainsGold("I would not say this is simple, but the answer is alpha beta.", "alpha beta", "en") {
		t.Fatal("expected an unrelated negation elsewhere in the answer to not block an unrelated phrase match")
	}
}

// FactCoveredInText (document grounding) deliberately carries no negation
// check at all, after four escalating heuristics (global parity, no check,
// per-sentence parity, a token window) each broke on a concrete input - see
// its doc comment. These tests cover what it still must get right: plain
// bag-of-stems overlap that is not thrown off by negations, decimals, or
// sentence structure elsewhere in the text, independent of polarity.

func TestFactCoveredInTextUnaffectedByUnrelatedNegationElsewhere(t *testing.T) {
	doc := "Docker не требует отдельного гипервизора. " +
		"Docker применяется для развёртывания серверных приложений."
	fact := "Docker применяется для развёртывания серверных приложений."
	if !FactCoveredInText(doc, fact, "ru") {
		t.Fatal("expected a non-negated fact to be grounded even though an unrelated sentence elsewhere is negated")
	}
}

func TestFactCoveredInTextAcceptsMatchingNegatedFact(t *testing.T) {
	doc := "Docker не требует отдельного гипервизора."
	fact := "Docker не требует отдельного гипервизора."
	if !FactCoveredInText(doc, fact, "ru") {
		t.Fatal("expected a negated fact to be grounded by an equally negated sentence")
	}
}

func TestFactCoveredInTextHandlesDecimalPeriods(t *testing.T) {
	text := "The maximum file size is 10.5 MiB."
	fact := "The maximum file size is 10.5 MiB."
	if !FactCoveredInText(text, fact, "en") {
		t.Fatal("expected a verbatim self-match containing a decimal number to be grounded")
	}
}

func TestFactCoveredInTextDecimalPeriodRussianCorpusCase(t *testing.T) {
	// Real sentence from testdata/ru-bench/corpus/doka/dsid_rueb6d629cda__color-contrast.txt.
	text := "Текст меньше 24px (обычное начертание): не ниже 4.5:1."
	fact := "Текст меньше 24px: не ниже 4.5:1."
	if !FactCoveredInText(text, fact, "ru") {
		t.Fatal("expected the real corpus ratio '4.5:1' to stay grounded instead of being fragmented at the decimal point")
	}
}

func TestFactCoveredInTextIgnoresUnrelatedNegationInSameSentence(t *testing.T) {
	text := "В примере значение 1024px ограничивает контент, хотя сам контент не будет растягиваться."
	fact := "В примере значение 1024px ограничивает контент."
	if !FactCoveredInText(text, fact, "ru") {
		t.Fatal("expected a fact to stay grounded when an unrelated negated clause shares its sentence")
	}
}

// factCovered (answer scoring) keeps the original whole-answer negation
// parity gate from round 1: it is a coarser, imperfect proxy (round 2
// flagged it as pre-existing: an unrelated hedge elsewhere in a
// multi-sentence answer can still flip the count), but every attempt to
// sharpen it for FactCoveredInText regressed elsewhere, and this project's
// synthesized answers are typically short enough that it holds up in
// practice.

func TestFactCoveredStillAppliesPolarityGuard(t *testing.T) {
	fact := "The default timeout is 30 seconds."
	answer := "The default timeout is not 30 seconds."
	if factCovered(answer, fact, "en") {
		t.Fatal("expected factCovered (answer scoring) to still reject a negated answer")
	}
}

// TestFactCoveredKnownGapMatchingParityDifferentScope documents, rather than
// guards against, the accepted limitation named in negationParity's doc
// comment: when an answer's own negation count happens to match the fact's,
// the gate does not look at which word each negation actually scopes over.
// This is intentionally a passing test, not a bug report - it exists so a
// future change to this function does not silently alter this known,
// accepted behavior without a test noticing.
func TestFactCoveredKnownGapMatchingParityDifferentScope(t *testing.T) {
	fact := "The default timeout is not 30 seconds."
	answer := "The default timeout is 30 seconds, but it is not configurable."
	if !factCovered(answer, fact, "en") {
		t.Fatal("this is the known, accepted gap: matching parity with different scope is not detected - if this now fails, negationParity's doc comment needs updating, not this test")
	}
}

func TestFactCoveredRussian(t *testing.T) {
	answer := "Максимальный размер файла составляет 10 МиБ."
	fact := "Максимальный размер файла — 10 МиБ."
	if !factCovered(answer, fact, "ru") {
		t.Fatal("expected russian fact to be covered")
	}
}

func TestScoreMixedLanguageUsesPerQuestionStemmer(t *testing.T) {
	submission := map[string]Answer{
		"q_ru": {QuestionID: "q_ru", Answer: "Кошка быстро бегает по улице.", DocumentIDs: []string{"dsid_r"}},
		"q_en": {QuestionID: "q_en", Answer: "The cat is running quickly.", DocumentIDs: []string{"dsid_e"}},
	}
	gold := []corpus.Question{
		{ID: "q_ru", Type: "basic", Language: "ru", ExpectedDocIDs: []string{"dsid_r"}, GoldAnswer: "бегала"},
		{ID: "q_en", Type: "basic", Language: "en", ExpectedDocIDs: []string{"dsid_e"}, GoldAnswer: "run"},
	}

	rep := Score(submission, gold)
	if rep.Total != 2 || rep.Matched != 2 {
		t.Fatalf("Total/Matched = %d/%d, want 2/2", rep.Total, rep.Matched)
	}
	if rep.AnswerContains != 2 {
		t.Fatalf("AnswerContains = %d, want 2 (one per language)", rep.AnswerContains)
	}
}
