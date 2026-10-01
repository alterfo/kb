package run

import "time"

// defaultAnswerBudget is the default per-run budget used to derive the fixed
// question count for the evolution ladder. It leaves headroom under the 2h/run
// ceiling that the bench plans enforce.
const defaultAnswerBudget = 90 * time.Minute

// Stage describes one rung of the evolution ladder. Each stage is the
// previous stage plus exactly one capability. EnvOverrides are passed through
// config.OverrideEnv to produce the answering environment; AnswerMode selects
// the naive or Graph-of-Thoughts path; PersistDir points at the already-indexed
// corpus (persist-a has no graph, persist-b has graph extraction enabled).
type Stage struct {
	Name         string
	EnvOverrides map[string]string
	AnswerMode   string
	PersistDir   string
}

// evolutionBaseline holds every capability flag the ladder touches, forced to
// its off/default value. config.OverrideEnv builds a stage's environment from
// the CALLER's ambient Env plus the stage's own EnvOverrides, so any flag a
// stage does not mention is inherited from whatever the caller already has
// set - silently breaking the "each rung adds exactly one capability" promise
// for any caller whose environment isn't already a blank slate. Every stage
// below starts from a copy of this baseline so each EnvOverrides map is
// complete, not a delta.
var evolutionBaseline = map[string]string{
	"KB_INDEX_GRAPH":           "false",
	"KB_HYBRID":                "false",
	"KB_LEXICAL_ONLY":          "false",
	"KB_RERANK":                "off",
	"KB_SUPERSEDE_MODE":        "soft",
	"KB_DETECT_CONTRADICTIONS": "false",
	"KB_QUALIFIER_FILTER":      "false",
}

func stageEnv(overrides map[string]string) map[string]string {
	env := make(map[string]string, len(evolutionBaseline))
	for k, v := range evolutionBaseline {
		env[k] = v
	}
	for k, v := range overrides {
		env[k] = v
	}
	return env
}

// EvolutionStages returns the 7 cumulative stages from the evolution ladder:
// Native -> Hybrid -> Graph -> Rerank -> Logic -> Temporal -> Qualifiers.
func EvolutionStages() []Stage {
	return []Stage{
		{
			Name:         "native",
			AnswerMode:   "naive",
			PersistDir:   "persist-a",
			EnvOverrides: stageEnv(nil),
		},
		{
			Name:       "hybrid",
			AnswerMode: "naive",
			PersistDir: "persist-a",
			EnvOverrides: stageEnv(map[string]string{
				"KB_HYBRID": "true",
			}),
		},
		{
			Name:       "graph",
			AnswerMode: "naive",
			PersistDir: "persist-b",
			EnvOverrides: stageEnv(map[string]string{
				"KB_INDEX_GRAPH": "true",
				"KB_HYBRID":      "true",
			}),
		},
		{
			Name:       "rerank",
			AnswerMode: "naive",
			PersistDir: "persist-b",
			EnvOverrides: stageEnv(map[string]string{
				"KB_INDEX_GRAPH": "true",
				"KB_HYBRID":      "true",
				"KB_RERANK":      "llm",
			}),
		},
		{
			Name:       "logic",
			AnswerMode: "got",
			PersistDir: "persist-b",
			EnvOverrides: stageEnv(map[string]string{
				"KB_INDEX_GRAPH": "true",
				"KB_HYBRID":      "true",
				"KB_RERANK":      "llm",
			}),
		},
		{
			Name:       "temporal",
			AnswerMode: "got",
			PersistDir: "persist-b",
			EnvOverrides: stageEnv(map[string]string{
				"KB_INDEX_GRAPH":           "true",
				"KB_HYBRID":                "true",
				"KB_RERANK":                "llm",
				"KB_SUPERSEDE_MODE":        "strict",
				"KB_DETECT_CONTRADICTIONS": "true",
			}),
		},
		{
			Name:       "qualifiers",
			AnswerMode: "got",
			PersistDir: "persist-b",
			EnvOverrides: stageEnv(map[string]string{
				"KB_INDEX_GRAPH":           "true",
				"KB_HYBRID":                "true",
				"KB_RERANK":                "llm",
				"KB_SUPERSEDE_MODE":        "strict",
				"KB_DETECT_CONTRADICTIONS": "true",
				"KB_QUALIFIER_FILTER":      "true",
			}),
		},
	}
}

// MaxQuestionCount returns the largest fixed question count that keeps the
// heaviest stage's full answer run under budget given a measured
// seconds-per-question rate, capped at poolSize (the matched-question-pool
// size). Non-positive inputs yield zero rather than an invalid count.
func MaxQuestionCount(secondsPerQuestion float64, budget time.Duration, poolSize int) int {
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
