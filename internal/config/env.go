package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type EnvLookup func(key string) (string, bool)

type Env struct {
	KBRoot               string
	PersistDir           string
	LLMBaseURL           string
	EmbedBaseURL         string
	EmbedIndexBaseURL    string
	LLMModel             string
	EmbedModel           string
	DescribeModel        string
	DescribeBatch        int
	Hybrid               bool
	Rerank               string
	AuthorityBonus       map[string]float64
	NoProxy              []string
	TopK                 int
	ChunkSize            int
	ChunkOverlap         int
	RRFK                 int
	CommunityAlgo        string
	DetectContradictions bool
	QualifierFilter      bool
	CandidateK           int
	PerDocCap            int
	SetMaxRounds         int
	AbstainThreshold     float64
	SupersedeMode        string
	IntraDocBudget       int
	AskRollingWindow     int
	StaleAfter           time.Duration
	MaxSubgoals          int
	MaxGapQueries        int
	LLMTimeout           time.Duration
	LLMMaxTokens         int
	LLMNoThink           bool
	IndexGraph           bool
}

// DefaultLocalLLMURL is the local LLM. It is pinned to the local
// instance on purpose: query-time embeddings (retrieval) must never contend
// with the remote chat/LLM box, and bulk indexing embeds are routed to the
// remote host via KB_EMBED_INDEX_BASE_URL. Only this local instance should
// serve live query embeddings.
const DefaultLocalLLMURL = "http://127.0.0.1:11434"

func defaultAuthorityBonus() map[string]float64 {
	return map[string]float64{
		"notes/":          0.15,
		"notes/approved/": 0.30,
	}
}

func Defaults() Env {
	return Env{
		KBRoot:           "./kb_root",
		PersistDir:       "./kb_root/.persist",
		LLMBaseURL:       DefaultLocalLLMURL,
		LLMModel:         "qwen3.8:latest",
		EmbedModel:       "qwen3-embedding",
		DescribeModel:    "qwen3.8:latest",
		DescribeBatch:    10,
		Hybrid:           true,
		Rerank:           "off",
		AuthorityBonus:   defaultAuthorityBonus(),
		NoProxy:          []string{"127.0.0.1"},
		TopK:             10,
		ChunkSize:        4096,
		ChunkOverlap:     512,
		RRFK:             60,
		CommunityAlgo:    "louvain",
		AskRollingWindow: 3,
		StaleAfter:       24 * time.Hour,
		LLMTimeout:       60 * time.Second,
		MaxSubgoals:      5,
		MaxGapQueries:    3,
		IndexGraph:       true,
	}
}

func LoadEnv(lookup EnvLookup) (Env, error) {
	e := Defaults()

	if v, ok := lookup("KB_ROOT"); ok && v != "" {
		e.KBRoot = v
	}
	if v, ok := lookup("PERSIST_DIR"); ok && v != "" {
		e.PersistDir = v
	}
	if v, ok := lookup("KB_LLM_BASE_URL"); ok && v != "" {
		e.LLMBaseURL = v
	}
	if v, ok := lookup("KB_EMBED_BASE_URL"); ok && v != "" {
		e.EmbedBaseURL = v
	}
	// EmbedIndexBaseURL defaults to the chat/LLM box (LLMBaseURL) so bulk
	// indexing embeds run on the remote host, never on the local machine
	// that serves live query embeddings. Override with KB_EMBED_INDEX_BASE_URL.
	if v, ok := lookup("KB_EMBED_INDEX_BASE_URL"); ok && v != "" {
		e.EmbedIndexBaseURL = v
	} else {
		e.EmbedIndexBaseURL = e.LLMBaseURL
	}
	if v, ok := lookup("KB_LLM_MODEL"); ok && v != "" {
		e.LLMModel = v
	}
	if v, ok := lookup("KB_EMBED_MODEL"); ok && v != "" {
		e.EmbedModel = v
	}
	if v, ok := lookup("KB_DESCRIBE_MODEL"); ok && v != "" {
		e.DescribeModel = v
	}
	if v, ok := lookup("KB_DESCRIBE_BATCH"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_DESCRIBE_BATCH: invalid positive int %q", v)
		}
		e.DescribeBatch = n
	}
	if v, ok := lookup("KB_HYBRID"); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Env{}, fmt.Errorf("KB_HYBRID: invalid bool %q: %w", v, err)
		}
		e.Hybrid = b
	}
	if v, ok := lookup("KB_RERANK"); ok && v != "" {
		switch v {
		case "off", "llm", "onnx":
			e.Rerank = v
		default:
			return Env{}, fmt.Errorf("KB_RERANK: invalid value %q (want off|llm|onnx)", v)
		}
	}
	if v, ok := lookup("KB_AUTHORITY_BONUS"); ok && v != "" {
		bonus, err := parseAuthorityBonus(v)
		if err != nil {
			return Env{}, fmt.Errorf("KB_AUTHORITY_BONUS: %w", err)
		}
		e.AuthorityBonus = bonus
	}
	if v, ok := lookup("KB_NO_PROXY"); ok && v != "" {
		e.NoProxy = splitAndTrim(v)
	}
	if v, ok := lookup("KB_TOP_K"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_TOP_K: invalid positive int %q", v)
		}
		e.TopK = n
	}
	if v, ok := lookup("KB_CHUNK_SIZE"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_CHUNK_SIZE: invalid positive int %q", v)
		}
		e.ChunkSize = n
	}
	if v, ok := lookup("KB_CHUNK_OVERLAP"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return Env{}, fmt.Errorf("KB_CHUNK_OVERLAP: invalid non-negative int %q", v)
		}
		e.ChunkOverlap = n
	}
	if v, ok := lookup("KB_RRF_K"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_RRF_K: invalid positive int %q", v)
		}
		e.RRFK = n
	}
	if v, ok := lookup("KB_COMMUNITY_ALGO"); ok && v != "" {
		switch v {
		case "louvain", "leiden":
			e.CommunityAlgo = v
		default:
			return Env{}, fmt.Errorf("KB_COMMUNITY_ALGO: invalid value %q (want louvain|leiden)", v)
		}
	}
	if v, ok := lookup("KB_DETECT_CONTRADICTIONS"); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Env{}, fmt.Errorf("KB_DETECT_CONTRADICTIONS: invalid bool %q", v)
		}
		e.DetectContradictions = b
	}
	if v, ok := lookup("KB_CANDIDATE_K"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_CANDIDATE_K: invalid positive int %q", v)
		}
		e.CandidateK = n
	}
	if v, ok := lookup("KB_INTRA_DOC_BUDGET"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return Env{}, fmt.Errorf("KB_INTRA_DOC_BUDGET: invalid non-negative int %q", v)
		}
		e.IntraDocBudget = n
	}
	if v, ok := lookup("KB_SUPERSEDE_MODE"); ok && v != "" {
		if v != "soft" && v != "strict" {
			return Env{}, fmt.Errorf("KB_SUPERSEDE_MODE: invalid value %q (want soft|strict)", v)
		}
		e.SupersedeMode = v
	}
	if v, ok := lookup("KB_ABSTAIN_THRESHOLD"); ok && v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 || f > 1 {
			return Env{}, fmt.Errorf("KB_ABSTAIN_THRESHOLD: invalid float in (0,1] %q", v)
		}
		e.AbstainThreshold = f
	}
	if v, ok := lookup("KB_SET_MAX_ROUNDS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_SET_MAX_ROUNDS: invalid positive int %q", v)
		}
		e.SetMaxRounds = n
	}
	if v, ok := lookup("KB_PER_DOC_CAP"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_PER_DOC_CAP: invalid positive int %q", v)
		}
		e.PerDocCap = n
	}
	if v, ok := lookup("KB_QUALIFIER_FILTER"); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Env{}, fmt.Errorf("KB_QUALIFIER_FILTER: invalid bool %q", v)
		}
		e.QualifierFilter = b
	}
	if v, ok := lookup("KB_ASK_ROLLING_WINDOW"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_ASK_ROLLING_WINDOW: invalid positive int %q", v)
		}
		e.AskRollingWindow = n
	}
	if v, ok := lookup("KB_STALE_AFTER"); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return Env{}, fmt.Errorf("KB_STALE_AFTER: invalid positive duration %q", v)
		}
		e.StaleAfter = d
	}
	if v, ok := lookup("KB_LLM_TIMEOUT"); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return Env{}, fmt.Errorf("KB_LLM_TIMEOUT: invalid positive duration %q", v)
		}
		e.LLMTimeout = d
	}
	if v, ok := lookup("KB_MAX_SUBGOALS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_MAX_SUBGOALS: invalid positive int %q", v)
		}
		e.MaxSubgoals = n
	}
	if v, ok := lookup("KB_MAX_GAP_QUERIES"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_MAX_GAP_QUERIES: invalid positive int %q", v)
		}
		e.MaxGapQueries = n
	}
	if v, ok := lookup("KB_LLM_MAX_TOKENS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Env{}, fmt.Errorf("KB_LLM_MAX_TOKENS: invalid positive int %q", v)
		}
		e.LLMMaxTokens = n
	}
	if v, ok := lookup("KB_LLM_NO_THINK"); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Env{}, fmt.Errorf("KB_LLM_NO_THINK: invalid bool %q: %w", v, err)
		}
		e.LLMNoThink = b
	}
	if v, ok := lookup("KB_INDEX_GRAPH"); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Env{}, fmt.Errorf("KB_INDEX_GRAPH: invalid bool %q: %w", v, err)
		}
		e.IndexGraph = b
	}

	return e, nil
}

func parseAuthorityBonus(v string) (map[string]float64, error) {
	result := make(map[string]float64)
	for _, pair := range strings.Split(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid pair %q (want prefix=bonus)", pair)
		}
		key := strings.TrimSpace(kv[0])
		val, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid bonus for %q: %w", key, err)
		}
		result[key] = val
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no valid prefix=bonus pairs found")
	}
	return result, nil
}

func splitAndTrim(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
