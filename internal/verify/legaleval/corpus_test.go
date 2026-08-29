package legaleval

import (
	"testing"
	"time"
)

func testDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCorpusAsOfAndLookup(t *testing.T) {
	c := NewCorpus([]Article{
		{ID: "a/ст1", File: "code.md", Number: "1", Redactions: []Redaction{{Date: testDate("2012-12-30"), FZ: "302-ФЗ"}, {Date: testDate("2015-03-08"), FZ: "42-ФЗ"}}},
		{ID: "a/ст2", File: "code.md", Number: "2"},
		{ID: "a/ст3", File: "code2.md", Number: "3", Redactions: []Redaction{{Date: testDate("2020-01-01"), FZ: "X-ФЗ"}}},
	})
	if !c.AsOf().Equal(testDate("2020-01-01")) {
		t.Fatalf("AsOf = %v, want 2020-01-01", c.AsOf())
	}
	if a, ok := c.Article("a/ст1"); !ok || a.Number != "1" || len(a.Redactions) != 2 {
		t.Fatalf("Article(a/ст1) = %+v, %v", a, ok)
	}
	if _, ok := c.Article("a/ст99"); ok {
		t.Fatal("Article(a/ст99) must not exist")
	}
}

func TestCorpusCurrentAt(t *testing.T) {
	c := NewCorpus([]Article{
		{ID: "a/ст1", Redactions: []Redaction{{Date: testDate("2012-12-30")}, {Date: testDate("2015-03-08")}}},
		{ID: "a/ст2"},
	})
	cases := []struct {
		id   string
		date string
		want bool
	}{
		{"a/ст1", "2015-03-08", true},
		{"a/ст1", "2013-01-01", true},
		{"a/ст1", "2012-01-01", false},
		{"a/ст1", "2016-01-01", true},
		{"a/ст2", "2000-01-01", true},
		{"a/ст99", "2015-03-08", false},
	}
	for _, tc := range cases {
		if got := c.CurrentAt(tc.id, testDate(tc.date)); got != tc.want {
			t.Errorf("CurrentAt(%s, %s) = %v, want %v", tc.id, tc.date, got, tc.want)
		}
	}
}

func TestCorpusResolve(t *testing.T) {
	c := NewCorpus([]Article{
		{ID: "a/ст1", File: "code.md"},
		{ID: "a/ст2", File: "code.md"},
		{ID: "b/ст1", File: "other.md"},
	})
	if id, ok := c.Resolve(Citation{FileName: "other.md"}); !ok || id != "b/ст1" {
		t.Fatalf("Resolve(other.md) = %q, %v", id, ok)
	}
	if _, ok := c.Resolve(Citation{FileName: "code.md"}); ok {
		t.Fatal("Resolve(code.md) must be ambiguous")
	}
	if _, ok := c.Resolve(Citation{FileName: "nope.md"}); ok {
		t.Fatal("Resolve(nope.md) must fail")
	}
	c.AddChunk("legal/a_ст1.md#0", "a/ст1")
	if id, ok := c.Resolve(Citation{FileName: "code.md", ChunkID: "legal/a_ст1.md#0"}); !ok || id != "a/ст1" {
		t.Fatalf("Resolve with chunk id = %q, %v", id, ok)
	}
}

func TestLoadCorpusGold(t *testing.T) {
	c, err := LoadCorpus("../../../internal/importer/legalru/testdata/gold/gk-rf-part1.md")
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if c.AsOf().Format("2006-01-02") != "2015-03-08" {
		t.Fatalf("gold corpus AsOf = %v, want 2015-03-08", c.AsOf())
	}
	for _, id := range []string{
		"гк-рф/ч1/р1/гл1/ст1",
		"гк-рф/ч1/р1/гл1/ст2",
		"гк-рф/ч1/р1/гл2/ст8",
		"гк-рф/ч1/р1/гл2/ст10",
		"гк-рф/ч1/р1/гл2/ст12",
		"гк-рф/ч1/р1/гл2/ст15",
		"гк-рф/ч1/р1/гл2/ст16",
	} {
		if _, ok := c.Article(id); !ok {
			t.Errorf("gold corpus missing article %s", id)
		}
	}
	if a, _ := c.Article("гк-рф/ч1/р1/гл1/ст1"); len(a.Redactions) != 2 {
		t.Errorf("article 1 redactions = %+v, want 2", a.Redactions)
	}
}
