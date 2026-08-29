package graph

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestLegalExtractorArticlePromptAndJSON(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{
		"entities":[
			{"name":"Осуществление гражданских прав","type":"институт","description":"общий предел осуществления прав"},
			{"name":"Статья 12","type":"статья","description":"ссылка на способы защиты"}
		],
		"relations":[
			{"source":"Статья 12","target":"Осуществление гражданских прав","type":"refers_to","description":"кросс-ссылка"}
		]
	}`}}
	e := NewLegalExtractor(chat, "model")

	got, err := e.ExtractArticle(context.Background(), "Статья 15. Осуществление гражданских прав.")
	if err != nil {
		t.Fatalf("ExtractArticle: %v", err)
	}
	if len(chat.calls) != 1 {
		t.Fatalf("Chat calls = %d, want 1", len(chat.calls))
	}
	sys := chat.calls[0].Messages[0].Content
	if sys == extractionSystemPrompt {
		t.Fatal("article prompt must differ from the generic extraction prompt")
	}
	if len(got.Entities) != 2 || got.Entities[0].Name != "Осуществление гражданских прав" {
		t.Fatalf("Entities = %+v", got.Entities)
	}
	if len(got.Relations) != 1 || got.Relations[0].Type != "refers_to" {
		t.Fatalf("Relations = %+v", got.Relations)
	}
}

func TestLegalExtractorArticleBrokenJSONFailsOpen(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: "не json"}}
	e := NewLegalExtractor(chat, "model")

	got, err := e.ExtractArticle(context.Background(), "text")
	if err != nil {
		t.Fatalf("ExtractArticle: %v", err)
	}
	if len(got.Entities) != 0 || len(got.Relations) != 0 {
		t.Fatalf("got %+v, want empty extraction", got)
	}
}

func TestLegalExtractorPlenumPromptAndJSON(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{
		"entities":[
			{"name":"Пункт 1","type":"пункт","description":"разъяснение"},
			{"name":"Статья 15","type":"статья","description":"ГК РФ"}
		],
		"relations":[{"source":"Пункт 1","target":"Статья 15","type":"interprets","description":"разъясняет статью 15"}]
	}`}}
	e := NewLegalExtractor(chat, "model")

	got, err := e.ExtractPlenum(context.Background(), "1. Разъяснить, что ...")
	if err != nil {
		t.Fatalf("ExtractPlenum: %v", err)
	}
	if len(chat.calls) != 1 {
		t.Fatalf("Chat calls = %d, want 1", len(chat.calls))
	}
	sys := chat.calls[0].Messages[0].Content
	if sys == extractionSystemPrompt || sys == legalArticleSystemPrompt {
		t.Fatal("plenum prompt must differ from the generic and article prompts")
	}
	if len(got.Relations) != 1 || got.Relations[0].Type != "interprets" {
		t.Fatalf("Relations = %+v", got.Relations)
	}
	if got.Relations[0].ValidFrom != nil || got.Relations[0].ValidTo != nil {
		t.Fatalf("INTERPRETS must not carry bi-temporal validity, got %+v", got.Relations[0])
	}
}

func TestLegalExtractorPlenumBrokenJSONFailsOpen(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: "nope"}}
	e := NewLegalExtractor(chat, "model")

	got, err := e.ExtractPlenum(context.Background(), "text")
	if err != nil {
		t.Fatalf("ExtractPlenum: %v", err)
	}
	if len(got.Entities) != 0 || len(got.Relations) != 0 {
		t.Fatalf("got %+v, want empty extraction", got)
	}
}

func TestLegalExtractorTransportErrorFailsOpen(t *testing.T) {
	chat := &fakeChat{err: errors.New("boom")}
	e := NewLegalExtractor(chat, "model")

	got, err := e.ExtractArticle(context.Background(), "text")
	if err != nil {
		t.Fatalf("ExtractArticle: %v", err)
	}
	if len(got.Entities) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}
}

func TestLegalExtractorNilChatAndEmptyTextSkipCall(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{"entities":[],"relations":[]}`}}
	e := NewLegalExtractor(chat, "model")

	if got, err := e.ExtractArticle(context.Background(), "   "); err != nil || len(got.Entities) != 0 {
		t.Fatalf("empty text: got %+v, err %v", got, err)
	}
	if len(chat.calls) != 0 {
		t.Fatalf("expected no Chat call for empty text, got %d", len(chat.calls))
	}

	nilChat := NewLegalExtractor(nil, "model")
	if got, err := nilChat.ExtractArticle(context.Background(), "text"); err != nil || len(got.Entities) != 0 {
		t.Fatalf("nil chat: got %+v, err %v", got, err)
	}
}

func TestParseExtractionTemporalFields(t *testing.T) {
	content := `{"entities":[],"relations":[
		{"source":"A","target":"B","type":"amends","valid_from":"2012-12-30","valid_to":"2015-03-08"},
		{"source":"C","target":"D","type":"amends","valid_from":"not-a-date"}
	]}`
	got, ok := parseExtraction(content)
	if !ok {
		t.Fatal("parseExtraction failed")
	}
	if len(got.Relations) != 2 {
		t.Fatalf("Relations = %+v", got.Relations)
	}
	r0 := got.Relations[0]
	if r0.ValidFrom == nil || !r0.ValidFrom.Equal(time.Date(2012, 12, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("ValidFrom = %v, want 2012-12-30", r0.ValidFrom)
	}
	if r0.ValidTo == nil || !r0.ValidTo.Equal(time.Date(2015, 3, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("ValidTo = %v, want 2015-03-08", r0.ValidTo)
	}
	if got.Relations[1].ValidFrom != nil {
		t.Fatalf("malformed valid_from must be nil, got %v", got.Relations[1].ValidFrom)
	}
}

func legalMetadata() map[string]string {
	return map[string]string{
		"kind":           KindLegalArticle,
		"id":             "гк-рф/ч1/р1/гл2/ст15",
		"code":           "гк-рф",
		"part":           "1",
		"section":        "1",
		"chapter":        "2",
		"article_number": "15",
		"article_title":  "Осуществление гражданских прав",
		"redactions":     "[2012-12-30:302-ФЗ 2015-03-08:42-ФЗ]",
	}
}

func TestBuildLegalArticleContributionAMENDSBounds(t *testing.T) {
	entities, relations, ok := BuildLegalArticleContribution(legalMetadata())
	if !ok {
		t.Fatal("BuildLegalArticleContribution returned ok=false")
	}
	if len(entities) != 3 {
		t.Fatalf("entities = %d, want article + 2 actions", len(entities))
	}
	if len(relations) != 2 {
		t.Fatalf("relations = %d, want 2 AMENDS", len(relations))
	}

	article := entities[0]
	if article.Type != "legal-article" || article.Name != "Статья 15. Осуществление гражданских прав" {
		t.Fatalf("article entity = %+v", article)
	}

	first, second := relations[0], relations[1]
	if first.Type != "amends" || second.Type != "amends" {
		t.Fatalf("relation types = %q, %q, want amends", first.Type, second.Type)
	}
	if first.Dst != article.ID || second.Dst != article.ID {
		t.Fatalf("AMENDS must point at the article, got %q and %q", first.Dst, second.Dst)
	}
	if first.Src == second.Src {
		t.Fatal("each redaction must have its own Action entity")
	}
	wantFrom1 := time.Date(2012, 12, 30, 0, 0, 0, 0, time.UTC)
	wantTo1 := time.Date(2015, 3, 8, 0, 0, 0, 0, time.UTC)
	if first.ValidFrom == nil || !first.ValidFrom.Equal(wantFrom1) {
		t.Fatalf("first ValidFrom = %v, want 2012-12-30", first.ValidFrom)
	}
	if first.ValidTo == nil || !first.ValidTo.Equal(wantTo1) {
		t.Fatalf("first ValidTo = %v, want 2015-03-08 (start of next redaction)", first.ValidTo)
	}
	if second.ValidFrom == nil || !second.ValidFrom.Equal(wantTo1) {
		t.Fatalf("second ValidFrom = %v, want 2015-03-08", second.ValidFrom)
	}
	if second.ValidTo != nil {
		t.Fatalf("current redaction ValidTo = %v, want nil (open-ended)", second.ValidTo)
	}
}

func TestBuildLegalArticleContributionSortsRedactions(t *testing.T) {
	m := legalMetadata()
	m["redactions"] = "[2015-03-08:42-ФЗ 2012-12-30:302-ФЗ]"
	_, relations, ok := BuildLegalArticleContribution(m)
	if !ok {
		t.Fatal("ok=false")
	}
	want := []time.Time{
		time.Date(2012, 12, 30, 0, 0, 0, 0, time.UTC),
		time.Date(2015, 3, 8, 0, 0, 0, 0, time.UTC),
	}
	for i, rel := range relations {
		if rel.ValidFrom == nil || !rel.ValidFrom.Equal(want[i]) {
			t.Fatalf("relations[%d] ValidFrom = %v, want %v", i, rel.ValidFrom, want[i])
		}
	}
}

func TestBuildLegalArticleContributionNoRedactions(t *testing.T) {
	m := legalMetadata()
	m["redactions"] = ""
	if _, _, ok := BuildLegalArticleContribution(m); ok {
		t.Fatal("ok=true without redactions, want false")
	}
}

func TestBuildLegalArticleEntityWithoutRedactions(t *testing.T) {
	m := legalMetadata()
	m["redactions"] = ""
	article, ok := BuildLegalArticleEntity(m)
	if !ok {
		t.Fatal("BuildLegalArticleEntity returned ok=false for a legal article")
	}
	if article.Type != "legal-article" || article.Name != "Статья 15. Осуществление гражданских прав" {
		t.Fatalf("article entity = %+v", article)
	}
	if article.ID != EntityID("гк-рф/ч1/р1/гл2/ст15", "legal-article") {
		t.Fatalf("article ID = %q", article.ID)
	}
	if _, ok := BuildLegalArticleEntity(map[string]string{}); ok {
		t.Fatal("ok=true without article metadata, want false")
	}
}

func TestBuildLegalArticleContributionFallbackID(t *testing.T) {
	m := legalMetadata()
	delete(m, "id")
	entities, relations, ok := BuildLegalArticleContribution(m)
	if !ok {
		t.Fatal("ok=false")
	}
	if len(entities) == 0 || entities[0].ID == "" {
		t.Fatalf("article entity = %+v", entities)
	}
	wantID := EntityID("гк-рф/ч1/р1/гл2/ст15", "legal-article")
	if entities[0].ID != wantID {
		t.Fatalf("article ID = %q, want %q", entities[0].ID, wantID)
	}
	if len(relations) != 2 {
		t.Fatalf("relations = %d, want 2", len(relations))
	}
}

func TestParseRedactionsMetadata(t *testing.T) {
	got := ParseRedactionsMetadata("[2012-12-30:302-ФЗ 2015-03-08:42-ФЗ]")
	if len(got) != 2 {
		t.Fatalf("redactions = %+v", got)
	}
	if got[0].FZ != "302-ФЗ" || !got[0].Date.Equal(time.Date(2012, 12, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("redactions[0] = %+v", got[0])
	}
	if got[1].FZ != "42-ФЗ" {
		t.Fatalf("redactions[1] = %+v", got[1])
	}

	if got := ParseRedactionsMetadata("2012-12-30:302-ФЗ"); len(got) != 1 {
		t.Fatalf("plain form: got %+v", got)
	}
	if got := ParseRedactionsMetadata("garbage"); len(got) != 0 {
		t.Fatalf("garbage: got %+v", got)
	}
	if got := ParseRedactionsMetadata(""); got != nil {
		t.Fatalf("empty: got %+v", got)
	}
	if got := ParseRedactionsMetadata("[2012-12-30:302-ФЗ 2015-03-08:42-ФЗ 2012-12-30:302-ФЗ]"); len(got) != 2 {
		t.Fatalf("duplicate redaction not deduplicated: got %+v", got)
	}
}

func newTestLegalUpdater(t *testing.T, chat ChatClient) (*GraphUpdater, graphstore.Store) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store := sqlite.NewGraphStore(db)
	updater := NewGraphUpdater(store, NewExtractor(chat, "model"), NewSummarizer(chat, "model")).
		WithLegalExtractor(NewLegalExtractor(chat, "model"))
	return updater, store
}

func TestUpdateDocumentLegalArticleAddsAMENDS(t *testing.T) {
	chat := &scriptedChat{responses: []string{`{"title":"Поправки","summary":"история редакций"}`}}
	updater, store := newTestLegalUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{{
		ID:       "c1",
		RefDocID: "doc1",
		Text:     "Статья 15. Осуществление гражданских прав.",
		Metadata: legalMetadata(),
	}}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	all, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	var amends []graphstore.Relation
	for _, r := range all {
		if r.Type == "amends" {
			amends = append(amends, r)
		}
	}
	if len(amends) != 2 {
		t.Fatalf("AMENDS relations = %+v, want 2", amends)
	}
	if len(amends[0].SourceChunks) == 0 {
		t.Fatal("AMENDS must be anchored to a source chunk")
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	types := map[string]bool{}
	for _, e := range entities {
		types[e.Type] = true
	}
	if !types["legal-article"] || !types["legal-amendment"] {
		t.Fatalf("entity types = %v, want legal-article and legal-amendment", types)
	}
}

func TestUpdateDocumentLegalPlenumAddsInterprets(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"entities":[{"name":"Пункт 1","type":"пункт"},{"name":"Статья 15","type":"статья"}],"relations":[{"source":"Пункт 1","target":"Статья 15","type":"interprets"}]}`,
		`{"title":"Разъяснения","summary":"пленум"}`,
	}}
	updater, store := newTestLegalUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{{
		ID:       "c2",
		RefDocID: "doc2",
		Text:     "1. Разъяснить, что ...",
		Metadata: map[string]string{"kind": KindLegalPlenum, "id": "вс-рф/пленум/пост-1/п1"},
	}}
	if _, err := updater.UpdateDocument(ctx, "doc2", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	all, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	var interprets []graphstore.Relation
	for _, r := range all {
		if r.Type == "interprets" {
			interprets = append(interprets, r)
		}
	}
	if len(interprets) != 1 {
		t.Fatalf("INTERPRETS relations = %+v, want 1", interprets)
	}
	wantSrc := EntityID("вс-рф/пленум/пост-1/п1", KindLegalPlenumPoint)
	if interprets[0].Src != wantSrc {
		t.Fatalf("INTERPRETS src = %q, want deterministic plenum-point anchor %q", interprets[0].Src, wantSrc)
	}
	if interprets[0].ValidFrom != nil || interprets[0].ValidTo != nil {
		t.Fatalf("INTERPRETS must not carry temporal validity, got %+v", interprets[0])
	}
	if len(interprets[0].SourceChunks) == 0 {
		t.Fatal("INTERPRETS must be anchored to a source chunk")
	}
}

func TestUpdateDocumentLegalPlenumCanonicalizesArticleRefs(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"entities":[],"relations":[]}`,
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"entities":[{"name":"Пункт 1","type":"пункт","description":"разъяснение"},{"name":"статья 8 ГК РФ","type":"статья","description":""}],"relations":[{"source":"Пункт 1","target":"статья 8 ГК РФ","type":"interprets","description":"разъясняет статью 8"}]}`,
		`{"title":"Разъяснения","summary":"пленум"}`,
	}}
	updater, store := newTestLegalUpdater(t, chat)
	ctx := context.Background()

	articleChunk := vector.Chunk{
		ID:       "c1",
		RefDocID: "doc1",
		Text:     "Статья 8. Основания возникновения гражданских прав и обязанностей.",
		Metadata: map[string]string{
			"kind": KindLegalArticle, "id": "гк-рф/ч1/р1/гл2/ст8",
			"code": "гк-рф", "part": "1", "section": "1", "chapter": "2", "article_number": "8",
			"article_title": "Основания возникновения гражданских прав и обязанностей",
			"redactions":    "[2012-12-30:302-ФЗ]",
		},
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{articleChunk}); err != nil {
		t.Fatalf("UpdateDocument article: %v", err)
	}

	plenumChunk := vector.Chunk{
		ID:       "c2",
		RefDocID: "doc2",
		Text:     "Согласно пункту 1 статьи 8 ГК РФ ...",
		Metadata: map[string]string{"kind": KindLegalPlenum, "id": "вс-рф/пленум/пост-25/п1"},
	}
	if _, err := updater.UpdateDocument(ctx, "doc2", []vector.Chunk{plenumChunk}); err != nil {
		t.Fatalf("UpdateDocument plenum: %v", err)
	}

	all, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	var interprets []graphstore.Relation
	for _, r := range all {
		if r.Type == "interprets" {
			interprets = append(interprets, r)
		}
	}
	if len(interprets) != 1 {
		t.Fatalf("INTERPRETS relations = %+v, want 1", interprets)
	}
	wantSrc := EntityID("вс-рф/пленум/пост-25/п1", KindLegalPlenumPoint)
	wantDst := EntityID("гк-рф/ч1/р1/гл2/ст8", KindLegalArticle)
	if interprets[0].Src != wantSrc {
		t.Fatalf("INTERPRETS src = %q, want %q", interprets[0].Src, wantSrc)
	}
	if interprets[0].Dst != wantDst {
		t.Fatalf("INTERPRETS dst = %q, want canonical article anchor %q", interprets[0].Dst, wantDst)
	}
}

func TestRelationsAsOfReconstructsCurrentRedaction(t *testing.T) {
	_, relations, ok := BuildLegalArticleContribution(legalMetadata())
	if !ok {
		t.Fatal("BuildLegalArticleContribution failed")
	}
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := sqlite.NewGraphStore(db)
	ctx := context.Background()

	entities, _, _ := BuildLegalArticleContribution(legalMetadata())
	if err := store.UpsertEntities(ctx, entities); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := store.UpsertRelations(ctx, relations); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	articleID := entities[0].ID
	at := time.Date(2014, 6, 1, 0, 0, 0, 0, time.UTC)
	rels, err := store.RelationsAsOf(ctx, []string{articleID}, at)
	if err != nil {
		t.Fatalf("RelationsAsOf(2014): %v", err)
	}
	if len(rels) != 1 || !rels[0].ValidFrom.Equal(time.Date(2012, 12, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("relations as of 2014 = %+v, want the 2012-12-30 redaction", rels)
	}

	at = time.Date(2016, 6, 1, 0, 0, 0, 0, time.UTC)
	rels, err = store.RelationsAsOf(ctx, []string{articleID}, at)
	if err != nil {
		t.Fatalf("RelationsAsOf(2016): %v", err)
	}
	if len(rels) != 1 || !rels[0].ValidFrom.Equal(time.Date(2015, 3, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("relations as of 2016 = %+v, want the 2015-03-08 redaction", rels)
	}
}

func TestUpdateDocumentReindexClosesSupersededRedaction(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"title":"Поправки","summary":"история редакций"}`,
	}}
	updater, store := newTestLegalUpdater(t, chat)
	ctx := context.Background()

	first := legalMetadata()
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{
		ID: "c1", RefDocID: "doc1", Text: "Статья 15.", Metadata: first,
	}}); err != nil {
		t.Fatalf("first UpdateDocument: %v", err)
	}

	second := legalMetadata()
	second["redactions"] = "[2012-12-30:302-ФЗ 2015-03-08:42-ФЗ 2020-01-01:300-ФЗ]"
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{
		ID: "c2", RefDocID: "doc1", Text: "Статья 15.", Metadata: second,
	}}); err != nil {
		t.Fatalf("second UpdateDocument: %v", err)
	}

	articleID := EntityID("гк-рф/ч1/р1/гл2/ст15", "legal-article")
	rels, err := store.RelationsAsOf(ctx, []string{articleID}, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RelationsAsOf(2024): %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("RelationsAsOf(2024) = %+v, want exactly one AMENDS edge (2020 redaction)", rels)
	}
	if rels[0].ValidFrom == nil || !rels[0].ValidFrom.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("current edge = %+v, want the 2020-01-01 redaction", rels[0])
	}

	all, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	var amends []graphstore.Relation
	for _, r := range all {
		if r.Type == "amends" {
			amends = append(amends, r)
		}
	}
	if len(amends) != 3 {
		t.Fatalf("AMENDS relations = %+v, want 3 (one per redaction)", amends)
	}
}

func TestUpdateDocumentReindexReopensSupersededRedaction(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"title":"Поправки","summary":"история редакций"}`,
	}}
	updater, store := newTestLegalUpdater(t, chat)
	ctx := context.Background()

	first := legalMetadata()
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{
		ID: "c1", RefDocID: "doc1", Text: "Статья 15.", Metadata: first,
	}}); err != nil {
		t.Fatalf("first UpdateDocument: %v", err)
	}

	second := legalMetadata()
	second["redactions"] = "[2012-12-30:302-ФЗ 2015-03-08:42-ФЗ 2020-01-01:300-ФЗ]"
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{
		ID: "c2", RefDocID: "doc1", Text: "Статья 15.", Metadata: second,
	}}, "c1"); err != nil {
		t.Fatalf("second UpdateDocument: %v", err)
	}

	third := legalMetadata()
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{
		ID: "c3", RefDocID: "doc1", Text: "Статья 15.", Metadata: third,
	}}, "c2"); err != nil {
		t.Fatalf("third UpdateDocument: %v", err)
	}

	articleID := EntityID("гк-рф/ч1/р1/гл2/ст15", "legal-article")
	rels, err := store.RelationsAsOf(ctx, []string{articleID}, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RelationsAsOf(2024): %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("RelationsAsOf(2024) = %+v, want exactly one AMENDS edge (reopened 2015 redaction)", rels)
	}
	if rels[0].ValidFrom == nil || !rels[0].ValidFrom.Equal(time.Date(2015, 3, 8, 0, 0, 0, 0, time.UTC)) || rels[0].ValidTo != nil {
		t.Fatalf("current edge = %+v, want the reopened 2015-03-08 redaction", rels[0])
	}

	all, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	var amends []graphstore.Relation
	for _, r := range all {
		if r.Type == "amends" {
			amends = append(amends, r)
		}
	}
	if len(amends) != 2 {
		t.Fatalf("AMENDS relations = %+v, want 2 (2020 edge pruned)", amends)
	}
}

func TestUpdateDocumentLegalPlenumIndexedBeforeArticleCanonicalizes(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"entities":[{"name":"Пункт 1","type":"пункт"},{"name":"Статья 15","type":"статья"}],"relations":[{"source":"Пункт 1","target":"Статья 15","type":"interprets"}]}`,
		`{"title":"Разъяснения","summary":"пленум"}`,
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"title":"Поправки","summary":"история редакций"}`,
	}}
	updater, store := newTestLegalUpdater(t, chat)
	ctx := context.Background()

	// Plenum indexed first: no legal-article anchor exists yet, so the
	// canonicalization at plenum index time must leave the transient
	// "статья" entity and its INTERPRETS edge in place.
	plenumChunk := vector.Chunk{
		ID:       "c1",
		RefDocID: "doc1",
		Text:     "1. Разъяснить, что ...",
		Metadata: map[string]string{"kind": KindLegalPlenum, "id": "вс-рф/пленум/пост-25/п1"},
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{plenumChunk}); err != nil {
		t.Fatalf("plenum UpdateDocument: %v", err)
	}
	transientID := EntityID("Статья 15", "статья")

	// The article arrives later: its anchor must absorb the stored
	// INTERPRETS edge and drop the transient duplicate.
	articleChunk := vector.Chunk{
		ID:       "c2",
		RefDocID: "doc2",
		Text:     "Статья 15. Осуществление гражданских прав.",
		Metadata: legalMetadata(),
	}
	if _, err := updater.UpdateDocument(ctx, "doc2", []vector.Chunk{articleChunk}); err != nil {
		t.Fatalf("article UpdateDocument: %v", err)
	}

	all, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	var interprets []graphstore.Relation
	for _, r := range all {
		if r.Type == "interprets" {
			interprets = append(interprets, r)
		}
	}
	if len(interprets) != 1 {
		t.Fatalf("INTERPRETS relations = %+v, want 1", interprets)
	}
	wantSrc := EntityID("вс-рф/пленум/пост-25/п1", KindLegalPlenumPoint)
	wantDst := EntityID("гк-рф/ч1/р1/гл2/ст15", KindLegalArticle)
	if interprets[0].Src != wantSrc || interprets[0].Dst != wantDst {
		t.Fatalf("INTERPRETS = %s -> %s, want canonical %s -> %s", interprets[0].Src, interprets[0].Dst, wantSrc, wantDst)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	byID := map[string]graphstore.Entity{}
	for _, e := range entities {
		byID[e.ID] = e
	}
	if _, ok := byID[transientID]; ok {
		t.Fatalf("transient entity %q must be removed after canonicalization", transientID)
	}
	article, ok := byID[wantDst]
	if !ok {
		t.Fatalf("canonical article %q missing", wantDst)
	}
	if article.Type != KindLegalArticle {
		t.Fatalf("article type = %q, want %q", article.Type, KindLegalArticle)
	}
}

func TestUpdateDocumentLegalSubArticleAnchorsToOwnCanon(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"entities":[],"relations":[]}`,
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"entities":[],"relations":[]}`,
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"entities":[{"name":"Пункт 1","type":"пункт","description":"разъяснение"},{"name":"статья 25.1","type":"статья","description":""}],"relations":[{"source":"Пункт 1","target":"статья 25.1","type":"interprets"}]}`,
		`{"title":"Разъяснения","summary":"пленум"}`,
	}}
	updater, store := newTestLegalUpdater(t, chat)
	ctx := context.Background()

	indexArticle := func(id, num, title string) {
		t.Helper()
		chunk := vector.Chunk{
			ID: id, RefDocID: id, Text: "Статья " + num + ". " + title,
			Metadata: map[string]string{
				"kind": KindLegalArticle, "id": id, "code": "гк-рф", "article_number": num, "article_title": title,
			},
		}
		if _, err := updater.UpdateDocument(ctx, id, []vector.Chunk{chunk}); err != nil {
			t.Fatalf("UpdateDocument %s: %v", id, err)
		}
	}
	indexArticle("гк-рф/ч1/р1/гл2/ст25", "25", "Восстановление")
	indexArticle("гк-рф/ч1/р1/гл2/ст25.1", "25.1", "Особенности")

	plenumChunk := vector.Chunk{
		ID: "c3", RefDocID: "doc3", Text: "1. Разъяснить статью 25.1 ...",
		Metadata: map[string]string{"kind": KindLegalPlenum, "id": "вс-рф/пленум/пост-1/п1"},
	}
	if _, err := updater.UpdateDocument(ctx, "doc3", []vector.Chunk{plenumChunk}); err != nil {
		t.Fatalf("plenum UpdateDocument: %v", err)
	}

	all, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	var interprets []graphstore.Relation
	for _, r := range all {
		if r.Type == "interprets" {
			interprets = append(interprets, r)
		}
	}
	if len(interprets) != 1 {
		t.Fatalf("INTERPRETS relations = %+v, want 1", interprets)
	}
	parentCanon := EntityID("гк-рф/ч1/р1/гл2/ст25", KindLegalArticle)
	subCanon := EntityID("гк-рф/ч1/р1/гл2/ст25.1", KindLegalArticle)
	if interprets[0].Dst == parentCanon {
		t.Fatalf("INTERPRETS dst = parent article %q, want sub-article canon %q", parentCanon, subCanon)
	}
	if interprets[0].Dst != subCanon {
		t.Fatalf("INTERPRETS dst = %q, want sub-article canon %q", interprets[0].Dst, subCanon)
	}
}

func TestUpdateDocumentLegalSubArticleRetargetsToOwnCanon(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"entities":[{"name":"Пункт 1","type":"пункт","description":"разъяснение"},{"name":"статья 25.1","type":"статья","description":""}],"relations":[{"source":"Пункт 1","target":"статья 25.1","type":"interprets"}]}`,
		`{"title":"Разъяснения","summary":"пленум"}`,
		`{"entities":[],"relations":[]}`,
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"entities":[],"relations":[]}`,
		`{"title":"Поправки","summary":"история редакций"}`,
	}}
	updater, store := newTestLegalUpdater(t, chat)
	ctx := context.Background()

	plenumChunk := vector.Chunk{
		ID: "c1", RefDocID: "doc1", Text: "1. Разъяснить статью 25.1 ...",
		Metadata: map[string]string{"kind": KindLegalPlenum, "id": "вс-рф/пленум/пост-1/п1"},
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{plenumChunk}); err != nil {
		t.Fatalf("plenum UpdateDocument: %v", err)
	}
	transientID := EntityID("статья 25.1", "статья")

	indexArticle := func(id, num, title string) {
		t.Helper()
		chunk := vector.Chunk{
			ID: id, RefDocID: id, Text: "Статья " + num + ". " + title,
			Metadata: map[string]string{
				"kind": KindLegalArticle, "id": id, "code": "гк-рф", "article_number": num, "article_title": title,
			},
		}
		if _, err := updater.UpdateDocument(ctx, id, []vector.Chunk{chunk}); err != nil {
			t.Fatalf("UpdateDocument %s: %v", id, err)
		}
	}
	indexArticle("гк-рф/ч1/р1/гл2/ст25", "25", "Восстановление")
	indexArticle("гк-рф/ч1/р1/гл2/ст25.1", "25.1", "Особенности")

	all, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	var interprets []graphstore.Relation
	for _, r := range all {
		if r.Type == "interprets" {
			interprets = append(interprets, r)
		}
	}
	if len(interprets) != 1 {
		t.Fatalf("INTERPRETS relations = %+v, want 1", interprets)
	}
	subCanon := EntityID("гк-рф/ч1/р1/гл2/ст25.1", KindLegalArticle)
	if interprets[0].Dst != subCanon {
		t.Fatalf("INTERPRETS dst = %q, want sub-article canon %q", interprets[0].Dst, subCanon)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	for _, e := range entities {
		if e.ID == transientID {
			t.Fatalf("transient entity %q must be removed after retarget", transientID)
		}
	}
}

func TestUpdateDocumentLegalCrossCodeCanonicalization(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"entities":[],"relations":[]}`,
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"entities":[],"relations":[]}`,
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"entities":[{"name":"Пункт 1","type":"пункт","description":"разъяснение"},{"name":"статья 5 УК РФ","type":"статья","description":""}],"relations":[{"source":"Пункт 1","target":"статья 5 УК РФ","type":"interprets"}]}`,
		`{"title":"Разъяснения","summary":"пленум"}`,
	}}
	updater, store := newTestLegalUpdater(t, chat)
	ctx := context.Background()

	indexArticle := func(id, code, num, title string) {
		t.Helper()
		chunk := vector.Chunk{
			ID: id, RefDocID: id, Text: "Статья " + num + ". " + title,
			Metadata: map[string]string{
				"kind": KindLegalArticle, "id": id, "code": code, "article_number": num, "article_title": title,
			},
		}
		if _, err := updater.UpdateDocument(ctx, id, []vector.Chunk{chunk}); err != nil {
			t.Fatalf("UpdateDocument %s: %v", id, err)
		}
	}
	indexArticle("гк-рф/ч1/р1/гл2/ст5", "гк-рф", "5", "Гражданские права")
	indexArticle("ук-рф/о1/гл1/ст5", "ук-рф", "5", "Уголовная ответственность")

	plenumChunk := vector.Chunk{
		ID: "c3", RefDocID: "doc3", Text: "1. Разъяснить статью 5 УК РФ ...",
		Metadata: map[string]string{"kind": KindLegalPlenum, "id": "вс-рф/пленум/пост-1/п1"},
	}
	if _, err := updater.UpdateDocument(ctx, "doc3", []vector.Chunk{plenumChunk}); err != nil {
		t.Fatalf("plenum UpdateDocument: %v", err)
	}

	all, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	var interprets []graphstore.Relation
	for _, r := range all {
		if r.Type == "interprets" {
			interprets = append(interprets, r)
		}
	}
	if len(interprets) != 1 {
		t.Fatalf("INTERPRETS relations = %+v, want 1", interprets)
	}
	wantDst := EntityID("ук-рф/о1/гл1/ст5", KindLegalArticle)
	if interprets[0].Dst != wantDst {
		t.Fatalf("INTERPRETS dst = %q, want УК РФ canon %q", interprets[0].Dst, wantDst)
	}
}

func TestUpdateDocumentLegalCrossCodeRetargetDoesNotMisAnchor(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"entities":[{"name":"Пункт 1","type":"пункт","description":"разъяснение"},{"name":"статья 5 УК РФ","type":"статья","description":""}],"relations":[{"source":"Пункт 1","target":"статья 5 УК РФ","type":"interprets"}]}`,
		`{"title":"Разъяснения","summary":"пленум"}`,
		`{"entities":[],"relations":[]}`,
		`{"title":"Поправки","summary":"история редакций"}`,
		`{"entities":[],"relations":[]}`,
		`{"title":"Поправки","summary":"история редакций"}`,
	}}
	updater, store := newTestLegalUpdater(t, chat)
	ctx := context.Background()

	plenumChunk := vector.Chunk{
		ID: "c1", RefDocID: "doc1", Text: "1. Разъяснить статью 5 УК РФ ...",
		Metadata: map[string]string{"kind": KindLegalPlenum, "id": "вс-рф/пленум/пост-1/п1"},
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{plenumChunk}); err != nil {
		t.Fatalf("plenum UpdateDocument: %v", err)
	}
	transientID := EntityID("статья 5 УК РФ", "статья")

	indexArticle := func(id, code, num, title string) {
		t.Helper()
		chunk := vector.Chunk{
			ID: id, RefDocID: id, Text: "Статья " + num + ". " + title,
			Metadata: map[string]string{
				"kind": KindLegalArticle, "id": id, "code": code, "article_number": num, "article_title": title,
			},
		}
		if _, err := updater.UpdateDocument(ctx, id, []vector.Chunk{chunk}); err != nil {
			t.Fatalf("UpdateDocument %s: %v", id, err)
		}
	}
	gkCanon := EntityID("гк-рф/ч1/р1/гл2/ст5", KindLegalArticle)
	ukCanon := EntityID("ук-рф/о1/гл1/ст5", KindLegalArticle)

	indexArticle("гк-рф/ч1/р1/гл2/ст5", "гк-рф", "5", "Гражданские права")

	all, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	for _, r := range all {
		if r.Type == "interprets" && r.Dst == gkCanon {
			t.Fatalf("INTERPRETS mis-anchored to ГК РФ canon %q for an УК РФ reference", gkCanon)
		}
	}
	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	transientAlive := false
	for _, e := range entities {
		if e.ID == transientID {
			transientAlive = true
		}
	}
	if !transientAlive {
		t.Fatal("transient УК РФ entity must survive the unrelated ГК РФ article index")
	}

	indexArticle("ук-рф/о1/гл1/ст5", "ук-рф", "5", "Уголовная ответственность")

	all, err = store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	var interprets []graphstore.Relation
	for _, r := range all {
		if r.Type == "interprets" {
			interprets = append(interprets, r)
		}
	}
	if len(interprets) != 1 {
		t.Fatalf("INTERPRETS relations = %+v, want 1", interprets)
	}
	if interprets[0].Dst != ukCanon {
		t.Fatalf("INTERPRETS dst = %q, want УК РФ canon %q", interprets[0].Dst, ukCanon)
	}
}
