package web

import (
	"encoding/json"
	"html/template"
	"regexp"
	"strings"

	"github.com/alterfo/kb/internal/engine/got"
)

var thinkRe = regexp.MustCompile(`(?s)<think>(.*?)(?:</think>|$)`)

func splitThink(s string) (answer, reasoning string) {
	var parts []string
	answer = thinkRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := thinkRe.FindStringSubmatch(m)
		if t := strings.TrimSpace(sub[1]); t != "" {
			parts = append(parts, t)
		}
		return ""
	})
	return strings.TrimSpace(answer), strings.Join(parts, "\n\n")
}

type askNodeView struct {
	got.Node
	AnswerHTML    template.HTML `json:"answer_html,omitempty"`
	ReasoningHTML template.HTML `json:"reasoning_html,omitempty"`
}

type askGraphView struct {
	got.ThoughtGraph
	Nodes              []askNodeView `json:"nodes"`
	FinalAnswerHTML    template.HTML `json:"final_answer_html,omitempty"`
	FinalReasoningHTML template.HTML `json:"final_reasoning_html,omitempty"`
}

func newAskGraphView(g got.ThoughtGraph) askGraphView {
	v := askGraphView{ThoughtGraph: g, Nodes: make([]askNodeView, 0, len(g.Nodes))}
	for _, n := range g.Nodes {
		nv := askNodeView{Node: n}
		if n.Answer != "" {
			ans, think := splitThink(n.Answer)
			nv.Answer = ans
			nv.AnswerHTML = renderMarkdown(ans)
			if think != "" {
				nv.ReasoningHTML = renderMarkdown(think)
			}
		}
		v.Nodes = append(v.Nodes, nv)
	}
	if g.FinalAnswer != "" {
		ans, think := splitThink(g.FinalAnswer)
		v.FinalAnswer = ans
		v.FinalAnswerHTML = renderMarkdown(ans)
		if think != "" {
			v.FinalReasoningHTML = renderMarkdown(think)
		}
	}
	return v
}

func marshalAskGraph(g got.ThoughtGraph) ([]byte, error) {
	return json.Marshal(newAskGraphView(g))
}

func askGraphJSONToJS(raw string) template.JS {
	var g got.ThoughtGraph
	if json.Unmarshal([]byte(raw), &g) != nil {
		return "null"
	}
	b, err := marshalAskGraph(g)
	if err != nil {
		return "null"
	}
	return template.JS(b)
}

func renderAnswer(s string) (answer, reasoning template.HTML) {
	ans, think := splitThink(s)
	answer = renderMarkdown(ans)
	if think != "" {
		reasoning = renderMarkdown(think)
	}
	return answer, reasoning
}
