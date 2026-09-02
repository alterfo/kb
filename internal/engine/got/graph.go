package got

import (
	"sync"

	"github.com/alterfo/kb/internal/engine/metrics"
)

// Node types.
const (
	NodeDecompose       = "decompose"
	NodePlan            = "plan"
	NodeSubgoal         = "subgoal"
	NodeAggregate       = "aggregate"
	NodeFindGaps        = "find_gaps"
	NodeRefineSubgoal   = "refine_subgoal"
	NodeRefineAggregate = "refine_aggregate"
	NodeFinalize        = "finalize"
)

// Node statuses.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusDone    = "done"
)

// Subgoal stages, tracked on subgoal/refine_subgoal nodes for live progress.
const (
	StageRetrieving      = "retrieving"
	StageScoringCoverage = "scoring_coverage"
	StageSynthesizing    = "synthesizing"
	StageDone            = "done"
)

// Source is a citation, provenance tracked by file_name/file_path so a
// caller can link a claim back to the document it came from.
type Source struct {
	FileName     string `json:"file_name"`
	FilePath     string `json:"file_path"`
	ChunkID      string `json:"chunk_id,omitempty"`
	DocID        string `json:"doc_id,omitempty"`
	SupersededBy string `json:"superseded_by,omitempty"`
}

// Node is one step of the thought graph. It is a plain, JSON-serializable
// value so the whole ThoughtGraph can be streamed as live progress.
type Node struct {
	ID             string   `json:"id"`
	Type           string   `json:"type"`
	ParentID       string   `json:"parent_id,omitempty"`
	Query          string   `json:"query,omitempty"`
	Deps           []string `json:"deps,omitempty"`
	Status         string   `json:"status"`
	Stage          string   `json:"stage,omitempty"`
	Coverage       float64  `json:"coverage,omitempty"`
	Covered        bool     `json:"covered,omitempty"`
	Answer         string   `json:"answer,omitempty"`
	Sources        []Source `json:"sources,omitempty"`
	Contradictions []string `json:"contradictions,omitempty"`
}

// ThoughtGraph is the full record of a Run: every node the orchestrator
// produced plus the final answer and its sources. It has no unexported
// fields, so it marshals to JSON directly and copies safely by value.
type ThoughtGraph struct {
	Query       string         `json:"query"`
	Nodes       []Node         `json:"nodes"`
	Refined     bool           `json:"refined"`
	FinalAnswer string         `json:"final_answer,omitempty"`
	Sources     []Source       `json:"sources,omitempty"`
	Degraded    []string       `json:"degraded,omitempty"`
	Metrics     metrics.Values `json:"metrics"`
}

// ProgressFunc receives a snapshot of the ThoughtGraph after every node
// change. Snapshots are independent copies safe to hold, mutate or
// marshal from another goroutine.
type ProgressFunc func(ThoughtGraph)

// graphBuilder accumulates a ThoughtGraph under a mutex so concurrent
// subgoal branches can update it safely, notifying progress after each
// change.
type graphBuilder struct {
	mu       sync.Mutex
	g        ThoughtGraph
	progress ProgressFunc
}

func newGraphBuilder(query string, progress ProgressFunc) *graphBuilder {
	return &graphBuilder{g: ThoughtGraph{Query: query}, progress: progress}
}

// setNode inserts a new node or replaces an existing one with the same ID.
func (b *graphBuilder) setNode(n Node) {
	b.mu.Lock()
	replaced := false
	for i := range b.g.Nodes {
		if b.g.Nodes[i].ID == n.ID {
			b.g.Nodes[i] = n
			replaced = true
			break
		}
	}
	if !replaced {
		b.g.Nodes = append(b.g.Nodes, n)
	}
	snap := b.snapshotLocked()
	b.mu.Unlock()
	b.notify(snap)
}

// setStage updates only the Stage field of an existing node, leaving the
// rest untouched.
func (b *graphBuilder) setStage(id, stage string) {
	b.mu.Lock()
	for i := range b.g.Nodes {
		if b.g.Nodes[i].ID == id {
			b.g.Nodes[i].Stage = stage
			break
		}
	}
	snap := b.snapshotLocked()
	b.mu.Unlock()
	b.notify(snap)
}

func (b *graphBuilder) setFinal(refined bool, answer string, sources []Source) {
	b.mu.Lock()
	b.g.Refined = refined
	b.g.FinalAnswer = answer
	b.g.Sources = append([]Source(nil), sources...)
	snap := b.snapshotLocked()
	b.mu.Unlock()
	b.notify(snap)
}

func (b *graphBuilder) snapshotLocked() ThoughtGraph {
	return ThoughtGraph{
		Query:       b.g.Query,
		Nodes:       append([]Node(nil), b.g.Nodes...),
		Refined:     b.g.Refined,
		FinalAnswer: b.g.FinalAnswer,
		Sources:     append([]Source(nil), b.g.Sources...),
		Degraded:    append([]string(nil), b.g.Degraded...),
		Metrics:     b.g.Metrics,
	}
}

func (b *graphBuilder) snapshot() ThoughtGraph {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

func (b *graphBuilder) notify(snap ThoughtGraph) {
	if b.progress != nil {
		b.progress(snap)
	}
}
