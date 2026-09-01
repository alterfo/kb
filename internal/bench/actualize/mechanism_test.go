package actualize

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connectors/chat/slack"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
	"github.com/alterfo/kb/internal/testkit"
)

const seedSource = "seed"

func seedRefDocID(id string) string {
	return seedSource + "/" + id
}

func seedDocuments() []connector.Document {
	docs := make([]connector.Document, 0, len(SeedDocs()))
	for _, d := range SeedDocs() {
		docs = append(docs, connector.Document{
			ID:     d.ID,
			Source: seedSource,
			Title:  d.Title,
			Body:   d.Body,
		})
	}
	return docs
}

func newMechanismChat() testkit.FakeChat {
	fc := testkit.NewFakeChat()
	for k := range fc.Responses {
		if strings.Contains(k, "extract a knowledge graph") {
			delete(fc.Responses, k)
		}
	}
	for k, v := range mechanismExtractions() {
		fc.Responses[k] = v
	}
	return fc
}

func fetchCorrections(t *testing.T, srvURL string) []connector.Document {
	t.Helper()
	c := slack.New()
	cfg := connector.Config{
		Name:    "avrora-slack",
		Config:  map[string]string{"base_url": srvURL, "channels": "C-AVRORA"},
		Secrets: map[string]string{"token": "SLACK_TOKEN"},
	}
	env := func(key string) (string, bool) {
		if key == "SLACK_TOKEN" {
			return "xoxb-secret", true
		}
		return "", false
	}
	if err := c.Resolve(context.Background(), cfg, env); err != nil {
		t.Fatalf("Resolve slack: %v", err)
	}

	out := make(chan connector.Document)
	done := make(chan struct{})
	var docs []connector.Document
	go func() {
		defer close(done)
		for d := range out {
			docs = append(docs, d)
		}
	}()
	if _, _, err := c.Fetch(context.Background(), connector.Cursor{}, out); err != nil {
		t.Fatalf("Fetch slack: %v", err)
	}
	<-done
	return docs
}

func indexCorrections(t *testing.T, ctx context.Context, ix *engine.Indexer, vs vector.Store, docs []connector.Document) map[string]string {
	t.Helper()
	byTS := make(map[string]string, len(ChatCorrections()))
	for _, c := range ChatCorrections() {
		byTS[c.TS] = c.CorrectsDocID
	}

	refs := make(map[string]string, len(ChatCorrections()))
	for _, d := range docs {
		ts, _ := d.Frontmatter["ts"].(string)
		correctsID := byTS[ts]
		if correctsID == "" {
			t.Fatalf("unexpected correction ts %q", ts)
		}
		if err := ix.IndexDocument(ctx, d); err != nil {
			t.Fatalf("IndexDocument correction %s: %v", correctsID, err)
		}
		ref, err := refDocIDByRawID(ctx, vs, d.ID)
		if err != nil {
			t.Fatalf("find correction %s ref doc id: %v", correctsID, err)
		}
		refs[correctsID] = ref
	}
	return refs
}

func refDocIDByRawID(ctx context.Context, vs vector.Store, rawID string) (string, error) {
	all, err := vs.AllForBM25(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range all {
		if c.Metadata["id"] == rawID {
			return c.RefDocID, nil
		}
	}
	return "", fmt.Errorf("no indexed chunk for raw id %q", rawID)
}

func assertSupersededBy(t *testing.T, ctx context.Context, vs vector.Store, docID, want string) {
	t.Helper()
	chunks, err := vs.ChunksByDoc(ctx, docID)
	if err != nil {
		t.Fatalf("ChunksByDoc(%q): %v", docID, err)
	}
	if len(chunks) == 0 {
		t.Fatalf("ChunksByDoc(%q) returned no chunks", docID)
	}
	for _, c := range chunks {
		if c.SupersededBy != want {
			t.Errorf("chunk %s of %q: superseded_by = %q, want %q", c.ID, docID, c.SupersededBy, want)
		}
	}
}

func relationExists(rels []graphstore.Relation, src, dst, typ string) bool {
	for _, r := range rels {
		if r.Src == src && r.Dst == dst && r.Type == typ {
			return true
		}
	}
	return false
}

func TestMechanism_SupersessionAndRelationClosure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	db, err := sqlite.Open(ctx, filepath.Join(root, "kb.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	fakeChat := newMechanismChat()
	fakeEmbed := testkit.NewFakeEmbedder()

	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	updater := graph.NewGraphUpdater(
		gs,
		graph.NewExtractor(fakeChat, "test"),
		graph.NewSummarizer(fakeChat, "test"),
	)
	idx := engine.NewIndexer(engine.Config{
		Root:         root,
		Vector:       vs,
		Graph:        updater,
		Embed:        fakeEmbed,
		EmbedModel:   "test",
		ChunkSize:    4096,
		ChunkOverlap: 512,
	})

	for _, d := range seedDocuments() {
		if err := idx.IndexDocument(ctx, d); err != nil {
			t.Fatalf("IndexDocument seed %q: %v", d.ID, err)
		}
	}

	srv := httptest.NewServer(NewFixtureHandler())
	defer srv.Close()

	corrections := fetchCorrections(t, srv.URL)
	if len(corrections) != len(ChatCorrections()) {
		t.Fatalf("fetched %d corrections, want %d", len(corrections), len(ChatCorrections()))
	}

	refs := indexCorrections(t, ctx, idx, vs, corrections)

	assertSupersededBy(t, ctx, vs, seedRefDocID("roadmap"), refs["roadmap"])
	assertSupersededBy(t, ctx, vs, seedRefDocID("budget"), refs["budget"])
	assertSupersededBy(t, ctx, vs, seedRefDocID("office"), "")

	av3ID := graph.EntityID("AV-3", "product")
	energolitID := graph.EntityID("ЭнергоЛит", "company")
	powercellID := graph.EntityID("PowerCell Rus", "company")

	before := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	after := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	relsBefore, err := gs.RelationsAsOf(ctx, []string{av3ID}, before)
	if err != nil {
		t.Fatalf("RelationsAsOf(before): %v", err)
	}
	if !relationExists(relsBefore, av3ID, energolitID, "supplier") {
		t.Errorf("before correction: missing ЭнергоЛит supplier relation in %+v", relsBefore)
	}
	if relationExists(relsBefore, av3ID, powercellID, "supplier") {
		t.Errorf("before correction: PowerCell Rus relation must not be valid yet: %+v", relsBefore)
	}

	relsAfter, err := gs.RelationsAsOf(ctx, []string{av3ID}, after)
	if err != nil {
		t.Fatalf("RelationsAsOf(after): %v", err)
	}
	if !relationExists(relsAfter, av3ID, powercellID, "supplier") {
		t.Errorf("after correction: missing PowerCell Rus supplier relation in %+v", relsAfter)
	}
	if relationExists(relsAfter, av3ID, energolitID, "supplier") {
		t.Errorf("after correction: ЭнергоЛит relation must be closed: %+v", relsAfter)
	}
}

func mechanismExtractions() map[string]string {
	return map[string]string{
		"Релиз дрона-курьера AV-3":           `{"entities":[{"name":"релиз AV-3","type":"event","description":"Релиз дрона AV-3"}],"relations":[]}`,
		"составляет 42 млн рублей":           `{"entities":[{"name":"бюджет AV-3","type":"budget","description":"Бюджет проекта AV-3"}],"relations":[]}`,
		"Глава производства — Игорь Ковалёв": `{"entities":[{"name":"глава производства","type":"role","description":"Игорь Ковалёв"}],"relations":[]}`,
		"Основной поставщик аккумуляторов":   `{"entities":[{"name":"AV-3","type":"product","description":"дрон-курьер"},{"name":"ЭнергоЛит","type":"company","description":"поставщик аккумуляторов"}],"relations":[{"source":"AV-3","target":"ЭнергоЛит","type":"supplier","description":"ЭнергоЛит поставляет аккумуляторы","valid_from":"2026-01-01"}]}`,
		"назначена на май 2026":              `{"entities":[{"name":"сертификация AV-3","type":"event","description":"Сертификация в Росавиации"}],"relations":[]}`,
		"Новосибирске, Академгородок":        `{"entities":[{"name":"головной офис","type":"location","description":"Новосибирск, Академгородок"}],"relations":[]}`,
		"вакансия инженера по авионике":      `{"entities":[{"name":"вакансия авионика","type":"vacancy","description":"инженер по авионике"}],"relations":[]}`,
		"25 кг":     `{"entities":[{"name":"масса AV-3","type":"spec","description":"25 кг"}],"relations":[]}`,
		"СибВенчур": `{"entities":[{"name":"раунд A","type":"funding","description":"Раунд A, ведущий инвестор СибВенчур"}],"relations":[]}`,
		"24 месяца": `{"entities":[{"name":"гарантия AV-3","type":"warranty","description":"24 месяца"}],"relations":[]}`,

		"переносится с 15 марта на 20 июня": `{"entities":[{"name":"релиз AV-3","type":"event","description":"Перенос на 20 июня 2026"}],"relations":[]}`,
		"увеличен до 55 млн рублей":         `{"entities":[{"name":"бюджет AV-3","type":"budget","description":"Увеличен до 55 млн рублей"}],"relations":[]}`,
		"сменяет Мария Литвинова":           `{"entities":[{"name":"глава производства","type":"role","description":"Мария Литвинова"}],"relations":[]}`,
		"на PowerCell Rus":                  `{"entities":[{"name":"AV-3","type":"product","description":"дрон-курьер"},{"name":"PowerCell Rus","type":"company","description":"новый поставщик аккумуляторов"}],"relations":[{"source":"AV-3","target":"PowerCell Rus","type":"supplier","description":"PowerCell Rus поставляет аккумуляторы","valid_from":"2026-04-01"}]}`,
		"перенесена с мая на август":        `{"entities":[{"name":"сертификация AV-3","type":"event","description":"Перенос на август 2026"}],"relations":[]}`,
	}
}
