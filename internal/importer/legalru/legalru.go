package legalru

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
)

type Importer struct{}

func New() *Importer { return &Importer{} }

func (i *Importer) Ext() string { return ".md" }

type Redaction struct {
	Date time.Time
	FZ   string
}

// PlenumPoint is one numbered point (пункт) of a Постановление Пленума
// Верховного Суда РФ.
type PlenumPoint struct {
	Number string
	Body   string
}

// Plenum is a resolution (постановление) of the Plenum of the Supreme
// Court of the Russian Federation: its official number/date plus the
// individually numbered points that clarify code articles.
type Plenum struct {
	Number string
	Date   time.Time
	Title  string
	Points []PlenumPoint
}

func (p *Plenum) ResolutionID() string { return "пост-" + p.Number }

type Article struct {
	CodeID     string
	CodeTitle  string
	Part       string
	Section    string
	Chapter    string
	Number     string
	Title      string
	Body       string
	Redactions []Redaction
}

func (a *Article) ID() string { return articleID(a) }

func (i *Importer) Import(path string) ([]connector.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("legalru: read %s: %w", path, err)
	}

	articles, plenums, err := parse(string(data))
	if err != nil {
		return nil, err
	}
	if len(articles) == 0 && len(plenums) == 0 {
		return nil, nil
	}

	var updatedAt time.Time
	if info, err := os.Stat(path); err == nil {
		updatedAt = info.ModTime()
	}

	docs := make([]connector.Document, 0, len(articles)+len(plenums))
	for _, a := range articles {
		docs = append(docs, documentFor(a, path, updatedAt))
	}
	for _, p := range plenums {
		for _, pt := range p.Points {
			docs = append(docs, plenumDocumentFor(p, pt, updatedAt))
		}
	}
	return docs, nil
}

func plenumPointID(p *Plenum, n string) string {
	return "вс-рф/пленум/" + p.ResolutionID() + "/п" + n
}

func plenumPointTitle(p *Plenum, n string) string {
	return "Пункт " + n + " Постановления Пленума ВС РФ от " + p.Date.Format("02.01.2006") + " N " + p.Number
}

func plenumDocumentFor(p *Plenum, pt PlenumPoint, updatedAt time.Time) connector.Document {
	fm := map[string]any{
		"resolution_number": p.Number,
		"resolution_date":   p.Date.Format("2006-01-02"),
	}
	if p.Title != "" {
		fm["resolution_title"] = p.Title
	}
	title := plenumPointTitle(p, pt.Number)
	return connector.Document{
		ID:          plenumPointID(p, pt.Number),
		Kind:        "legal-plenum",
		Title:       title,
		UpdatedAt:   updatedAt,
		Body:        title + "\n\n" + pt.Body,
		Frontmatter: fm,
	}
}

func documentFor(a *Article, path string, updatedAt time.Time) connector.Document {
	fm := map[string]any{
		"code":           a.CodeID,
		"code_title":     a.CodeTitle,
		"article_number": a.Number,
		"article_title":  a.Title,
	}
	if a.Part != "" {
		fm["part"] = a.Part
	}
	if a.Section != "" {
		fm["section"] = a.Section
	}
	if a.Chapter != "" {
		fm["chapter"] = a.Chapter
	}
	if r, ok := latestRedaction(a); ok {
		fm["redaction_date"] = r.Date.Format("2006-01-02")
		fm["fz_number"] = r.FZ
	}
	if len(a.Redactions) > 0 {
		rs := make([]string, 0, len(a.Redactions))
		for _, r := range a.Redactions {
			rs = append(rs, r.Date.Format("2006-01-02")+":"+r.FZ)
		}
		fm["redactions"] = rs
	}

	title := "Статья " + a.Number
	if a.Title != "" {
		title += ". " + a.Title
	}

	return connector.Document{
		ID:          articleID(a),
		Kind:        "legal-article",
		Title:       title,
		UpdatedAt:   updatedAt,
		Body:        a.Body,
		Frontmatter: fm,
	}
}

func articleID(a *Article) string {
	parts := make([]string, 0, 6)
	parts = append(parts, a.CodeID)
	if a.Part != "" {
		parts = append(parts, "ч"+a.Part)
	}
	if a.Section != "" {
		parts = append(parts, "р"+a.Section)
	}
	if a.Chapter != "" {
		parts = append(parts, "гл"+a.Chapter)
	}
	parts = append(parts, "ст"+a.Number)
	return strings.Join(parts, "/")
}

func latestRedaction(a *Article) (Redaction, bool) {
	var best Redaction
	ok := false
	for _, r := range a.Redactions {
		if !ok || r.Date.After(best.Date) {
			best, ok = r, true
		}
	}
	return best, ok
}

type parserState struct {
	codeID    string
	codeTitle string
	part      string
	section   string
	chapter   string
	cur       *Article
	body      []string
	articles  []*Article
	plenum    *Plenum
	curPoint  *PlenumPoint
	plenums   []*Plenum
}

func (s *parserState) flush() {
	if s.cur != nil {
		s.cur.Body = strings.TrimRight(strings.Join(s.body, "\n"), "\n")
		s.articles = append(s.articles, s.cur)
		s.cur = nil
		s.body = nil
	}
	s.flushPoint()
	if s.plenum != nil {
		s.plenums = append(s.plenums, s.plenum)
		s.plenum = nil
	}
}

func (s *parserState) flushPoint() {
	if s.plenum == nil || s.curPoint == nil {
		return
	}
	s.curPoint.Body = strings.TrimSpace(strings.Join(s.body, "\n"))
	s.plenum.Points = append(s.plenum.Points, *s.curPoint)
	s.curPoint = nil
	s.body = nil
}

func (s *parserState) startPlenum(m []string) {
	s.flush()
	date, err := parseRedactionDate(m[1], m[2], m[3])
	if err != nil {
		date = time.Time{}
	}
	s.plenum = &Plenum{Number: m[4], Date: date, Title: strings.TrimSpace(m[5])}
	s.curPoint = nil
	s.body = nil
}

func (s *parserState) startPoint(number string) {
	s.flushPoint()
	s.curPoint = &PlenumPoint{Number: number}
	s.body = nil
}

func (s *parserState) startArticle(number, title, heading string) {
	a := &Article{
		CodeID:    s.codeID,
		CodeTitle: s.codeTitle,
		Part:      s.part,
		Section:   s.section,
		Chapter:   s.chapter,
		Number:    number,
		Title:     strings.TrimSpace(title),
	}
	s.cur = a
	body := "Статья " + number
	if t := strings.TrimSpace(title); t != "" {
		body += ". " + t
	}
	s.body = []string{body}
	s.scanRedactions(heading)
}

func (s *parserState) bodyLine(line string) {
	if s.cur == nil && s.curPoint == nil {
		return
	}
	s.body = append(s.body, line)
	if s.cur != nil {
		s.scanRedactions(line)
	}
}

func (s *parserState) scanRedactions(line string) {
	if s.cur == nil {
		return
	}
	for _, m := range redactionRe.FindAllStringSubmatch(line, -1) {
		date, err := parseRedactionDate(m[1], m[2], m[3])
		if err != nil {
			continue
		}
		r := Redaction{Date: date, FZ: m[4]}
		if s.hasRedaction(r) {
			// Real codex texts repeat the same amendment marker on several
			// paragraphs of one article; duplicate redactions would
			// otherwise produce empty AMENDS intervals (valid_to ==
			// valid_from) in the temporal graph.
			continue
		}
		s.cur.Redactions = append(s.cur.Redactions, r)
	}
}

func (s *parserState) hasRedaction(want Redaction) bool {
	for _, r := range s.cur.Redactions {
		if r.Date.Equal(want.Date) && r.FZ == want.FZ {
			return true
		}
	}
	return false
}

func parse(src string) ([]*Article, []*Plenum, error) {
	state := &parserState{}
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			state.bodyLine("")
			continue
		}

		if m := plenumTitleRe.FindStringSubmatch(trimmed); m != nil {
			state.startPlenum(m)
			continue
		}
		if state.plenum != nil {
			if m := plenumPointRe.FindStringSubmatch(trimmed); m != nil {
				state.startPoint(m[1])
				continue
			}
		}
		if m := codexRe.FindStringSubmatch(trimmed); m != nil {
			state.flush()
			state.codeID = strings.ToLower(m[1])
			state.codeTitle = strings.TrimSpace(m[2])
			continue
		}
		if n := partNum(trimmed); n != "" {
			state.flush()
			state.part = n
			continue
		}
		if m := matchSection(trimmed); m != nil {
			state.flush()
			state.section = numFromMixed(m[1])
			continue
		}
		if m := matchChapter(trimmed); m != nil {
			state.flush()
			state.chapter = numFromMixed(m[1])
			continue
		}
		if m := matchArticle(trimmed); m != nil && state.codeID != "" {
			state.flush()
			state.startArticle(m[1], m[2], trimmed)
			continue
		}
		state.bodyLine(line)
	}
	state.flush()

	if len(state.articles) == 0 && len(state.plenums) == 0 {
		return nil, nil, nil
	}
	return state.articles, state.plenums, nil
}

var (
	codexRe          = regexp.MustCompile(`(?i)^#{1,6}\s*\[([a-zа-я0-9-]+)\]\s*(.*)$`)
	plenumTitleRe    = regexp.MustCompile(`(?i)^#{1,6}\s*Постановление\s+Пленума\s+Верховного\s+Суда\s+РФ\s+от\s+(\d{1,2})\.(\d{1,2})\.(\d{4})\s+(?:N|№)\s*([0-9]+(?:-[A-ZА-Я]+)*)(?:\s+(.*))?$`)
	plenumPointRe    = regexp.MustCompile(`(?i)^#{1,6}\s*Пункт\s+([0-9]+(?:\.[0-9]+)*)[\.:]?\s*(.*)$`)
	partHeadingRe    = regexp.MustCompile(`(?i)^#{1,6}\s*Часть\s+([^\s.]+)[\.:]?\s*(.*)$`)
	partBareRe       = regexp.MustCompile(`(?i)^Часть\s+([^\s.]+)[\.:]?\s*$`)
	sectionHeadingRe = regexp.MustCompile(`(?i)^#{1,6}\s*Раздел\s+([0-9IVXLCDM]+)[\.:]?\s*(.*)$`)
	sectionBareRe    = regexp.MustCompile(`(?i)^Раздел\s+([0-9IVXLCDM]+)[\.:]\s*(.*)$`)
	chapterHeadingRe = regexp.MustCompile(`(?i)^#{1,6}\s*Глава\s+([0-9IVXLCDM]+)[\.:]?\s*(.*)$`)
	chapterBareRe    = regexp.MustCompile(`(?i)^Глава\s+([0-9IVXLCDM]+)[\.:]\s*(.*)$`)
	articleHeadingRe = regexp.MustCompile(`(?i)^#{1,6}\s*Статья\s+([0-9]+(?:\.[0-9]+)*)[\.:]?\s*(.*)$`)
	articleBareRe    = regexp.MustCompile(`(?i)^Статья\s+([0-9]+(?:\.[0-9]+)*)[\.:]\s*(.*)$`)
	redactionRe      = regexp.MustCompile(`(?i)в\s+(?:редакции|ред\.)\s+Федерального\s+закона\s+от\s+(\d{1,2})\.(\d{1,2})\.(\d{4})\s+(?:N|№)\s*([0-9]+(?:-[A-ZА-Я]+)*)`)
)

func partNum(line string) string {
	if m := partHeadingRe.FindStringSubmatch(line); m != nil {
		return ordinalOrNum(m[1])
	}
	if m := partBareRe.FindStringSubmatch(line); m != nil {
		return ordinalOrNum(m[1])
	}
	return ""
}

func matchSection(line string) []string {
	if m := sectionHeadingRe.FindStringSubmatch(line); m != nil {
		return m
	}
	return sectionBareRe.FindStringSubmatch(line)
}

func matchChapter(line string) []string {
	if m := chapterHeadingRe.FindStringSubmatch(line); m != nil {
		return m
	}
	return chapterBareRe.FindStringSubmatch(line)
}

func matchArticle(line string) []string {
	if m := articleHeadingRe.FindStringSubmatch(line); m != nil {
		return m
	}
	return articleBareRe.FindStringSubmatch(line)
}

var partOrdinals = map[string]string{
	"первая": "1", "вторая": "2", "третья": "3",
	"четвертая": "4", "четвёртая": "4", "пятая": "5",
	"шестая": "6", "седьмая": "7", "восьмая": "8",
	"девятая": "9", "десятая": "10",
}

func ordinalOrNum(s string) string {
	if n, ok := partOrdinals[strings.ToLower(s)]; ok {
		return n
	}
	if _, err := strconv.Atoi(s); err == nil {
		return s
	}
	return ""
}

var romanNumeralRe = regexp.MustCompile(`^(?i)M{0,3}(CM|CD|D?C{0,3})(XC|XL|L?X{0,3})(IX|IV|V?I{0,3})$`)

func numFromMixed(s string) string {
	if s == "" {
		return ""
	}
	if _, err := strconv.Atoi(s); err == nil {
		return s
	}
	v, ok := romanToInt(s)
	if !ok {
		return ""
	}
	return strconv.Itoa(v)
}

func romanToInt(s string) (int, bool) {
	up := strings.ToUpper(s)
	if !romanNumeralRe.MatchString(up) {
		return 0, false
	}
	vals := map[byte]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}
	total := 0
	for i := 0; i < len(up); i++ {
		v := vals[up[i]]
		if i+1 < len(up) && vals[up[i+1]] > v {
			total -= v
		} else {
			total += v
		}
	}
	return total, true
}

func parseRedactionDate(day, month, year string) (time.Time, error) {
	d, err1 := strconv.Atoi(day)
	m, err2 := strconv.Atoi(month)
	y, err3 := strconv.Atoi(year)
	if err1 != nil || err2 != nil || err3 != nil || m < 1 || m > 12 || d < 1 {
		return time.Time{}, fmt.Errorf("legalru: invalid redaction date %s.%s.%s", day, month, year)
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	// time.Date normalizes overflow (31.02 -> 03.03); a typo in the corpus
	// must fail the parse instead of silently shifting the redaction date.
	if t.Day() != d || int(t.Month()) != m || t.Year() != y {
		return time.Time{}, fmt.Errorf("legalru: invalid redaction date %s.%s.%s", day, month, year)
	}
	return t, nil
}
