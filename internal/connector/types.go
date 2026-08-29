package connector

import (
	"context"
	"time"
)

type EnvLookup func(key string) (string, bool)

type Document struct {
	ID          string
	Source      string
	Kind        string
	Title       string
	URL         string
	UpdatedAt   time.Time
	Body        string
	Visibility  string
	Summary     string
	Frontmatter map[string]any
}

type Cursor struct {
	Value string
}

type Config struct {
	Name    string
	Type    string
	Config  map[string]string
	Secrets map[string]string
}

type AuthKind string

const (
	AuthNone   AuthKind = ""
	AuthBearer AuthKind = "bearer"
	AuthBasic  AuthKind = "basic"
	AuthAPIKey AuthKind = "apikey"
	AuthOAuth  AuthKind = "oauth"
)

type AuthSpec struct {
	Kind     AuthKind
	Token    string
	Username string
	Password string
	Header   string
}

type FetchInfo struct {
	ItemCount     int
	FullReconcile bool
	// PrunePrefixes lists document-ID prefixes that were fully enumerated
	// this run even though the source as a whole was not (e.g. GitHub
	// re-fetches contents/wiki fully but issues only incrementally). When
	// non-empty, the sink prunes only documents whose id matches one of
	// these prefixes; documents outside them are incremental-only and must
	// not be pruned. FullReconcile stays the signal for a complete
	// enumeration of the whole source.
	PrunePrefixes []string
}

type Connector interface {
	Type() string
	Resolve(ctx context.Context, cfg Config, env EnvLookup) error
	// Fetch closes out once it stops producing (success or error) so callers can range over it.
	Fetch(ctx context.Context, since Cursor, out chan<- Document) (Cursor, FetchInfo, error)
}

type Sink interface {
	Write(ctx context.Context, d Document) error
	// Prune removes files of sourceName whose id is not in seen. When
	// prefixes is non-empty, only documents whose id has one of the
	// prefixes are eligible for removal; everything else is preserved.
	Prune(ctx context.Context, sourceName string, seen map[string]struct{}, prefixes ...string) error
	Tombstone(ctx context.Context, sourceName, id string) error
}
