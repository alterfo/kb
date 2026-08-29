package planner

import (
	_ "embed"
	"strings"
)

//go:embed prompts/task.txt
var promptTask string

//go:embed prompts/review.txt
var promptReview string

//go:embed prompts/make_plan.txt
var promptMakePlan string

// prompts holds the prompt templates. Fields are overridable in tests so
// that the runner can be exercised without the embedded text.
type prompts struct {
	task     string
	review   string
	makePlan string
}

func defaultPrompts() prompts {
	return prompts{
		task:     promptTask,
		review:   promptReview,
		makePlan: promptMakePlan,
	}
}

func render(tmpl string, vars map[string]string) string {
	out := tmpl
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}
