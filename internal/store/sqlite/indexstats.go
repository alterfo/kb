package sqlite

import (
	"context"
	"fmt"
)

// IndexStats is a point-in-time summary of one kb.db index. It is used by
// the shadow-reindex flow to compare the current and candidate indexes
// before an operator cuts over.
type IndexStats struct {
	CorpusVersion  int
	EmbedDim       int
	HasEmbedDim    bool
	Chunks         int
	EmbeddedChunks int
	Entities       int
	Relations      int
	Communities    int
}

// IndexStats gathers the counts used by index-migration comparison.
func (d *DB) IndexStats(ctx context.Context) (IndexStats, error) {
	var out IndexStats
	var err error
	if out.CorpusVersion, err = d.CorpusVersion(ctx); err != nil {
		return out, err
	}
	if out.EmbedDim, out.HasEmbedDim, err = d.EmbedDim(ctx); err != nil {
		return out, err
	}
	if out.Chunks, err = d.ChunkCount(ctx); err != nil {
		return out, err
	}
	if out.EmbeddedChunks, err = d.EmbeddedChunkCount(ctx); err != nil {
		return out, err
	}

	gs := NewGraphStore(d)
	allEntities, err := gs.AllEntities(ctx)
	if err != nil {
		return out, fmt.Errorf("sqlite: IndexStats: entities: %w", err)
	}
	allRelations, err := gs.AllRelations(ctx)
	if err != nil {
		return out, fmt.Errorf("sqlite: IndexStats: relations: %w", err)
	}
	allCommunities, err := gs.AllCommunities(ctx)
	if err != nil {
		return out, fmt.Errorf("sqlite: IndexStats: communities: %w", err)
	}
	out.Entities = len(allEntities)
	out.Relations = len(allRelations)
	out.Communities = len(allCommunities)
	return out, nil
}

// EmbeddedChunkCount returns the number of chunks with a stored embedding.
func (d *DB) EmbeddedChunkCount(ctx context.Context) (int, error) {
	var n int
	err := d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks WHERE embedding IS NOT NULL`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count embedded chunks: %w", err)
	}
	return n, nil
}
