package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/store/graphstore"
)

func ts(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func mustUpsertEntities(t *testing.T, gs *GraphStore, entities []graphstore.Entity) {
	t.Helper()
	if err := gs.UpsertEntities(context.Background(), entities); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
}

func mustUpsertRelations(t *testing.T, gs *GraphStore, relations []graphstore.Relation) {
	t.Helper()
	if err := gs.UpsertRelations(context.Background(), relations); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
}

func TestUpsertEntitiesMergesSourceChunks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "alice|person", Name: "Alice", Type: "person", Description: "a dev", SourceChunks: []string{"c1"}},
	})
	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "alice|person", Name: "Alice", Type: "person", SourceChunks: []string{"c2", "c1"}},
	})

	matched, err := gs.MatchEntities(ctx, []string{"Alice"})
	if err != nil {
		t.Fatalf("MatchEntities: %v", err)
	}
	if len(matched) != 1 {
		t.Fatalf("got %d entities, want 1", len(matched))
	}
	got := append([]string{}, matched[0].SourceChunks...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"c1", "c2"}) {
		t.Fatalf("SourceChunks = %v, want union [c1 c2]", got)
	}
	if matched[0].Description != "a dev" {
		t.Fatalf("Description = %q, want preserved %q", matched[0].Description, "a dev")
	}
}

func TestUpsertRelationsMergesSourceChunksAndSumsWeight(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 2, SourceChunks: []string{"c2"}},
	})

	_, relations, err := gs.Neighbors(ctx, "a", 1)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(relations) != 1 {
		t.Fatalf("got %d relations, want 1", len(relations))
	}
	if relations[0].Weight != 3 {
		t.Fatalf("Weight = %v, want 3 (sum)", relations[0].Weight)
	}
	got := append([]string{}, relations[0].SourceChunks...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"c1", "c2"}) {
		t.Fatalf("SourceChunks = %v, want union [c1 c2]", got)
	}
}

func TestUpsertRelationsRecomputesDegree(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
		{ID: "c", Name: "C", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}},
		{ID: "a->c", Src: "a", Dst: "c", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}},
	})

	matched, err := gs.MatchEntities(ctx, []string{"A"})
	if err != nil {
		t.Fatalf("MatchEntities: %v", err)
	}
	if len(matched) != 1 || matched[0].Degree != 2 {
		t.Fatalf("degree for A = %+v, want 2", matched)
	}
}

func TestMigrateAddsTemporalColumnsToExistingSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
		CREATE TABLE entities (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			source_chunks TEXT NOT NULL DEFAULT '[]',
			degree INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE relations (
			id TEXT PRIMARY KEY,
			src TEXT NOT NULL,
			dst TEXT NOT NULL,
			type TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			weight REAL NOT NULL DEFAULT 0,
			source_chunks TEXT NOT NULL DEFAULT '[]'
		);
	`); err != nil {
		raw.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO relations (id, src, dst, type, weight, source_chunks)
		VALUES ('r1', 'a', 'b', 'knows', 1, '["c1"]')
	`); err != nil {
		raw.Close()
		t.Fatalf("seed legacy relation: %v", err)
	}
	raw.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	defer db.Close()

	rows, err := db.sql.QueryContext(ctx, `PRAGMA table_info(relations)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = true
	}
	rows.Close()
	for _, col := range []string{"valid_from", "valid_to", "created_at", "expired_at"} {
		if !cols[col] {
			t.Fatalf("migration did not add column %q", col)
		}
	}

	gs := NewGraphStore(db)
	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 1 || all[0].ID != "r1" || all[0].ValidFrom != nil || all[0].ValidTo != nil || all[0].ExpiredAt != nil {
		t.Fatalf("migrated relation = %+v, want r1 with nil temporal fields", all)
	}

	var idxCount int
	if err := db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pragma_index_list('relations')
		WHERE name = 'idx_relations_temporal'
	`).Scan(&idxCount); err != nil {
		t.Fatalf("check temporal index: %v", err)
	}
	if idxCount != 1 {
		t.Fatal("temporal index idx_relations_temporal missing after migration")
	}
}

func TestMigrateAddsRelationConfidenceColumns(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		db := openTestDB(t)
		ctx := context.Background()

		rows, err := db.sql.QueryContext(ctx, `PRAGMA table_info(relations)`)
		if err != nil {
			t.Fatalf("table_info: %v", err)
		}
		cols := map[string]bool{}
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull, pk int
			var dflt any
			if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
				rows.Close()
				t.Fatalf("scan table_info: %v", err)
			}
			cols[name] = true
		}
		rows.Close()
		for _, col := range []string{"confidence", "provenance", "extractor_version"} {
			if !cols[col] {
				t.Fatalf("fresh schema missing column %q", col)
			}
		}
	})

	t.Run("legacy rows get defaults", func(t *testing.T) {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "old.db")

		raw, err := sql.Open("sqlite3", path)
		if err != nil {
			t.Fatalf("open raw db: %v", err)
		}
		if _, err := raw.ExecContext(ctx, `
			CREATE TABLE relations (
				id TEXT PRIMARY KEY,
				src TEXT NOT NULL,
				dst TEXT NOT NULL,
				type TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				weight REAL NOT NULL DEFAULT 0,
				source_chunks TEXT NOT NULL DEFAULT '[]'
			);
		`); err != nil {
			raw.Close()
			t.Fatalf("create legacy schema: %v", err)
		}
		if _, err := raw.ExecContext(ctx, `
			INSERT INTO relations (id, src, dst, type, weight, source_chunks)
			VALUES ('r1', 'a', 'b', 'knows', 1, '["c1"]')
		`); err != nil {
			raw.Close()
			t.Fatalf("seed legacy relation: %v", err)
		}
		raw.Close()

		db, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open legacy db: %v", err)
		}
		defer db.Close()

		all, err := NewGraphStore(db).AllRelations(ctx)
		if err != nil {
			t.Fatalf("AllRelations: %v", err)
		}
		if len(all) != 1 {
			t.Fatalf("got %d relations, want 1", len(all))
		}
		got := all[0]
		if got.Confidence != 1.0 || got.Provenance != "legacy" || got.ExtractorVersion != "" {
			t.Fatalf("legacy relation defaults = %+v, want confidence 1.0 provenance legacy version empty", got)
		}
	})
}

func TestPutRelationCarriesOverProvenanceFields(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{{
		ID:               "a->b",
		Src:              "a",
		Dst:              "b",
		Type:             "knows",
		Weight:           1,
		Confidence:       0.7,
		Provenance:       "extraction",
		ExtractorVersion: "model-v1",
		SourceChunks:     []string{"c1"},
	}})

	if err := gs.PutRelation(ctx, graphstore.Relation{
		ID:          "a->b",
		Src:         "a",
		Dst:         "b",
		Type:        "knows",
		Description: "manual edit",
		Weight:      2,
	}); err != nil {
		t.Fatalf("PutRelation: %v", err)
	}

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d relations, want 1", len(all))
	}
	got := all[0]
	if got.Confidence != 0.7 || got.Provenance != "extraction" || got.ExtractorVersion != "model-v1" {
		t.Fatalf("PutRelation overwrote provenance fields: %+v", got)
	}
	if got.Description != "manual edit" {
		t.Fatalf("PutRelation description = %q, want manual edit", got.Description)
	}
}

func TestPutRelationReopenClearsClosedWindow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
	})
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	mustUpsertRelations(t, gs, []graphstore.Relation{{
		ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1,
		SourceChunks: []string{"c1"}, ValidFrom: &from, ValidTo: &to,
	}})

	now := time.Now()
	if err := gs.PutRelation(ctx, graphstore.Relation{
		ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1,
		ValidFrom: &now, Reopen: true,
	}); err != nil {
		t.Fatalf("PutRelation reopen: %v", err)
	}

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d relations, want 1", len(all))
	}
	got := all[0]
	if got.ValidTo != nil || got.ExpiredAt != nil {
		t.Fatalf("reopen kept closed window: %+v", got)
	}
	if got.ValidFrom == nil || !got.ValidFrom.Equal(now) {
		t.Fatalf("reopen valid_from = %v, want %v", got.ValidFrom, now)
	}
	neighbors, _, err := gs.Neighbors(ctx, "a", 1)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 1 {
		t.Fatalf("neighbors after reopen = %d, want 1", len(neighbors))
	}
}

func seedChunk(t *testing.T, db *DB, id, refDocID string) {
	t.Helper()
	if _, err := db.sql.ExecContext(context.Background(), `
		INSERT INTO chunks (id, ref_doc_id, text, file_path, file_name, source, token_count, chunk_index)
		VALUES (?, ?, 'text', 'p', 'f', 'src', 1, 0)
	`, id, refDocID); err != nil {
		t.Fatalf("seed chunk %s: %v", id, err)
	}
}

func TestOverlappingChunksReturnsSharedEntityChunks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	seedChunk(t, db, "docA#0", "docA")
	seedChunk(t, db, "docB#0", "docB")
	seedChunk(t, db, "docC#0", "docC")

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "alice", Name: "Alice", Type: "person", SourceChunks: []string{"docA#0", "docB#0"}},
		{ID: "bob", Name: "Bob", Type: "person", SourceChunks: []string{"docA#0"}},
		{ID: "carol", Name: "Carol", Type: "person", SourceChunks: []string{"docB#0", "docC#0"}},
	})

	got, err := gs.OverlappingChunks(ctx, []string{"alice", "bob"}, "docA", 1)
	if err != nil {
		t.Fatalf("OverlappingChunks: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"docB#0"}) {
		t.Fatalf("OverlappingChunks(alice,bob excl docA min 1) = %v, want [docB#0]", got)
	}

	got, err = gs.OverlappingChunks(ctx, []string{"alice", "bob"}, "docA", 2)
	if err != nil {
		t.Fatalf("OverlappingChunks(min 2): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("OverlappingChunks(min 2) = %v, want none (no chunk shares 2 entities)", got)
	}

	got, err = gs.OverlappingChunks(ctx, []string{"carol"}, "docB", 1)
	if err != nil {
		t.Fatalf("OverlappingChunks(carol excl docB): %v", err)
	}
	if !reflect.DeepEqual(got, []string{"docC#0"}) {
		t.Fatalf("OverlappingChunks(carol excl docB) = %v, want [docC#0]", got)
	}

	got, err = gs.OverlappingChunks(ctx, []string{"nobody"}, "docA", 1)
	if err != nil {
		t.Fatalf("OverlappingChunks(nobody): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("OverlappingChunks(nobody) = %v, want none", got)
	}
}

func TestOverlappingChunksExcludesClosedAndOwnChunks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	seedChunk(t, db, "docA#0", "docA")
	seedChunk(t, db, "docB#0", "docB")
	if _, err := db.sql.ExecContext(ctx, `
		INSERT INTO chunks (id, ref_doc_id, text, file_path, file_name, source, token_count, chunk_index, valid_to)
		VALUES ('docC#0', 'docC', 'text', 'p', 'f', 'src', 1, 0, '2026-01-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed closed chunk: %v", err)
	}

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "alice", Name: "Alice", Type: "person", SourceChunks: []string{"docA#0", "docB#0", "docC#0"}},
	})

	got, err := gs.OverlappingChunks(ctx, []string{"alice"}, "docB", 1)
	if err != nil {
		t.Fatalf("OverlappingChunks: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"docA#0"}) {
		t.Fatalf("OverlappingChunks = %v, want [docA#0] (own chunk and closed chunk excluded)", got)
	}
}

func TestUpsertRelationsClosesConflictInsteadOfOverwriting(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
		{ID: "c", Name: "C", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2024, 1, 1)},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->c", Src: "a", Dst: "c", Type: "knows", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2024, 6, 1)},
	})

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d relations, want 2 (old closed + new), got %+v", len(all), all)
	}
	byID := map[string]graphstore.Relation{}
	for _, r := range all {
		byID[r.ID] = r
	}
	old := byID["a->b"]
	if old.ValidTo == nil || !old.ValidTo.Equal(*ts(2024, 6, 1)) {
		t.Fatalf("closed relation valid_to = %v, want 2024-06-01", old.ValidTo)
	}
	if old.ExpiredAt == nil || !old.ExpiredAt.Equal(*ts(2024, 6, 1)) {
		t.Fatalf("closed relation expired_at = %v, want 2024-06-01", old.ExpiredAt)
	}
	newRel := byID["a->c"]
	if newRel.ValidFrom == nil || !newRel.ValidFrom.Equal(*ts(2024, 6, 1)) || newRel.ValidTo != nil || newRel.ExpiredAt != nil {
		t.Fatalf("new relation = %+v, want open with valid_from 2024-06-01", newRel)
	}

	matched, err := gs.MatchEntities(ctx, []string{"A"})
	if err != nil {
		t.Fatalf("MatchEntities: %v", err)
	}
	if len(matched) != 1 || matched[0].Degree != 1 {
		t.Fatalf("degree for A = %+v, want 1 (only open relation counts)", matched)
	}
}

func TestUpsertRelationsNoConflictCloseKeepsBothEdgesOpen(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
		{ID: "c", Name: "C", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "decided", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2024, 1, 1)},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->c", Src: "a", Dst: "c", Type: "decided", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2024, 6, 1), NoConflictClose: true},
	})

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d relations, want 2 (both open)", len(all))
	}
	for _, r := range all {
		if r.ValidTo != nil || r.ExpiredAt != nil {
			t.Fatalf("relation %s must stay open, got valid_to=%v expired_at=%v", r.ID, r.ValidTo, r.ExpiredAt)
		}
	}

	neighbors, rels, err := gs.Neighbors(ctx, "a", 1)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 2 || len(rels) != 2 {
		t.Fatalf("Neighbors = %d entities, %d relations, want 2 and 2 (both decisions reachable)", len(neighbors), len(rels))
	}
}

func TestUpsertRelationsMergeWithoutConflictKeepsSingleVersion(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2024, 1, 1)},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 2, SourceChunks: []string{"c2"}, ValidFrom: ts(2024, 6, 1)},
	})

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d relations, want 1 (merge must not create versions)", len(all))
	}
	r := all[0]
	if r.Weight != 3 {
		t.Fatalf("Weight = %v, want 3 (sum)", r.Weight)
	}
	if r.ValidFrom == nil || !r.ValidFrom.Equal(*ts(2024, 1, 1)) || r.ValidTo != nil {
		t.Fatalf("temporal fields not preserved on merge: %+v", r)
	}
	got := append([]string{}, r.SourceChunks...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"c1", "c2"}) {
		t.Fatalf("SourceChunks = %v, want [c1 c2]", got)
	}
}

func TestUpsertRelationsMergeClosesOpenEdgeOnReindex(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "art", Name: "Статья 15", Type: "legal-article"},
		{ID: "fz2012", Name: "ФЗ 2012", Type: "legal-amendment"},
		{ID: "fz2015", Name: "ФЗ 2015", Type: "legal-amendment"},
		{ID: "fz2020", Name: "ФЗ 2020", Type: "legal-amendment"},
	})
	// First ingest: redactions 2012 and 2015; the 2015 edge is open-ended.
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "fz2012->art", Src: "fz2012", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2012, 12, 30), ValidTo: ts(2015, 3, 8)},
		{ID: "fz2015->art", Src: "fz2015", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2015, 3, 8)},
	})
	// Re-index after the corpus gained a 2020 redaction: the 2015 edge must
	// be closed at 2020-01-01, not stay open next to the new edge.
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "fz2012->art", Src: "fz2012", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2012, 12, 30), ValidTo: ts(2015, 3, 8)},
		{ID: "fz2015->art", Src: "fz2015", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2015, 3, 8), ValidTo: ts(2020, 1, 1)},
		{ID: "fz2020->art", Src: "fz2020", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2020, 1, 1)},
	})

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d relations, want 3 (no versions created)", len(all))
	}
	byID := map[string]graphstore.Relation{}
	for _, r := range all {
		byID[r.ID] = r
	}
	if v := byID["fz2015->art"].ValidTo; v == nil || !v.Equal(*ts(2020, 1, 1)) {
		t.Fatalf("2015 edge valid_to = %v, want 2020-01-01 (closed on re-index)", v)
	}
	if v := byID["fz2020->art"].ValidTo; v != nil {
		t.Fatalf("2020 edge valid_to = %v, want open-ended", v)
	}

	rels, err := gs.RelationsAsOf(ctx, []string{"art"}, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RelationsAsOf(2024): %v", err)
	}
	if len(rels) != 1 || rels[0].ID != "fz2020->art" {
		t.Fatalf("RelationsAsOf(2024) = %+v, want exactly the 2020 redaction", rels)
	}
}

func TestUpsertRelationsMergeReopensClosedEdgeOnShrunkReindex(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "art", Name: "Статья 15", Type: "legal-article"},
		{ID: "fz2012", Name: "ФЗ 2012", Type: "legal-amendment"},
		{ID: "fz2015", Name: "ФЗ 2015", Type: "legal-amendment"},
		{ID: "fz2020", Name: "ФЗ 2020", Type: "legal-amendment"},
	})
	// First ingest: redactions 2012 and 2015; the 2015 edge is open-ended.
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "fz2012->art", Src: "fz2012", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2012, 12, 30), ValidTo: ts(2015, 3, 8)},
		{ID: "fz2015->art", Src: "fz2015", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2015, 3, 8)},
	})
	// Re-index flow strips the previous chunks before the fresh contribution
	// is merged in (UpdateDocument's RemoveChunks runs first).
	if _, err := gs.RemoveChunks(ctx, []string{"c1"}); err != nil {
		t.Fatalf("RemoveChunks(c1): %v", err)
	}
	// Re-index after the corpus gained a 2020 redaction: 2015 closes at 2020.
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "fz2012->art", Src: "fz2012", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2012, 12, 30), ValidTo: ts(2015, 3, 8)},
		{ID: "fz2015->art", Src: "fz2015", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2015, 3, 8), ValidTo: ts(2020, 1, 1)},
		{ID: "fz2020->art", Src: "fz2020", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2020, 1, 1)},
	})
	if _, err := gs.RemoveChunks(ctx, []string{"c2"}); err != nil {
		t.Fatalf("RemoveChunks: %v", err)
	}
	// Re-index after the 2020 redaction was removed from the corpus: merge
	// the fresh contribution in which the 2015 redaction is current again.
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "fz2012->art", Src: "fz2012", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c3"}, ValidFrom: ts(2012, 12, 30), ValidTo: ts(2015, 3, 8)},
		{ID: "fz2015->art", Src: "fz2015", Dst: "art", Type: "amends", Weight: 1, SourceChunks: []string{"c3"}, ValidFrom: ts(2015, 3, 8)},
	})
	if err := gs.PruneOrphans(ctx); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d relations, want 2 (2020 edge pruned)", len(all))
	}
	byID := map[string]graphstore.Relation{}
	for _, r := range all {
		byID[r.ID] = r
	}
	if v := byID["fz2015->art"].ValidTo; v != nil {
		t.Fatalf("2015 edge valid_to = %v, want open-ended after shrink", v)
	}
	if v := byID["fz2012->art"].ValidTo; v == nil || !v.Equal(*ts(2015, 3, 8)) {
		t.Fatalf("2012 edge valid_to = %v, want 2015-03-08", v)
	}

	rels, err := gs.RelationsAsOf(ctx, []string{"art"}, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RelationsAsOf(2024): %v", err)
	}
	if len(rels) != 1 || rels[0].ID != "fz2015->art" {
		t.Fatalf("RelationsAsOf(2024) = %+v, want exactly the reopened 2015 redaction", rels)
	}
}

func TestRelationsAsOfReturnsFactCurrentAtDate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
		{ID: "c", Name: "C", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2024, 1, 1)},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->c", Src: "a", Dst: "c", Type: "knows", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2024, 6, 1)},
	})

	assertIDs := func(at time.Time, want ...string) {
		t.Helper()
		rels, err := gs.RelationsAsOf(ctx, []string{"a"}, at)
		if err != nil {
			t.Fatalf("RelationsAsOf(%v): %v", at, err)
		}
		got := map[string]bool{}
		for _, r := range rels {
			got[r.ID] = true
		}
		if len(got) != len(want) {
			t.Fatalf("RelationsAsOf(%v) = %v, want %v", at, got, want)
		}
		for _, id := range want {
			if !got[id] {
				t.Fatalf("RelationsAsOf(%v) missing %q, got %v", at, id, got)
			}
		}
	}

	assertIDs(*ts(2024, 3, 1), "a->b")
	assertIDs(*ts(2024, 6, 1), "a->c")
	assertIDs(*ts(2024, 12, 1), "a->c")
}

func TestRelationsAsOfFromDstSideAndAfterClose(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
		{ID: "c", Name: "C", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2024, 1, 1)},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->c", Src: "a", Dst: "c", Type: "knows", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2024, 6, 1)},
	})

	rels, err := gs.RelationsAsOf(ctx, []string{"b"}, *ts(2024, 3, 1))
	if err != nil {
		t.Fatalf("RelationsAsOf(b): %v", err)
	}
	if len(rels) != 1 || rels[0].ID != "a->b" {
		t.Fatalf("RelationsAsOf(b, 2024-03-01) = %+v, want [a->b]", rels)
	}

	rels, err = gs.RelationsAsOf(ctx, []string{"a", "b"}, *ts(2024, 3, 1))
	if err != nil {
		t.Fatalf("RelationsAsOf([a b]): %v", err)
	}
	if len(rels) != 1 || rels[0].ID != "a->b" {
		t.Fatalf("RelationsAsOf([a b], 2024-03-01) = %+v, want [a->b]", rels)
	}
}

func TestNeighborsHonorsTimeParameter(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
		{ID: "c", Name: "C", Type: "x"},
		{ID: "d", Name: "D", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2024, 1, 1)},
		{ID: "a->c", Src: "a", Dst: "c", Type: "works_with", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2024, 1, 1)},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->d", Src: "a", Dst: "d", Type: "works_with", Weight: 1, SourceChunks: []string{"c3"}, ValidFrom: ts(2024, 6, 1)},
	})

	ids := func(es []graphstore.Entity) map[string]bool {
		out := map[string]bool{}
		for _, e := range es {
			out[e.ID] = true
		}
		return out
	}
	relIDs := func(rs []graphstore.Relation) map[string]bool {
		out := map[string]bool{}
		for _, r := range rs {
			out[r.ID] = true
		}
		return out
	}

	_, rels, err := gs.Neighbors(ctx, "a", 1, *ts(2024, 3, 1))
	if err != nil {
		t.Fatalf("Neighbors(2024-03-01): %v", err)
	}
	got := relIDs(rels)
	if !got["a->b"] || !got["a->c"] || got["a->d"] || len(got) != 2 {
		t.Fatalf("Neighbors(2024-03-01) relations = %v, want [a->b a->c]", got)
	}

	_, rels, err = gs.Neighbors(ctx, "a", 1, *ts(2024, 6, 1))
	if err != nil {
		t.Fatalf("Neighbors(2024-06-01): %v", err)
	}
	got = relIDs(rels)
	if !got["a->b"] || !got["a->d"] || got["a->c"] || len(got) != 2 {
		t.Fatalf("Neighbors(2024-06-01) relations = %v, want [a->b a->d]", got)
	}

	entities, rels, err := gs.Neighbors(ctx, "a", 1)
	if err != nil {
		t.Fatalf("Neighbors(default): %v", err)
	}
	got = relIDs(rels)
	if !got["a->b"] || !got["a->d"] || got["a->c"] || len(got) != 2 {
		t.Fatalf("Neighbors(default) relations = %v, want [a->b a->d]", got)
	}
	eIDs := ids(entities)
	if !eIDs["b"] || !eIDs["d"] || eIDs["c"] || len(eIDs) != 2 {
		t.Fatalf("Neighbors(default) entities = %v, want [b d]", eIDs)
	}
}

func TestMatchEntitiesAcceptsOptionalTime(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "alice|person", Name: "Alice", Type: "person"},
	})

	matched, err := gs.MatchEntities(ctx, []string{"Alice"}, *ts(2024, 1, 1))
	if err != nil {
		t.Fatalf("MatchEntities with time: %v", err)
	}
	if len(matched) != 1 || matched[0].ID != "alice|person" {
		t.Fatalf("MatchEntities with time = %+v, want [alice|person]", matched)
	}
	// Entities carry no validity window of their own, so the time
	// parameter must not change the result (signature parity with
	// Neighbors; temporal filtering applies to relations only).
	without, err := gs.MatchEntities(ctx, []string{"Alice"})
	if err != nil {
		t.Fatalf("MatchEntities without time: %v", err)
	}
	if len(without) != 1 || without[0].ID != "alice|person" {
		t.Fatalf("MatchEntities without time = %+v, want the same [alice|person]", without)
	}
}

func TestNonTemporalUpsertsKeepMultipleEdgesPerPredicate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
		{ID: "c", Name: "C", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}},
		{ID: "a->c", Src: "a", Dst: "c", Type: "knows", Weight: 1, SourceChunks: []string{"c2"}},
	})

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d relations, want 2 (legacy behavior preserved)", len(all))
	}
	for _, r := range all {
		if r.ValidTo != nil || r.ExpiredAt != nil {
			t.Fatalf("non-temporal relation unexpectedly closed: %+v", r)
		}
	}
}

func TestNeighborsOneAndTwoHops(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
		{ID: "c", Name: "C", Type: "x"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}},
		{ID: "b->c", Src: "b", Dst: "c", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}},
	})

	entities1, relations1, err := gs.Neighbors(ctx, "a", 1)
	if err != nil {
		t.Fatalf("Neighbors(1): %v", err)
	}
	if len(entities1) != 1 || entities1[0].ID != "b" {
		t.Fatalf("1-hop entities = %+v, want [b]", entities1)
	}
	if len(relations1) != 1 {
		t.Fatalf("1-hop relations = %+v, want 1", relations1)
	}

	entities2, relations2, err := gs.Neighbors(ctx, "a", 2)
	if err != nil {
		t.Fatalf("Neighbors(2): %v", err)
	}
	ids := map[string]bool{}
	for _, e := range entities2 {
		ids[e.ID] = true
	}
	if !ids["b"] || !ids["c"] || len(entities2) != 2 {
		t.Fatalf("2-hop entities = %+v, want [b c]", entities2)
	}
	if len(relations2) != 2 {
		t.Fatalf("2-hop relations = %+v, want 2", relations2)
	}
}

func TestMatchEntitiesExactAndNormalized(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "alice|person", Name: "Alice Smith", Type: "person"},
	})

	for _, name := range []string{"Alice Smith", "  alice smith  ", "ALICE SMITH"} {
		matched, err := gs.MatchEntities(ctx, []string{name})
		if err != nil {
			t.Fatalf("MatchEntities(%q): %v", name, err)
		}
		if len(matched) != 1 || matched[0].ID != "alice|person" {
			t.Fatalf("MatchEntities(%q) = %+v, want [alice|person]", name, matched)
		}
	}

	matched, err := gs.MatchEntities(ctx, []string{"Bob"})
	if err != nil {
		t.Fatalf("MatchEntities(Bob): %v", err)
	}
	if len(matched) != 0 {
		t.Fatalf("MatchEntities(Bob) = %+v, want empty", matched)
	}
}

func TestCommunitiesForReturnsCommunitiesContainingEntity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	if err := gs.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "comm1", Level: 0, Members: []string{"a", "b"}, Title: "Team", Summary: "s", SourceChunks: []string{"c1"}},
		{ID: "comm2", Level: 0, Members: []string{"c"}, Title: "Other", SourceChunks: []string{"c2"}},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	got, err := gs.CommunitiesFor(ctx, []string{"a"})
	if err != nil {
		t.Fatalf("CommunitiesFor: %v", err)
	}
	if len(got) != 1 || got[0].ID != "comm1" {
		t.Fatalf("CommunitiesFor(a) = %+v, want [comm1]", got)
	}
}

func TestPruneOrphansRemovesEntitiesAndRelationsWithEmptySourceChunks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "keep", Name: "Keep", Type: "x", SourceChunks: []string{"c1"}},
		{ID: "orphan", Name: "Orphan", Type: "x", SourceChunks: nil},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "keep->keep", Src: "keep", Dst: "keep", Type: "self", SourceChunks: []string{"c1"}},
		{ID: "orphan-rel", Src: "keep", Dst: "keep", Type: "self", SourceChunks: nil},
	})
	if err := gs.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "orphan-comm", Members: []string{"keep"}, SourceChunks: nil},
		{ID: "dead-comm", Members: []string{"orphan"}, SourceChunks: []string{"c2"}},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	if err := gs.PruneOrphans(ctx); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}

	matched, err := gs.MatchEntities(ctx, []string{"Keep", "Orphan"})
	if err != nil {
		t.Fatalf("MatchEntities: %v", err)
	}
	if len(matched) != 1 || matched[0].ID != "keep" {
		t.Fatalf("after PruneOrphans MatchEntities = %+v, want only [keep]", matched)
	}

	var relCount int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM relations`).Scan(&relCount); err != nil {
		t.Fatalf("count relations: %v", err)
	}
	if relCount != 1 {
		t.Fatalf("relations remaining = %d, want 1", relCount)
	}

	var commCount int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM communities`).Scan(&commCount); err != nil {
		t.Fatalf("count communities: %v", err)
	}
	if commCount != 0 {
		t.Fatalf("communities remaining = %d, want 0", commCount)
	}
}

func TestAllEntitiesAndAllRelations(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x", SourceChunks: []string{"c1"}},
		{ID: "b", Name: "B", Type: "x", SourceChunks: []string{"c1"}},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}},
	})

	entities, err := gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("AllEntities = %+v, want 2", entities)
	}

	relations, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 1 {
		t.Fatalf("AllRelations = %+v, want 1", relations)
	}
}

func TestAllCommunities(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	if err := gs.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "comm1", Members: []string{"a"}, Title: "T1", SourceChunks: []string{"c1"}},
		{ID: "comm2", Members: []string{"b"}, Title: "T2", SourceChunks: []string{"c2"}},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	got, err := gs.AllCommunities(ctx)
	if err != nil {
		t.Fatalf("AllCommunities: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("AllCommunities = %+v, want 2", got)
	}
}

func TestDeleteCommunities(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	if err := gs.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "comm1", Members: []string{"a"}, SourceChunks: []string{"c1"}},
		{ID: "comm2", Members: []string{"b"}, SourceChunks: []string{"c2"}},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	if err := gs.DeleteCommunities(ctx, []string{"comm1"}); err != nil {
		t.Fatalf("DeleteCommunities: %v", err)
	}

	all, err := gs.AllCommunities(ctx)
	if err != nil {
		t.Fatalf("AllCommunities: %v", err)
	}
	if len(all) != 1 || all[0].ID != "comm2" {
		t.Fatalf("after delete = %+v, want [comm2]", all)
	}
}

func TestRemoveChunksSubtractsAndRecomputesWeightAndDegree(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x", SourceChunks: []string{"c1", "c2"}},
		{ID: "b", Name: "B", Type: "x", SourceChunks: []string{"c1"}},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}},
	})

	touched, err := gs.RemoveChunks(ctx, []string{"c1"})
	if err != nil {
		t.Fatalf("RemoveChunks: %v", err)
	}
	touchedSet := map[string]bool{}
	for _, id := range touched {
		touchedSet[id] = true
	}
	if !touchedSet["a"] || !touchedSet["b"] {
		t.Fatalf("touched = %v, want to include a and b", touched)
	}

	matched, err := gs.MatchEntities(ctx, []string{"A", "B"})
	if err != nil {
		t.Fatalf("MatchEntities: %v", err)
	}
	byID := map[string]graphstore.Entity{}
	for _, e := range matched {
		byID[e.ID] = e
	}
	if !reflect.DeepEqual(byID["a"].SourceChunks, []string{"c2"}) {
		t.Fatalf("a.SourceChunks = %v, want [c2]", byID["a"].SourceChunks)
	}
	if len(byID["b"].SourceChunks) != 0 {
		t.Fatalf("b.SourceChunks = %v, want empty", byID["b"].SourceChunks)
	}
	if byID["a"].Degree != 0 || byID["b"].Degree != 0 {
		t.Fatalf("degree not recomputed: a=%d b=%d, want 0/0", byID["a"].Degree, byID["b"].Degree)
	}

	if err := gs.PruneOrphans(ctx); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}
	afterPrune, err := gs.MatchEntities(ctx, []string{"A", "B"})
	if err != nil {
		t.Fatalf("MatchEntities after prune: %v", err)
	}
	if len(afterPrune) != 1 || afterPrune[0].ID != "a" {
		t.Fatalf("after prune = %+v, want only [a]", afterPrune)
	}
}

func TestRemoveChunksEmptyInput(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	touched, err := gs.RemoveChunks(ctx, nil)
	if err != nil {
		t.Fatalf("RemoveChunks(nil): %v", err)
	}
	if len(touched) != 0 {
		t.Fatalf("touched = %v, want empty", touched)
	}
}

func TestEmptyInputsAreNoop(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	if err := gs.UpsertEntities(ctx, nil); err != nil {
		t.Fatalf("UpsertEntities(nil): %v", err)
	}
	if err := gs.UpsertRelations(ctx, nil); err != nil {
		t.Fatalf("UpsertRelations(nil): %v", err)
	}
	if err := gs.UpsertCommunities(ctx, nil); err != nil {
		t.Fatalf("UpsertCommunities(nil): %v", err)
	}
	comms, err := gs.CommunitiesFor(ctx, nil)
	if err != nil || comms != nil {
		t.Fatalf("CommunitiesFor(nil) = %v, %v; want nil, nil", comms, err)
	}
}

func TestDecodeStringsEmptyAndInvalid(t *testing.T) {
	if got, err := decodeStrings(""); err != nil || got != nil {
		t.Fatalf("decodeStrings(\"\") = %v, %v; want nil, nil", got, err)
	}
	if _, err := decodeStrings("not json"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestUpsertRelationsSameStartDifferentDstStaysOpen(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "law", Name: "law", Type: "legal-amendment"},
		{ID: "a1", Name: "a1", Type: "legal-article"},
		{ID: "a2", Name: "a2", Type: "legal-article"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "law->a1", Src: "law", Dst: "a1", Type: "amends", Weight: 1, SourceChunks: []string{"c1"}, ValidFrom: ts(2015, 3, 8)},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "law->a2", Src: "law", Dst: "a2", Type: "amends", Weight: 1, SourceChunks: []string{"c2"}, ValidFrom: ts(2015, 3, 8)},
	})

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d relations, want 2 (one law amending two articles at once)", len(all))
	}
	for _, r := range all {
		if r.ValidTo != nil || r.ExpiredAt != nil {
			t.Fatalf("simultaneous AMENDS edge %s must stay open, got %+v", r.ID, r)
		}
	}
}

func TestReplaceRelationRetargetsAndPreservesFields(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "point", Name: "Пункт 1", Type: "legal-plenum-point"},
		{ID: "transient", Name: "Статья 15", Type: "статья"},
		{ID: "canon", Name: "Статья 15", Type: "legal-article"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "point->transient", Src: "point", Dst: "transient", Type: "interprets", Weight: 1, SourceChunks: []string{"c1"}, Description: "разъясняет"},
	})

	if err := gs.ReplaceRelation(ctx, "point->transient", graphstore.Relation{
		ID:   "point->canon",
		Src:  "point",
		Dst:  "canon",
		Type: "interprets",
	}); err != nil {
		t.Fatalf("ReplaceRelation: %v", err)
	}

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 1 || all[0].ID != "point->canon" {
		t.Fatalf("relations = %+v, want exactly the retargeted point->canon", all)
	}
	r := all[0]
	if r.Dst != "canon" || len(r.SourceChunks) != 1 || r.SourceChunks[0] != "c1" || r.Weight != 1 || r.Description != "разъясняет" {
		t.Fatalf("retargeted relation lost fields: %+v", r)
	}

	entities, err := gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	degree := map[string]int{}
	for _, e := range entities {
		degree[e.ID] = e.Degree
	}
	if degree["canon"] != 1 || degree["point"] != 1 || degree["transient"] != 0 {
		t.Fatalf("degrees = %v, want canon=1 point=1 transient=0", degree)
	}
}

func TestReplaceRelationMergesIntoExistingTarget(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "p1", Name: "Пункт 1", Type: "legal-plenum-point"},
		{ID: "p2", Name: "Пункт 2", Type: "legal-plenum-point"},
		{ID: "transient", Name: "Статья 15", Type: "статья"},
		{ID: "canon", Name: "Статья 15", Type: "legal-article"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "p1->canon", Src: "p1", Dst: "canon", Type: "interprets", Weight: 1, SourceChunks: []string{"c1"}},
		{ID: "p2->transient", Src: "p2", Dst: "transient", Type: "interprets", Weight: 1, SourceChunks: []string{"c2"}},
	})
	// p2's edge was canonicalized later than p1's: the replacement must
	// merge into the existing canonical edge, not overwrite it.
	if err := gs.ReplaceRelation(ctx, "p2->transient", graphstore.Relation{
		ID:   "p2->canon",
		Src:  "p2",
		Dst:  "canon",
		Type: "interprets",
	}); err != nil {
		t.Fatalf("ReplaceRelation: %v", err)
	}

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("relations = %+v, want 2 (p2 edge merged into p1->canon)", all)
	}
	byID := map[string]graphstore.Relation{}
	for _, r := range all {
		byID[r.ID] = r
	}
	if r := byID["p1->canon"]; len(r.SourceChunks) != 1 {
		t.Fatalf("p1->canon must be untouched, got %+v", r)
	}
	if r := byID["p2->canon"]; r.Weight != 1 || len(r.SourceChunks) != 1 || r.SourceChunks[0] != "c2" {
		t.Fatalf("p2->canon = %+v, want c2 preserved", r)
	}
}

func TestDeleteEntityRemovesIncidentRelations(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "point", Name: "Пункт 1", Type: "legal-plenum-point"},
		{ID: "transient", Name: "Статья 15", Type: "статья"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "point->transient", Src: "point", Dst: "transient", Type: "interprets", Weight: 1, SourceChunks: []string{"c1"}},
	})

	if err := gs.DeleteEntity(ctx, "transient"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	entities, err := gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 1 || entities[0].ID != "point" {
		t.Fatalf("entities = %+v, want only the point", entities)
	}
	relations, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 0 {
		t.Fatalf("relations = %+v, want none", relations)
	}
}

func TestDeleteEntityRecomputesNeighborDegree(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a", Name: "A", Type: "t"},
		{ID: "b", Name: "B", Type: "t"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "a->b", Src: "a", Dst: "b", Type: "r", Weight: 1},
	})

	byID := func() map[string]graphstore.Entity {
		entities, err := gs.AllEntities(ctx)
		if err != nil {
			t.Fatalf("AllEntities: %v", err)
		}
		m := map[string]graphstore.Entity{}
		for _, e := range entities {
			m[e.ID] = e
		}
		return m
	}
	if got := byID()["a"].Degree; got != 1 {
		t.Fatalf("a degree before delete = %d, want 1", got)
	}

	if err := gs.DeleteEntity(ctx, "b"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}
	after := byID()
	if _, ok := after["b"]; ok {
		t.Fatal("b should be deleted")
	}
	if got := after["a"].Degree; got != 0 {
		t.Fatalf("a degree after delete = %d, want 0", got)
	}
}

func TestMigrateAddsStaleColumnToCommunities(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
		CREATE TABLE communities (
			id TEXT PRIMARY KEY,
			level INTEGER NOT NULL DEFAULT 0,
			members TEXT NOT NULL DEFAULT '[]',
			summary TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			source_chunks TEXT NOT NULL DEFAULT '[]'
		);
		INSERT INTO communities (id, members, summary, source_chunks)
		VALUES ('c1', '["a"]', 'old', '["chunk1"]')
	`); err != nil {
		raw.Close()
		t.Fatalf("create legacy communities: %v", err)
	}
	raw.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	defer db.Close()

	rows, err := db.sql.QueryContext(ctx, `PRAGMA table_info(communities)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = true
	}
	rows.Close()
	if !cols["stale"] {
		t.Fatal("migration did not add column stale")
	}

	var stale int
	if err := db.sql.QueryRowContext(ctx, `SELECT stale FROM communities WHERE id = 'c1'`).Scan(&stale); err != nil {
		t.Fatalf("read stale of legacy row: %v", err)
	}
	if stale != 0 {
		t.Fatalf("legacy community stale = %d, want 0", stale)
	}
}

func TestMarkCommunitiesStaleAndCount(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	if err := gs.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "c1", Members: []string{"a"}, SourceChunks: []string{"chunk1"}},
		{ID: "c2", Members: []string{"b"}, SourceChunks: []string{"chunk2"}},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	if n, err := gs.StaleCommunityCount(ctx); err != nil || n != 0 {
		t.Fatalf("StaleCommunityCount before mark = %d, %v; want 0", n, err)
	}
	if err := gs.MarkCommunitiesStale(ctx, []string{"c1"}); err != nil {
		t.Fatalf("MarkCommunitiesStale: %v", err)
	}
	if n, err := gs.StaleCommunityCount(ctx); err != nil || n != 1 {
		t.Fatalf("StaleCommunityCount after mark = %d, %v; want 1", n, err)
	}

	all, err := gs.AllCommunities(ctx)
	if err != nil {
		t.Fatalf("AllCommunities: %v", err)
	}
	byID := map[string]graphstore.Community{}
	for _, c := range all {
		byID[c.ID] = c
	}
	if !byID["c1"].Stale {
		t.Fatalf("c1 Stale = false, want true: %+v", byID["c1"])
	}
	if byID["c2"].Stale {
		t.Fatalf("c2 Stale = true, want false: %+v", byID["c2"])
	}
}

func TestPruneOrphansKeepsStaleCommunities(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "keep", Name: "Keep", Type: "x", SourceChunks: []string{"c1"}},
	})
	if err := gs.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "stale-empty", Members: []string{"keep"}, SourceChunks: nil},
		{ID: "fresh-empty", Members: []string{"keep"}, SourceChunks: nil},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}
	if err := gs.MarkCommunitiesStale(ctx, []string{"stale-empty"}); err != nil {
		t.Fatalf("MarkCommunitiesStale: %v", err)
	}

	if err := gs.PruneOrphans(ctx); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}

	all, err := gs.AllCommunities(ctx)
	if err != nil {
		t.Fatalf("AllCommunities: %v", err)
	}
	var ids []string
	for _, c := range all {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	if !reflect.DeepEqual(ids, []string{"stale-empty"}) {
		t.Fatalf("communities after PruneOrphans = %v, want [stale-empty] (stale rows survive)", ids)
	}
}

func TestRefreshStaleCommunitiesDelegatesToHook(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	called := 0
	gs.RefreshFunc = func(ctx context.Context) (int, error) {
		called++
		return 2, nil
	}
	n, err := gs.RefreshStaleCommunities(ctx)
	if err != nil || n != 2 || called != 1 {
		t.Fatalf("RefreshStaleCommunities = %d, %v (calls %d); want 2, nil, 1 call", n, err, called)
	}

	gs.RefreshFunc = nil
	if _, err := gs.RefreshStaleCommunities(ctx); err == nil {
		t.Fatalf("RefreshStaleCommunities without hook: want error, got nil")
	}
}

func TestReplaceRelationValidatesAndNoopsMissingOld(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	if err := gs.ReplaceRelation(ctx, "", graphstore.Relation{ID: "x"}); err == nil {
		t.Fatal("ReplaceRelation with empty oldID should fail")
	}
	if err := gs.ReplaceRelation(ctx, "x", graphstore.Relation{ID: ""}); err == nil {
		t.Fatal("ReplaceRelation with empty rel.ID should fail")
	}
	if err := gs.ReplaceRelation(ctx, "missing", graphstore.Relation{ID: "x"}); err != nil {
		t.Fatalf("ReplaceRelation on missing oldID should be a no-op, got %v", err)
	}

	all, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("relations = %+v, want none", all)
	}
}

func TestDeleteEntityEmptyIsNoop(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	if err := gs.DeleteEntity(ctx, ""); err != nil {
		t.Fatalf("DeleteEntity(\"\") should be a no-op, got %v", err)
	}
}

func TestReplaceRelationOnClosedDBFails(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)
	db.Close()

	if err := gs.ReplaceRelation(ctx, "a", graphstore.Relation{ID: "b"}); err == nil {
		t.Fatal("ReplaceRelation on closed DB should fail")
	}
}

func TestDeleteEntityOnClosedDBFails(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)
	db.Close()

	if err := gs.DeleteEntity(ctx, "a"); err == nil {
		t.Fatal("DeleteEntity on closed DB should fail")
	}
}

func TestOverlappingChunksCorruptSourceChunksFails(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	if _, err := db.sql.ExecContext(ctx, `
		INSERT INTO entities (id, name, type, description, source_chunks, degree)
		VALUES ('broken', 'Broken', 'person', '', '[not json', 0)
	`); err != nil {
		t.Fatalf("seed corrupt entity: %v", err)
	}

	got, err := gs.OverlappingChunks(ctx, []string{"broken"}, "docX", 1)
	if err != nil {
		t.Fatalf("OverlappingChunks on corrupt source_chunks must fail open: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("OverlappingChunks on corrupt source_chunks = %v, want none", got)
	}
}

func TestDeleteRelationRecomputesDegrees(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gs := NewGraphStore(db)

	mustUpsertEntities(t, gs, []graphstore.Entity{
		{ID: "a|person", Name: "A", Type: "person"},
		{ID: "b|person", Name: "B", Type: "person"},
	})
	mustUpsertRelations(t, gs, []graphstore.Relation{
		{ID: "rel1", Src: "a|person", Dst: "b|person", Type: "knows", Weight: 1},
	})

	degrees := func() map[string]int {
		entities, err := gs.AllEntities(ctx)
		if err != nil {
			t.Fatalf("AllEntities: %v", err)
		}
		out := make(map[string]int, len(entities))
		for _, e := range entities {
			out[e.ID] = e.Degree
		}
		return out
	}

	before := degrees()
	if before["a|person"] != 1 || before["b|person"] != 1 {
		t.Fatalf("degrees before delete = %v, want both 1", before)
	}

	if err := gs.DeleteRelation(ctx, "rel1"); err != nil {
		t.Fatalf("DeleteRelation: %v", err)
	}

	after := degrees()
	if after["a|person"] != 0 || after["b|person"] != 0 {
		t.Fatalf("degrees after delete = %v, want both 0", after)
	}
}

func TestDeleteRelationMissingIDIsNoop(t *testing.T) {
	db := openTestDB(t)
	gs := NewGraphStore(db)
	if err := gs.DeleteRelation(context.Background(), "nope"); err != nil {
		t.Fatalf("DeleteRelation(missing id) = %v, want nil", err)
	}
}

func TestDeleteRelationEmptyIDIsNoop(t *testing.T) {
	db := openTestDB(t)
	gs := NewGraphStore(db)
	if err := gs.DeleteRelation(context.Background(), ""); err != nil {
		t.Fatalf("DeleteRelation(empty id) = %v, want nil", err)
	}
}

func TestDeleteRelationOnClosedDBFails(t *testing.T) {
	db := openTestDB(t)
	gs := NewGraphStore(db)
	db.Close()
	if err := gs.DeleteRelation(context.Background(), "a"); err == nil {
		t.Fatal("DeleteRelation on closed DB should fail")
	}
}
