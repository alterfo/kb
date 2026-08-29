package graphstore

import (
	"context"
	"time"
)

type Entity struct {
	ID           string
	Name         string
	Type         string
	Description  string
	SourceChunks []string
	Degree       int
}

type Relation struct {
	ID               string
	Src              string
	Dst              string
	Type             string
	Description      string
	Weight           float64
	Confidence       float64
	Provenance       string
	ExtractorVersion string
	SourceChunks     []string
	ValidFrom        *time.Time
	ValidTo          *time.Time
	CreatedAt        time.Time
	ExpiredAt        *time.Time
	NoConflictClose  bool
	Reopen           bool
}

type Community struct {
	ID           string
	Level        int
	Members      []string
	Summary      string
	Title        string
	SourceChunks []string
	Stale        bool
}

type Store interface {
	UpsertEntities(ctx context.Context, entities []Entity) error
	UpsertRelations(ctx context.Context, relations []Relation) error
	// PutEntity atomically overwrites the entity row without the extraction
	// merge (no source-chunk union, no keep-existing-on-empty). Manual
	// ontology edits use it so submitted fields are authoritative: an empty
	// description or source_chunks clears the previous values.
	PutEntity(ctx context.Context, e Entity) error
	// PutRelation atomically overwrites the relation row without the
	// extraction merge: description, weight and source_chunks are replaced,
	// not accumulated. CreatedAt and temporal windows are preserved from the
	// existing row when the replacement leaves them zero.
	PutRelation(ctx context.Context, r Relation) error
	// MatchEntities returns entities whose normalized name matches one of
	// the given names. The optional time parameter is accepted for
	// signature parity with Neighbors: entities carry no validity window
	// of their own, so the result is identical at any time (temporal
	// filtering applies to relations via Neighbors/RelationsAsOf).
	MatchEntities(ctx context.Context, names []string, at ...time.Time) ([]Entity, error)
	Neighbors(ctx context.Context, entityID string, hops int, at ...time.Time) ([]Entity, []Relation, error)
	RelationsAsOf(ctx context.Context, ids []string, t time.Time) ([]Relation, error)
	UpsertCommunities(ctx context.Context, communities []Community) error
	CommunitiesFor(ctx context.Context, ids []string) ([]Community, error)
	PruneOrphans(ctx context.Context) error
	OverlappingChunks(ctx context.Context, entityIDs []string, excludeRefDocID string, minShared int) ([]string, error)

	AllEntities(ctx context.Context) ([]Entity, error)
	AllRelations(ctx context.Context) ([]Relation, error)
	AllCommunities(ctx context.Context) ([]Community, error)
	DeleteCommunities(ctx context.Context, ids []string) error
	MarkCommunitiesStale(ctx context.Context, ids []string) error
	StaleCommunityCount(ctx context.Context) (int, error)
	RefreshStaleCommunities(ctx context.Context) (int, error)
	// RemoveChunks strips chunkIDs from every entity's/relation's
	// SourceChunks (and recomputes relation weight / entity degree), without
	// deleting the now-possibly-empty rows. Callers follow up with
	// PruneOrphans once new contributions have been merged in. Returns the
	// IDs of entities whose SourceChunks or incident relations changed.
	RemoveChunks(ctx context.Context, chunkIDs []string) ([]string, error)
	// ReplaceRelation atomically replaces the relation with oldID by rel,
	// carrying over the old row's fields (SourceChunks, weight, windows,
	// timestamps) where the replacement leaves them zero. It exists for
	// canonicalization rewrites where the dst (and therefore the derived
	// id) changes. A no-op when no relation with oldID exists. If a
	// relation with rel.ID already exists, the replacement merges into it.
	ReplaceRelation(ctx context.Context, oldID string, rel Relation) error
	// DeleteEntity removes the entity with the given id and every relation
	// incident to it.
	DeleteEntity(ctx context.Context, id string) error
	// DeleteRelation removes the relation with the given id and recomputes
	// the incident entities' degrees. A missing id is a no-op.
	DeleteRelation(ctx context.Context, id string) error
}
