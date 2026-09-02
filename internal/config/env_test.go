package config

import (
	"reflect"
	"testing"
	"time"
)

func fakeLookup(m map[string]string) EnvLookup {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestLoadEnv_Defaults(t *testing.T) {
	e, err := LoadEnv(fakeLookup(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d := Defaults()
	if e.KBRoot != d.KBRoot {
		t.Errorf("KBRoot = %q, want %q", e.KBRoot, d.KBRoot)
	}
	if e.LLMBaseURL != d.LLMBaseURL {
		t.Errorf("LLMBaseURL = %q, want %q", e.LLMBaseURL, d.LLMBaseURL)
	}
	if e.Hybrid != true {
		t.Errorf("Hybrid = %v, want true", e.Hybrid)
	}
	if e.TopK != 10 {
		t.Errorf("TopK = %d, want 10", e.TopK)
	}
	if e.StaleAfter != 24*time.Hour {
		t.Errorf("StaleAfter = %v, want 24h", e.StaleAfter)
	}
	if e.DescribeModel != "qwen3.8:latest" {
		t.Errorf("DescribeModel = %q, want qwen3.8:latest", e.DescribeModel)
	}
	if e.DescribeBatch != 10 {
		t.Errorf("DescribeBatch = %d, want 10", e.DescribeBatch)
	}
	if e.AskRollingWindow != 3 {
		t.Errorf("AskRollingWindow = %d, want 3", e.AskRollingWindow)
	}
	if !reflect.DeepEqual(e.NoProxy, []string{"127.0.0.1"}) {
		t.Errorf("NoProxy = %v, want [127.0.0.1]", e.NoProxy)
	}
	if !e.FTS5 {
		t.Errorf("FTS5 = %v, want true", e.FTS5)
	}
	if e.ANNPrefilter {
		t.Errorf("ANNPrefilter = %v, want false", e.ANNPrefilter)
	}
	if e.PIIRedact {
		t.Errorf("PIIRedact = %v, want false", e.PIIRedact)
	}
	if e.WebRateLimit != 0 {
		t.Errorf("WebRateLimit = %d, want 0", e.WebRateLimit)
	}
	if e.AuthorityBonus["notes/"] != 0.15 || e.AuthorityBonus["notes/approved/"] != 0.30 {
		t.Errorf("AuthorityBonus = %v, want defaults", e.AuthorityBonus)
	}
}

func TestLoadEnv_Overrides(t *testing.T) {
	m := map[string]string{
		"KB_ROOT":                  "/tmp/root",
		"PERSIST_DIR":              "/tmp/persist",
		"KB_LLM_BASE_URL":          "http://example.com:11434",
		"KB_LLM_MODEL":             "llama3",
		"KB_EMBED_MODEL":           "bge-m3",
		"KB_HYBRID":                "false",
		"KB_RERANK":                "llm",
		"KB_AUTHORITY_BONUS":       "notes/=0.2, notes/approved/=0.5",
		"KB_NO_PROXY":              "host1, host2 , host3",
		"KB_TOP_K":                 "25",
		"KB_CHUNK_SIZE":            "1024",
		"KB_CHUNK_OVERLAP":         "100",
		"KB_RRF_K":                 "30",
		"KB_COMMUNITY_ALGO":        "leiden",
		"KB_DETECT_CONTRADICTIONS": "true",
		"KB_QUALIFIER_FILTER":      "true",
		"KB_CANDIDATE_K":           "80",
		"KB_PER_DOC_CAP":           "6",
		"KB_SET_MAX_ROUNDS":        "7",
		"KB_ABSTAIN_THRESHOLD":     "0.6",
		"KB_SUPERSEDE_MODE":        "strict",
		"KB_INTRA_DOC_BUDGET":      "5000",
		"KB_STALE_AFTER":           "6h30m",
		"KB_DESCRIBE_MODEL":        "llama3",
		"KB_DESCRIBE_BATCH":        "5",
		"KB_ASK_ROLLING_WINDOW":    "7",
		"KB_MAX_SUBGOALS":          "3",
		"KB_MAX_GAP_QUERIES":       "2",
		"KB_LLM_NO_THINK":          "true",
		"KB_FTS5":                  "false",
		"KB_ANN_PREFILTER":         "true",
		"KB_PII_REDACT":            "true",
		"KB_WEB_RATE_LIMIT":        "30",
	}
	e, err := LoadEnv(fakeLookup(m))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.KBRoot != "/tmp/root" {
		t.Errorf("KBRoot = %q", e.KBRoot)
	}
	if e.PersistDir != "/tmp/persist" {
		t.Errorf("PersistDir = %q", e.PersistDir)
	}
	if e.LLMBaseURL != "http://example.com:11434" {
		t.Errorf("LLMBaseURL = %q", e.LLMBaseURL)
	}
	if e.LLMModel != "llama3" {
		t.Errorf("LLMModel = %q", e.LLMModel)
	}
	if e.EmbedModel != "bge-m3" {
		t.Errorf("EmbedModel = %q", e.EmbedModel)
	}
	if e.Hybrid != false {
		t.Errorf("Hybrid = %v, want false", e.Hybrid)
	}
	if e.Rerank != "llm" {
		t.Errorf("Rerank = %q, want llm", e.Rerank)
	}
	if e.AuthorityBonus["notes/"] != 0.2 || e.AuthorityBonus["notes/approved/"] != 0.5 {
		t.Errorf("AuthorityBonus = %v", e.AuthorityBonus)
	}
	if !reflect.DeepEqual(e.NoProxy, []string{"host1", "host2", "host3"}) {
		t.Errorf("NoProxy = %v", e.NoProxy)
	}
	if e.TopK != 25 {
		t.Errorf("TopK = %d", e.TopK)
	}
	if e.ChunkSize != 1024 {
		t.Errorf("ChunkSize = %d", e.ChunkSize)
	}
	if e.ChunkOverlap != 100 {
		t.Errorf("ChunkOverlap = %d", e.ChunkOverlap)
	}
	if e.RRFK != 30 {
		t.Errorf("RRFK = %d", e.RRFK)
	}
	if e.CommunityAlgo != "leiden" {
		t.Errorf("CommunityAlgo = %q, want leiden", e.CommunityAlgo)
	}
	if !e.DetectContradictions {
		t.Errorf("DetectContradictions = %v, want true", e.DetectContradictions)
	}

	if !e.QualifierFilter {
		t.Errorf("QualifierFilter = %v, want true", e.QualifierFilter)
	}

	if e.CandidateK != 80 {
		t.Errorf("CandidateK = %d, want 80", e.CandidateK)
	}
	if e.PerDocCap != 6 {
		t.Errorf("PerDocCap = %d, want 6", e.PerDocCap)
	}
	if e.SetMaxRounds != 7 {
		t.Errorf("SetMaxRounds = %d, want 7", e.SetMaxRounds)
	}
	if e.AbstainThreshold != 0.6 {
		t.Errorf("AbstainThreshold = %v, want 0.6", e.AbstainThreshold)
	}
	if e.SupersedeMode != "strict" {
		t.Errorf("SupersedeMode = %q, want strict", e.SupersedeMode)
	}
	if e.IntraDocBudget != 5000 {
		t.Errorf("IntraDocBudget = %d, want 5000", e.IntraDocBudget)
	}
	if e.StaleAfter != 6*time.Hour+30*time.Minute {
		t.Errorf("StaleAfter = %v, want 6h30m", e.StaleAfter)
	}
	if e.DescribeModel != "llama3" {
		t.Errorf("DescribeModel = %q, want llama3", e.DescribeModel)
	}
	if e.DescribeBatch != 5 {
		t.Errorf("DescribeBatch = %d, want 5", e.DescribeBatch)
	}
	if e.AskRollingWindow != 7 {
		t.Errorf("AskRollingWindow = %d, want 7", e.AskRollingWindow)
	}
	if e.MaxSubgoals != 3 {
		t.Errorf("MaxSubgoals = %d, want 3", e.MaxSubgoals)
	}
	if e.MaxGapQueries != 2 {
		t.Errorf("MaxGapQueries = %d, want 2", e.MaxGapQueries)
	}
	if !e.LLMNoThink {
		t.Errorf("LLMNoThink = %v, want true", e.LLMNoThink)
	}
	if e.FTS5 {
		t.Errorf("FTS5 = %v, want false", e.FTS5)
	}
	if !e.ANNPrefilter {
		t.Errorf("ANNPrefilter = %v, want true", e.ANNPrefilter)
	}
	if !e.PIIRedact {
		t.Errorf("PIIRedact = %v, want true", e.PIIRedact)
	}
	if e.WebRateLimit != 30 {
		t.Errorf("WebRateLimit = %d, want 30", e.WebRateLimit)
	}
}

func TestLoadEnv_InvalidValues(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
	}{
		{"bad hybrid bool", "KB_HYBRID", "notabool"},
		{"bad rerank enum", "KB_RERANK", "gpu"},
		{"bad authority bonus format", "KB_AUTHORITY_BONUS", "notes/"},
		{"bad authority bonus number", "KB_AUTHORITY_BONUS", "notes/=notanumber"},
		{"bad top_k not a number", "KB_TOP_K", "abc"},
		{"bad top_k zero", "KB_TOP_K", "0"},
		{"bad top_k negative", "KB_TOP_K", "-5"},
		{"bad chunk_size", "KB_CHUNK_SIZE", "-1"},
		{"bad chunk_overlap", "KB_CHUNK_OVERLAP", "-1"},
		{"bad rrf_k", "KB_RRF_K", "0"},
		{"bad community algo", "KB_COMMUNITY_ALGO", "girvan"},
		{"bad detect contradictions bool", "KB_DETECT_CONTRADICTIONS", "notabool"},
		{"bad qualifier filter bool", "KB_QUALIFIER_FILTER", "notabool"},
		{"bad candidate_k zero", "KB_CANDIDATE_K", "0"},
		{"bad candidate_k not a number", "KB_CANDIDATE_K", "many"},
		{"bad per_doc_cap zero", "KB_PER_DOC_CAP", "0"},
		{"bad per_doc_cap negative", "KB_PER_DOC_CAP", "-2"},
		{"bad set_max_rounds not a number", "KB_SET_MAX_ROUNDS", "many"},
		{"bad set_max_rounds zero", "KB_SET_MAX_ROUNDS", "0"},
		{"bad set_max_rounds negative", "KB_SET_MAX_ROUNDS", "-1"},
		{"bad abstain_threshold not a number", "KB_ABSTAIN_THRESHOLD", "nope"},
		{"bad abstain_threshold zero", "KB_ABSTAIN_THRESHOLD", "0"},
		{"bad abstain_threshold negative", "KB_ABSTAIN_THRESHOLD", "-0.2"},
		{"bad abstain_threshold above one", "KB_ABSTAIN_THRESHOLD", "1.1"},
		{"bad supersede_mode", "KB_SUPERSEDE_MODE", "hard"},
		{"bad intra_doc_budget negative", "KB_INTRA_DOC_BUDGET", "-1"},
		{"bad intra_doc_budget not a number", "KB_INTRA_DOC_BUDGET", "many"},
		{"bad stale_after format", "KB_STALE_AFTER", "soon"},
		{"bad stale_after zero", "KB_STALE_AFTER", "0s"},
		{"bad stale_after negative", "KB_STALE_AFTER", "-1h"},
		{"bad describe batch not a number", "KB_DESCRIBE_BATCH", "abc"},
		{"bad describe batch zero", "KB_DESCRIBE_BATCH", "0"},
		{"bad describe batch negative", "KB_DESCRIBE_BATCH", "-5"},
		{"bad ask rolling window not a number", "KB_ASK_ROLLING_WINDOW", "abc"},
		{"bad ask rolling window zero", "KB_ASK_ROLLING_WINDOW", "0"},
		{"bad ask rolling window negative", "KB_ASK_ROLLING_WINDOW", "-2"},
		{"bad max subgoals not a number", "KB_MAX_SUBGOALS", "many"},
		{"bad max subgoals zero", "KB_MAX_SUBGOALS", "0"},
		{"bad max gap queries not a number", "KB_MAX_GAP_QUERIES", "many"},
		{"bad max gap queries zero", "KB_MAX_GAP_QUERIES", "0"},
		{"bad llm no think bool", "KB_LLM_NO_THINK", "notabool"},
		{"bad fts5 bool", "KB_FTS5", "notabool"},
		{"bad ann prefilter bool", "KB_ANN_PREFILTER", "notabool"},
		{"bad pii redact bool", "KB_PII_REDACT", "notabool"},
		{"bad web rate limit", "KB_WEB_RATE_LIMIT", "-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadEnv(fakeLookup(map[string]string{tc.key: tc.val}))
			if err == nil {
				t.Errorf("expected error for %s=%q, got nil", tc.key, tc.val)
			}
		})
	}
}

func TestLoadEnv_EmptyValuesFallBackToDefaults(t *testing.T) {
	m := map[string]string{
		"KB_ROOT":   "",
		"KB_TOP_K":  "",
		"KB_HYBRID": "",
	}
	e, err := LoadEnv(fakeLookup(m))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d := Defaults()
	if e.KBRoot != d.KBRoot || e.TopK != d.TopK || e.Hybrid != d.Hybrid {
		t.Errorf("expected defaults for empty values, got %+v", e)
	}
}
