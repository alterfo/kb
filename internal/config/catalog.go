package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type EffectiveVar struct {
	Name      string
	Value     string
	Default   string
	Sensitive bool
}

type DirectVarSpec struct {
	Name      string
	Default   string
	Sensitive bool
	Validate  func(string) error
}

type Preset struct {
	Name        string
	Description string
	Apply       func(*Env)
}

var presets = []Preset{
	{
		Name:        "fast",
		Description: "Low-latency retrieval with cheaper GoT and no LLM reranking",
		Apply: func(e *Env) {
			e.Rerank = "off"
			e.QualifierFilter = false
			e.LLMNoThink = true
			e.MaxSubgoals = 3
			e.MaxGapQueries = 2
			e.CandidateK = 20
		},
	},
	{
		Name:        "quality",
		Description: "Higher recall retrieval with LLM reranking and deeper GoT",
		Apply: func(e *Env) {
			e.Rerank = "llm"
			e.QualifierFilter = true
			e.LLMNoThink = true
			e.MaxSubgoals = 8
			e.MaxGapQueries = 5
			e.CandidateK = 40
			e.PerDocCap = 4
		},
	},
}

var directKBVars = []DirectVarSpec{
	{Name: "KB_DOTENV", Default: ".env"},
	{Name: "KB_SOCKS_PROXY", Validate: validateSOCKSProxy},
	{Name: "KB_DISCORD_TOKEN", Sensitive: true},
	{Name: "KB_LLM_IT", Validate: validateOneOrZero},
	{Name: "KB_VERIFY_MIN_HITRATE", Validate: validateUnitInterval},
	{Name: "KB_LEGALEVAL_MIN_ENTITY_RECALL", Validate: validateUnitInterval},
	{Name: "KB_LEGALEVAL_MIN_NHSR", Validate: validateUnitInterval},
	{Name: "KB_DEMO_ADDR", Validate: validateHostPort},
	{Name: "KB_DEMO_LEON_QA_COUNT", Validate: validatePositiveInt},
	{Name: "KB_DEMO_ROOT"},
	{Name: "KB_MCP_TEST_STDIO_HELPER"},
	{Name: "KB_PLAN_RUN_ID"},
	{Name: "KB_PLAN_TARGET"},
	{Name: "KB_WEB_AUTH_TOKEN", Sensitive: true},
}

var directSecretVars = []DirectVarSpec{
	{Name: "GITHUB_TOKEN", Sensitive: true},
	{Name: "GITLAB_TOKEN", Sensitive: true},
	{Name: "CONFLUENCE_EMAIL", Sensitive: true},
	{Name: "CONFLUENCE_TOKEN", Sensitive: true},
	{Name: "SLACK_BOT_TOKEN", Sensitive: true},
	{Name: "TELEGRAM_BOT_TOKEN", Sensitive: true},
	{Name: "MATTERMOST_TOKEN", Sensitive: true},
	{Name: "YANDEX_TRACKER_OAUTH_TOKEN", Sensitive: true},
	{Name: "YANDEX_TRACKER_ORG_ID", Sensitive: true},
	{Name: "YOUTRACK_TOKEN", Sensitive: true},
	{Name: "KAITEN_TOKEN", Sensitive: true},
	{Name: "WEEEK_TOKEN", Sensitive: true},
	{Name: "TRELLO_KEY", Sensitive: true},
	{Name: "TRELLO_TOKEN", Sensitive: true},
}

func ApplyPreset(e *Env, name string) error {
	for _, p := range presets {
		if p.Name == name {
			p.Apply(e)
			return nil
		}
	}
	return fmt.Errorf("unknown preset %q (want fast|quality)", name)
}

func Presets() []Preset {
	return append([]Preset(nil), presets...)
}

func ValidateEnv(e Env) error {
	return validateEnv(e)
}

func ValidateDirectEnv(lookup EnvLookup) error {
	return validateDirectEnv(lookup)
}

func validateEnv(e Env) error {
	if e.Rerank != "" && e.Rerank != "off" && e.Rerank != "llm" && e.Rerank != "onnx" {
		return fmt.Errorf("KB_RERANK: invalid value %q (want off|llm|onnx)", e.Rerank)
	}
	if e.CommunityAlgo != "" && e.CommunityAlgo != "louvain" && e.CommunityAlgo != "leiden" {
		return fmt.Errorf("KB_COMMUNITY_ALGO: invalid value %q (want louvain|leiden)", e.CommunityAlgo)
	}
	if e.SupersedeMode != "" && e.SupersedeMode != "soft" && e.SupersedeMode != "strict" {
		return fmt.Errorf("KB_SUPERSEDE_MODE: invalid value %q (want soft|strict)", e.SupersedeMode)
	}
	if e.TopK <= 0 {
		return fmt.Errorf("KB_TOP_K: must be positive")
	}
	if e.ChunkSize <= 0 {
		return fmt.Errorf("KB_CHUNK_SIZE: must be positive")
	}
	if e.ChunkOverlap < 0 {
		return fmt.Errorf("KB_CHUNK_OVERLAP: must be non-negative")
	}
	if e.RRFK <= 0 {
		return fmt.Errorf("KB_RRF_K: must be positive")
	}
	if e.CandidateK <= 0 {
		return fmt.Errorf("KB_CANDIDATE_K: must be positive")
	}
	if e.PerDocCap <= 0 {
		return fmt.Errorf("KB_PER_DOC_CAP: must be positive")
	}
	if e.SetMaxRounds <= 0 {
		return fmt.Errorf("KB_SET_MAX_ROUNDS: must be positive")
	}
	if e.AbstainThreshold < 0 || e.AbstainThreshold > 1 {
		return fmt.Errorf("KB_ABSTAIN_THRESHOLD: must be in [0,1]")
	}
	if e.IntraDocBudget < 0 {
		return fmt.Errorf("KB_INTRA_DOC_BUDGET: must be non-negative")
	}
	if e.DescribeBatch <= 0 {
		return fmt.Errorf("KB_DESCRIBE_BATCH: must be positive")
	}
	if e.AskRollingWindow <= 0 {
		return fmt.Errorf("KB_ASK_ROLLING_WINDOW: must be positive")
	}
	if e.StaleAfter <= 0 {
		return fmt.Errorf("KB_STALE_AFTER: must be positive")
	}
	if e.LLMTimeout <= 0 {
		return fmt.Errorf("KB_LLM_TIMEOUT: must be positive")
	}
	if e.LLMMaxTokens < 0 {
		return fmt.Errorf("KB_LLM_MAX_TOKENS: must be non-negative")
	}
	if e.MaxSubgoals <= 0 {
		return fmt.Errorf("KB_MAX_SUBGOALS: must be positive")
	}
	if e.MaxGapQueries <= 0 {
		return fmt.Errorf("KB_MAX_GAP_QUERIES: must be positive")
	}
	if e.WebRateLimit < 0 {
		return fmt.Errorf("KB_WEB_RATE_LIMIT: must be non-negative")
	}
	return nil
}

func validateDirectEnv(lookup EnvLookup) error {
	if lookup == nil {
		return nil
	}
	for _, spec := range directVarSpecs() {
		if spec.Validate == nil {
			continue
		}
		raw, ok := lookup(spec.Name)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		if err := spec.Validate(raw); err != nil {
			return fmt.Errorf("%s: %w", spec.Name, err)
		}
	}
	return nil
}

func EffectiveVars(env Env, lookup EnvLookup) []EffectiveVar {
	vars := envVars(env)
	for _, spec := range directVarSpecs() {
		value := "(unset)"
		if lookup != nil {
			if raw, ok := lookup(spec.Name); ok {
				if spec.Sensitive {
					value = "<set>"
				} else {
					value = strings.TrimSpace(raw)
				}
			}
		}
		vars = append(vars, EffectiveVar{
			Name:      spec.Name,
			Value:     value,
			Default:   spec.Default,
			Sensitive: spec.Sensitive,
		})
	}
	sort.Slice(vars, func(i, j int) bool {
		return vars[i].Name < vars[j].Name
	})
	return vars
}

// Fingerprint returns a stable SHA-256 digest over the effective
// configuration (the resolved Env values plus all directly-read KB_* and
// connector variables). It is used to key Ask-response caches so a config
// change produces a different cache namespace. Sensitive values are folded
// in by presence only ("<set>"), never by their literal secret.
func Fingerprint(env Env, lookup EnvLookup) string {
	h := sha256.New()
	for _, v := range EffectiveVars(env, lookup) {
		fmt.Fprintf(h, "%s=%s\n", v.Name, v.Value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func directVarSpecs() []DirectVarSpec {
	out := make([]DirectVarSpec, 0, len(directKBVars)+len(directSecretVars))
	out = append(out, directKBVars...)
	out = append(out, directSecretVars...)
	return out
}

func envVars(e Env) []EffectiveVar {
	return []EffectiveVar{
		{Name: "KB_ROOT", Value: e.KBRoot, Default: "./kb_root"},
		{Name: "PERSIST_DIR", Value: e.PersistDir, Default: "./kb_root/.persist"},
		{Name: "KB_LLM_BASE_URL", Value: e.LLMBaseURL, Default: DefaultLocalLLMURL},
		{Name: "KB_EMBED_BASE_URL", Value: e.EmbedBaseURL},
		{Name: "KB_EMBED_INDEX_BASE_URL", Value: e.EmbedIndexBaseURL},
		{Name: "KB_LLM_MODEL", Value: e.LLMModel, Default: "qwen3.8:latest"},
		{Name: "KB_EMBED_MODEL", Value: e.EmbedModel, Default: "qwen3-embedding"},
		{Name: "KB_DESCRIBE_MODEL", Value: e.DescribeModel, Default: "qwen3.8:latest"},
		{Name: "KB_DESCRIBE_BATCH", Value: strconv.Itoa(e.DescribeBatch), Default: "10"},
		{Name: "KB_HYBRID", Value: strconv.FormatBool(e.Hybrid), Default: "true"},
		{Name: "KB_RERANK", Value: e.Rerank, Default: "off"},
		{Name: "KB_AUTHORITY_BONUS", Value: formatAuthorityBonus(e.AuthorityBonus), Default: "notes/=0.15,notes/approved/=0.30"},
		{Name: "KB_NO_PROXY", Value: strings.Join(e.NoProxy, ","), Default: "127.0.0.1"},
		{Name: "KB_TOP_K", Value: strconv.Itoa(e.TopK), Default: "10"},
		{Name: "KB_CHUNK_SIZE", Value: strconv.Itoa(e.ChunkSize), Default: "4096"},
		{Name: "KB_CHUNK_OVERLAP", Value: strconv.Itoa(e.ChunkOverlap), Default: "512"},
		{Name: "KB_RRF_K", Value: strconv.Itoa(e.RRFK), Default: "60"},
		{Name: "KB_COMMUNITY_ALGO", Value: e.CommunityAlgo, Default: "louvain"},
		{Name: "KB_DETECT_CONTRADICTIONS", Value: strconv.FormatBool(e.DetectContradictions), Default: "false"},
		{Name: "KB_QUALIFIER_FILTER", Value: strconv.FormatBool(e.QualifierFilter), Default: "false"},
		{Name: "KB_CANDIDATE_K", Value: strconv.Itoa(e.CandidateK), Default: "20"},
		{Name: "KB_PER_DOC_CAP", Value: strconv.Itoa(e.PerDocCap), Default: "2"},
		{Name: "KB_SET_MAX_ROUNDS", Value: strconv.Itoa(e.SetMaxRounds), Default: "3"},
		{Name: "KB_ABSTAIN_THRESHOLD", Value: formatOptionalFloat(e.AbstainThreshold), Default: ""},
		{Name: "KB_SUPERSEDE_MODE", Value: e.SupersedeMode, Default: "soft"},
		{Name: "KB_INTRA_DOC_BUDGET", Value: strconv.Itoa(e.IntraDocBudget), Default: ""},
		{Name: "KB_ASK_ROLLING_WINDOW", Value: strconv.Itoa(e.AskRollingWindow), Default: "3"},
		{Name: "KB_STALE_AFTER", Value: e.StaleAfter.String(), Default: "24h"},
		{Name: "KB_MAX_SUBGOALS", Value: strconv.Itoa(e.MaxSubgoals), Default: "5"},
		{Name: "KB_MAX_GAP_QUERIES", Value: strconv.Itoa(e.MaxGapQueries), Default: "3"},
		{Name: "KB_LLM_TIMEOUT", Value: e.LLMTimeout.String(), Default: "60s"},
		{Name: "KB_LLM_MAX_TOKENS", Value: strconv.Itoa(e.LLMMaxTokens), Default: ""},
		{Name: "KB_LLM_NO_THINK", Value: strconv.FormatBool(e.LLMNoThink), Default: "false"},
		{Name: "KB_INDEX_GRAPH", Value: strconv.FormatBool(e.IndexGraph), Default: "true"},
		{Name: "KB_FTS5", Value: strconv.FormatBool(e.FTS5), Default: "true"},
		{Name: "KB_ANN_PREFILTER", Value: strconv.FormatBool(e.ANNPrefilter), Default: "false"},
		{Name: "KB_PII_REDACT", Value: strconv.FormatBool(e.PIIRedact), Default: "false"},
		{Name: "KB_WEB_RATE_LIMIT", Value: strconv.Itoa(e.WebRateLimit), Default: "0"},
	}
}

func formatAuthorityBonus(m map[string]float64) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strconv.FormatFloat(m[k], 'f', -1, 64))
	}
	return strings.Join(parts, ",")
}

func formatOptionalFloat(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func validateSOCKSProxy(v string) error {
	u, err := url.Parse(v)
	if err != nil {
		return err
	}
	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return fmt.Errorf("must use socks5:// or socks5h://")
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}

func validateUnitInterval(v string) error {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fmt.Errorf("invalid number")
	}
	if f < 0 || f > 1 {
		return fmt.Errorf("must be in [0,1]")
	}
	return nil
}

func validateOneOrZero(v string) error {
	if v != "0" && v != "1" {
		return fmt.Errorf("must be 0 or 1")
	}
	return nil
}

func validatePositiveInt(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("invalid integer")
	}
	if n <= 0 {
		return fmt.Errorf("must be positive")
	}
	return nil
}

func validateHostPort(v string) error {
	if _, _, err := net.SplitHostPort(v); err != nil {
		return err
	}
	return nil
}
