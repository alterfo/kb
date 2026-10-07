package guard

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

type Reason string

const (
	ReasonEmail            Reason = "email"
	ReasonPhone            Reason = "phone"
	ReasonIPv4             Reason = "ipv4"
	ReasonIPv6             Reason = "ipv6"
	ReasonURLToken         Reason = "url_token"
	ReasonJWT              Reason = "jwt"
	ReasonSecretKey        Reason = "secret_key"
	ReasonLongHex          Reason = "long_hex"
	ReasonLongBase64       Reason = "long_base64"
	ReasonPath             Reason = "filesystem_path"
	ReasonPrivateHost      Reason = "private_host"
	ReasonDenyTerm         Reason = "deny_term"
	ReasonSourceName       Reason = "source_name"
	ReasonSecretEnvName    Reason = "secret_env_name"
	ReasonConfidentialTerm Reason = "confidential_term"
	ReasonCorpusTerm       Reason = "corpus_term"
	ReasonEmpty            Reason = "empty_query"
	ReasonTooLong          Reason = "query_too_long"
)

type Verdict struct {
	Allowed bool
	Reasons []Reason
}

type TermSource interface {
	Terms() ([]string, error)
}

type Options struct {
	Denylist          []string
	SourceNames       []string
	SecretEnvNames    []string
	ConfidentialTerms []string
	TermSource        TermSource
	MaxQueryLength    int
}

type Guard struct {
	denylist          []string
	sourceNames       []string
	secretEnvNames    []string
	confidentialTerms []string
	termSource        TermSource
	maxQueryLength    int
}

type detector struct {
	match  func(string) bool
	reason Reason
}

var (
	emailRE = regexp.MustCompile(`(?i)\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)

	phoneIntlRE  = regexp.MustCompile(`(?:\+|00)[0-9]{1,3}[\s().-]*[0-9]{1,4}[\s().-]*[0-9]{3,4}[\s().-]*[0-9]{2,6}`)
	phoneRuRE    = regexp.MustCompile(`(?i)\b8[\s-]?\(?[0-9]{3}\)?[\s-]?[0-9]{3}[\s-]?[0-9]{2}[\s-]?[0-9]{2}\b`)
	phoneUsRE    = regexp.MustCompile(`\b\(?[0-9]{3}\)?[\s.-][0-9]{3}[\s.-][0-9]{4}\b`)
	phonePlainRE = regexp.MustCompile(`\b[0-9]{10,11}\b`)

	ipv4RE = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\b`)
	ipv6RE = regexp.MustCompile(`(?i)(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}|(?:[0-9a-f]{1,4}:){1,7}:[0-9a-f]{0,4}|::[0-9a-f]{1,4}`)

	urlUserinfoRE = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^/\s@:]+:[^/\s@]+@`)
	urlTokenRE    = regexp.MustCompile(`(?i)[?&](?:token|access[_-]?token|api[_-]?key|apikey|auth|secret|client[_-]?secret|refresh[_-]?token|signature|key)=[^&#\s"']+`)

	jwtRE = regexp.MustCompile(`(?i)\beyJ[A-Za-z0-9_-]{2,}\.[A-Za-z0-9_-]{2,}\.[A-Za-z0-9_-]{2,}\b`)

	awsKeyRE    = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
	githubKeyRE = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)
	slackKeyRE  = regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)
	openAIKeyRE = regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)

	longHexRE    = regexp.MustCompile(`(?i)(?:0x)?[0-9a-f]{32,}`)
	longBase64RE = regexp.MustCompile(`(?i)[A-Za-z0-9+/]{40,}={0,2}`)

	unixPathRE    = regexp.MustCompile(`(?i)(?:^|[\s"'(\[=])(?:/|~)(?:etc|usr|var|tmp|home|root|private|proc|sys|opt|srv|Users|Library|Volumes|Applications)(?:/|\b)`)
	dotfilePathRE = regexp.MustCompile(`(?i)(?:^|[\s"'(\[=])~/?\.(?:ssh|aws|gnupg|docker|kube|config)(?:/|\b)`)
	windowsPathRE = regexp.MustCompile(`(?i)\b[A-Za-z]:\\[^\\\s]+`)

	privateHostRE = regexp.MustCompile(`(?i)(localhost|127\.0\.0\.1|0\.0\.0\.0|::1|[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.(?:local|internal|lan|home|corp|intranet|test)|\*\.(?:local|internal|lan|home|corp|intranet))`)
)

var detectors = []detector{
	{match: emailRE.MatchString, reason: ReasonEmail},
	{match: phoneIntlRE.MatchString, reason: ReasonPhone},
	{match: phoneRuRE.MatchString, reason: ReasonPhone},
	{match: phoneUsRE.MatchString, reason: ReasonPhone},
	{match: phonePlainRE.MatchString, reason: ReasonPhone},
	{match: ipv4RE.MatchString, reason: ReasonIPv4},
	{match: ipv6RE.MatchString, reason: ReasonIPv6},
	{match: urlUserinfoRE.MatchString, reason: ReasonURLToken},
	{match: urlTokenRE.MatchString, reason: ReasonURLToken},
	{match: jwtRE.MatchString, reason: ReasonJWT},
	{match: awsKeyRE.MatchString, reason: ReasonSecretKey},
	{match: githubKeyRE.MatchString, reason: ReasonSecretKey},
	{match: slackKeyRE.MatchString, reason: ReasonSecretKey},
	{match: openAIKeyRE.MatchString, reason: ReasonSecretKey},
	{match: longHexRE.MatchString, reason: ReasonLongHex},
	{match: longBase64RE.MatchString, reason: ReasonLongBase64},
	{match: unixPathRE.MatchString, reason: ReasonPath},
	{match: dotfilePathRE.MatchString, reason: ReasonPath},
	{match: windowsPathRE.MatchString, reason: ReasonPath},
	{match: privateHostRE.MatchString, reason: ReasonPrivateHost},
}

const defaultMaxQueryLength = 2000

func New(opts Options) *Guard {
	max := opts.MaxQueryLength
	if max <= 0 {
		max = defaultMaxQueryLength
	}
	return &Guard{
		denylist:          append([]string(nil), opts.Denylist...),
		sourceNames:       append([]string(nil), opts.SourceNames...),
		secretEnvNames:    append([]string(nil), opts.SecretEnvNames...),
		confidentialTerms: append([]string(nil), opts.ConfidentialTerms...),
		termSource:        opts.TermSource,
		maxQueryLength:    max,
	}
}

func (g *Guard) Check(query string) (Verdict, error) {
	if strings.TrimSpace(query) == "" {
		return Verdict{Allowed: false, Reasons: []Reason{ReasonEmpty}}, nil
	}
	if g.maxQueryLength > 0 && utf8.RuneCountInString(query) > g.maxQueryLength {
		return Verdict{Allowed: false, Reasons: []Reason{ReasonTooLong}}, nil
	}

	reasons := make([]Reason, 0, 8)
	seen := make(map[Reason]bool)
	add := func(reason Reason) {
		if seen[reason] {
			return
		}
		seen[reason] = true
		reasons = append(reasons, reason)
	}

	for _, d := range detectors {
		if d.match(query) {
			add(d.reason)
		}
	}
	if containsTerm(query, g.denylist) {
		add(ReasonDenyTerm)
	}
	if containsTerm(query, g.sourceNames) {
		add(ReasonSourceName)
	}
	if containsTerm(query, g.secretEnvNames) {
		add(ReasonSecretEnvName)
	}
	if containsTerm(query, g.confidentialTerms) {
		add(ReasonConfidentialTerm)
	}
	if g.termSource != nil {
		terms, err := g.termSource.Terms()
		if err != nil {
			return Verdict{Allowed: false, Reasons: reasons}, err
		}
		if containsTerm(query, terms) {
			add(ReasonCorpusTerm)
		}
	}

	if len(reasons) == 0 {
		return Verdict{Allowed: true, Reasons: nil}, nil
	}
	return Verdict{Allowed: false, Reasons: reasons}, nil
}

func containsTerm(query string, terms []string) bool {
	q := strings.ToLower(query)
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term != "" && strings.Contains(q, strings.ToLower(term)) {
			return true
		}
	}
	return false
}
