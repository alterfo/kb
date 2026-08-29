package graph

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

// routingChat answers by system-prompt category: small-talk classification,
// chat decision extraction, and everything else (generic extraction /
// summarization) separately, so tests can prove which pipeline ran.
type routingChat struct {
	smallTalk bool
	smallErr  error
	chatResp  string
	chatErr   error
	generic   string
	calls     []llm.ChatRequest
}

func (c *routingChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	c.calls = append(c.calls, req)
	if len(req.Messages) == 0 {
		return llm.ChatResponse{}, errors.New("no messages")
	}
	sys := req.Messages[0].Content
	switch {
	case strings.Contains(sys, "DECIDED"):
		// Checked first: the chat decision prompt also mentions small talk.
		if c.chatErr != nil {
			return llm.ChatResponse{}, c.chatErr
		}
		return llm.ChatResponse{Content: c.chatResp}, nil
	case strings.Contains(sys, "small talk"):
		if c.smallErr != nil {
			return llm.ChatResponse{}, c.smallErr
		}
		return llm.ChatResponse{Content: fmt.Sprintf(`{"is_smalltalk": %v}`, c.smallTalk)}, nil
	default:
		return llm.ChatResponse{Content: c.generic}, nil
	}
}

func chatChunk(id, user, ts, text string) vector.Chunk {
	return vector.Chunk{
		ID:       id,
		RefDocID: "doc1",
		Text:     text,
		Metadata: map[string]string{
			"kind":      KindChatMessage,
			"user":      user,
			"ts":        ts,
			"thread_id": "t1",
		},
	}
}

const (
	proposedPostgresJSON = `{"entities":[{"name":"Postgres","type":"topic","description":"база данных"}],"relations":[{"source":"alice","target":"Postgres","type":"PROPOSED"}]}`
	decidedPostgresJSON  = `{"entities":[{"name":"Postgres","type":"topic","description":"база данных"}],"relations":[{"source":"bob","target":"Postgres","type":"DECIDED"}]}`
	decidedK8sJSON       = `{"entities":[{"name":"Kubernetes","type":"topic","description":"оркестратор"}],"relations":[{"source":"alice","target":"Kubernetes","type":"DECIDED"}]}`
	chatSummaryJSON      = `{"title":"T","summary":"S"}`
)

type twoDecisionsChat struct {
	calls int
}

func (c *twoDecisionsChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if len(req.Messages) == 0 {
		return llm.ChatResponse{}, errors.New("no messages")
	}
	sys := req.Messages[0].Content
	switch {
	case strings.Contains(sys, "DECIDED"):
		c.calls++
		if c.calls == 1 {
			return llm.ChatResponse{Content: decidedPostgresJSON}, nil
		}
		return llm.ChatResponse{Content: decidedK8sJSON}, nil
	case strings.Contains(sys, "small talk"):
		return llm.ChatResponse{Content: `{"is_smalltalk": false}`}, nil
	default:
		return llm.ChatResponse{Content: chatSummaryJSON}, nil
	}
}

func TestChatExtractorThreadMiniGraphPreservesThread(t *testing.T) {
	t1 := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(5 * time.Minute)
	chat := &scriptedChat{responses: []string{proposedPostgresJSON, decidedPostgresJSON}}
	e := NewChatExtractor(chat, "model")
	ctx := context.Background()

	chunks := []vector.Chunk{
		chatChunk("c1", "alice", t1.Format(time.RFC3339), "Предлагаю перейти на Postgres"),
		chatChunk("c2", "bob", t2.Format(time.RFC3339), "Согласен, переходим на Postgres"),
	}
	ents, rels, err := e.ExtractThread(ctx, chunks)
	if err != nil {
		t.Fatalf("ExtractThread: %v", err)
	}
	if len(chat.calls) != 2 {
		t.Fatalf("Chat calls = %d, want 2 (one per non-filtered chunk)", len(chat.calls))
	}
	if sys := chat.calls[0].Messages[0].Content; sys == extractionSystemPrompt {
		t.Fatal("chat decision prompt must differ from the generic extraction prompt")
	}

	if len(ents) != 3 {
		t.Fatalf("entities = %+v, want 3 (alice, bob, Postgres)", ents)
	}
	byName := map[string]graphstore.Entity{}
	for _, en := range ents {
		byName[en.Name] = en
	}
	for _, want := range []string{"alice", "bob", "Postgres"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("entity %q missing: %+v", want, ents)
		}
	}
	if byName["alice"].Type != "chat-user" || byName["bob"].Type != "chat-user" || byName["Postgres"].Type != "topic" {
		t.Fatalf("entity types wrong: %+v", ents)
	}
	if len(byName["alice"].SourceChunks) != 1 || byName["alice"].SourceChunks[0] != "c1" {
		t.Fatalf("alice SourceChunks = %v, want [c1]", byName["alice"].SourceChunks)
	}
	if len(byName["bob"].SourceChunks) != 1 || byName["bob"].SourceChunks[0] != "c2" {
		t.Fatalf("bob SourceChunks = %v, want [c2]", byName["bob"].SourceChunks)
	}
	if len(byName["Postgres"].SourceChunks) != 2 {
		t.Fatalf("Postgres SourceChunks = %v, want both chunks (deduped topic)", byName["Postgres"].SourceChunks)
	}

	if len(rels) != 2 {
		t.Fatalf("relations = %+v, want 2 (PROPOSED + DECIDED)", rels)
	}
	byType := map[string]graphstore.Relation{}
	for _, r := range rels {
		byType[r.Type] = r
	}
	proposed, ok := byType["PROPOSED"]
	if !ok || proposed.Src != byName["alice"].ID || proposed.Dst != byName["Postgres"].ID {
		t.Fatalf("PROPOSED = %+v, want alice->Postgres", proposed)
	}
	if proposed.ValidFrom == nil || !proposed.ValidFrom.Equal(t1) {
		t.Fatalf("PROPOSED ValidFrom = %v, want %v (message timestamp)", proposed.ValidFrom, t1)
	}
	decided, ok := byType["DECIDED"]
	if !ok || decided.Src != byName["bob"].ID || decided.Dst != byName["Postgres"].ID {
		t.Fatalf("DECIDED = %+v, want bob->Postgres", decided)
	}
	if decided.ValidFrom == nil || !decided.ValidFrom.Equal(t2) {
		t.Fatalf("DECIDED ValidFrom = %v, want %v (message timestamp)", decided.ValidFrom, t2)
	}
	if len(proposed.SourceChunks) != 1 || proposed.SourceChunks[0] != "c1" {
		t.Fatalf("PROPOSED SourceChunks = %v, want [c1]", proposed.SourceChunks)
	}
	if len(decided.SourceChunks) != 1 || decided.SourceChunks[0] != "c2" {
		t.Fatalf("DECIDED SourceChunks = %v, want [c2]", decided.SourceChunks)
	}
}

func TestChatExtractorSmallTalkFilterHeuristic(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: decidedPostgresJSON}}
	e := NewChatExtractor(chat, "model")
	ctx := context.Background()

	chunks := []vector.Chunk{
		chatChunk("c1", "alice", "2026-01-10T10:00:00Z", "ок"),
		chatChunk("c2", "bob", "2026-01-10T10:05:00Z", "Согласен, переходим на Postgres"),
	}
	ents, rels, err := e.ExtractThread(ctx, chunks)
	if err != nil {
		t.Fatalf("ExtractThread: %v", err)
	}
	if len(chat.calls) != 1 {
		t.Fatalf("Chat calls = %d, want 1 (small-talk chunk must not call the LLM)", len(chat.calls))
	}
	for _, en := range ents {
		if en.Name == "alice" {
			t.Fatalf("small-talk chunk 'ок' must not contribute a speaker entity: %+v", ents)
		}
	}
	if len(rels) != 1 || rels[0].Type != "DECIDED" {
		t.Fatalf("relations = %+v, want only the substantive chunk's DECIDED edge", rels)
	}
}

func TestChatExtractorSmallTalkLLMClassify(t *testing.T) {
	chat := &routingChat{smallTalk: true, chatResp: decidedPostgresJSON, generic: chatSummaryJSON}
	e := NewChatExtractor(chat, "model")
	e.Classify = true
	ctx := context.Background()

	// Long enough to pass the length heuristic, classified small-talk by LLM.
	chunks := []vector.Chunk{chatChunk("c1", "alice", "2026-01-10T10:00:00Z", "Спасибо за помощь вчера вечером")}
	ents, rels, err := e.ExtractThread(ctx, chunks)
	if err != nil {
		t.Fatalf("ExtractThread: %v", err)
	}
	if len(ents) != 0 || len(rels) != 0 {
		t.Fatalf("LLM-classified small talk must be filtered: entities=%+v relations=%+v", ents, rels)
	}
}

func TestChatExtractorSmallTalkClassifyFailOpen(t *testing.T) {
	chat := &routingChat{smallErr: errors.New("llm down"), chatResp: decidedPostgresJSON, generic: chatSummaryJSON}
	e := NewChatExtractor(chat, "model")
	e.Classify = true
	ctx := context.Background()

	chunks := []vector.Chunk{chatChunk("c1", "alice", "2026-01-10T10:00:00Z", "Предлагаю перейти на Postgres")}
	ents, rels, err := e.ExtractThread(ctx, chunks)
	if err != nil {
		t.Fatalf("ExtractThread: %v", err)
	}
	// Classifier failure must not filter the chunk (fail-open): the
	// deterministic speaker and the decision edge survive.
	if len(ents) != 2 || len(rels) != 1 {
		t.Fatalf("classifier failure must keep the chunk: entities=%+v relations=%+v", ents, rels)
	}
}

func TestChatExtractorDecisionEdgeTemporalValidity(t *testing.T) {
	ref := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	secs := ref.Unix()
	millis := ref.UnixMilli()
	cases := []struct {
		name string
		ts   string
		want *time.Time
	}{
		{"rfc3339", "2026-01-10T10:00:00Z", &ref},
		{"unix-seconds", fmt.Sprintf("%d", secs), &ref},
		{"unix-millis", fmt.Sprintf("%d", millis), &ref},
		{"unparseable", "soon", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chat := &fakeChat{resp: llm.ChatResponse{Content: decidedPostgresJSON}}
			e := NewChatExtractor(chat, "model")
			chunks := []vector.Chunk{chatChunk("c1", "alice", tc.ts, "Предлагаю перейти на Postgres")}
			_, rels, err := e.ExtractThread(context.Background(), chunks)
			if err != nil {
				t.Fatalf("ExtractThread: %v", err)
			}
			if tc.want == nil {
				if len(rels) != 1 || rels[0].ValidFrom != nil {
					t.Fatalf("relations = %+v, want un-stamped edge for unparseable ts", rels)
				}
				return
			}
			if len(rels) != 1 || rels[0].ValidFrom == nil || !rels[0].ValidFrom.Equal(*tc.want) {
				t.Fatalf("relations = %+v, want ValidFrom %v", rels, tc.want)
			}
		})
	}
}

func TestChatExtractorGluedThreadAttribution(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: decidedPostgresJSON}}
	e := NewChatExtractor(chat, "model")
	ctx := context.Background()

	// A single glued chunk carrying both speakers: the LLM attributes the
	// DECIDED edge to bob, and the edge must be stamped with bob's
	// timestamp, not the root message's.
	chunks := []vector.Chunk{{
		ID:       "c1",
		RefDocID: "doc1",
		Text:     "alice: Предлагаю перейти на Postgres\n\nbob: Согласен, переходим",
		Metadata: map[string]string{
			"kind":      KindChatMessage,
			"user":      "alice",
			"ts":        "2026-01-10T10:00:00Z",
			"thread_id": "t1",
			"speakers":  `[{"user":"alice","ts":"2026-01-10T10:00:00Z"},{"user":"bob","ts":"2026-01-10T10:05:00Z"}]`,
		},
	}}
	ents, rels, err := e.ExtractThread(ctx, chunks)
	if err != nil {
		t.Fatalf("ExtractThread: %v", err)
	}
	byName := map[string]graphstore.Entity{}
	for _, en := range ents {
		byName[en.Name] = en
	}
	if _, ok := byName["alice"]; !ok {
		t.Fatalf("missing alice entity: %+v", ents)
	}
	if _, ok := byName["bob"]; !ok {
		t.Fatalf("missing bob entity: %+v", ents)
	}
	if len(rels) != 1 {
		t.Fatalf("relations = %+v, want 1 DECIDED edge", rels)
	}
	if rels[0].Src != byName["bob"].ID {
		t.Fatalf("DECIDED src = %v, want bob", rels[0].Src)
	}
	want := time.Date(2026, 1, 10, 10, 5, 0, 0, time.UTC)
	if rels[0].ValidFrom == nil || !rels[0].ValidFrom.Equal(want) {
		t.Fatalf("DECIDED ValidFrom = %v, want bob's message timestamp %v", rels[0].ValidFrom, want)
	}
	if len(byName["alice"].SourceChunks) != 1 || byName["alice"].SourceChunks[0] != "c1" {
		t.Fatalf("alice SourceChunks = %v, want [c1]", byName["alice"].SourceChunks)
	}
	if len(byName["bob"].SourceChunks) != 1 || byName["bob"].SourceChunks[0] != "c1" {
		t.Fatalf("bob SourceChunks = %v, want [c1]", byName["bob"].SourceChunks)
	}
}

func TestChatExtractorDropsNonDecisionEdgeTypes(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{
		"entities":[
			{"name":"Postgres","type":"topic"},
			{"name":"MySQL","type":"topic"}
		],
		"relations":[
			{"source":"alice","target":"Postgres","type":"DECIDED"},
			{"source":"alice","target":"MySQL","type":"knows"}
		]
	}`}}
	e := NewChatExtractor(chat, "model")
	_, rels, err := e.ExtractThread(context.Background(), []vector.Chunk{chatChunk("c1", "alice", "2026-01-10T10:00:00Z", "Предлагаю перейти на Postgres")})
	if err != nil {
		t.Fatalf("ExtractThread: %v", err)
	}
	if len(rels) != 1 || rels[0].Type != "DECIDED" {
		t.Fatalf("relations = %+v, want only the DECIDED edge (non-decision types dropped)", rels)
	}
}

func TestChatExtractorFailOpenBrokenJSONAndTransportError(t *testing.T) {
	for name, chat := range map[string]*fakeChat{
		"broken json": {resp: llm.ChatResponse{Content: "не json"}},
		"llm down":    {err: errors.New("boom")},
	} {
		t.Run(name, func(t *testing.T) {
			e := NewChatExtractor(chat, "model")
			ents, rels, err := e.ExtractThread(context.Background(), []vector.Chunk{chatChunk("c1", "alice", "2026-01-10T10:00:00Z", "Предлагаю перейти на Postgres")})
			if err != nil {
				t.Fatalf("ExtractThread must fail open, got %v", err)
			}
			if len(ents) != 1 || ents[0].Type != "chat-user" || ents[0].Name != "alice" {
				t.Fatalf("deterministic speaker entity must survive: %+v", ents)
			}
			if len(rels) != 0 {
				t.Fatalf("relations = %+v, want none without a usable LLM response", rels)
			}
		})
	}
}

func TestChatExtractorUnknownSpeakerWithoutUser(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: decidedPostgresJSON}}
	e := NewChatExtractor(chat, "model")
	chunk := chatChunk("c1", "", "2026-01-10T10:00:00Z", "Предлагаю перейти на Postgres")
	ents, _, err := e.ExtractThread(context.Background(), []vector.Chunk{chunk})
	if err != nil {
		t.Fatalf("ExtractThread: %v", err)
	}
	found := false
	for _, en := range ents {
		if en.Type == "chat-user" && en.Name == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing fallback speaker entity: %+v", ents)
	}
}

func newChatUpdater(t *testing.T, chat ChatClient) (*GraphUpdater, graphstore.Store) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store := sqlite.NewGraphStore(db)
	updater := NewGraphUpdater(store, NewExtractor(chat, "model"), NewSummarizer(chat, "model")).
		WithChatExtractor(NewChatExtractor(chat, "model"))
	return updater, store
}

func TestUpdateDocumentChatPathUsesChatExtractor(t *testing.T) {
	chat := &routingChat{chatResp: decidedPostgresJSON, generic: chatSummaryJSON}
	updater, store := newChatUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{chatChunk("c1", "alice", "2026-01-10T10:00:00Z", "Предлагаю перейти на Postgres")}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	for _, req := range chat.calls {
		if len(req.Messages) > 0 && req.Messages[0].Content == extractionSystemPrompt {
			t.Fatal("chat document must not go through the generic extractor")
		}
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("entities = %+v, want 2 (speaker + topic)", entities)
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 1 || relations[0].Type != "DECIDED" {
		t.Fatalf("relations = %+v, want one DECIDED edge", relations)
	}
	want := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	if relations[0].ValidFrom == nil || !relations[0].ValidFrom.Equal(want) {
		t.Fatalf("DECIDED ValidFrom = %v, want %v", relations[0].ValidFrom, want)
	}
}

func TestUpdateDocumentChatReindexNoDuplicates(t *testing.T) {
	chat := &routingChat{chatResp: decidedPostgresJSON, generic: chatSummaryJSON}
	updater, store := newChatUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{chatChunk("c1", "alice", "2026-01-10T10:00:00Z", "Предлагаю перейти на Postgres")}
	for i := 0; i < 2; i++ {
		if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
			t.Fatalf("UpdateDocument #%d: %v", i+1, err)
		}
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("entities after reindex = %+v, want 2 (no duplicates)", entities)
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 1 {
		t.Fatalf("relations after reindex = %+v, want 1 (no duplicates)", relations)
	}
	if len(relations[0].SourceChunks) != 1 || relations[0].SourceChunks[0] != "c1" {
		t.Fatalf("relation SourceChunks = %v, want [c1] (not accumulated)", relations[0].SourceChunks)
	}
}

func TestUpdateDocumentChatDecisionsDoNotCloseEachOther(t *testing.T) {
	t1 := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(2 * time.Hour)
	chat := &twoDecisionsChat{}
	updater, store := newChatUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{
		chatChunk("c1", "alice", t1.Format(time.RFC3339), "Решили перейти на Postgres"),
		chatChunk("c2", "alice", t2.Format(time.RFC3339), "Решили использовать Kubernetes"),
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 2 {
		t.Fatalf("relations = %+v, want 2 DECIDED edges (a later decision must not close an earlier one)", relations)
	}
	for _, r := range relations {
		if r.Type != "DECIDED" || r.ValidTo != nil {
			t.Fatalf("relation = %+v, want an open DECIDED edge", r)
		}
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	aliceID := ""
	topicIDs := map[string]string{}
	for _, en := range entities {
		if en.Type == "chat-user" {
			aliceID = en.ID
		} else {
			topicIDs[en.Name] = en.ID
		}
	}
	if aliceID == "" || len(topicIDs) != 2 {
		t.Fatalf("entities = %+v, want alice + Postgres + Kubernetes", entities)
	}

	neighbors, rels, err := store.Neighbors(ctx, aliceID, 1)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	got := map[string]bool{}
	for _, n := range neighbors {
		got[n.ID] = true
	}
	for name, id := range topicIDs {
		if !got[id] {
			t.Fatalf("neighbor expansion misses decision about %s (%s)", name, id)
		}
	}
	if len(rels) != 2 {
		t.Fatalf("neighbor relations = %d, want 2", len(rels))
	}
}

func TestUpdateDocumentChatRoutingFallbackByThreadID(t *testing.T) {
	chat := &routingChat{chatResp: decidedPostgresJSON, generic: chatSummaryJSON}
	updater, store := newChatUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{{
		ID:       "c1",
		RefDocID: "doc1",
		Text:     "Предлагаю перейти на Postgres",
		Metadata: map[string]string{
			"thread_id": "t1", // kind metadata absent: ChatChunker marker routes chat
			"user":      "alice",
			"ts":        "2026-01-10T10:00:00Z",
		},
	}}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 1 || relations[0].Type != "DECIDED" {
		t.Fatalf("relations = %+v, want chat path (thread_id fallback) to produce a DECIDED edge", relations)
	}
}

func TestUpdateDocumentChatFallsBackToGenericExtractor(t *testing.T) {
	chat := &scriptedChat{responses: []string{aliceKnowsBobJSON(), chatSummaryJSON}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{chatChunk("c1", "alice", "2026-01-10T10:00:00Z", "Предлагаю перейти на Postgres")}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}
	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("entities = %+v, want generic extraction result (2 people)", entities)
	}
}
