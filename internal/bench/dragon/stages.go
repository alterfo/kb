package dragon

import "time"

// defaultAnswerBudget is the default per-run budget used to derive the fixed
// question count for the evolution ladder. It leaves headroom under the 2h/run
// ceiling that the DRAGON bench plan enforces.
const defaultAnswerBudget = 90 * time.Minute

// Stage describes one rung of the DRAGON evolution ladder. Each stage is the
// previous stage plus exactly one capability. EnvOverrides are passed through
// config.LoadEnv to produce the answering environment; AnswerMode selects the
// naive or Graph-of-Thoughts path; PersistDir points at the already-indexed
// corpus (persist-a has no graph, persist-b has graph extraction enabled).
type Stage struct {
	Name         string
	EnvOverrides map[string]string
	AnswerMode   string
	PersistDir   string
}

// evolutionStages returns the 7 cumulative stages from the plan Overview:
// Native -> Hybrid -> Graph -> Rerank -> Logic -> Temporal -> Qualifiers.
func evolutionStages() []Stage {
	return []Stage{
		{
			Name:       "native",
			AnswerMode: "naive",
			PersistDir: "persist-a",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH": "false",
				"KB_HYBRID":      "false",
			},
		},
		{
			Name:       "hybrid",
			AnswerMode: "naive",
			PersistDir: "persist-a",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH": "false",
				"KB_HYBRID":      "true",
			},
		},
		{
			Name:       "graph",
			AnswerMode: "naive",
			PersistDir: "persist-b",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH": "true",
				"KB_HYBRID":      "true",
			},
		},
		{
			Name:       "rerank",
			AnswerMode: "naive",
			PersistDir: "persist-b",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH": "true",
				"KB_HYBRID":      "true",
				"KB_RERANK":      "llm",
			},
		},
		{
			Name:       "logic",
			AnswerMode: "got",
			PersistDir: "persist-b",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH": "true",
				"KB_HYBRID":      "true",
				"KB_RERANK":      "llm",
			},
		},
		{
			Name:       "temporal",
			AnswerMode: "got",
			PersistDir: "persist-b",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH":           "true",
				"KB_HYBRID":                "true",
				"KB_RERANK":                "llm",
				"KB_SUPERSEDE_MODE":        "strict",
				"KB_DETECT_CONTRADICTIONS": "true",
			},
		},
		{
			Name:       "qualifiers",
			AnswerMode: "got",
			PersistDir: "persist-b",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH":           "true",
				"KB_HYBRID":                "true",
				"KB_RERANK":                "llm",
				"KB_SUPERSEDE_MODE":        "strict",
				"KB_DETECT_CONTRADICTIONS": "true",
				"KB_QUALIFIER_FILTER":      "true",
			},
		},
	}
}

// maxQuestionCount returns the largest fixed question count that keeps the
// heaviest stage's full answer run under budget given a measured
// seconds-per-question rate, capped at poolSize (the matched-question-pool
// size). Non-positive inputs yield zero rather than an invalid count.
func maxQuestionCount(secondsPerQuestion float64, budget time.Duration, poolSize int) int {
	if secondsPerQuestion <= 0 || poolSize <= 0 {
		return 0
	}
	secs := budget.Seconds()
	if secs <= 0 {
		return 0
	}
	n := int(secs / secondsPerQuestion)
	for n > 0 && float64(n)*secondsPerQuestion > secs {
		n--
	}
	if n > poolSize {
		n = poolSize
	}
	return n
}
