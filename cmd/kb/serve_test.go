package main

import (
	"testing"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine/rerank"
)

func TestRerankFromEnv(t *testing.T) {
	cases := []struct {
		rerank string
		want   string
	}{
		{"off", "noop"},
		{"", "noop"},
		{"llm", "llm"},
		{"onnx", "onnx"},
	}
	for _, tc := range cases {
		t.Run(tc.rerank, func(t *testing.T) {
			env := config.Defaults()
			env.Rerank = tc.rerank
			got := rerankFromEnv(env, nil)
			switch tc.want {
			case "noop":
				if _, ok := got.(rerank.Noop); !ok {
					t.Errorf("rerankFromEnv(%q) = %T, want Noop", tc.rerank, got)
				}
			case "llm":
				if _, ok := got.(*rerank.LLM); !ok {
					t.Errorf("rerankFromEnv(%q) = %T, want *LLM", tc.rerank, got)
				}
			case "onnx":
				if _, ok := got.(rerank.ONNX); !ok {
					t.Errorf("rerankFromEnv(%q) = %T, want ONNX", tc.rerank, got)
				}
			}
		})
	}
}
