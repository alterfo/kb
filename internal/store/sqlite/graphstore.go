package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/store/graphstore"
)

type GraphStore struct {
	db *DB
	// RefreshFunc recomputes stale communities. Set by the wiring layer
	// (cmd/kb) to the GraphUpdater's refresh method, which owns detection
	// and summarization; nil means a caller-visible refresh is a no-op.
	RefreshFunc func(ctx context.Context) (int, error)
}

func NewGraphStore(db *DB) *GraphStore {
	return &GraphStore{db: db}
}

var _ graphstore.Store = (*GraphStore)(nil)

func (s *GraphStore) UpsertEntities(ctx context.Context, entities []graphstore.Entity) error {
	if len(entities) == 0 {
		return nil
	}
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: UpsertEntities: begin: %w", err)
	}
	defer tx.Rollback()

	for _, e := range entities {
		if e.ID == "" {
			return fmt.Errorf("sqlite: UpsertEntities: entity missing id (name=%q)", e.Name)
		}
		existing, ok, err := getEntityTx(ctx, tx, e.ID)
		if err != nil {
			return err
		}
		merged := e
		merged.SourceChunks = unionStrings(nil, e.SourceChunks)
		if ok {
			merged.SourceChunks = unionStrings(existing.SourceChunks, e.SourceChunks)
			if merged.Description == "" {
				merged.Description = existing.Description
			}
			if merged.Name == "" {
				merged.Name = existing.Name
			}
			if merged.Type == "" {
				merged.Type = existing.Type
			}
			merged.Degree = existing.Degree
		}
		if err := putEntityTx(ctx, tx, merged); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *GraphStore) PutEntity(ctx context.Context, e graphstore.Entity) error {
	if e.ID == "" {
		return fmt.Errorf("sqlite: PutEntity: entity missing id (name=%q)", e.Name)
	}
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: PutEntity: begin: %w", err)
	}
	defer tx.Rollback()
	if existing, ok, err := getEntityTx(ctx, tx, e.ID); err != nil {
		return err
	} else if ok && e.Degree == 0 {
		e.Degree = existing.Degree
	}
	if err := putEntityTx(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *GraphStore) UpsertRelations(ctx context.Context, relations []graphstore.Relation) error {
	if len(relations) == 0 {
		return nil
	}
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: UpsertRelations: begin: %w", err)
	}
	defer tx.Rollback()

	touched := map[string]struct{}{}
	for _, r := range relations {
		if r.ID == "" {
			return fmt.Errorf("sqlite: UpsertRelations: relation missing id (src=%q dst=%q)", r.Src, r.Dst)
		}
		if r.ValidFrom != nil && !r.NoConflictClose {
			if err := closeConflictingRelationsTx(ctx, tx, r); err != nil {
				return err
			}
		}
		existing, ok, err := getRelationTx(ctx, tx, r.ID)
		if err != nil {
			return err
		}
		merged := r
		merged.SourceChunks = unionStrings(nil, r.SourceChunks)
		if ok {
			merged.SourceChunks = unionStrings(existing.SourceChunks, r.SourceChunks)
			merged.Weight = existing.Weight + r.Weight
			if merged.Confidence == 0 {
				merged.Confidence = existing.Confidence
			}
			if merged.Provenance == "" {
				merged.Provenance = existing.Provenance
			}
			if merged.ExtractorVersion == "" {
				merged.ExtractorVersion = existing.ExtractorVersion
			}
			if merged.Description == "" {
				merged.Description = existing.Description
			}
			merged.ValidFrom = existing.ValidFrom
			merged.ValidTo = existing.ValidTo
			merged.CreatedAt = existing.CreatedAt
			merged.ExpiredAt = existing.ExpiredAt
			if len(existing.SourceChunks) == 0 {
				// Re-derivation: RemoveChunks just stripped this relation's
				// previous supporting chunks, so the incoming window is
				// authoritative in both directions — a grown redaction list
				// closes the previously open-ended AMENDS edge, while a
				// shrunk or corrected list reopens/re-windows it.
				merged.ValidFrom = r.ValidFrom
				merged.ValidTo = r.ValidTo
				merged.ExpiredAt = r.ExpiredAt
			} else if existing.ValidTo == nil && r.ValidTo != nil &&
				(existing.ValidFrom == nil || r.ValidTo.After(*existing.ValidFrom)) {
				// Multi-document accumulation: the relation is still
				// supported by other chunks, so only the growth direction is
				// applied — close the open edge at the new fact's date
				// instead of leaving two current facts simultaneously.
				merged.ValidTo = r.ValidTo
			}
		} else if merged.CreatedAt.IsZero() {
			merged.CreatedAt = time.Now()
		}
		if err := putRelationTx(ctx, tx, merged); err != nil {
			return err
		}
		touched[merged.Src] = struct{}{}
		touched[merged.Dst] = struct{}{}
	}

	for id := range touched {
		if _, err := tx.ExecContext(ctx, `
			UPDATE entities SET degree = (
				SELECT COUNT(*) FROM relations WHERE (src = ? OR dst = ?) AND valid_to IS NULL
			)
			WHERE id = ?
		`, id, id, id); err != nil {
			return fmt.Errorf("sqlite: UpsertRelations: recompute degree for %q: %w", id, err)
		}
	}

	return tx.Commit()
}

func (s *GraphStore) PutRelation(ctx context.Context, r graphstore.Relation) error {
	if r.ID == "" {
		return fmt.Errorf("sqlite: PutRelation: relation missing id (src=%q dst=%q)", r.Src, r.Dst)
	}
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: PutRelation: begin: %w", err)
	}
	defer tx.Rollback()
	if existing, ok, err := getRelationTx(ctx, tx, r.ID); err != nil {
		return err
	} else if ok {
		if r.CreatedAt.IsZero() {
			r.CreatedAt = existing.CreatedAt
		}
		if r.Confidence == 0 {
			r.Confidence = existing.Confidence
		}
		if r.Provenance == "" {
			r.Provenance = existing.Provenance
		}
		if r.ExtractorVersion == "" {
			r.ExtractorVersion = existing.ExtractorVersion
		}
		if r.ValidFrom == nil {
			r.ValidFrom = existing.ValidFrom
		}
		if r.Reopen {
			r.ValidTo = nil
			r.ExpiredAt = nil
		} else {
			if r.ValidTo == nil {
				r.ValidTo = existing.ValidTo
			}
			if r.ExpiredAt == nil {
				r.ExpiredAt = existing.ExpiredAt
			}
		}
	}
	if err := putRelationTx(ctx, tx, r); err != nil {
		return err
	}
	for _, id := range []string{r.Src, r.Dst} {
		if id == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE entities SET degree = (
				SELECT COUNT(*) FROM relations WHERE (src = ? OR dst = ?) AND valid_to IS NULL
			)
			WHERE id = ?
		`, id, id, id); err != nil {
			return fmt.Errorf("sqlite: PutRelation: recompute degree for %q: %w", id, err)
		}
	}
	return tx.Commit()
}

func (s *GraphStore) MatchEntities(ctx context.Context, names []string, at ...time.Time) ([]graphstore.Entity, error) {
	if len(names) == 0 {
		return nil, nil
	}
	normalized := make(map[string]struct{}, len(names))
	for _, n := range names {
		normalized[normalizeEntityName(n)] = struct{}{}
	}

	rows, err := s.db.sql.QueryContext(ctx, `
		SELECT id, name, type, description, source_chunks, degree FROM entities
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: MatchEntities: %w", err)
	}
	defer rows.Close()

	var matched []graphstore.Entity
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: MatchEntities: scan: %w", err)
		}
		if _, ok := normalized[normalizeEntityName(e.Name)]; ok {
			matched = append(matched, e)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: MatchEntities: %w", err)
	}
	return matched, nil
}

func (s *GraphStore) Neighbors(ctx context.Context, entityID string, hops int, at ...time.Time) ([]graphstore.Entity, []graphstore.Relation, error) {
	if entityID == "" || hops <= 0 {
		return nil, nil, nil
	}

	t := time.Now()
	if len(at) > 0 {
		t = at[0]
	}

	visited := map[string]struct{}{entityID: {}}
	relSeen := map[string]struct{}{}
	var neighborIDs []string
	var relations []graphstore.Relation

	frontier := []string{entityID}
	for h := 0; h < hops && len(frontier) > 0; h++ {
		rels, err := relationsTouching(ctx, s.db.sql, frontier, &t)
		if err != nil {
			return nil, nil, err
		}
		var next []string
		for _, r := range rels {
			if _, ok := relSeen[r.ID]; !ok {
				relSeen[r.ID] = struct{}{}
				relations = append(relations, r)
			}
			for _, id := range [2]string{r.Src, r.Dst} {
				if _, ok := visited[id]; !ok {
					visited[id] = struct{}{}
					neighborIDs = append(neighborIDs, id)
					next = append(next, id)
				}
			}
		}
		frontier = next
	}

	entities, err := entitiesByIDs(ctx, s.db.sql, neighborIDs)
	if err != nil {
		return nil, nil, err
	}
	return entities, relations, nil
}

func (s *GraphStore) RelationsAsOf(ctx context.Context, ids []string, t time.Time) ([]graphstore.Relation, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)*2+2)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, args...)
	args = append(args, encodeTime(&t), encodeTime(&t))

	rows, err := s.db.sql.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, src, dst, type, description, weight, confidence, provenance, extractor_version, source_chunks,
			valid_from, valid_to, created_at, expired_at FROM relations
		WHERE (src IN (%s) OR dst IN (%s))
			AND (valid_from IS NULL OR valid_from <= ?)
			AND (valid_to IS NULL OR valid_to > ?)
	`, placeholders, placeholders), args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: RelationsAsOf: %w", err)
	}
	defer rows.Close()

	var out []graphstore.Relation
	for rows.Next() {
		r, err := scanRelation(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: RelationsAsOf: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: RelationsAsOf: %w", err)
	}
	return out, nil
}

func (s *GraphStore) UpsertCommunities(ctx context.Context, communities []graphstore.Community) error {
	if len(communities) == 0 {
		return nil
	}
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: UpsertCommunities: begin: %w", err)
	}
	defer tx.Rollback()

	for _, c := range communities {
		if c.ID == "" {
			return fmt.Errorf("sqlite: UpsertCommunities: community missing id (title=%q)", c.Title)
		}
		membersJSON, err := json.Marshal(unionStrings(nil, c.Members))
		if err != nil {
			return fmt.Errorf("sqlite: UpsertCommunities: encode members: %w", err)
		}
		chunksJSON, err := json.Marshal(unionStrings(nil, c.SourceChunks))
		if err != nil {
			return fmt.Errorf("sqlite: UpsertCommunities: encode source_chunks: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO communities (id, level, members, summary, title, source_chunks, stale)
			VALUES (?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT(id) DO UPDATE SET
				level = excluded.level,
				members = excluded.members,
				summary = excluded.summary,
				title = excluded.title,
				source_chunks = excluded.source_chunks,
				stale = excluded.stale
		`, c.ID, c.Level, string(membersJSON), c.Summary, c.Title, string(chunksJSON)); err != nil {
			return fmt.Errorf("sqlite: UpsertCommunities: %q: %w", c.ID, err)
		}
	}
	return tx.Commit()
}

func (s *GraphStore) CommunitiesFor(ctx context.Context, ids []string) ([]graphstore.Community, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}

	rows, err := s.db.sql.QueryContext(ctx, `
		SELECT id, level, members, summary, title, source_chunks, stale FROM communities
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: CommunitiesFor: %w", err)
	}
	defer rows.Close()

	var out []graphstore.Community
	for rows.Next() {
		c, err := scanCommunity(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: CommunitiesFor: scan: %w", err)
		}
		for _, m := range c.Members {
			if _, ok := want[m]; ok {
				out = append(out, c)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: CommunitiesFor: %w", err)
	}
	return out, nil
}

func (s *GraphStore) PruneOrphans(ctx context.Context) error {
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: PruneOrphans: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM entities WHERE source_chunks = '[]'`); err != nil {
		return fmt.Errorf("sqlite: PruneOrphans: entities: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE source_chunks = '[]'`); err != nil {
		return fmt.Errorf("sqlite: PruneOrphans: relations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM communities WHERE source_chunks = '[]' AND stale = 0`); err != nil {
		return fmt.Errorf("sqlite: PruneOrphans: communities: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM communities WHERE NOT EXISTS (
			SELECT 1 FROM json_each(communities.members) AS m
			JOIN entities ON entities.id = m.value
		) AND stale = 0
	`); err != nil {
		return fmt.Errorf("sqlite: PruneOrphans: communities without members: %w", err)
	}
	return tx.Commit()
}

func (s *GraphStore) OverlappingChunks(ctx context.Context, entityIDs []string, excludeRefDocID string, minShared int) ([]string, error) {
	if len(entityIDs) == 0 || minShared <= 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(entityIDs)), ",")
	args := make([]any, 0, len(entityIDs)+2)
	for _, id := range entityIDs {
		args = append(args, id)
	}
	args = append(args, excludeRefDocID, minShared)

	rows, err := s.db.sql.QueryContext(ctx, `
		SELECT c.id
		FROM chunks c
		JOIN entities e ON e.id IN (`+placeholders+`)
		JOIN json_each(e.source_chunks) AS ec ON ec.value = c.id
		WHERE c.ref_doc_id != ? AND c.valid_to IS NULL
		GROUP BY c.id
		HAVING COUNT(DISTINCT e.id) >= ?
		ORDER BY c.id
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: OverlappingChunks: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite: OverlappingChunks: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: OverlappingChunks: %w", err)
	}
	return out, nil
}

func (s *GraphStore) AllEntities(ctx context.Context) ([]graphstore.Entity, error) {
	rows, err := s.db.sql.QueryContext(ctx, `
		SELECT id, name, type, description, source_chunks, degree FROM entities
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: AllEntities: %w", err)
	}
	defer rows.Close()

	var out []graphstore.Entity
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: AllEntities: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: AllEntities: %w", err)
	}
	return out, nil
}

func (s *GraphStore) AllRelations(ctx context.Context) ([]graphstore.Relation, error) {
	rows, err := s.db.sql.QueryContext(ctx, `
		SELECT id, src, dst, type, description, weight, confidence, provenance, extractor_version, source_chunks,
			valid_from, valid_to, created_at, expired_at FROM relations
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: AllRelations: %w", err)
	}
	defer rows.Close()

	var out []graphstore.Relation
	for rows.Next() {
		r, err := scanRelation(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: AllRelations: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: AllRelations: %w", err)
	}
	return out, nil
}

func (s *GraphStore) AllCommunities(ctx context.Context) ([]graphstore.Community, error) {
	rows, err := s.db.sql.QueryContext(ctx, `
		SELECT id, level, members, summary, title, source_chunks, stale FROM communities
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: AllCommunities: %w", err)
	}
	defer rows.Close()

	var out []graphstore.Community
	for rows.Next() {
		c, err := scanCommunity(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: AllCommunities: scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: AllCommunities: %w", err)
	}
	return out, nil
}

func (s *GraphStore) DeleteCommunities(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := s.db.sql.ExecContext(ctx, fmt.Sprintf(`
		DELETE FROM communities WHERE id IN (%s)
	`, placeholders), args...); err != nil {
		return fmt.Errorf("sqlite: DeleteCommunities: %w", err)
	}
	return nil
}

func (s *GraphStore) MarkCommunitiesStale(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := s.db.sql.ExecContext(ctx, fmt.Sprintf(`
		UPDATE communities SET stale = 1 WHERE id IN (%s)
	`, placeholders), args...); err != nil {
		return fmt.Errorf("sqlite: MarkCommunitiesStale: %w", err)
	}
	return nil
}

func (s *GraphStore) StaleCommunityCount(ctx context.Context) (int, error) {
	var n int
	if err := s.db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM communities WHERE stale = 1`).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: StaleCommunityCount: %w", err)
	}
	return n, nil
}

// RefreshStaleCommunities delegates to the wiring layer's RefreshFunc when
// set; without one it fails loudly so a missing wiring never silently skips
// community refresh. The GraphUpdater owns the actual detection because it
// holds the detector and summarizer.
func (s *GraphStore) RefreshStaleCommunities(ctx context.Context) (int, error) {
	if s.RefreshFunc == nil {
		return 0, fmt.Errorf("sqlite: RefreshStaleCommunities: RefreshFunc not wired")
	}
	return s.RefreshFunc(ctx)
}

func (s *GraphStore) RemoveChunks(ctx context.Context, chunkIDs []string) ([]string, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	remove := make(map[string]struct{}, len(chunkIDs))
	for _, id := range chunkIDs {
		remove[id] = struct{}{}
	}

	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: RemoveChunks: begin: %w", err)
	}
	defer tx.Rollback()

	entityRows, err := tx.QueryContext(ctx, `
		SELECT id, name, type, description, source_chunks, degree FROM entities
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: RemoveChunks: query entities: %w", err)
	}
	var entities []graphstore.Entity
	for entityRows.Next() {
		e, err := scanEntity(entityRows)
		if err != nil {
			entityRows.Close()
			return nil, fmt.Errorf("sqlite: RemoveChunks: scan entity: %w", err)
		}
		entities = append(entities, e)
	}
	if err := entityRows.Err(); err != nil {
		entityRows.Close()
		return nil, fmt.Errorf("sqlite: RemoveChunks: %w", err)
	}
	entityRows.Close()

	touched := map[string]struct{}{}

	for _, e := range entities {
		if !intersectsChunks(e.SourceChunks, remove) {
			continue
		}
		e.SourceChunks = subtractChunks(e.SourceChunks, remove)
		if err := putEntityTx(ctx, tx, e); err != nil {
			return nil, fmt.Errorf("sqlite: RemoveChunks: update entity %q: %w", e.ID, err)
		}
		touched[e.ID] = struct{}{}
	}

	relationRows, err := tx.QueryContext(ctx, `
		SELECT id, src, dst, type, description, weight, confidence, provenance, extractor_version, source_chunks,
			valid_from, valid_to, created_at, expired_at FROM relations
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: RemoveChunks: query relations: %w", err)
	}
	var relations []graphstore.Relation
	for relationRows.Next() {
		r, err := scanRelation(relationRows)
		if err != nil {
			relationRows.Close()
			return nil, fmt.Errorf("sqlite: RemoveChunks: scan relation: %w", err)
		}
		relations = append(relations, r)
	}
	if err := relationRows.Err(); err != nil {
		relationRows.Close()
		return nil, fmt.Errorf("sqlite: RemoveChunks: %w", err)
	}
	relationRows.Close()

	for _, r := range relations {
		if !intersectsChunks(r.SourceChunks, remove) {
			continue
		}
		r.SourceChunks = subtractChunks(r.SourceChunks, remove)
		r.Weight = float64(len(r.SourceChunks))
		if err := putRelationTx(ctx, tx, r); err != nil {
			return nil, fmt.Errorf("sqlite: RemoveChunks: update relation %q: %w", r.ID, err)
		}
		touched[r.Src] = struct{}{}
		touched[r.Dst] = struct{}{}
	}

	for id := range touched {
		if _, err := tx.ExecContext(ctx, `
			UPDATE entities SET degree = (
				SELECT COUNT(*) FROM relations
				WHERE (src = ? OR dst = ?) AND source_chunks != '[]' AND valid_to IS NULL
			) WHERE id = ?
		`, id, id, id); err != nil {
			return nil, fmt.Errorf("sqlite: RemoveChunks: recompute degree for %q: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: RemoveChunks: commit: %w", err)
	}

	out := make([]string, 0, len(touched))
	for id := range touched {
		out = append(out, id)
	}
	return out, nil
}

// ReplaceRelation atomically re-points a relation: the row with oldID is
// replaced by rel (typically the same relation with a retargeted dst and a
// re-derived id). Field values already set on rel win; zeros carry over
// from the old row, so a canonicalization rewrite does not drop the
// relation's source chunks, weight, or validity window. If a relation with
// rel.ID already exists (another document contributed the same canonical
// edge), the incoming contribution merges into it instead of overwriting
// it.
func (s *GraphStore) ReplaceRelation(ctx context.Context, oldID string, rel graphstore.Relation) error {
	if oldID == "" || rel.ID == "" {
		return fmt.Errorf("sqlite: ReplaceRelation: oldID and rel.ID are required")
	}
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: ReplaceRelation: begin: %w", err)
	}
	defer tx.Rollback()

	existing, ok, err := getRelationTx(ctx, tx, oldID)
	if err != nil {
		return err
	}
	if !ok {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE id = ?`, oldID); err != nil {
		return fmt.Errorf("sqlite: ReplaceRelation: delete %q: %w", oldID, err)
	}

	merged := rel
	contribWeight := rel.Weight
	if contribWeight == 0 {
		contribWeight = existing.Weight
	}
	target, hasTarget, err := getRelationTx(ctx, tx, rel.ID)
	if err != nil {
		return err
	}
	if hasTarget {
		merged.SourceChunks = unionStrings(target.SourceChunks, rel.SourceChunks)
		merged.Weight = target.Weight + contribWeight
		if merged.Confidence == 0 {
			merged.Confidence = target.Confidence
		}
		if merged.Provenance == "" {
			merged.Provenance = target.Provenance
		}
		if merged.ExtractorVersion == "" {
			merged.ExtractorVersion = target.ExtractorVersion
		}
		if merged.Description == "" {
			merged.Description = target.Description
		}
		if merged.CreatedAt.IsZero() {
			merged.CreatedAt = target.CreatedAt
		}
		if merged.ExpiredAt == nil {
			merged.ExpiredAt = target.ExpiredAt
		}
		if merged.ValidFrom == nil {
			merged.ValidFrom = target.ValidFrom
		}
		if merged.ValidTo == nil {
			merged.ValidTo = target.ValidTo
		}
	} else {
		merged.SourceChunks = unionStrings(existing.SourceChunks, rel.SourceChunks)
		merged.Weight = contribWeight
		if merged.Confidence == 0 {
			merged.Confidence = existing.Confidence
		}
		if merged.Provenance == "" {
			merged.Provenance = existing.Provenance
		}
		if merged.ExtractorVersion == "" {
			merged.ExtractorVersion = existing.ExtractorVersion
		}
		if merged.Description == "" {
			merged.Description = existing.Description
		}
		if merged.CreatedAt.IsZero() {
			merged.CreatedAt = existing.CreatedAt
		}
		if merged.ExpiredAt == nil {
			merged.ExpiredAt = existing.ExpiredAt
		}
		if merged.ValidFrom == nil {
			merged.ValidFrom = existing.ValidFrom
		}
		if merged.ValidTo == nil {
			merged.ValidTo = existing.ValidTo
		}
	}
	if err := putRelationTx(ctx, tx, merged); err != nil {
		return err
	}
	for _, id := range []string{existing.Src, existing.Dst, merged.Src, merged.Dst} {
		if _, err := tx.ExecContext(ctx, `
			UPDATE entities SET degree = (
				SELECT COUNT(*) FROM relations WHERE (src = ? OR dst = ?) AND valid_to IS NULL
			)
			WHERE id = ?
		`, id, id, id); err != nil {
			return fmt.Errorf("sqlite: ReplaceRelation: recompute degree for %q: %w", id, err)
		}
	}
	return tx.Commit()
}

// DeleteEntity removes the entity with the given id and every relation
// incident to it.
func (s *GraphStore) DeleteEntity(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: DeleteEntity: begin: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT src, dst FROM relations WHERE src = ? OR dst = ?`, id, id)
	if err != nil {
		return fmt.Errorf("sqlite: DeleteEntity: query relations of %q: %w", id, err)
	}
	neighbors := map[string]struct{}{}
	for rows.Next() {
		var src, dst string
		if err := rows.Scan(&src, &dst); err != nil {
			rows.Close()
			return fmt.Errorf("sqlite: DeleteEntity: scan relation of %q: %w", id, err)
		}
		if src != id {
			neighbors[src] = struct{}{}
		}
		if dst != id {
			neighbors[dst] = struct{}{}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: DeleteEntity: iterate relations of %q: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entities WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: DeleteEntity: delete entity %q: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE src = ? OR dst = ?`, id, id); err != nil {
		return fmt.Errorf("sqlite: DeleteEntity: delete relations of %q: %w", id, err)
	}
	for neighborID := range neighbors {
		if _, err := tx.ExecContext(ctx, `
			UPDATE entities SET degree = (
				SELECT COUNT(*) FROM relations WHERE (src = ? OR dst = ?) AND valid_to IS NULL
			)
			WHERE id = ?
		`, neighborID, neighborID, neighborID); err != nil {
			return fmt.Errorf("sqlite: DeleteEntity: recompute degree for %q: %w", neighborID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: DeleteEntity: commit: %w", err)
	}
	return nil
}

// DeleteRelation removes the relation with the given id and recomputes the
// incident entities' degrees. A missing id is a no-op.
func (s *GraphStore) DeleteRelation(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: DeleteRelation: begin: %w", err)
	}
	defer tx.Rollback()

	var src, dst string
	err = tx.QueryRowContext(ctx, `SELECT src, dst FROM relations WHERE id = ?`, id).Scan(&src, &dst)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sqlite: DeleteRelation: find %q: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: DeleteRelation: delete %q: %w", id, err)
	}
	for _, entityID := range []string{src, dst} {
		if _, err := tx.ExecContext(ctx, `
			UPDATE entities SET degree = (
				SELECT COUNT(*) FROM relations WHERE (src = ? OR dst = ?) AND valid_to IS NULL
			)
			WHERE id = ?
		`, entityID, entityID, entityID); err != nil {
			return fmt.Errorf("sqlite: DeleteRelation: recompute degree for %q: %w", entityID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: DeleteRelation: commit: %w", err)
	}
	return nil
}

func intersectsChunks(chunks []string, remove map[string]struct{}) bool {
	for _, c := range chunks {
		if _, ok := remove[c]; ok {
			return true
		}
	}
	return false
}

func subtractChunks(chunks []string, remove map[string]struct{}) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		if _, ok := remove[c]; !ok {
			out = append(out, c)
		}
	}
	return out
}

func getEntityTx(ctx context.Context, tx *sql.Tx, id string) (graphstore.Entity, bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, name, type, description, source_chunks, degree FROM entities WHERE id = ?
	`, id)
	e, err := scanEntity(row)
	if err == sql.ErrNoRows {
		return graphstore.Entity{}, false, nil
	}
	if err != nil {
		return graphstore.Entity{}, false, fmt.Errorf("sqlite: get entity %q: %w", id, err)
	}
	return e, true, nil
}

func putEntityTx(ctx context.Context, tx *sql.Tx, e graphstore.Entity) error {
	chunksJSON, err := json.Marshal(e.SourceChunks)
	if err != nil {
		return fmt.Errorf("sqlite: put entity %q: encode source_chunks: %w", e.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO entities (id, name, type, description, source_chunks, degree)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			type = excluded.type,
			description = excluded.description,
			source_chunks = excluded.source_chunks,
			degree = excluded.degree
	`, e.ID, e.Name, e.Type, e.Description, string(chunksJSON), e.Degree); err != nil {
		return fmt.Errorf("sqlite: put entity %q: %w", e.ID, err)
	}
	return nil
}

func getRelationTx(ctx context.Context, tx *sql.Tx, id string) (graphstore.Relation, bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, src, dst, type, description, weight, confidence, provenance, extractor_version, source_chunks,
			valid_from, valid_to, created_at, expired_at FROM relations WHERE id = ?
	`, id)
	r, err := scanRelation(row)
	if err == sql.ErrNoRows {
		return graphstore.Relation{}, false, nil
	}
	if err != nil {
		return graphstore.Relation{}, false, fmt.Errorf("sqlite: get relation %q: %w", id, err)
	}
	return r, true, nil
}

func putRelationTx(ctx context.Context, tx *sql.Tx, r graphstore.Relation) error {
	if r.Confidence == 0 {
		r.Confidence = 1.0
	}
	if r.Provenance == "" {
		r.Provenance = "legacy"
	}
	chunksJSON, err := json.Marshal(r.SourceChunks)
	if err != nil {
		return fmt.Errorf("sqlite: put relation %q: encode source_chunks: %w", r.ID, err)
	}
	createdAt := r.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO relations (id, src, dst, type, description, weight, confidence, provenance, extractor_version, source_chunks,
			valid_from, valid_to, created_at, expired_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			src = excluded.src,
			dst = excluded.dst,
			type = excluded.type,
			description = excluded.description,
			weight = excluded.weight,
			confidence = excluded.confidence,
			provenance = excluded.provenance,
			extractor_version = excluded.extractor_version,
			source_chunks = excluded.source_chunks,
			valid_from = excluded.valid_from,
			valid_to = excluded.valid_to,
			created_at = excluded.created_at,
			expired_at = excluded.expired_at
	`, r.ID, r.Src, r.Dst, r.Type, r.Description, r.Weight, r.Confidence, r.Provenance, r.ExtractorVersion, string(chunksJSON),
		encodeTime(r.ValidFrom), encodeTime(r.ValidTo), encodeTime(&createdAt), encodeTime(r.ExpiredAt)); err != nil {
		return fmt.Errorf("sqlite: put relation %q: %w", r.ID, err)
	}
	return nil
}

// closeConflictingRelationsTx closes every open relation with the same
// src+type as r but a different dst, at r's validity start (or now when the
// new fact carries no real-world time), so the new fact supersedes it
// instead of the old edge being overwritten in place. A same-start fact
// (new ValidFrom not strictly after the old relation's ValidFrom) is a
// distinct simultaneous fact, not a supersession — e.g. one federal law
// amending several articles at once must not close its own AMENDS edges
// to the other articles.
func closeConflictingRelationsTx(ctx context.Context, tx *sql.Tx, r graphstore.Relation) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, src, dst, type, description, weight, confidence, provenance, extractor_version, source_chunks,
			valid_from, valid_to, created_at, expired_at FROM relations
		WHERE src = ? AND type = ? AND dst != ? AND valid_to IS NULL
			AND (valid_from IS NULL OR ? IS NULL OR valid_from < ?)
	`, r.Src, r.Type, r.Dst, encodeTime(r.ValidFrom), encodeTime(r.ValidFrom))
	if err != nil {
		return fmt.Errorf("sqlite: closeConflictingRelations: %w", err)
	}
	var open []graphstore.Relation
	for rows.Next() {
		rel, err := scanRelation(rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("sqlite: closeConflictingRelations: scan: %w", err)
		}
		open = append(open, rel)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("sqlite: closeConflictingRelations: %w", err)
	}
	rows.Close()

	closeAt := time.Now()
	if r.ValidFrom != nil {
		closeAt = *r.ValidFrom
	}
	for _, rel := range open {
		if _, err := tx.ExecContext(ctx, `
			UPDATE relations SET valid_to = ?, expired_at = ? WHERE id = ?
		`, encodeTime(&closeAt), encodeTime(&closeAt), rel.ID); err != nil {
			return fmt.Errorf("sqlite: closeConflictingRelations: close %q: %w", rel.ID, err)
		}
	}
	return nil
}

func encodeTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func decodeTime(raw any) (*time.Time, error) {
	s, ok := raw.(string)
	if !ok || s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil, fmt.Errorf("decode time %q: %w", s, err)
	}
	return &t, nil
}

func relationsTouching(ctx context.Context, db *sql.DB, ids []string, at *time.Time) ([]graphstore.Relation, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)*2)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, args...)

	query := fmt.Sprintf(`
		SELECT id, src, dst, type, description, weight, confidence, provenance, extractor_version, source_chunks,
			valid_from, valid_to, created_at, expired_at FROM relations
		WHERE (src IN (%s) OR dst IN (%s))
	`, placeholders, placeholders)
	if at != nil {
		query += `AND (valid_from IS NULL OR valid_from <= ?) AND (valid_to IS NULL OR valid_to > ?)`
		args = append(args, encodeTime(at), encodeTime(at))
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: relationsTouching: %w", err)
	}
	defer rows.Close()

	var out []graphstore.Relation
	for rows.Next() {
		r, err := scanRelation(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: relationsTouching: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: relationsTouching: %w", err)
	}
	return out, nil
}

func entitiesByIDs(ctx context.Context, db *sql.DB, ids []string) ([]graphstore.Entity, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, name, type, description, source_chunks, degree FROM entities WHERE id IN (%s)
	`, placeholders), args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: entitiesByIDs: %w", err)
	}
	defer rows.Close()

	var out []graphstore.Entity
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: entitiesByIDs: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: entitiesByIDs: %w", err)
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEntity(row scanner) (graphstore.Entity, error) {
	var e graphstore.Entity
	var chunksJSON string
	if err := row.Scan(&e.ID, &e.Name, &e.Type, &e.Description, &chunksJSON, &e.Degree); err != nil {
		return graphstore.Entity{}, err
	}
	chunks, err := decodeStrings(chunksJSON)
	if err != nil {
		return graphstore.Entity{}, fmt.Errorf("decode source_chunks: %w", err)
	}
	e.SourceChunks = chunks
	return e, nil
}

func scanRelation(row scanner) (graphstore.Relation, error) {
	var r graphstore.Relation
	var chunksJSON string
	var validFrom, validTo, createdAt, expiredAt any
	if err := row.Scan(&r.ID, &r.Src, &r.Dst, &r.Type, &r.Description, &r.Weight, &r.Confidence, &r.Provenance, &r.ExtractorVersion, &chunksJSON,
		&validFrom, &validTo, &createdAt, &expiredAt); err != nil {
		return graphstore.Relation{}, err
	}
	chunks, err := decodeStrings(chunksJSON)
	if err != nil {
		return graphstore.Relation{}, fmt.Errorf("decode source_chunks: %w", err)
	}
	if r.ValidFrom, err = decodeTime(validFrom); err != nil {
		return graphstore.Relation{}, err
	}
	if r.ValidTo, err = decodeTime(validTo); err != nil {
		return graphstore.Relation{}, err
	}
	if t, err := decodeTime(createdAt); err != nil {
		return graphstore.Relation{}, err
	} else if t != nil {
		r.CreatedAt = *t
	}
	if r.ExpiredAt, err = decodeTime(expiredAt); err != nil {
		return graphstore.Relation{}, err
	}
	r.SourceChunks = chunks
	return r, nil
}

func scanCommunity(row scanner) (graphstore.Community, error) {
	var c graphstore.Community
	var membersJSON, chunksJSON string
	var stale int
	if err := row.Scan(&c.ID, &c.Level, &membersJSON, &c.Summary, &c.Title, &chunksJSON, &stale); err != nil {
		return graphstore.Community{}, err
	}
	members, err := decodeStrings(membersJSON)
	if err != nil {
		return graphstore.Community{}, fmt.Errorf("decode members: %w", err)
	}
	chunks, err := decodeStrings(chunksJSON)
	if err != nil {
		return graphstore.Community{}, fmt.Errorf("decode source_chunks: %w", err)
	}
	c.Members = members
	c.SourceChunks = chunks
	c.Stale = stale != 0
	return c, nil
}

func decodeStrings(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func normalizeEntityName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
