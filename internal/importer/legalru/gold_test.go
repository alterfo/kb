package legalru

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type goldEntity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

type goldRelation struct {
	Src         string `json:"src"`
	Dst         string `json:"dst"`
	Type        string `json:"type"`
	Description string `json:"description"`
	ValidFrom   string `json:"valid_from"`
	ValidTo     string `json:"valid_to"`
}

type goldGraph struct {
	Entities  []goldEntity   `json:"entities"`
	Relations []goldRelation `json:"relations"`
}

type qaPair struct {
	Question          string   `json:"question"`
	ExpectedArticles  []string `json:"expected_articles"`
	ExpectedPlenumPts []string `json:"expected_plenum_points"`
	Justification     string   `json:"justification"`
}

const goldDir = "testdata/gold"

func TestGoldCorpus_Parses(t *testing.T) {
	docs, err := New().Import(filepath.Join(goldDir, "gk-rf-part1.md"))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 7 {
		t.Fatalf("expected 7 article documents, got %d", len(docs))
	}

	wantIDs := []string{
		"гк-рф/ч1/р1/гл1/ст1",
		"гк-рф/ч1/р1/гл1/ст2",
		"гк-рф/ч1/р1/гл2/ст8",
		"гк-рф/ч1/р1/гл2/ст10",
		"гк-рф/ч1/р1/гл2/ст12",
		"гк-рф/ч1/р1/гл2/ст15",
		"гк-рф/ч1/р1/гл2/ст16",
	}
	byID := map[string][]string{}
	for i, d := range docs {
		if d.ID != wantIDs[i] {
			t.Errorf("docs[%d].ID = %q, want %q", i, d.ID, wantIDs[i])
		}
		var reds []string
		if v, ok := d.Frontmatter["redactions"].([]string); ok {
			reds = v
		}
		byID[d.ID] = reds
	}

	wantRedactions := map[string][]string{
		"гк-рф/ч1/р1/гл1/ст1":  {"2012-12-30:302-ФЗ", "2015-03-08:42-ФЗ"},
		"гк-рф/ч1/р1/гл1/ст2":  {},
		"гк-рф/ч1/р1/гл2/ст8":  {},
		"гк-рф/ч1/р1/гл2/ст10": {"2012-12-30:302-ФЗ", "2015-03-08:42-ФЗ"},
		"гк-рф/ч1/р1/гл2/ст12": {},
		"гк-рф/ч1/р1/гл2/ст15": {"2015-03-08:42-ФЗ"},
		"гк-рф/ч1/р1/гл2/ст16": {},
	}
	for id, want := range wantRedactions {
		got, ok := byID[id]
		if !ok {
			t.Errorf("corpus missing article %s", id)
			continue
		}
		if len(got) != len(want) {
			t.Errorf("article %s redactions = %v, want %v", id, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("article %s redactions[%d] = %q, want %q", id, i, got[i], want[i])
			}
		}
	}

	if docs[0].Frontmatter["redaction_date"] != "2015-03-08" {
		t.Errorf("article 1 redaction_date = %v, want 2015-03-08", docs[0].Frontmatter["redaction_date"])
	}
	if docs[0].Frontmatter["fz_number"] != "42-ФЗ" {
		t.Errorf("article 1 fz_number = %v, want 42-ФЗ", docs[0].Frontmatter["fz_number"])
	}
}

func TestGoldGraph_FixtureValid(t *testing.T) {
	g := readGoldGraph(t)
	entities := map[string]goldEntity{}
	for _, e := range g.Entities {
		if e.ID == "" || e.Name == "" || e.Type == "" {
			t.Errorf("entity with empty field: %+v", e)
		}
		if _, dup := entities[e.ID]; dup {
			t.Errorf("duplicate entity id %q", e.ID)
		}
		entities[e.ID] = e
	}

	counts := map[string]int{}
	for _, e := range g.Entities {
		counts[e.Type]++
	}
	wantCounts := map[string]int{"legal-article": 7, "legal-amendment": 2, "legal-plenum": 8}
	for typ, want := range wantCounts {
		if counts[typ] != want {
			t.Errorf("entity type %q count = %d, want %d", typ, counts[typ], want)
		}
	}

	var amends, interprets int
	amendsByArticle := map[string][]goldRelation{}
	for _, r := range g.Relations {
		src, srcOK := entities[r.Src]
		dst, dstOK := entities[r.Dst]
		if !srcOK {
			t.Errorf("relation src %q not in entities", r.Src)
		}
		if !dstOK {
			t.Errorf("relation dst %q not in entities", r.Dst)
		}
		if srcOK && dstOK && r.Type == "amends" {
			if src.Type != "legal-amendment" {
				t.Errorf("amends src %q type = %q, want legal-amendment", r.Src, src.Type)
			}
			if dst.Type != "legal-article" {
				t.Errorf("amends dst %q type = %q, want legal-article", r.Dst, dst.Type)
			}
			amends++
			amendsByArticle[r.Dst] = append(amendsByArticle[r.Dst], r)
		}
		if srcOK && dstOK && r.Type == "interprets" {
			if src.Type != "legal-plenum" {
				t.Errorf("interprets src %q type = %q, want legal-plenum", r.Src, src.Type)
			}
			if dst.Type != "legal-article" {
				t.Errorf("interprets dst %q type = %q, want legal-article", r.Dst, dst.Type)
			}
			if r.ValidFrom != "" || r.ValidTo != "" {
				t.Errorf("interprets relation %s->%s must have no temporal fields", r.Src, r.Dst)
			}
			interprets++
		}
		if r.Type != "amends" && r.Type != "interprets" {
			t.Errorf("relation %s->%s type = %q, want amends or interprets", r.Src, r.Dst, r.Type)
		}
		if r.ValidFrom != "" {
			if _, err := time.Parse("2006-01-02", r.ValidFrom); err != nil {
				t.Errorf("relation %s->%s valid_from %q not an ISO date", r.Src, r.Dst, r.ValidFrom)
			}
		}
		if r.ValidTo != "" {
			if _, err := time.Parse("2006-01-02", r.ValidTo); err != nil {
				t.Errorf("relation %s->%s valid_to %q not an ISO date", r.Src, r.Dst, r.ValidTo)
			}
			if r.ValidFrom != "" && r.ValidTo < r.ValidFrom {
				t.Errorf("relation %s->%s valid_to %q before valid_from %q", r.Src, r.Dst, r.ValidTo, r.ValidFrom)
			}
		}
	}
	if amends != 5 {
		t.Errorf("amends relations = %d, want 5", amends)
	}
	if interprets != 8 {
		t.Errorf("interprets relations = %d, want 8", interprets)
	}

	wantAmends := map[string][]goldRelation{
		"гк-рф/ч1/р1/гл1/ст1": {
			{Src: "фз-302-2012", ValidFrom: "2012-12-30", ValidTo: "2015-03-08"},
			{Src: "фз-42-2015", ValidFrom: "2015-03-08", ValidTo: ""},
		},
		"гк-рф/ч1/р1/гл2/ст10": {
			{Src: "фз-302-2012", ValidFrom: "2012-12-30", ValidTo: "2015-03-08"},
			{Src: "фз-42-2015", ValidFrom: "2015-03-08", ValidTo: ""},
		},
		"гк-рф/ч1/р1/гл2/ст15": {
			{Src: "фз-42-2015", ValidFrom: "2015-03-08", ValidTo: ""},
		},
	}
	for article, want := range wantAmends {
		got := amendsByArticle[article]
		if len(got) != len(want) {
			t.Errorf("amends for %s = %d, want %d", article, len(got), len(want))
			continue
		}
		for i := range want {
			if got[i].Src != want[i].Src || got[i].ValidFrom != want[i].ValidFrom || got[i].ValidTo != want[i].ValidTo {
				t.Errorf("amends[%d] for %s = %+v, want %+v", i, article, got[i], want[i])
			}
		}
	}
}

func TestGoldQAPairs_FixtureValid(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(goldDir, "qa_pairs.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var pairs []qaPair
	if err := json.Unmarshal(raw, &pairs); err != nil {
		t.Fatalf("qa_pairs.json invalid JSON: %v", err)
	}
	if len(pairs) == 0 {
		t.Fatal("qa_pairs.json is empty")
	}

	docs, err := New().Import(filepath.Join(goldDir, "gk-rf-part1.md"))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	articleIDs := map[string]bool{}
	for _, d := range docs {
		articleIDs[d.ID] = true
	}

	g := readGoldGraph(t)
	entityIDs := map[string]bool{}
	for _, e := range g.Entities {
		entityIDs[e.ID] = true
	}
	interpretsBySrc := map[string]map[string]bool{}
	for _, r := range g.Relations {
		if r.Type == "interprets" {
			if interpretsBySrc[r.Src] == nil {
				interpretsBySrc[r.Src] = map[string]bool{}
			}
			interpretsBySrc[r.Src][r.Dst] = true
		}
	}

	plenumData, err := os.ReadFile(filepath.Join(goldDir, "plenum-25-2015.md"))
	if err != nil {
		t.Fatalf("ReadFile plenum: %v", err)
	}
	plenumText := string(plenumData)

	for i, p := range pairs {
		if p.Question == "" {
			t.Errorf("pair %d has empty question", i)
		}
		if len(p.ExpectedArticles) == 0 {
			t.Errorf("pair %d has no expected_articles", i)
		}
		if len(p.ExpectedPlenumPts) == 0 {
			t.Errorf("pair %d has no expected_plenum_points", i)
		}
		if p.Justification == "" {
			t.Errorf("pair %d has empty justification", i)
		}
		for _, a := range p.ExpectedArticles {
			if !articleIDs[a] {
				t.Errorf("pair %d expected article %q not in corpus", i, a)
			}
		}
		for _, pt := range p.ExpectedPlenumPts {
			if !entityIDs[pt] {
				t.Errorf("pair %d expected plenum point %q not in expected graph", i, pt)
			}
			if !strings.Contains(plenumText, "## Пункт "+plenumPointNumber(pt)) {
				t.Errorf("pair %d plenum point %q missing in plenum fixture", i, pt)
			}
			for _, a := range p.ExpectedArticles {
				if !interpretsBySrc[pt][a] {
					t.Errorf("pair %d expected interprets relation %s->%s missing in expected graph", i, pt, a)
				}
			}
		}
	}
}

func plenumPointNumber(id string) string {
	segs := strings.Split(id, "/")
	return strings.TrimPrefix(segs[len(segs)-1], "п")
}

func readGoldGraph(t *testing.T) goldGraph {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(goldDir, "expected_graph.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var g goldGraph
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("expected_graph.json invalid JSON: %v", err)
	}
	return g
}
