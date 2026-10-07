package web

import (
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/engine/got"
)

func TestAskGraphViewRendersMarkdownAndSplitsThink(t *testing.T) {
	g := got.ThoughtGraph{
		Nodes:       []got.Node{{ID: "a", Answer: "<think>hmm</think>**bold**"}},
		FinalAnswer: "<think>plan</think>- item",
	}
	v := newAskGraphView(g)
	if !strings.Contains(string(v.Nodes[0].AnswerHTML), "<strong>bold</strong>") || strings.Contains(v.Nodes[0].Answer, "think") {
		t.Fatalf("node: %+v", v.Nodes[0])
	}
	if !strings.Contains(string(v.Nodes[0].ReasoningHTML), "hmm") {
		t.Fatalf("node reasoning: %+v", v.Nodes[0])
	}
	if !strings.Contains(string(v.FinalAnswerHTML), "<li>item</li>") || !strings.Contains(string(v.FinalReasoningHTML), "plan") {
		t.Fatalf("final: %+v", v)
	}
	b, err := marshalAskGraph(g)
	if err != nil || !strings.Contains(string(b), `"final_answer_html"`) || !strings.Contains(string(b), `"nodes"`) {
		t.Fatalf("json: %s %v", b, err)
	}
}
