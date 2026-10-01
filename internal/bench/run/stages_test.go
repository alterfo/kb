package run

import (
	"reflect"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/config"
)

func TestEvolutionStages(t *testing.T) {
	stages := EvolutionStages()
	if len(stages) != 7 {
		t.Fatalf("len(EvolutionStages) = %d, want 7", len(stages))
	}

	want := []Stage{
		{
			Name:       "native",
			AnswerMode: "naive",
			PersistDir: "persist-a",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH":           "false",
				"KB_HYBRID":                "false",
				"KB_LEXICAL_ONLY":          "false",
				"KB_RERANK":                "off",
				"KB_SUPERSEDE_MODE":        "soft",
				"KB_DETECT_CONTRADICTIONS": "false",
				"KB_QUALIFIER_FILTER":      "false",
			},
		},
		{
			Name:       "hybrid",
			AnswerMode: "naive",
			PersistDir: "persist-a",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH":           "false",
				"KB_HYBRID":                "true",
				"KB_LEXICAL_ONLY":          "false",
				"KB_RERANK":                "off",
				"KB_SUPERSEDE_MODE":        "soft",
				"KB_DETECT_CONTRADICTIONS": "false",
				"KB_QUALIFIER_FILTER":      "false",
			},
		},
		{
			Name:       "graph",
			AnswerMode: "naive",
			PersistDir: "persist-b",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH":           "true",
				"KB_HYBRID":                "true",
				"KB_LEXICAL_ONLY":          "false",
				"KB_RERANK":                "off",
				"KB_SUPERSEDE_MODE":        "soft",
				"KB_DETECT_CONTRADICTIONS": "false",
				"KB_QUALIFIER_FILTER":      "false",
			},
		},
		{
			Name:       "rerank",
			AnswerMode: "naive",
			PersistDir: "persist-b",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH":           "true",
				"KB_HYBRID":                "true",
				"KB_LEXICAL_ONLY":          "false",
				"KB_RERANK":                "llm",
				"KB_SUPERSEDE_MODE":        "soft",
				"KB_DETECT_CONTRADICTIONS": "false",
				"KB_QUALIFIER_FILTER":      "false",
			},
		},
		{
			Name:       "logic",
			AnswerMode: "got",
			PersistDir: "persist-b",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH":           "true",
				"KB_HYBRID":                "true",
				"KB_LEXICAL_ONLY":          "false",
				"KB_RERANK":                "llm",
				"KB_SUPERSEDE_MODE":        "soft",
				"KB_DETECT_CONTRADICTIONS": "false",
				"KB_QUALIFIER_FILTER":      "false",
			},
		},
		{
			Name:       "temporal",
			AnswerMode: "got",
			PersistDir: "persist-b",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH":           "true",
				"KB_HYBRID":                "true",
				"KB_LEXICAL_ONLY":          "false",
				"KB_RERANK":                "llm",
				"KB_SUPERSEDE_MODE":        "strict",
				"KB_DETECT_CONTRADICTIONS": "true",
				"KB_QUALIFIER_FILTER":      "false",
			},
		},
		{
			Name:       "qualifiers",
			AnswerMode: "got",
			PersistDir: "persist-b",
			EnvOverrides: map[string]string{
				"KB_INDEX_GRAPH":           "true",
				"KB_HYBRID":                "true",
				"KB_LEXICAL_ONLY":          "false",
				"KB_RERANK":                "llm",
				"KB_SUPERSEDE_MODE":        "strict",
				"KB_DETECT_CONTRADICTIONS": "true",
				"KB_QUALIFIER_FILTER":      "true",
			},
		},
	}

	for i := range want {
		t.Run(want[i].Name, func(t *testing.T) {
			got := stages[i]
			if got.Name != want[i].Name {
				t.Errorf("Name = %q, want %q", got.Name, want[i].Name)
			}
			if got.AnswerMode != want[i].AnswerMode {
				t.Errorf("AnswerMode = %q, want %q", got.AnswerMode, want[i].AnswerMode)
			}
			if got.PersistDir != want[i].PersistDir {
				t.Errorf("PersistDir = %q, want %q", got.PersistDir, want[i].PersistDir)
			}
			if !reflect.DeepEqual(got.EnvOverrides, want[i].EnvOverrides) {
				t.Errorf("EnvOverrides = %v, want %v", got.EnvOverrides, want[i].EnvOverrides)
			}
		})
	}
}

func TestEvolutionStagesKeySetNeverShrinks(t *testing.T) {
	stages := EvolutionStages()
	for i := 1; i < len(stages); i++ {
		prev := stages[i-1].EnvOverrides
		for k := range prev {
			if _, ok := stages[i].EnvOverrides[k]; !ok {
				t.Errorf("stage %d dropped override %s from the previous stage", i, k)
			}
		}
	}
}

func TestEvolutionStagesDoNotLeakAmbientCapabilities(t *testing.T) {
	ambient, err := config.LoadEnv(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	ambient.Rerank = "llm"
	ambient.SupersedeMode = "strict"
	ambient.DetectContradictions = true
	ambient.QualifierFilter = true
	ambient.LexicalOnly = true
	ambient.Hybrid = true
	ambient.IndexGraph = true

	native := EvolutionStages()[0]
	got, err := config.OverrideEnv(ambient, native.EnvOverrides)
	if err != nil {
		t.Fatalf("OverrideEnv: %v", err)
	}
	if got.Rerank != "off" {
		t.Errorf("stage %q Rerank = %q, want off despite ambient KB_RERANK=llm", native.Name, got.Rerank)
	}
	if got.SupersedeMode != "soft" {
		t.Errorf("stage %q SupersedeMode = %q, want soft despite ambient strict", native.Name, got.SupersedeMode)
	}
	if got.DetectContradictions {
		t.Errorf("stage %q DetectContradictions = true, want false despite ambient true", native.Name)
	}
	if got.QualifierFilter {
		t.Errorf("stage %q QualifierFilter = true, want false despite ambient true", native.Name)
	}
	if got.LexicalOnly {
		t.Errorf("stage %q LexicalOnly = true, want false despite ambient true", native.Name)
	}
	if got.Hybrid {
		t.Errorf("stage %q Hybrid = true, want false (native is the pre-hybrid baseline)", native.Name)
	}
	if got.IndexGraph {
		t.Errorf("stage %q IndexGraph = true, want false (native is the pre-graph baseline)", native.Name)
	}
}

func TestMaxQuestionCount(t *testing.T) {
	cases := []struct {
		name               string
		secondsPerQuestion float64
		budget             time.Duration
		poolSize           int
		want               int
	}{
		{"exact fit", 10, 100 * time.Second, 100, 10},
		{"floor", 17, 45 * time.Minute, 200, 158},
		{"capped by pool", 1, 10 * time.Minute, 5, 5},
		{"rate exceeds budget", 200, 100 * time.Second, 100, 0},
		{"zero rate", 0, time.Minute, 100, 0},
		{"zero pool", 1, time.Minute, 0, 0},
		{"negative budget", 10, -time.Minute, 100, 0},
		{"default budget", 10, defaultAnswerBudget, 600, 540},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaxQuestionCount(tc.secondsPerQuestion, tc.budget, tc.poolSize); got != tc.want {
				t.Errorf("MaxQuestionCount(%v, %v, %d) = %d, want %d",
					tc.secondsPerQuestion, tc.budget, tc.poolSize, got, tc.want)
			}
		})
	}
}
