package legalru

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExt(t *testing.T) {
	if got := New().Ext(); got != ".md" {
		t.Fatalf("Ext() = %q, want .md", got)
	}
}

func TestImport_FixtureStructure(t *testing.T) {
	docs, err := New().Import("testdata/sample.md")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 article documents, got %d", len(docs))
	}

	wantIDs := []string{
		"гк-рф/ч1/р1/гл1/ст1",
		"гк-рф/ч1/р1/гл1/ст2",
		"гк-рф/ч1/р1/гл2/ст8",
	}
	for i, want := range wantIDs {
		if docs[i].ID != want {
			t.Errorf("docs[%d].ID = %q, want %q", i, docs[i].ID, want)
		}
	}

	st1 := docs[0]
	if st1.Kind != "legal-article" {
		t.Errorf("Kind = %q, want legal-article", st1.Kind)
	}
	if st1.Title != "Статья 1. Основные начала гражданского законодательства" {
		t.Errorf("Title = %q", st1.Title)
	}
	if st1.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set from file mtime")
	}

	checks := map[string]string{
		"code":           "гк-рф",
		"code_title":     "Гражданский кодекс Российской Федерации",
		"part":           "1",
		"section":        "1",
		"chapter":        "1",
		"article_number": "1",
		"article_title":  "Основные начала гражданского законодательства",
	}
	for k, want := range checks {
		if got := docs[0].Frontmatter[k]; got != want {
			t.Errorf("article 1 frontmatter[%q] = %v, want %q", k, got, want)
		}
	}

	if got := docs[1].Frontmatter["chapter"]; got != "1" {
		t.Errorf("article 2 chapter = %v, want 1", got)
	}
	if got := docs[2].Frontmatter["chapter"]; got != "2" {
		t.Errorf("article 8 chapter = %v, want 2", got)
	}
}

func TestImport_Redactions(t *testing.T) {
	docs, err := New().Import("testdata/sample.md")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	st1 := docs[0]
	if got := st1.Frontmatter["redaction_date"]; got != "2015-03-08" {
		t.Errorf("article 1 redaction_date = %v, want 2015-03-08", got)
	}
	if got := st1.Frontmatter["fz_number"]; got != "42-ФЗ" {
		t.Errorf("article 1 fz_number = %v, want 42-ФЗ", got)
	}
	rs, ok := st1.Frontmatter["redactions"].([]string)
	if !ok {
		t.Fatalf("article 1 redactions = %T, want []string", st1.Frontmatter["redactions"])
	}
	want := []string{"2012-12-30:302-ФЗ", "2015-03-08:42-ФЗ"}
	if len(rs) != len(want) {
		t.Fatalf("article 1 redactions = %v, want %v", rs, want)
	}
	for i := range want {
		if rs[i] != want[i] {
			t.Errorf("article 1 redactions[%d] = %q, want %q", i, rs[i], want[i])
		}
	}

	st8 := docs[2]
	if got := st8.Frontmatter["redaction_date"]; got != "2015-03-08" {
		t.Errorf("article 8 redaction_date = %v, want 2015-03-08", got)
	}
	if got := st8.Frontmatter["fz_number"]; got != "42-ФЗ" {
		t.Errorf("article 8 fz_number = %v, want 42-ФЗ", got)
	}

	st2 := docs[1]
	if _, ok := st2.Frontmatter["redaction_date"]; ok {
		t.Error("article 2 should have no redaction_date")
	}
	if _, ok := st2.Frontmatter["redactions"]; ok {
		t.Error("article 2 should have no redactions")
	}
}

func TestParse_DeduplicatesRepeatedRedactionMarker(t *testing.T) {
	src := `# [гк-рф] Гражданский кодекс Российской Федерации

# Часть первая

## Раздел I. Общие положения

### Глава 1. Гражданское законодательство

#### Статья 15. Осуществление гражданских прав (в ред. Федерального закона от 30.12.2012 N 302-ФЗ)
1. Пункт в редакции Федерального закона от 30.12.2012 N 302-ФЗ.
2. Ещё один пункт (в ред. Федерального закона от 30.12.2012 N 302-ФЗ).
`
	articles, _, err := parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(articles) != 1 {
		t.Fatalf("got %d articles, want 1", len(articles))
	}
	if len(articles[0].Redactions) != 1 {
		t.Fatalf("redactions = %+v, want exactly one (deduplicated) redaction", articles[0].Redactions)
	}
}

func TestImport_Body(t *testing.T) {
	docs, err := New().Import("testdata/sample.md")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	body := docs[0].Body
	for _, want := range []string{
		"Статья 1. Основные начала гражданского законодательства",
		"(в редакции Федерального закона от 30.12.2012 N 302-ФЗ)",
		"1. Гражданское законодательство основывается",
		"2. Граждане (физические лица)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("article 1 body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(docs[2].Body, "Статья 1.") {
		t.Error("article 8 body must not contain article 1 content")
	}
}

func TestImport_NotLegalCorpus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("# Заметки\n\nОбычный текст без структуры кодекса.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	docs, err := New().Import(path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 documents for non-legal corpus, got %d", len(docs))
	}
}

func TestImport_ArticleWithoutCodex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "law.md")
	content := "Статья 5. Защита нарушенных прав\n\n1. Защита нарушенных или оспоренных гражданских прав осуществляется судом.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	docs, err := New().Import(path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 documents without codex header, got %d", len(docs))
	}
}

func TestImport_CodexWithoutArticles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.md")
	if err := os.WriteFile(path, []byte("# [кодекс] Тестовый кодекс\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	docs, err := New().Import(path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 documents, got %d", len(docs))
	}
}

func TestImport_Plenum(t *testing.T) {
	docs, err := New().Import("testdata/gold/plenum-25-2015.md")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 8 {
		t.Fatalf("expected 8 plenum point documents, got %d", len(docs))
	}

	wantIDs := []string{
		"вс-рф/пленум/пост-25/п1",
		"вс-рф/пленум/пост-25/п2",
		"вс-рф/пленум/пост-25/п4",
		"вс-рф/пленум/пост-25/п7",
		"вс-рф/пленум/пост-25/п11",
		"вс-рф/пленум/пост-25/п12",
		"вс-рф/пленум/пост-25/п13",
		"вс-рф/пленум/пост-25/п15",
	}
	for i, want := range wantIDs {
		if docs[i].ID != want {
			t.Errorf("docs[%d].ID = %q, want %q", i, docs[i].ID, want)
		}
		if docs[i].Kind != "legal-plenum" {
			t.Errorf("docs[%d].Kind = %q, want legal-plenum", i, docs[i].Kind)
		}
	}

	p1 := docs[0]
	wantTitle := "Пункт 1 Постановления Пленума ВС РФ от 23.06.2015 N 25"
	if p1.Title != wantTitle {
		t.Errorf("Title = %q, want %q", p1.Title, wantTitle)
	}
	if !strings.HasPrefix(p1.Body, wantTitle+"\n\n") {
		t.Errorf("Body should start with the point title, got prefix %q", p1.Body[:min(60, len(p1.Body))])
	}
	if got := p1.Frontmatter["resolution_number"]; got != "25" {
		t.Errorf("resolution_number = %v, want 25", got)
	}
	if got := p1.Frontmatter["resolution_date"]; got != "2015-06-23" {
		t.Errorf("resolution_date = %v, want 2015-06-23", got)
	}
	if got := p1.Frontmatter["resolution_title"]; !strings.Contains(fmt.Sprint(got), "О применении судами") {
		t.Errorf("resolution_title = %v, want the resolution title", got)
	}
	if !strings.Contains(p1.Body, "добросовестные") {
		t.Errorf("point 1 body missing its text: %q", p1.Body)
	}
}

func TestParse_PlenumPoints(t *testing.T) {
	src := `# Постановление Пленума Верховного Суда РФ от 23.06.2015 N 25 «О применении...»

## Пункт 1

Первый пункт.

## Пункт 1.1

Подпункт.
`
	_, plenums, err := parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(plenums) != 1 {
		t.Fatalf("expected 1 plenum, got %d", len(plenums))
	}
	p := plenums[0]
	if p.Number != "25" || p.Date.Format("2006-01-02") != "2015-06-23" {
		t.Errorf("plenum = %+v", p)
	}
	if len(p.Points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(p.Points))
	}
	if p.Points[0].Number != "1" || strings.TrimSpace(p.Points[0].Body) != "Первый пункт." {
		t.Errorf("point 1 = %+v", p.Points[0])
	}
	if p.Points[1].Number != "1.1" || strings.TrimSpace(p.Points[1].Body) != "Подпункт." {
		t.Errorf("point 1.1 = %+v", p.Points[1])
	}
	if plenumPointID(p, "1.1") != "вс-рф/пленум/пост-25/п1.1" {
		t.Errorf("plenumPointID = %q", plenumPointID(p, "1.1"))
	}
}

func TestImport_MissingFile(t *testing.T) {
	if _, err := New().Import("testdata/does-not-exist.md"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParse_ArticleWithoutPunctuationHeading(t *testing.T) {
	src := "# [x] Кодекс\n\n#### Статья 3\n\nТекст статьи без точки после номера.\n"
	articles, _, err := parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}
	if articles[0].Number != "3" || articles[0].ID() != "x/ст3" {
		t.Errorf("article = %+v", articles[0])
	}
}

func TestParse_SubArticleNumber(t *testing.T) {
	src := "# [x] Кодекс\n\n#### Статья 15.1. Переход прав\n\n1. Текст.\n"
	articles, _, err := parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}
	if articles[0].Number != "15.1" {
		t.Errorf("Number = %q, want 15.1", articles[0].Number)
	}
	if articles[0].ID() != "x/ст15.1" {
		t.Errorf("ID = %q, want x/ст15.1", articles[0].ID())
	}
}

func TestParse_PlaintextCorpus(t *testing.T) {
	src := "# [к] Кодекс\n\nЧасть первая\n\nРаздел I. Общие положения\n\nГлава 2. Права\n\nСтатья 15. Компенсация морального вреда\n\n1. Суд может возложить на нарушителя обязанность денежной компенсации морального вреда.\n"
	articles, _, err := parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}
	a := articles[0]
	if a.ID() != "к/ч1/р1/гл2/ст15" {
		t.Errorf("ID = %q, want к/ч1/р1/гл2/ст15", a.ID())
	}
	if a.Part != "1" || a.Section != "1" || a.Chapter != "2" {
		t.Errorf("hierarchy = part %q section %q chapter %q", a.Part, a.Section, a.Chapter)
	}
}

func TestNumFromMixed(t *testing.T) {
	cases := map[string]string{
		"1": "1", "3": "3", "I": "1", "IV": "4", "IX": "9", "XII": "12", "XX": "20", "": "",
	}
	for in, want := range cases {
		if got := numFromMixed(in); got != want {
			t.Errorf("numFromMixed(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOrdinalPart(t *testing.T) {
	cases := map[string]string{
		"первая": "1", "вторая": "2", "третья": "3", "четвертая": "4", "четвёртая": "4",
		"пятая": "5", "7": "7", "foo": "",
	}
	for in, want := range cases {
		if got := ordinalOrNum(in); got != want {
			t.Errorf("ordinalOrNum(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRedactions(t *testing.T) {
	src := "# [x] Кодекс\n\n#### Статья 1. Название\n\n(в редакции Федерального закона от 30.12.2012 N 302-ФЗ)\n\n1. Пункт. (в ред. Федерального закона от 08.03.2015 № 42-ФЗ)\n"
	articles, _, err := parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}
	rs := articles[0].Redactions
	if len(rs) != 2 {
		t.Fatalf("expected 2 redactions, got %d", len(rs))
	}
	if rs[0].Date.Format("2006-01-02") != "2012-12-30" || rs[0].FZ != "302-ФЗ" {
		t.Errorf("redaction[0] = %+v", rs[0])
	}
	if rs[1].Date.Format("2006-01-02") != "2015-03-08" || rs[1].FZ != "42-ФЗ" {
		t.Errorf("redaction[1] = %+v", rs[1])
	}
	latest, ok := latestRedaction(articles[0])
	if !ok || latest.Date.Format("2006-01-02") != "2015-03-08" || latest.FZ != "42-ФЗ" {
		t.Errorf("latestRedaction = %+v, %v", latest, ok)
	}
}

func TestParse_Deterministic(t *testing.T) {
	a1, _, err := parse(string(readFixture(t)))
	if err != nil {
		t.Fatalf("parse #1: %v", err)
	}
	a2, _, err := parse(string(readFixture(t)))
	if err != nil {
		t.Fatalf("parse #2: %v", err)
	}
	if len(a1) != len(a2) {
		t.Fatalf("article counts differ: %d vs %d", len(a1), len(a2))
	}
	for i := range a1 {
		if a1[i].Body != a2[i].Body {
			t.Fatalf("article %d body not deterministic", i)
		}
	}
}

func readFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/sample.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return data
}

func TestImport_RedactionDateIsTimeFree(t *testing.T) {
	docs, err := New().Import("testdata/sample.md")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, ok := docs[0].Frontmatter["redaction_date"].(string); !ok {
		t.Errorf("redaction_date = %T, want string", docs[0].Frontmatter["redaction_date"])
	}
}

func TestParseRedactionDate(t *testing.T) {
	d, err := parseRedactionDate("30", "12", "2012")
	if err != nil {
		t.Fatalf("parseRedactionDate: %v", err)
	}
	if d != time.Date(2012, 12, 30, 0, 0, 0, 0, time.UTC) {
		t.Errorf("date = %v", d)
	}
	if _, err := parseRedactionDate("30", "13", "2012"); err == nil {
		t.Error("expected error for month 13")
	}
	if _, err := parseRedactionDate("0", "1", "2012"); err == nil {
		t.Error("expected error for day 0")
	}
	for _, bad := range [][3]string{{"31", "2", "2020"}, {"30", "2", "2012"}, {"31", "4", "2021"}} {
		if _, err := parseRedactionDate(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("expected error for impossible date %s.%s.%s", bad[0], bad[1], bad[2])
		}
	}
}
