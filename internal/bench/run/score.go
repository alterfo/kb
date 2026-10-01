package run

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/kljensen/snowball/english"
	"github.com/kljensen/snowball/russian"
)

type ScoreStat struct {
	Count               int     `json:"count"`
	RetrievalHits       int     `json:"retrieval_hits"`
	AnswerContains      int     `json:"answer_contains_gold"`
	FactsCoverage       float64 `json:"avg_facts_coverage,omitempty"`
	AvgContextPrecision float64 `json:"avg_context_precision,omitempty"`
	AvgContextRecall    float64 `json:"avg_context_recall,omitempty"`
	ContextEligible     int     `json:"context_eligible,omitempty"`
	AvgFaithfulness     float64 `json:"avg_faithfulness,omitempty"`
	FaithEligible       int     `json:"faith_eligible,omitempty"`
	FaithFailed         int     `json:"faith_failed,omitempty"`
}

type ScoreReport struct {
	Total               int                   `json:"total"`
	Matched             int                   `json:"matched"`
	RetrievalHits       int                   `json:"retrieval_hits"`
	AnswerContains      int                   `json:"answer_contains_gold"`
	AvgFactsCoverage    float64               `json:"avg_facts_coverage,omitempty"`
	AvgContextPrecision float64               `json:"avg_context_precision,omitempty"`
	AvgContextRecall    float64               `json:"avg_context_recall,omitempty"`
	ContextEligible     int                   `json:"context_eligible,omitempty"`
	AvgFaithfulness     float64               `json:"avg_faithfulness,omitempty"`
	FaithEligible       int                   `json:"faith_eligible,omitempty"`
	FaithFailed         int                   `json:"faith_failed,omitempty"`
	Types               map[string]*ScoreStat `json:"types"`
}

func (r *ScoreReport) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "total=%d matched=%d retrieval_hit=%d/%d answer_contains=%d/%d",
		r.Total, r.Matched, r.RetrievalHits, r.Matched, r.AnswerContains, r.Matched)
	if r.AvgFactsCoverage > 0 {
		fmt.Fprintf(&b, " facts_coverage=%.2f", r.AvgFactsCoverage)
	}
	if r.ContextEligible > 0 {
		fmt.Fprintf(&b, " context_precision=%.2f context_recall=%.2f (n=%d)", r.AvgContextPrecision, r.AvgContextRecall, r.ContextEligible)
	}
	if r.FaithEligible > 0 {
		fmt.Fprintf(&b, " faithfulness=%.2f (n=%d failed=%d)", r.AvgFaithfulness, r.FaithEligible, r.FaithFailed)
	}
	types := make([]string, 0, len(r.Types))
	for t := range r.Types {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		st := r.Types[t]
		extra := ""
		if st.Count > 0 && st.FactsCoverage > 0 {
			extra = fmt.Sprintf(" facts=%.2f", st.FactsCoverage)
		}
		if st.Count > 0 && (st.AvgContextPrecision > 0 || st.AvgContextRecall > 0) {
			if extra != "" {
				extra += " "
			}
			extra += fmt.Sprintf("ctx_p=%.2f ctx_r=%.2f", st.AvgContextPrecision, st.AvgContextRecall)
		}
		fmt.Fprintf(&b, " %s(n=%d retrieval=%d answer=%d%s)", t, st.Count, st.RetrievalHits, st.AnswerContains, extra)
	}
	return b.String()
}

func Score(submission map[string]Answer, gold []corpus.Question) *ScoreReport {
	return ScoreWithJudge(context.Background(), submission, gold, nil)
}

func ScoreWithJudge(ctx context.Context, submission map[string]Answer, gold []corpus.Question, judge FaithfulnessJudge) *ScoreReport {
	rep := &ScoreReport{Total: len(gold), Types: map[string]*ScoreStat{}}
	factsSums := map[string]float64{}
	factsCounts := map[string]int{}
	ctxPrecSums := map[string]float64{}
	ctxPrecCounts := map[string]int{}
	ctxRecallSums := map[string]float64{}
	ctxRecallCounts := map[string]int{}
	faithSums := map[string]float64{}
	faithEligCounts := map[string]int{}
	faithFailedCounts := map[string]int{}
	for _, q := range gold {
		entry, ok := submission[q.ID]
		if !ok {
			continue
		}
		rep.Matched++

		st, ok := rep.Types[q.Type]
		if !ok {
			st = &ScoreStat{}
			rep.Types[q.Type] = st
		}
		st.Count++

		if retrievalHit(entry.DocumentIDs, q.ExpectedDocIDs) {
			rep.RetrievalHits++
			st.RetrievalHits++
		}
		if answerContainsGold(entry.Answer, q.GoldAnswer, q.Language) {
			rep.AnswerContains++
			st.AnswerContains++
		}
		if len(q.AnswerFacts) > 0 {
			covered := 0
			for _, fact := range q.AnswerFacts {
				if factCovered(entry.Answer, fact, q.Language) {
					covered++
				}
			}
			frac := float64(covered) / float64(len(q.AnswerFacts))
			factsSums[q.Type] += frac
			factsCounts[q.Type]++
		}
		facts := contextGoldFacts(q)
		if len(facts) > 0 {
			ctxPrecSums[q.Type] += ContextPrecision(entry.ContextChunks, facts, q.Language)
			ctxPrecCounts[q.Type]++
			ctxRecallSums[q.Type] += ContextRecall(entry.ContextChunks, facts, q.Language)
			ctxRecallCounts[q.Type]++
		}
		if judge != nil {
			faithEligCounts[q.Type]++
			var f float64
			err := fmt.Errorf("bench: no context to judge")
			if len(entry.ContextChunks) > 0 {
				f, err = judge.Judge(ctx, entry.Answer, entry.ContextChunks)
			}
			if err == nil {
				faithSums[q.Type] += f
			} else {
				faithFailedCounts[q.Type]++
			}
		}
	}
	var totalFactsSum float64
	var totalFactsCount int
	for t, sum := range factsSums {
		rep.Types[t].FactsCoverage = sum / float64(factsCounts[t])
		totalFactsSum += sum
		totalFactsCount += factsCounts[t]
	}
	if totalFactsCount > 0 {
		rep.AvgFactsCoverage = totalFactsSum / float64(totalFactsCount)
	}
	var totalCtxPrec, totalCtxRecall float64
	var totalCtxEligible int
	for t, eligible := range ctxPrecCounts {
		rep.Types[t].AvgContextPrecision = ctxPrecSums[t] / float64(eligible)
		rep.Types[t].AvgContextRecall = ctxRecallSums[t] / float64(ctxRecallCounts[t])
		rep.Types[t].ContextEligible = eligible
		totalCtxPrec += ctxPrecSums[t]
		totalCtxRecall += ctxRecallSums[t]
		totalCtxEligible += eligible
	}
	if totalCtxEligible > 0 {
		rep.AvgContextPrecision = totalCtxPrec / float64(totalCtxEligible)
		rep.AvgContextRecall = totalCtxRecall / float64(totalCtxEligible)
		rep.ContextEligible = totalCtxEligible
	}
	var totalFaith float64
	var totalFaithEligible, totalFaithFailed int
	for t, eligible := range faithEligCounts {
		rep.Types[t].AvgFaithfulness = faithSums[t] / float64(eligible)
		rep.Types[t].FaithEligible = eligible
		rep.Types[t].FaithFailed = faithFailedCounts[t]
		totalFaith += faithSums[t]
		totalFaithEligible += eligible
		totalFaithFailed += faithFailedCounts[t]
	}
	if totalFaithEligible > 0 {
		rep.AvgFaithfulness = totalFaith / float64(totalFaithEligible)
		rep.FaithEligible = totalFaithEligible
		rep.FaithFailed = totalFaithFailed
	}
	return rep
}

func retrievalHit(foundIDs, wantIDs []string) bool {
	if len(wantIDs) == 0 {
		return false
	}
	found := make(map[string]struct{}, len(foundIDs))
	for _, id := range foundIDs {
		found[id] = struct{}{}
	}
	for _, want := range wantIDs {
		if _, ok := found[want]; ok {
			return true
		}
	}
	return false
}

var setItemRe = regexp.MustCompile(`'([^']*)'`)
var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

func isRussian(lang string) bool {
	return strings.EqualFold(strings.TrimSpace(lang), "ru")
}

func stem(t, lang string) string {
	if isRussian(lang) {
		return russian.Stem(t, true)
	}
	return english.Stem(t, true)
}

func stemSequence(s, lang string) []string {
	tokens := wordRe.FindAllString(strings.ToLower(s), -1)
	stems := make([]string, len(tokens))
	for i, t := range tokens {
		stems[i] = stem(t, lang)
	}
	return stems
}

var negationWordsEN = map[string]struct{}{
	"not": {}, "no": {}, "never": {}, "none": {}, "neither": {}, "nor": {}, "cannot": {},
}

var negationWordsRU = map[string]struct{}{
	"не": {}, "ни": {}, "нет": {}, "никогда": {}, "нельзя": {},
}

func negationWordsFor(lang string) map[string]struct{} {
	if isRussian(lang) {
		return negationWordsRU
	}
	return negationWordsEN
}

// negatedAt reports whether one of the two tokens immediately preceding
// position i in rawTokens is a negation marker, e.g. "not" in "... is not
// 30 seconds" immediately before a contiguous-stem match on "30 seconds".
func negatedAt(rawTokens []string, i int, lang string) bool {
	neg := negationWordsFor(lang)
	for back := 1; back <= 2 && i-back >= 0; back++ {
		if _, ok := neg[rawTokens[i-back]]; ok {
			return true
		}
	}
	return false
}

func phraseStemsPresent(modelStems, modelTokens []string, phrase, lang string) bool {
	phraseStems := stemSequence(phrase, lang)
	if len(phraseStems) == 0 || len(phraseStems) > len(modelStems) {
		return false
	}
	for i := 0; i+len(phraseStems) <= len(modelStems); i++ {
		match := true
		for j, ps := range phraseStems {
			if modelStems[i+j] != ps {
				match = false
				break
			}
		}
		if match && !negatedAt(modelTokens, i, lang) {
			return true
		}
	}
	return false
}

func answerContainsGold(modelAnswer, goldAnswer, lang string) bool {
	gold := strings.TrimSpace(goldAnswer)
	if gold == "" {
		return false
	}
	modelTokens := wordRe.FindAllString(strings.ToLower(modelAnswer), -1)
	modelStems := stemSequence(modelAnswer, lang)
	if strings.HasPrefix(gold, "[") && strings.HasSuffix(gold, "]") {
		items := setItemRe.FindAllStringSubmatch(gold, -1)
		if len(items) == 0 {
			return false
		}
		for _, m := range items {
			item := strings.TrimSpace(m[1])
			if item == "" {
				continue
			}
			if !phraseStemsPresent(modelStems, modelTokens, item, lang) {
				return false
			}
		}
		return true
	}
	return phraseStemsPresent(modelStems, modelTokens, gold, lang)
}

// GoldAnswerInText reports whether the gold answer phrase appears in the
// candidate text, matched by language-aware stems. It reuses the same
// contiguous-phrase check as answerContainsGold but against an arbitrary text
// (for example a source document body), so generators can gate generated
// gold answers on the corpus without duplicating scorer logic.
func GoldAnswerInText(candidate, gold, lang string) bool {
	return answerContainsGold(candidate, gold, lang)
}

// FactCoveredInText reports whether fact is grounded in text (typically a
// whole source document) on bag-of-stems overlap alone, with no negation
// check.
//
// This is a deliberately narrower guarantee than factCovered's, reached
// after four escalating attempts at automatic polarity detection here
// (global parity over the whole text, no check at all, per-sentence parity,
// a local token-window tag) each had a reviewer find a concrete real input
// it mis-scored: global and per-sentence parity both false-rejected valid
// facts on negations unrelated to the fact's own claim (including on real
// corpus sentences), splitting text into "sentences" fragmented decimal
// numbers and ratios, and the token-window both over- and under-reached
// depending on sentence structure, while bag-of-stems overlap can still
// out-vote a single correctly-placed negation tag once enough other stems
// match. Negation-scope detection needs more than regex/window heuristics
// to do reliably, and is not worth the complexity or the repeated
// regressions here. The residual risk this accepts - a generated fact that
// is the exact negated opposite of what the document says still reads as
// "grounded" - is caught instead by the mandatory human spot-check of
// generated questions before any generated set is trusted (see the
// generator-pilot review step in the RU dynamic bench plan).
func FactCoveredInText(text, fact, lang string) bool {
	return stemCoverage(text, fact, lang)
}

func stemCoverage(haystack, fact, lang string) bool {
	haystackStems := factContentStems(haystack, lang)
	if len(haystackStems) == 0 {
		return false
	}
	present := make(map[string]struct{}, len(haystackStems))
	for _, s := range haystackStems {
		present[s] = struct{}{}
	}
	factStems := factContentStems(fact, lang)
	if len(factStems) == 0 {
		return false
	}
	hit := 0
	for _, s := range factStems {
		if _, ok := present[s]; ok {
			hit++
		}
	}
	return float64(hit)/float64(len(factStems)) >= factCoverageThreshold
}

const factCoverageThreshold = 0.8

var factStopwordsEN = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "of": {}, "to": {},
	"in": {}, "on": {}, "at": {}, "for": {}, "is": {}, "are": {}, "was": {},
	"were": {}, "be": {}, "been": {}, "being": {}, "it": {}, "its": {},
	"this": {}, "that": {}, "these": {}, "those": {}, "as": {}, "by": {},
	"with": {}, "from": {}, "into": {}, "per": {}, "than": {}, "then": {},
	"there": {}, "their": {}, "they": {}, "you": {}, "your": {}, "we": {},
	"our": {}, "us": {}, "he": {}, "she": {}, "his": {}, "her": {}, "him": {},
	"which": {}, "who": {}, "whom": {}, "what": {}, "when": {}, "where": {},
	"how": {}, "not": {}, "no": {}, "if": {}, "so": {}, "such": {}, "can": {},
	"may": {}, "will": {}, "would": {}, "should": {}, "could": {}, "must": {},
	"has": {}, "have": {}, "had": {}, "do": {}, "does": {}, "did": {},
	"about": {}, "over": {}, "under": {}, "between": {}, "any": {}, "all": {},
	"each": {}, "other": {}, "some": {}, "more": {}, "most": {}, "also": {},
}

var factStopwordsRU = map[string]struct{}{
	"и": {}, "в": {}, "во": {}, "на": {}, "с": {}, "со": {}, "по": {},
	"из": {}, "от": {}, "к": {}, "ко": {}, "для": {}, "о": {}, "об": {},
	"обо": {}, "не": {}, "ни": {}, "что": {}, "это": {}, "как": {}, "так": {},
	"его": {}, "её": {}, "их": {}, "мы": {}, "вы": {}, "они": {}, "он": {},
	"она": {}, "оно": {}, "был": {}, "была": {}, "было": {}, "были": {},
	"есть": {}, "будет": {}, "будут": {}, "может": {}, "могут": {},
	"также": {}, "при": {}, "без": {}, "до": {}, "за": {}, "под": {},
	"над": {}, "между": {}, "через": {}, "или": {}, "а": {}, "но": {},
	"то": {}, "же": {}, "ли": {}, "бы": {}, "уже": {}, "ещё": {}, "все": {},
	"весь": {}, "вся": {}, "всё": {}, "который": {}, "которая": {},
	"которое": {}, "которые": {}, "этот": {}, "эта": {}, "эти": {},
	"тот": {}, "та": {}, "те": {},
}

func factStopwordsFor(lang string) map[string]struct{} {
	if isRussian(lang) {
		return factStopwordsRU
	}
	return factStopwordsEN
}

var digitLetterBoundaryRe = regexp.MustCompile(`([0-9])([[:alpha:]])|([[:alpha:]])([0-9])`)

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func factContentStems(s, lang string) []string {
	s = digitLetterBoundaryRe.ReplaceAllStringFunc(s, func(m string) string {
		return string(m[0]) + " " + string(m[1])
	})
	tokens := wordRe.FindAllString(strings.ToLower(s), -1)
	stopwords := factStopwordsFor(lang)
	stems := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if len([]rune(t)) < 2 && !isDigits(t) {
			continue
		}
		if _, stop := stopwords[t]; stop {
			continue
		}
		stems = append(stems, stem(t, lang))
	}
	return stems
}

// negationParity counts negation markers in s and reports it modulo 2, so a
// fact and an answer that disagree on parity (one negated, the other not)
// are known to disagree in polarity even though they may share every other
// content word. factCovered is the only caller: it compares a model
// answer against one fact at a time, and this project's synthesized
// answers are typically short enough (a few sentences at most) that a
// whole-answer negation count is a reasonable, if imperfect, proxy for
// which specific claim is negated. See FactCoveredInText's doc comment for
// why the same approach does not hold up for a whole source document.
func negationParity(s, lang string) int {
	tokens := wordRe.FindAllString(strings.ToLower(s), -1)
	neg := negationWordsFor(lang)
	count := 0
	for _, t := range tokens {
		if _, ok := neg[t]; ok {
			count++
		}
	}
	return count % 2
}

func factCovered(modelAnswer, fact, lang string) bool {
	if negationParity(modelAnswer, lang) != negationParity(fact, lang) {
		return false
	}
	return stemCoverage(modelAnswer, fact, lang)
}

func LoadSubmission(path string) (map[string]Answer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("bench: open submission: %w", err)
	}
	defer f.Close()

	out := map[string]Answer{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\uFEFF"))
		if line == "" {
			continue
		}
		var a Answer
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			return nil, fmt.Errorf("bench: parse submission line %d: %w", lineNo, err)
		}
		if a.QuestionID == "" {
			continue
		}
		out[a.QuestionID] = a
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("bench: scan submission: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("bench: no valid submission records in %s", path)
	}
	return out, nil
}

func SaveScoreReport(path string, rep *ScoreReport) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("bench: encode score report: %w", err)
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("bench: create score report dir: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("bench: write score report: %w", err)
	}
	return nil
}

type ScoreHistoryEntry struct {
	Timestamp time.Time   `json:"timestamp"`
	Report    ScoreReport `json:"report"`
}

func AppendScoreHistory(path string, rep *ScoreReport) error {
	return appendScoreHistoryAt(path, rep, time.Now().UTC())
}

func appendScoreHistoryAt(path string, rep *ScoreReport, timestamp time.Time) error {
	entries, err := LoadScoreHistory(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	entries = append(entries, ScoreHistoryEntry{Timestamp: timestamp, Report: *rep})
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("bench: encode score history: %w", err)
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("bench: create score history dir: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("bench: write score history: %w", err)
	}
	return nil
}

func LoadScoreHistory(path string) ([]ScoreHistoryEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []ScoreHistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("bench: parse score history: %w", err)
	}
	return entries, nil
}
