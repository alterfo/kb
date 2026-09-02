package config

import (
	"strings"
	"testing"
)

func TestApplyPreset_Fast(t *testing.T) {
	env := Defaults()
	if err := ApplyPreset(&env, "fast"); err != nil {
		t.Fatalf("ApplyPreset(fast): %v", err)
	}
	if env.Rerank != "off" {
		t.Errorf("Rerank = %q, want off", env.Rerank)
	}
	if env.QualifierFilter {
		t.Errorf("QualifierFilter = true, want false")
	}
	if !env.LLMNoThink {
		t.Errorf("LLMNoThink = false, want true")
	}
	if env.MaxSubgoals != 3 {
		t.Errorf("MaxSubgoals = %d, want 3", env.MaxSubgoals)
	}
	if env.MaxGapQueries != 2 {
		t.Errorf("MaxGapQueries = %d, want 2", env.MaxGapQueries)
	}
	if env.CandidateK != 20 {
		t.Errorf("CandidateK = %d, want 20", env.CandidateK)
	}
}

func TestApplyPreset_Quality(t *testing.T) {
	env := Defaults()
	if err := ApplyPreset(&env, "quality"); err != nil {
		t.Fatalf("ApplyPreset(quality): %v", err)
	}
	if env.Rerank != "llm" {
		t.Errorf("Rerank = %q, want llm", env.Rerank)
	}
	if !env.QualifierFilter {
		t.Errorf("QualifierFilter = false, want true")
	}
	if env.MaxSubgoals != 8 {
		t.Errorf("MaxSubgoals = %d, want 8", env.MaxSubgoals)
	}
	if env.MaxGapQueries != 5 {
		t.Errorf("MaxGapQueries = %d, want 5", env.MaxGapQueries)
	}
	if env.CandidateK != 40 {
		t.Errorf("CandidateK = %d, want 40", env.CandidateK)
	}
	if env.PerDocCap != 4 {
		t.Errorf("PerDocCap = %d, want 4", env.PerDocCap)
	}
}

func TestApplyPreset_Unknown(t *testing.T) {
	env := Defaults()
	if err := ApplyPreset(&env, "turbo"); err == nil {
		t.Fatal("ApplyPreset(turbo) = nil, want error")
	}
}

func TestValidateDirectEnv(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
		ok    bool
	}{
		{"valid socks", "KB_SOCKS_PROXY", "socks5://127.0.0.1:3333", true},
		{"invalid socks scheme", "KB_SOCKS_PROXY", "http://127.0.0.1:3333", false},
		{"valid one", "KB_LLM_IT", "1", true},
		{"invalid one", "KB_LLM_IT", "true", false},
		{"valid threshold", "KB_VERIFY_MIN_HITRATE", "0.75", true},
		{"invalid threshold", "KB_VERIFY_MIN_HITRATE", "1.5", false},
		{"invalid legal recall", "KB_LEGALEVAL_MIN_ENTITY_RECALL", "nope", false},
		{"invalid hostport", "KB_DEMO_ADDR", "localhost", false},
		{"invalid positive int", "KB_DEMO_LEON_QA_COUNT", "0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDirectEnv(fakeLookup(map[string]string{tc.key: tc.value}))
			if tc.ok && err != nil {
				t.Fatalf("ValidateDirectEnv(%s=%q) = %v, want nil", tc.key, tc.value, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("ValidateDirectEnv(%s=%q) = nil, want error", tc.key, tc.value)
			}
		})
	}
}

func TestEffectiveVarsRedactsSecrets(t *testing.T) {
	env := Defaults()
	lookup := fakeLookup(map[string]string{
		"KB_DISCORD_TOKEN":  "discord-secret",
		"GITHUB_TOKEN":      "github-secret",
		"KB_WEB_AUTH_TOKEN": "web-secret",
		"KB_SOCKS_PROXY":    "socks5://127.0.0.1:3333",
	})
	vars := EffectiveVars(env, lookup)

	discord := findVar(vars, "KB_DISCORD_TOKEN")
	if discord == nil || discord.Value != "<set>" {
		t.Fatalf("KB_DISCORD_TOKEN = %#v, want <set>", discord)
	}
	github := findVar(vars, "GITHUB_TOKEN")
	if github == nil || github.Value != "<set>" {
		t.Fatalf("GITHUB_TOKEN = %#v, want <set>", github)
	}
	webAuth := findVar(vars, "KB_WEB_AUTH_TOKEN")
	if webAuth == nil || webAuth.Value != "<set>" {
		t.Fatalf("KB_WEB_AUTH_TOKEN = %#v, want <set>", webAuth)
	}
	socks := findVar(vars, "KB_SOCKS_PROXY")
	if socks == nil || socks.Value != "socks5://127.0.0.1:3333" {
		t.Fatalf("KB_SOCKS_PROXY = %#v, want proxy URL", socks)
	}
}

func TestEffectiveVarsContainsCoreConfig(t *testing.T) {
	vars := EffectiveVars(Defaults(), nil)
	for _, want := range []string{"KB_ROOT", "PERSIST_DIR", "KB_TOP_K", "KB_RERANK", "KB_SOCKS_PROXY"} {
		if findVar(vars, want) == nil {
			t.Errorf("missing %s in effective config", want)
		}
	}
	topK := findVar(vars, "KB_TOP_K")
	if topK == nil || topK.Value != "10" {
		t.Fatalf("KB_TOP_K = %#v, want 10", topK)
	}
}

func findVar(vars []EffectiveVar, name string) *EffectiveVar {
	for i := range vars {
		if vars[i].Name == name {
			return &vars[i]
		}
	}
	return nil
}

func TestValidateEnvRangeFailures(t *testing.T) {
	base := Defaults()
	base.TopK = 0
	if err := ValidateEnv(base); err == nil || !strings.Contains(err.Error(), "KB_TOP_K") {
		t.Fatalf("ValidateEnv(TopK=0) = %v, want KB_TOP_K error", err)
	}
}

func TestFingerprintStableAcrossCalls(t *testing.T) {
	env := Defaults()
	lookup := fakeLookup(map[string]string{"KB_SOCKS_PROXY": "socks5://127.0.0.1:3333"})
	a := Fingerprint(env, lookup)
	b := Fingerprint(env, lookup)
	if a != b {
		t.Fatalf("Fingerprint not stable: %q != %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("Fingerprint length = %d, want sha256 hex (64)", len(a))
	}
}

func TestFingerprintChangesWithConfig(t *testing.T) {
	env := Defaults()
	lookup := fakeLookup(nil)
	base := Fingerprint(env, lookup)

	changed := env
	changed.TopK = 25
	if Fingerprint(changed, lookup) == base {
		t.Fatal("Fingerprint unchanged after TopK change")
	}

	changedProxy := fakeLookup(map[string]string{"KB_SOCKS_PROXY": "socks5://127.0.0.1:3333"})
	if Fingerprint(env, changedProxy) == base {
		t.Fatal("Fingerprint unchanged after direct proxy change")
	}
}

func TestFingerprintTreatsSecretPresenceOnly(t *testing.T) {
	env := Defaults()
	a := fakeLookup(map[string]string{"GITHUB_TOKEN": "token-a"})
	b := fakeLookup(map[string]string{"GITHUB_TOKEN": "token-b"})
	if Fingerprint(env, a) != Fingerprint(env, b) {
		t.Fatal("Fingerprint changed across secret value rotation; want presence-only")
	}
}
