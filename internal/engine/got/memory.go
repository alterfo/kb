package got

import (
	"fmt"
	"strings"
)

type rollingMemory struct {
	window  int
	entries []subgoalResult
}

func newRollingMemory(window int) *rollingMemory {
	return &rollingMemory{window: window}
}

func (m *rollingMemory) add(r subgoalResult) {
	if m.window <= 0 || len(r.Deps) > 0 {
		return
	}
	m.entries = append(m.entries, r)
	if len(m.entries) > m.window {
		keep := len(m.entries) - m.window
		copy(m.entries, m.entries[keep:])
		m.entries = m.entries[:m.window]
	}
}

func (m *rollingMemory) snapshot() []subgoalResult {
	if len(m.entries) == 0 {
		return nil
	}
	return append([]subgoalResult(nil), m.entries...)
}

func mergeRollingMemory(deps []subgoalResult, memory []subgoalResult) []subgoalResult {
	out := make([]subgoalResult, 0, len(deps)+len(memory))
	seen := make(map[string]bool, len(deps)+len(memory))
	for _, d := range deps {
		key := rollingMemoryKey(d)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	for _, m := range memory {
		key := rollingMemoryKey(m)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	return out
}

func rollingMemoryKey(r subgoalResult) string {
	if r.ID != "" {
		return r.ID
	}
	return r.Query
}

func formatRollingMemoryContext(deps []subgoalResult, memory []subgoalResult) string {
	entries := mergeRollingMemory(deps, memory)
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Previously resolved sub-answers:\n")
	for _, d := range entries {
		fmt.Fprintf(&b, "- %s: %s\n", d.Query, d.Answer)
	}
	return b.String()
}
