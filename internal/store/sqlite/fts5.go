package sqlite

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/vector"
)

const metaKeyFTSVersion = "fts_version"

// FTS5Index is the SQLite-backed lexical candidate generator. It mirrors the
// active chunks table into an FTS5 virtual table and lazily rebuilds that
// mirror only when the corpus write-generation counter changes.
type FTS5Index struct {
	db *DB
}

func NewFTS5Index(db *DB) *FTS5Index {
	return &FTS5Index{db: db}
}

var _ bm25.Indexer = (*FTS5Index)(nil)

// Refresh rebuilds the FTS5 mirror if the corpus version has changed. The
// rebuild happens inside SQLite (insert-select from chunks), so it never
// loads the full corpus into memory.
func (i *FTS5Index) Refresh(ctx context.Context, versioner bm25.CorpusVersioner, _ bm25.ChunkLister) error {
	version, err := versioner.CorpusVersion(ctx)
	if err != nil {
		return err
	}
	current, ok, err := i.db.getMetaInt(ctx, metaKeyFTSVersion)
	if err != nil {
		return err
	}
	if ok && current == version {
		return nil
	}

	tx, err := i.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: fts5 rebuild begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks_fts`); err != nil {
		return fmt.Errorf("sqlite: fts5 rebuild clear: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chunks_fts(id, text)
		SELECT id, text FROM chunks WHERE valid_to IS NULL
	`); err != nil {
		return fmt.Errorf("sqlite: fts5 rebuild populate: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO kb_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, metaKeyFTSVersion, strconv.Itoa(version)); err != nil {
		return fmt.Errorf("sqlite: fts5 rebuild version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: fts5 rebuild commit: %w", err)
	}
	return nil
}

// Search runs an FTS5 MATCH query over the active-chunk mirror and returns
// candidate IDs in ascending BM25 rank order (more negative is better for
// SQLite's bm25() implementation).
func (i *FTS5Index) Search(query string, k int) []bm25.ScoredID {
	if k <= 0 {
		return nil
	}
	match := fts5Query(query)
	if match == "" {
		return nil
	}

	rows, err := i.db.sql.QueryContext(context.Background(), `
		SELECT id, bm25(chunks_fts, 1.2, 0.75) AS score
		FROM chunks_fts
		WHERE chunks_fts MATCH ?
		ORDER BY score, id
		LIMIT ?
	`, match, k)
	if err != nil {
		return nil
	}
	defer rows.Close()

	results := make([]bm25.ScoredID, 0, k)
	for rows.Next() {
		var res bm25.ScoredID
		if err := rows.Scan(&res.ID, &res.Score); err != nil {
			return nil
		}
		results = append(results, res)
	}
	if err := rows.Err(); err != nil {
		return nil
	}
	return results
}

// Chunk loads one active chunk by ID, used by the retriever to resolve
// lexical-only hits that never appeared in a dense vector result.
func (i *FTS5Index) Chunk(id string) (vector.Chunk, bool) {
	rows, err := i.db.sql.QueryContext(context.Background(), `SELECT `+chunkSelectCols+` FROM chunks WHERE id = ? AND valid_to IS NULL`, id)
	if err != nil {
		return vector.Chunk{}, false
	}
	defer rows.Close()
	if !rows.Next() {
		return vector.Chunk{}, false
	}
	c, err := scanChunk(rows)
	if err != nil {
		return vector.Chunk{}, false
	}
	if rows.Err() != nil {
		return vector.Chunk{}, false
	}
	return c, true
}

// fts5Query converts the free-text query into a safe FTS5 expression. Every
// token is quoted and OR-ed so the query behaves like the legacy in-memory
// BM25 path (any matching term contributes), while punctuation and FTS
// operators from user input cannot change the query syntax.
func fts5Query(query string) string {
	tokens := bm25.Tokenize(query)
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, len(tokens))
	for idx, token := range tokens {
		parts[idx] = `"` + strings.ReplaceAll(token, `"`, `""`) + `"`
	}
	return strings.Join(parts, " OR ")
}
