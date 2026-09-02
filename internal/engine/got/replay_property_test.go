package got

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestRunDeterministicReplayAgainstFakeLLM(t *testing.T) {
	decompose := `[{"subquestion":"root"},{"subquestion":"dep","depends_on":[0]},{"subquestion":"tail","depends_on":[0,1]},{"subquestion":"self","depends_on":[3]}]`
	var wantFingerprint string

	for run := 0; run < 8; run++ {
		retriever := &recordingRetriever{}
		cfg := baseConfig()
		cfg.Retriever = retriever
		cfg.Chat = dependencyAwareChat{decompose: decompose}

		g := New(cfg).Run(context.Background(), "replay query")
		fingerprint := replayFingerprint(g, retriever)
		if run == 0 {
			wantFingerprint = fingerprint
			continue
		}
		if fingerprint != wantFingerprint {
			t.Fatalf("run %d diverged from run 0\nfirst: %s\nrun:   %s", run, wantFingerprint, fingerprint)
		}
	}
}

func replayFingerprint(g ThoughtGraph, retriever *recordingRetriever) string {
	nodes := append([]Node(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	var b strings.Builder
	fmt.Fprintf(&b, "final:%s|", g.FinalAnswer)
	for _, n := range nodes {
		if n.Type != NodeSubgoal {
			continue
		}
		deps := append([]string(nil), n.Deps...)
		sort.Strings(deps)
		fmt.Fprintf(&b, "%s|%s|%s|%s;", n.ID, n.Query, n.Answer, strings.Join(deps, ","))
	}

	queries := append([]string(nil), retriever.queries...)
	sort.Strings(queries)
	b.WriteString("queries:")
	b.WriteString(strings.Join(queries, "\x00"))
	return b.String()
}

func TestRunDoesNotEmitEmptyResolvedSubAnswers(t *testing.T) {
	retriever := &recordingRetriever{}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = dependencyAwareChat{decompose: `[{"subquestion":"a","depends_on":[1]},{"subquestion":"b","depends_on":[0]}]`}

	g := New(cfg).Run(context.Background(), "q")
	if g.FinalAnswer == "" {
		t.Fatal("expected a fail-open final answer")
	}
	for _, query := range retriever.queries {
		if strings.Contains(query, "- : ") || strings.Contains(query, "- :\n") {
			t.Fatalf("empty resolved sub-answer leaked into retrieval query:\n%s", query)
		}
	}
}
