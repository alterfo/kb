package guard

import (
	"errors"
	"strings"
	"testing"
)

type fakeTerms struct {
	terms []string
	err   error
}

func (f fakeTerms) Terms() ([]string, error) {
	return f.terms, f.err
}

func TestCheckBlocksEachDetector(t *testing.T) {
	g := New(Options{})
	tests := []struct {
		name  string
		query string
		want  Reason
	}{
		{"email", "send this to alice@example.com", ReasonEmail},
		{"phone intl", "call +7 912 345-67-89", ReasonPhone},
		{"phone ru", "call 8 (912) 345-67-89", ReasonPhone},
		{"phone us", "call 555-123-4567", ReasonPhone},
		{"phone plain", "call 89123456789", ReasonPhone},
		{"ipv4", "server at 192.168.1.10", ReasonIPv4},
		{"ipv6", "use 2001:db8::1", ReasonIPv6},
		{"url token", "curl https://api.example.com/?token=abc123", ReasonURLToken},
		{"url userinfo", "https://user:pass@example.com/x", ReasonURLToken},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", ReasonJWT},
		{"aws key", "AKIAIOSFODNN7EXAMPLE", ReasonSecretKey},
		{"github key", "ghp_123456789012345678901234567890123456", ReasonSecretKey},
		{"slack key", "xoxb-123456789012-abcdefghijklmnop", ReasonSecretKey},
		{"long hex", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", ReasonLongHex},
		{"long base64", "dGhpcyBpcyBhIGxvbmdlciBiYXNlNjQgc3RyaW5nIHRoYXQgaXMgdXNlZCBmb3IgdGVzdGluZyBzZWNyZXRz", ReasonLongBase64},
		{"unix path", "look at /etc/passwd", ReasonPath},
		{"dotfile path", "check ~/.ssh/config", ReasonPath},
		{"windows path", "file C:\\Users\\alice\\secret.txt", ReasonPath},
		{"private host", "host is mybox.local", ReasonPrivateHost},
		{"localhost", "localhost:11434", ReasonPrivateHost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.Check(tt.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Allowed {
				t.Fatalf("query allowed, want blocked by %s", tt.want)
			}
			if !hasReason(got.Reasons, tt.want) {
				t.Fatalf("reasons = %v, want %s", got.Reasons, tt.want)
			}
		})
	}
}

func TestCheckDenyTermSources(t *testing.T) {
	g := New(Options{
		Denylist:          []string{"super-secret-project"},
		SourceNames:       []string{"main-org"},
		SecretEnvNames:    []string{"GITHUB_TOKEN"},
		ConfidentialTerms: []string{"confidential"},
	})
	tests := []struct {
		name  string
		query string
		want  Reason
	}{
		{"denylist", "tell me about super-secret-project", ReasonDenyTerm},
		{"source name", "does main-org use Go", ReasonSourceName},
		{"secret env name", "where is GITHUB_TOKEN used", ReasonSecretEnvName},
		{"confidential", "what is the confidential roadmap", ReasonConfidentialTerm},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.Check(tt.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Allowed {
				t.Fatalf("query allowed, want blocked by %s", tt.want)
			}
			if !hasReason(got.Reasons, tt.want) {
				t.Fatalf("reasons = %v, want %s", got.Reasons, tt.want)
			}
		})
	}
}

func TestCheckCorpusTerms(t *testing.T) {
	g := New(Options{TermSource: fakeTerms{terms: []string{"SecretEntity", "Quarterly Roadmap.md"}}})
	got, err := g.Check("summarize SecretEntity")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Allowed {
		t.Fatal("query allowed, want blocked by corpus term")
	}
	if !hasReason(got.Reasons, ReasonCorpusTerm) {
		t.Fatalf("reasons = %v, want %s", got.Reasons, ReasonCorpusTerm)
	}
}

func TestCheckTermSourceError(t *testing.T) {
	g := New(Options{TermSource: fakeTerms{err: errors.New("boom")}})
	got, err := g.Check("harmless query")
	if err == nil {
		t.Fatal("want error from term source, got nil")
	}
	if got.Allowed {
		t.Fatal("query allowed on term source error")
	}
}

func TestCheckBenignQueries(t *testing.T) {
	g := New(Options{})
	tests := []string{
		"How does SQLite FTS5 tokenizer work?",
		"Explain cosine similarity for embeddings",
		"What is the HTTP status code for created?",
		"Version 2024-10-11 release notes",
		"See https://example.com/docs for details",
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			got, err := g.Check(query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Allowed {
				t.Fatalf("query blocked, reasons = %v", got.Reasons)
			}
		})
	}
}

func TestCheckEmptyQuery(t *testing.T) {
	g := New(Options{})
	for _, query := range []string{"", "   \n\t"} {
		got, err := g.Check(query)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Allowed {
			t.Fatal("empty query allowed")
		}
		if !hasReason(got.Reasons, ReasonEmpty) {
			t.Fatalf("reasons = %v, want %s", got.Reasons, ReasonEmpty)
		}
	}
}

func TestCheckTooLongQuery(t *testing.T) {
	g := New(Options{MaxQueryLength: 10})
	got, err := g.Check(strings.Repeat("a", 11))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Allowed {
		t.Fatal("overlong query allowed")
	}
	if !hasReason(got.Reasons, ReasonTooLong) {
		t.Fatalf("reasons = %v, want %s", got.Reasons, ReasonTooLong)
	}
}

func hasReason(reasons []Reason, want Reason) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}
