package dragon

import (
	"time"

	runbench "github.com/alterfo/kb/internal/bench/run"
)

// Stage is the generic evolution-ladder rung, re-exported for backward
// compatibility with code that referenced the DRAGON-local type.
type Stage = runbench.Stage

// evolutionStages returns the generic evolution ladder.
func evolutionStages() []Stage {
	return runbench.EvolutionStages()
}

// maxQuestionCount returns the generic budget-fitting question count.
func maxQuestionCount(secondsPerQuestion float64, budget time.Duration, poolSize int) int {
	return runbench.MaxQuestionCount(secondsPerQuestion, budget, poolSize)
}
