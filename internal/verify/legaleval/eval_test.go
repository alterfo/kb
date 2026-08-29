package legaleval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeJudge struct {
	relevant bool
	truthful bool
	err      error
	evidence []string
	lastQ    string
	lastAns  string
	judged   []string
}

func (f *fakeJudge) StatuteRelevant(ctx context.Context, question string, article Article) (Verdict, error) {
	f.judged = append(f.judged, "statute:"+article.ID)
	if f.err != nil {
		return Verdict{}, f.err
	}
	return Verdict{Passed: f.relevant, Detail: "fake"}, nil
}

func (f *fakeJudge) ClaimTruthful(ctx context.Context, question, answer string, evidence []string) (Verdict, error) {
	f.lastQ = question
	f.lastAns = answer
	f.evidence = append([]string(nil), evidence...)
	f.judged = append(f.judged, "claim")
	if f.err != nil {
		return Verdict{}, f.err
	}
	return Verdict{Passed: f.truthful, Detail: "fake"}, nil
}

func evalFixture() (*Corpus, *Plenum) {
	c := NewCorpus([]Article{
		{ID: "к/ст1", File: "code.md", Number: "1", Body: "Статья 1: равенство участников.", Redactions: []Redaction{{Date: testDate("2012-12-30"), FZ: "302-ФЗ"}, {Date: testDate("2015-03-08"), FZ: "42-ФЗ"}}},
		{ID: "к/ст2", File: "code.md", Number: "2", Body: "Статья 2: отношения, регулируемые кодексом."},
	})
	p := NewPlenum([]PlenumPoint{{ID: "пл/п1", Body: "Пункт 1 разъясняет добросовестность."}})
	return c, p
}

func fakeAsk(answers map[string]Answer) AskFunc {
	return func(ctx context.Context, q string) (Answer, error) {
		if a, ok := answers[q]; ok {
			return a, nil
		}
		return Answer{Text: "no answer"}, nil
	}
}

func TestEvalMetricsHappyPath(t *testing.T) {
	corpus, plenum := evalFixture()
	judge := &fakeJudge{relevant: true, truthful: true}
	pairs := []QAPair{
		{Question: "Q1", ExpectedArticles: []string{"к/ст1"}, ExpectedPlenumPoints: []string{"пл/п1"}},
		{Question: "Q2", ExpectedArticles: []string{"к/ст2"}},
	}
	corpus.AddChunk("chunk/к_ст1", "к/ст1")
	corpus.AddChunk("chunk/к_ст2", "к/ст2")
	ask := fakeAsk(map[string]Answer{
		"Q1": {Text: "Ответ 1", Citations: []Citation{{FileName: "code.md", ChunkID: "chunk/к_ст1"}}},
		"Q2": {Text: "Ответ 2", Citations: []Citation{{FileName: "code.md", ChunkID: "chunk/к_ст2"}}},
	})

	rep, err := (&Eval{Ask: ask, Judge: judge, Corpus: corpus, Plenum: plenum}).Run(context.Background(), pairs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(rep.Results))
	}
	if rep.NHSR != (Metric{Total: 2, Passed: 2}) {
		t.Fatalf("NHSR = %+v", rep.NHSR)
	}
	if rep.SRR != (Metric{Total: 2, Passed: 2}) {
		t.Fatalf("SRR = %+v", rep.SRR)
	}
	if rep.LCT != (Metric{Total: 2, Passed: 2}) {
		t.Fatalf("LCT = %+v", rep.LCT)
	}
	if rep.AskErrors != 0 || rep.JudgeErrors != 0 {
		t.Fatalf("errors: ask=%d judge=%d", rep.AskErrors, rep.JudgeErrors)
	}
	if judge.lastQ != "Q2" || judge.lastAns != "Ответ 2" {
		t.Fatalf("last judged question/answer = %q / %q", judge.lastQ, judge.lastAns)
	}
	if len(judge.evidence) != 1 || !strings.Contains(judge.evidence[0], "Статья к/ст2") {
		t.Fatalf("evidence = %v", judge.evidence)
	}
	if !strings.Contains(rep.Summary(), "Non-Hallucinated-Statute-Rate: 1.000 (2/2)") {
		t.Fatalf("summary = %q", rep.Summary())
	}
}

func TestEvalHallucinatedAndStaleStatutes(t *testing.T) {
	corpus := NewCorpus([]Article{
		{ID: "к/ст1", File: "code.md", Number: "1", Redactions: []Redaction{{Date: testDate("2015-03-08"), FZ: "42-ФЗ"}}},
		{ID: "к/ст2", File: "code.md", Number: "2"},
		{ID: "к/ст3", File: "code.md", Number: "3", Redactions: []Redaction{{Date: testDate("2020-01-01"), FZ: "X-ФЗ"}}},
	})
	corpus.AddChunk("c1", "к/ст1")
	corpus.AddChunk("c99", "к/ст99")
	corpus.AddChunk("c3", "к/ст3")
	ask := fakeAsk(map[string]Answer{
		"Q": {Text: "Ответ", Citations: []Citation{
			{FileName: "code.md", ChunkID: "c1"},
			{FileName: "code.md", ChunkID: "c99"},
			{FileName: "code.md", ChunkID: "c3"},
		}},
	})
	rep, err := (&Eval{Ask: ask, Judge: &fakeJudge{relevant: true, truthful: true}, Corpus: corpus, AsOf: testDate("2019-01-01")}).Run(context.Background(), []QAPair{{Question: "Q"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.NHSR.Total != 3 || rep.NHSR.Passed != 1 {
		t.Fatalf("NHSR = %+v, want 1/3 (c1 current, c99 unknown, c3 stale at 2019-01-01)", rep.NHSR)
	}
}

func TestEvalPlenumDocCitationNotHallucinated(t *testing.T) {
	corpus, plenum := evalFixture()
	ask := fakeAsk(map[string]Answer{
		"Q": {Text: "Ответ", Citations: []Citation{
			{FileName: "code.md", ChunkID: "c1"},
			// The resolution document itself, not one of its points.
			{FileName: "plenum.md", ChunkID: "chunk/пл_док"},
		}},
	})
	resolve := func(ctx context.Context, c Citation) (string, bool) {
		if c.ChunkID == "chunk/пл_док" {
			return "пл", true
		}
		if c.ChunkID == "c1" {
			return "к/ст1", true
		}
		return "", false
	}
	rep, err := (&Eval{Ask: ask, Corpus: corpus, Plenum: plenum, Resolve: resolve}).Run(context.Background(), []QAPair{{Question: "Q"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// A citation to the plenum document is not a statute claim: it must be
	// excluded from the NHSR denominator, leaving only the article citation.
	if rep.NHSR != (Metric{Total: 1, Passed: 1}) {
		t.Fatalf("NHSR = %+v, want 1/1 (plenum-doc citation excluded)", rep.NHSR)
	}
}

func TestEvalAskError(t *testing.T) {
	corpus, _ := evalFixture()
	ask := func(ctx context.Context, q string) (Answer, error) {
		return Answer{}, errors.New("ask boom")
	}
	rep, err := (&Eval{Ask: ask, Judge: &fakeJudge{truthful: true}, Corpus: corpus}).Run(context.Background(), []QAPair{{Question: "Q"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.AskErrors != 1 || len(rep.Results) != 1 || rep.Results[0].AskError == "" {
		t.Fatalf("rep = %+v", rep)
	}
	if rep.NHSR.Total != 0 || rep.SRR.Total != 0 || rep.LCT.Total != 0 {
		t.Fatalf("failed ask must not contribute to metrics: %+v", rep)
	}
}

func TestEvalJudgeError(t *testing.T) {
	corpus, _ := evalFixture()
	corpus.AddChunk("c1", "к/ст1")
	ask := fakeAsk(map[string]Answer{"Q": {Text: "Ответ", Citations: []Citation{{FileName: "code.md", ChunkID: "c1"}}}})
	judge := &fakeJudge{err: errors.New("judge boom")}
	rep, err := (&Eval{Ask: ask, Judge: judge, Corpus: corpus}).Run(context.Background(), []QAPair{{Question: "Q"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.JudgeErrors != 2 {
		t.Fatalf("JudgeErrors = %d, want 2 (statute + claim)", rep.JudgeErrors)
	}
	if rep.SRR.Total != 0 || rep.LCT.Total != 0 {
		t.Fatalf("judge errors must not count: %+v", rep)
	}
	if rep.NHSR != (Metric{Total: 1, Passed: 1}) {
		t.Fatalf("NHSR = %+v", rep.NHSR)
	}
	if len(rep.Results[0].JudgeErrors) != 2 {
		t.Fatalf("per-result judge errors = %v", rep.Results[0].JudgeErrors)
	}
}

func TestEvalNilJudge(t *testing.T) {
	corpus, _ := evalFixture()
	corpus.AddChunk("c1", "к/ст1")
	ask := fakeAsk(map[string]Answer{"Q": {Text: "Ответ", Citations: []Citation{{FileName: "code.md", ChunkID: "c1"}}}})
	rep, err := (&Eval{Ask: ask, Corpus: corpus}).Run(context.Background(), []QAPair{{Question: "Q"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.NHSR != (Metric{Total: 1, Passed: 1}) {
		t.Fatalf("NHSR = %+v", rep.NHSR)
	}
	if rep.SRR.Total != 0 || rep.LCT.Total != 0 {
		t.Fatalf("nil judge must skip SRR/LCT: %+v", rep)
	}
}

func TestEvalDeduplicatesAndSkipsNonStatutes(t *testing.T) {
	corpus, plenum := evalFixture()
	corpus.AddChunk("c1", "к/ст1")
	corpus.AddChunk("p1", "пл/п1")
	ask := fakeAsk(map[string]Answer{"Q": {Text: "Ответ", Citations: []Citation{
		{FileName: "code.md", ChunkID: "c1"},
		{FileName: "code.md", ChunkID: "c1"},
		{FileName: "plenum.md", ChunkID: "p1"},
		{FileName: "unknown.md"},
	}}})
	judge := &fakeJudge{relevant: true, truthful: true}
	rep, err := (&Eval{Ask: ask, Judge: judge, Corpus: corpus, Plenum: plenum}).Run(context.Background(), []QAPair{{Question: "Q", ExpectedPlenumPoints: []string{"пл/п1"}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.NHSR.Total != 2 || rep.NHSR.Passed != 1 {
		t.Fatalf("NHSR = %+v, want 1/2 (c1 deduped and current; plenum citation excluded; unresolvable citation hallucinated)", rep.NHSR)
	}
	if rep.SRR.Total != 1 {
		t.Fatalf("SRR = %+v, want only one statute judged", rep.SRR)
	}
	if len(judge.evidence) != 1 || !strings.Contains(judge.evidence[0], "пл/п1") {
		t.Fatalf("plenum evidence = %v", judge.evidence)
	}
}

func TestEvalRequiresAskAndCorpus(t *testing.T) {
	if _, err := (&Eval{}).Run(context.Background(), nil); err == nil {
		t.Fatal("expected error when Ask/Corpus missing")
	}
}

func TestEvalGoldCorpusEndToEnd(t *testing.T) {
	corpus, err := LoadCorpus("../../../internal/importer/legalru/testdata/gold/gk-rf-part1.md")
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	pairs, err := LoadQAPairs("../../../internal/importer/legalru/testdata/gold/qa_pairs.json")
	if err != nil {
		t.Fatalf("LoadQAPairs: %v", err)
	}
	answers := map[string]Answer{}
	for i, p := range pairs {
		var cits []Citation
		for _, id := range p.ExpectedArticles {
			cits = append(cits, Citation{FileName: "legal.md", ChunkID: "chunk" + id})
			corpus.AddChunk("chunk"+id, id)
		}
		answers[p.Question] = Answer{Text: fmt.Sprintf("Ответ по паре %d", i), Citations: cits}
	}
	plenum, err := LoadPlenumPoints("../../../internal/importer/legalru/testdata/gold/plenum-25-2015.md", "вс-рф/пленум/пост-25")
	if err != nil {
		t.Fatalf("LoadPlenumPoints: %v", err)
	}
	judge := &fakeJudge{relevant: true, truthful: true}
	rep, err := (&Eval{Ask: fakeAsk(answers), Judge: judge, Corpus: corpus, Plenum: NewPlenum(plenum)}).Run(context.Background(), pairs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Results) != len(pairs) {
		t.Fatalf("results = %d, want %d", len(rep.Results), len(pairs))
	}
	if rep.AskErrors != 0 || rep.JudgeErrors != 0 {
		t.Fatalf("errors: ask=%d judge=%d", rep.AskErrors, rep.JudgeErrors)
	}
	if rep.NHSR.Total != len(pairs) {
		t.Fatalf("NHSR total = %d, want %d (one citation per pair)", rep.NHSR.Total, len(pairs))
	}
	if rep.NHSR.Passed != len(pairs) {
		t.Fatalf("NHSR passed = %d, want %d (all gold articles current as of corpus AsOf)", rep.NHSR.Passed, len(pairs))
	}
	for _, r := range rep.Results {
		if len(r.CitedStatutes) != 1 {
			t.Fatalf("pair %q cited statutes = %v", r.Question, r.CitedStatutes)
		}
	}
	if len(judge.evidence) == 0 {
		t.Fatal("evidence must include article and plenum texts")
	}
}
