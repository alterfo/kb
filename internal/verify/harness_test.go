package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/importer/legalru"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

type chatFunc func(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)

func (f chatFunc) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return f(ctx, req)
}

func extractionForSystemPrompt(sys string) bool {
	return strings.Contains(sys, "extract a knowledge graph")
}

func summaryForSystemPrompt(sys string) bool {
	return strings.Contains(sys, "summarize a cluster")
}

func newHarnessStore(t *testing.T) (graphstore.Store, func()) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	return sqlite.NewGraphStore(db), func() { db.Close() }
}

func harnessUpdater(store graphstore.Store, chat graph.ChatClient) *graph.GraphUpdater {
	return graph.NewGraphUpdater(store, graph.NewExtractor(chat, "model"), graph.NewSummarizer(chat, "model")).
		WithLegalExtractor(graph.NewLegalExtractor(chat, "model"))
}

func readStoreGraph(t *testing.T, store graphstore.Store) Graph {
	t.Helper()
	ctx := context.Background()
	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	return Graph{Entities: entities, Relations: relations}
}

// ---- synthetic corpus ----

var syntheticDocs = []string{
	"Alice is the engineering lead at Acme Corp. She reports to Bob, the CTO, and attends the Monday sync.",
	"Bob is the CTO of Acme Corp and leads the Atlas platform team. Carol assists Bob.",
	"Carol works at Acme Corp as Bob's assistant.",
	"Dave works at Acme Corp on the Atlas platform as a backend engineer.",
	"The Atlas platform is developed by Acme Corp and uses PostgreSQL and Go.",
	"Eve is a product manager at Acme Corp.",
	"Acme Corp is headquartered in Amsterdam.",
	"The Atlas platform team meets every Monday at the Monday sync.",
	"Acme Corp's legal department is led by Frank.",
	"The legal department is part of Acme Corp.",
}

var syntheticResponses = map[string]string{
	syntheticDocs[0]: `{"entities":[
		{"name":"Alice","type":"person","description":"engineering lead"},
		{"name":"Acme Corp","type":"company","description":"software company"},
		{"name":"Bob","type":"person","description":"CTO"},
		{"name":"Monday sync","type":"event","description":"weekly meeting"}],
		"relations":[
		{"source":"Alice","target":"Bob","type":"reports_to"},
		{"source":"Alice","target":"Acme Corp","type":"works_at"},
		{"source":"Alice","target":"Monday sync","type":"attends"}]}`,
	syntheticDocs[1]: `{"entities":[
		{"name":"Bob","type":"person","description":"CTO"},
		{"name":"Acme Corp","type":"company"},
		{"name":"Atlas platform team","type":"team"},
		{"name":"Carol","type":"person","description":"assistant"}],
		"relations":[
		{"source":"Bob","target":"Acme Corp","type":"works_at"},
		{"source":"Bob","target":"Atlas platform team","type":"leads"},
		{"source":"Carol","target":"Bob","type":"assists"}]}`,
	syntheticDocs[2]: `{"entities":[
		{"name":"Carol","type":"person","description":"assistant"},
		{"name":"Acme Corp","type":"company"},
		{"name":"Bob","type":"person"}],
		"relations":[{"source":"Carol","target":"Acme Corp","type":"works_at"}]}`,
	syntheticDocs[3]: `{"entities":[
		{"name":"Dave","type":"person","description":"backend engineer"},
		{"name":"Acme Corp","type":"company"},
		{"name":"Atlas","type":"product","description":"platform"}],
		"relations":[
		{"source":"Dave","target":"Acme Corp","type":"works_at"},
		{"source":"Dave","target":"Atlas","type":"works_on"}]}`,
	syntheticDocs[4]: `{"entities":[
		{"name":"Atlas","type":"product","description":"platform"},
		{"name":"Acme Corp","type":"company"},
		{"name":"PostgreSQL","type":"technology","description":"database"},
		{"name":"Go","type":"technology","description":"programming language"}],
		"relations":[
		{"source":"Acme Corp","target":"Atlas","type":"develops"},
		{"source":"Atlas","target":"PostgreSQL","type":"uses"},
		{"source":"Atlas","target":"Go","type":"uses"}]}`,
	syntheticDocs[5]: `{"entities":[
		{"name":"Eve","type":"person","description":"product manager"},
		{"name":"Acme Corp","type":"company"}],
		"relations":[{"source":"Eve","target":"Acme Corp","type":"works_at"}]}`,
	syntheticDocs[6]: `{"entities":[
		{"name":"Acme Corp","type":"company"},
		{"name":"Amsterdam","type":"location","description":"city"}],
		"relations":[{"source":"Acme Corp","target":"Amsterdam","type":"headquartered_in"}]}`,
	syntheticDocs[7]: `{"entities":[
		{"name":"Atlas platform team","type":"team"},
		{"name":"Monday sync","type":"event"}],
		"relations":[{"source":"Atlas platform team","target":"Monday sync","type":"meets_at"}]}`,
	syntheticDocs[8]: `{"entities":[
		{"name":"Legal department","type":"department"},
		{"name":"Frank","type":"person","description":"legal lead"},
		{"name":"Acme Corp","type":"company"}],
		"relations":[{"source":"Frank","target":"Legal department","type":"leads"}]}`,
	syntheticDocs[9]: `{"entities":[
		{"name":"Legal department","type":"department"},
		{"name":"Acme Corp","type":"company"}],
		"relations":[{"source":"Legal department","target":"Acme Corp","type":"part_of"}]}`,
}

func syntheticChat() chatFunc {
	return func(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
		sys, user := chatMessages(req)
		if summaryForSystemPrompt(sys) {
			return llm.ChatResponse{Content: `{"title":"t","summary":"s"}`}, nil
		}
		if resp, ok := syntheticResponses[user]; ok {
			return llm.ChatResponse{Content: resp}, nil
		}
		return llm.ChatResponse{Content: `{"entities":[],"relations":[]}`}, nil
	}
}

func chatMessages(req llm.ChatRequest) (system, user string) {
	if len(req.Messages) == 0 {
		return "", ""
	}
	return req.Messages[0].Content, req.Messages[len(req.Messages)-1].Content
}

func TestDumpSyntheticGolden(t *testing.T) {
	if os.Getenv("DUMP_SYNTHETIC_GOLDEN") == "" {
		t.Skip("set DUMP_SYNTHETIC_GOLDEN=1 to regenerate testdata/synthetic/expected_graph.json")
	}
	store, cleanup := newHarnessStore(t)
	defer cleanup()
	updater := harnessUpdater(store, syntheticChat())
	ctx := context.Background()
	for i, text := range syntheticDocs {
		chunk := vector.Chunk{ID: fmt.Sprintf("synthetic/doc%d", i+1), RefDocID: fmt.Sprintf("doc%d", i+1), Text: text}
		if _, err := updater.UpdateDocument(ctx, fmt.Sprintf("doc%d", i+1), []vector.Chunk{chunk}); err != nil {
			t.Fatalf("UpdateDocument doc%d: %v", i+1, err)
		}
	}
	got := readStoreGraph(t, store)
	type goldEnt struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
	}
	type goldRel struct {
		ID          string  `json:"id"`
		Src         string  `json:"src"`
		Dst         string  `json:"dst"`
		Type        string  `json:"type"`
		Description string  `json:"description"`
		Weight      float64 `json:"weight"`
		ValidFrom   string  `json:"valid_from"`
		ValidTo     string  `json:"valid_to"`
	}
	ents := make([]goldEnt, 0, len(got.Entities))
	for _, e := range got.Entities {
		ents = append(ents, goldEnt{ID: e.ID, Name: e.Name, Type: e.Type, Description: e.Description})
	}
	rels := make([]goldRel, 0, len(got.Relations))
	for _, r := range got.Relations {
		rels = append(rels, goldRel{
			ID: r.ID, Src: r.Src, Dst: r.Dst, Type: r.Type, Description: r.Description,
			Weight: r.Weight, ValidFrom: isoOrEmpty(r.ValidFrom), ValidTo: isoOrEmpty(r.ValidTo),
		})
	}
	raw, err := json.MarshalIndent(struct {
		Entities  []goldEnt `json:"entities"`
		Relations []goldRel `json:"relations"`
	}{ents, rels}, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "synthetic", "expected_graph.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Logf("wrote %d entities, %d relations", len(got.Entities), len(got.Relations))
}

func isoOrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func TestHarnessSyntheticGoldenDiff(t *testing.T) {
	store, cleanup := newHarnessStore(t)
	defer cleanup()
	updater := harnessUpdater(store, syntheticChat())
	ctx := context.Background()
	for i, text := range syntheticDocs {
		chunk := vector.Chunk{ID: fmt.Sprintf("synthetic/doc%d", i+1), RefDocID: fmt.Sprintf("doc%d", i+1), Text: text}
		if _, err := updater.UpdateDocument(ctx, fmt.Sprintf("doc%d", i+1), []vector.Chunk{chunk}); err != nil {
			t.Fatalf("UpdateDocument doc%d: %v", i+1, err)
		}
	}
	got := readStoreGraph(t, store)

	raw, err := os.ReadFile(filepath.Join("testdata", "synthetic", "expected_graph.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var want Graph
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("expected_graph.json invalid JSON: %v", err)
	}

	rep := DiffGraph(got, want)
	if rep.HasDifferences() {
		t.Fatalf("synthetic extraction drifted from golden graph:\n%s", formatReport(rep))
	}
	if len(got.Entities) != len(want.Entities) || len(got.Relations) != len(want.Relations) {
		t.Fatalf("counts: got %d entities %d relations, want %d/%d",
			len(got.Entities), len(got.Relations), len(want.Entities), len(want.Relations))
	}
	names := map[string]bool{}
	for _, e := range got.Entities {
		names[e.Name] = true
	}
	for _, wantName := range []string{"Alice", "Bob", "Acme Corp", "Atlas", "PostgreSQL", "Go", "Frank", "Monday sync"} {
		if !names[wantName] {
			t.Errorf("golden graph missing expected entity %q", wantName)
		}
	}
}

func formatReport(rep Report) string {
	var b strings.Builder
	for _, id := range rep.MissingEntities {
		fmt.Fprintf(&b, "missing entity: %s\n", id)
	}
	for _, id := range rep.ExtraEntities {
		fmt.Fprintf(&b, "extra entity: %s\n", id)
	}
	for _, m := range rep.MismatchedEntities {
		fmt.Fprintf(&b, "entity %s %s: got %q want %q\n", m.ID, m.Field, m.Got, m.Want)
	}
	for _, id := range rep.MissingRelations {
		fmt.Fprintf(&b, "missing relation: %s\n", id)
	}
	for _, id := range rep.ExtraRelations {
		fmt.Fprintf(&b, "extra relation: %s\n", id)
	}
	for _, m := range rep.MismatchedRelations {
		fmt.Fprintf(&b, "relation %s %s: got %q want %q\n", m.ID, m.Field, m.Got, m.Want)
	}
	return b.String()
}

// ---- legalru gold corpus ----

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

const legalGoldDir = "../importer/legalru/testdata/gold"

func readLegalGold(t *testing.T) goldGraph {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(legalGoldDir, "expected_graph.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var g goldGraph
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("expected_graph.json invalid JSON: %v", err)
	}
	return g
}

func legalChat(gold goldGraph) chatFunc {
	pointResponses := map[string]string{}
	for _, e := range gold.Entities {
		if e.Type != "legal-plenum" {
			continue
		}
		var target string
		desc := ""
		for _, r := range gold.Relations {
			if r.Type == "interprets" && r.Src == e.ID {
				target = articleNameFor(gold, r.Dst)
				desc = r.Description
				break
			}
		}
		pointResponses[e.Name] = fmt.Sprintf(`{"entities":[
			{"name":%q,"type":"пункт","description":%q},
			{"name":%q,"type":"статья","description":""}],
			"relations":[{"source":%q,"target":%q,"type":"interprets","description":%q}]}`,
			e.Name, e.Description, target, e.Name, target, desc)
	}

	return func(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
		sys, user := chatMessages(req)
		if summaryForSystemPrompt(sys) {
			return llm.ChatResponse{Content: `{"title":"t","summary":"s"}`}, nil
		}
		if extractionForSystemPrompt(sys) {
			for point, resp := range pointResponses {
				if strings.HasPrefix(user, point) {
					return llm.ChatResponse{Content: resp}, nil
				}
			}
			if strings.HasPrefix(user, "Статья ") {
				return llm.ChatResponse{Content: `{"entities":[],"relations":[]}`}, nil
			}
		}
		return llm.ChatResponse{Content: `{"entities":[],"relations":[]}`}, nil
	}
}

func articleNameFor(gold goldGraph, articleID string) string {
	for _, e := range gold.Entities {
		if e.ID == articleID && e.Type == "legal-article" {
			return e.Name
		}
	}
	return ""
}

func TestHarnessLegalGoldDiff(t *testing.T) {
	gold := readLegalGold(t)

	store, cleanup := newHarnessStore(t)
	defer cleanup()
	updater := harnessUpdater(store, legalChat(gold))
	ctx := context.Background()

	docs, err := legalru.New().Import(filepath.Join(legalGoldDir, "gk-rf-part1.md"))
	if err != nil {
		t.Fatalf("legalru.Import: %v", err)
	}
	for i, doc := range docs {
		metadata := map[string]string{"kind": graph.KindLegalArticle, "id": doc.ID}
		for k, v := range doc.Frontmatter {
			metadata[k] = fmt.Sprint(v)
		}
		chunk := vector.Chunk{ID: "legalru/" + doc.ID, RefDocID: doc.ID, Text: doc.Body, Metadata: metadata}
		if _, err := updater.UpdateDocument(ctx, doc.ID, []vector.Chunk{chunk}); err != nil {
			t.Fatalf("UpdateDocument article %d: %v", i, err)
		}
	}

	plenumChunks := plenumChunks(t)
	for _, chunk := range plenumChunks {
		if _, err := updater.UpdateDocument(ctx, chunk.RefDocID, []vector.Chunk{chunk}); err != nil {
			t.Fatalf("UpdateDocument plenum %s: %v", chunk.ID, err)
		}
	}

	got := readStoreGraph(t, store)
	want := normalizeLegalGold(t, gold)

	rep := DiffGraph(got, want)
	if rep.HasDifferences() {
		t.Fatalf("legal extraction drifted from golden graph:\n%s", formatReport(rep))
	}

	var amends, interprets int
	for _, r := range got.Relations {
		switch r.Type {
		case "amends":
			amends++
		case "interprets":
			interprets++
		}
	}
	if amends != 5 || interprets != 8 {
		t.Fatalf("got %d amends, %d interprets; want 5 and 8", amends, interprets)
	}
	types := map[string]int{}
	for _, e := range got.Entities {
		types[e.Type]++
	}
	if types["legal-article"] != 7 || types["legal-amendment"] != 2 {
		t.Fatalf("entity type counts = %v, want 7 legal-article and 2 legal-amendment", types)
	}
	if types[graph.KindLegalPlenumPoint] != 8 {
		t.Fatalf("entity type counts = %v, want 8 legal-plenum-point", types)
	}
	entityTypes := map[string]string{}
	for _, e := range got.Entities {
		entityTypes[e.ID] = e.Type
	}
	for _, r := range got.Relations {
		if r.Type != "interprets" {
			continue
		}
		if entityTypes[r.Src] != graph.KindLegalPlenumPoint || entityTypes[r.Dst] != "legal-article" {
			t.Fatalf("INTERPRETS edge %s -> %s must connect deterministic anchors, got types %q -> %q", r.Src, r.Dst, entityTypes[r.Src], entityTypes[r.Dst])
		}
	}
}

func plenumChunks(t *testing.T) []vector.Chunk {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(legalGoldDir, "plenum-25-2015.md"))
	if err != nil {
		t.Fatalf("ReadFile plenum: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	var chunks []vector.Chunk
	var curNum, curBody []string
	flush := func() {
		if len(curNum) == 0 {
			return
		}
		id := "вс-рф/пленум/пост-25/п" + strings.Join(curNum, "")
		title := fmt.Sprintf("Пункт %s Постановления Пленума ВС РФ от 23.06.2015 N 25", strings.Join(curNum, ""))
		text := title + "\n\n" + strings.TrimSpace(strings.Join(curBody, "\n"))
		chunks = append(chunks, vector.Chunk{
			ID:       id,
			RefDocID: "пленум-25-2015",
			Text:     text,
			Metadata: map[string]string{"kind": graph.KindLegalPlenum, "id": id},
		})
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## Пункт ") {
			flush()
			curNum = []string{strings.TrimSpace(strings.TrimPrefix(trimmed, "## Пункт "))}
			curBody = nil
			continue
		}
		if len(curNum) > 0 && !strings.HasPrefix(trimmed, "#") {
			curBody = append(curBody, line)
		}
	}
	flush()
	return chunks
}

func normalizeLegalGold(t *testing.T, gold goldGraph) Graph {
	t.Helper()
	articleName := map[string]string{}
	for _, e := range gold.Entities {
		if e.Type == "legal-article" {
			articleName[e.ID] = e.Name
		}
	}

	lastAmended := map[string]string{}
	for _, e := range gold.Entities {
		if e.Type != "legal-article" {
			continue
		}
		for _, r := range gold.Relations {
			if r.Type == "amends" && r.Dst == e.ID {
				lastAmended[r.Src] = e.ID
			}
		}
	}
	amendmentName := map[string]string{}
	for _, e := range gold.Entities {
		if e.Type == "legal-amendment" {
			amendmentName[e.ID] = e.Name
		}
	}

	var want Graph
	seen := map[string]bool{}
	addEntity := func(e graphstore.Entity) {
		if !seen[e.ID] {
			seen[e.ID] = true
			want.Entities = append(want.Entities, e)
		}
	}
	for _, e := range gold.Entities {
		switch e.Type {
		case "legal-article":
			addEntity(graphstore.Entity{
				ID: graph.EntityID(e.ID, "legal-article"), Name: e.Name, Type: "legal-article", Description: e.Description,
			})
		case "legal-amendment":
			addEntity(graphstore.Entity{
				ID: graph.EntityID(e.Name, "legal-amendment"), Name: e.Name, Type: "legal-amendment",
				Description: "Поправка к " + lastAmended[e.ID],
			})
		case "legal-plenum":
			addEntity(graphstore.Entity{
				ID: graph.EntityID(e.ID, graph.KindLegalPlenumPoint), Name: graph.LegalPlenumPointName(e.ID, map[string]string{"id": e.ID}), Type: graph.KindLegalPlenumPoint, Description: e.Description,
			})
		}
	}
	for _, r := range gold.Relations {
		switch r.Type {
		case "amends":
			src := graph.EntityID(amendmentName[r.Src], "legal-amendment")
			dst := graph.EntityID(r.Dst, "legal-article")
			addEntity(graphstore.Entity{ID: dst, Name: articleName[r.Dst], Type: "legal-article"})
			want.Relations = append(want.Relations, graphstore.Relation{
				ID: graph.RelationID(src, dst, "amends"), Src: src, Dst: dst, Type: "amends",
				Description: r.Description, Weight: 1,
				ValidFrom: parseGoldDate(r.ValidFrom), ValidTo: parseGoldDate(r.ValidTo),
			})
		case "interprets":
			src := graph.EntityID(r.Src, graph.KindLegalPlenumPoint)
			refName := articleName[r.Dst]
			dst := graph.EntityID(r.Dst, "legal-article")
			addEntity(graphstore.Entity{ID: dst, Name: refName, Type: "legal-article"})
			want.Relations = append(want.Relations, graphstore.Relation{
				ID: graph.RelationID(src, dst, "interprets"), Src: src, Dst: dst, Type: "interprets",
				Description: r.Description, Weight: 1,
			})
		}
	}
	return want
}

func parseGoldDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

// ---- mutation tests ----

func TestHarnessCatchesCorruptedGraph(t *testing.T) {
	gold := readLegalGold(t)
	want := normalizeLegalGold(t, gold)
	if DiffGraph(want, want).HasDifferences() {
		t.Fatal("want graph must be self-consistent")
	}

	missingEntity := copyGraph(want)
	missingEntity.Entities = missingEntity.Entities[:len(missingEntity.Entities)-1]
	rep := DiffGraph(missingEntity, want)
	if len(rep.MissingEntities) == 0 {
		t.Fatal("harness must report a missing entity")
	}

	extraRelation := copyGraph(want)
	extraRelation.Relations = append(extraRelation.Relations, graphstore.Relation{ID: "bogus", Src: "x", Dst: "y", Type: "knows"})
	rep = DiffGraph(extraRelation, want)
	if len(rep.ExtraRelations) == 0 || rep.ExtraRelations[0] != "bogus" {
		t.Fatalf("harness must report the extra relation, got %+v", rep.ExtraRelations)
	}

	wrongTemporal := copyGraph(want)
	for i := range wrongTemporal.Relations {
		if wrongTemporal.Relations[i].Type == "amends" && wrongTemporal.Relations[i].ValidFrom != nil {
			wrong := time.Date(2013, 1, 1, 0, 0, 0, 0, time.UTC)
			wrongTemporal.Relations[i].ValidFrom = &wrong
			break
		}
	}
	rep = DiffGraph(wrongTemporal, want)
	found := false
	for _, m := range rep.MismatchedRelations {
		if m.Field == "valid_from" {
			found = true
		}
	}
	if !found {
		t.Fatalf("harness must flag a wrong valid_from, got %+v", rep.MismatchedRelations)
	}

	corrupted := copyGraph(want)
	corrupted.Entities = corrupted.Entities[:len(corrupted.Entities)-2]
	corrupted.Relations = corrupted.Relations[:len(corrupted.Relations)-1]
	if !DiffGraph(corrupted, want).HasDifferences() {
		t.Fatal("combined corruption must be caught")
	}
}

func copyGraph(g Graph) Graph {
	out := Graph{Entities: append([]graphstore.Entity(nil), g.Entities...), Relations: append([]graphstore.Relation(nil), g.Relations...)}
	return out
}

func TestSyntheticGoldenFixtureCanonicalIDs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "synthetic", "expected_graph.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var g Graph
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("expected_graph.json invalid JSON: %v", err)
	}
	ids := map[string]bool{}
	for _, e := range g.Entities {
		if e.ID == "" || e.Name == "" || e.Type == "" {
			t.Fatalf("entity with empty field: %+v", e)
		}
		if ids[e.ID] {
			t.Fatalf("duplicate entity id %q", e.ID)
		}
		ids[e.ID] = true
	}
	byName := map[string]string{}
	for _, e := range g.Entities {
		byName[e.Name] = e.ID
	}
	for _, r := range g.Relations {
		if !ids[r.Src] || !ids[r.Dst] {
			t.Fatalf("relation %+v references unknown endpoints", r)
		}
	}
}
