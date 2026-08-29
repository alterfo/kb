package got

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestSelectModeGlobalMarkers(t *testing.T) {
	queries := []string{
		"Какие главные темы покрывает база?",
		"Дай обзор основных направлений",
		"What are the main themes?",
		"What topics does the base cover?",
	}
	for _, q := range queries {
		if m := selectMode(q); m != retriever.ModeGlobal {
			t.Fatalf("selectMode(%q) = %s, want global", q, m)
		}
	}
}

func TestSelectModeLocalMarkers(t *testing.T) {
	queries := []string{
		"Что конкретно делает функция Parse?",
		"Как работает алгоритм дедупликации?",
		"В чем разница между local и global?",
		"Explain how the retriever fuses results",
		"Что такое bi-temporal граф?",
	}
	for _, q := range queries {
		if m := selectMode(q); m != retriever.ModeLocal {
			t.Fatalf("selectMode(%q) = %s, want local", q, m)
		}
	}
}

func TestSelectModeDefaultsToDrift(t *testing.T) {
	queries := []string{
		"Какие отношения связывают компанию A и компанию B?",
		"Что говорится о налогах в этом отчете?",
		"Who collaborated with Acme?",
	}
	for _, q := range queries {
		if m := selectMode(q); m != retriever.ModeDrift {
			t.Fatalf("selectMode(%q) = %s, want drift", q, m)
		}
	}
}

func TestDecomposeWithModesAssignsPerSubquery(t *testing.T) {
	o := New(Config{
		Chat: fakeChat{resp: llm.ChatResponse{Content: `["Сколько всего документов", "как работает алгоритм", "что связывает X и Y"]`}},
	})
	specs := o.decomposeWithModes(context.Background(), "q")
	if len(specs) != 3 {
		t.Fatalf("got %d specs, want 3", len(specs))
	}
	want := []retriever.Mode{retriever.ModeSet, retriever.ModeLocal, retriever.ModeDrift}
	for i, w := range want {
		if specs[i].Mode != w {
			t.Fatalf("specs[%d].Mode = %s, want %s (query %q)", i, specs[i].Mode, w, specs[i].Query)
		}
	}
}

func TestRunRoutesModesToRetriever(t *testing.T) {
	var modes []retriever.Mode
	fr := fakeRetriever{
		byQuery: map[string][]vector.ScoredChunk{
			"what are the main themes":   goodChunks("g"),
			"who collaborated with Acme": goodChunks("d"),
		},
		modes: &modes,
	}
	chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
		"You break a user question":         {Content: `["what are the main themes","who collaborated with Acme"]`},
		"Given the original question":       {Content: `[]`},
		"You combine sub-answers":           {Content: "final"},
		"You answer a focused sub-question": {Content: "sub"},
	}}

	cfg := baseConfig()
	cfg.Retriever = fr
	cfg.Chat = chat
	o := New(cfg)

	g := o.Run(context.Background(), "original question")
	if g.FinalAnswer == "" {
		t.Fatal("expected a final answer")
	}
	if len(modes) != 2 {
		t.Fatalf("retriever modes = %v, want [drift global]", modes)
	}
	want := map[retriever.Mode]int{retriever.ModeGlobal: 1, retriever.ModeDrift: 1}
	for _, m := range modes {
		want[m]--
	}
	for m, n := range want {
		if n != 0 {
			t.Fatalf("mode %s used %d times, want 1", m, n)
		}
	}
}

func TestSelectModeSetMarkers(t *testing.T) {
	queries := []string{
		"Сколько инцидентов было в апреле?",
		"How many incidents happened in April?",
		"List all owners of the billing service",
		"Перечисли все команды платформы",
		"What is the number of open tickets?",
	}
	for _, q := range queries {
		if m := selectMode(q); m != retriever.ModeSet {
			t.Fatalf("selectMode(%q) = %s, want set", q, m)
		}
	}
}
