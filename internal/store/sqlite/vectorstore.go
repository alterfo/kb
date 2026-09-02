package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/store/vector"
)

type VectorStore struct {
	db *DB
}

func NewVectorStore(db *DB) *VectorStore {
	return &VectorStore{db: db}
}

var _ vector.Store = (*VectorStore)(nil)

func (s *VectorStore) EnsureDim(ctx context.Context, dim int) error {
	if dim <= 0 {
		return fmt.Errorf("sqlite: EnsureDim: dim must be positive, got %d", dim)
	}
	existing, ok, err := s.db.getMetaInt(ctx, metaKeyEmbedDim)
	if err != nil {
		return err
	}
	if ok {
		if existing != dim {
			return fmt.Errorf("%w: index dim=%d, requested dim=%d", vector.ErrDimMismatch, existing, dim)
		}
		return nil
	}
	return s.db.setMetaInt(ctx, metaKeyEmbedDim, dim)
}

// Reembed clears the recorded embedding dimension and all stored embeddings,
// allowing a subsequent EnsureDim with a different dimension to succeed.
func (s *VectorStore) Reembed(ctx context.Context) error {
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: Reembed: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE chunks SET embedding = NULL`); err != nil {
		return fmt.Errorf("sqlite: Reembed: clear embeddings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM doc_hashes`); err != nil {
		return fmt.Errorf("sqlite: Reembed: clear doc hashes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM kb_meta WHERE key = ?`, metaKeyEmbedDim); err != nil {
		return fmt.Errorf("sqlite: Reembed: clear dim: %w", err)
	}
	if err := bumpCorpusVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *VectorStore) Upsert(ctx context.Context, chunks []vector.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	dim, dimKnown, err := s.db.getMetaInt(ctx, metaKeyEmbedDim)
	if err != nil {
		return err
	}

	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: Upsert: begin: %w", err)
	}
	defer tx.Rollback()

	if err := s.upsertChunks(ctx, tx, chunks, dim, dimKnown); err != nil {
		return err
	}
	if err := bumpCorpusVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceByDoc atomically soft-closes every active chunk of docID and writes
// its replacement chunks in a single transaction. If writing the replacements
// fails, the soft-close is rolled back so the document never becomes
// unretrievable.
func (s *VectorStore) ReplaceByDoc(ctx context.Context, docID string, chunks []vector.Chunk) error {
	dim, dimKnown, err := s.db.getMetaInt(ctx, metaKeyEmbedDim)
	if err != nil {
		return err
	}

	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: ReplaceByDoc: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE chunks SET valid_to = ? WHERE ref_doc_id = ? AND valid_to IS NULL
	`, time.Now().UTC().Format(time.RFC3339Nano), docID); err != nil {
		return fmt.Errorf("sqlite: ReplaceByDoc: soft-close: %w", err)
	}
	if err := s.upsertChunks(ctx, tx, chunks, dim, dimKnown); err != nil {
		return fmt.Errorf("sqlite: ReplaceByDoc: upsert chunks: %w", err)
	}
	if err := bumpCorpusVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *VectorStore) upsertChunks(ctx context.Context, tx *sql.Tx, chunks []vector.Chunk, dim int, dimKnown bool) error {
	if len(chunks) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chunks (id, ref_doc_id, text, file_path, file_name, source, token_count, chunk_index, embedding, metadata, created_at, valid_to, replaces, superseded_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			ref_doc_id = excluded.ref_doc_id,
			text = excluded.text,
			file_path = excluded.file_path,
			file_name = excluded.file_name,
			source = excluded.source,
			token_count = excluded.token_count,
			chunk_index = excluded.chunk_index,
			embedding = excluded.embedding,
			metadata = excluded.metadata,
			created_at = chunks.created_at,
			valid_to = CASE WHEN excluded.valid_to IS NOT NULL THEN excluded.valid_to ELSE chunks.valid_to END,
			replaces = excluded.replaces,
			superseded_by = excluded.superseded_by
	`)
	if err != nil {
		return fmt.Errorf("sqlite: upsertChunks: prepare: %w", err)
	}
	defer stmt.Close()

	for _, c := range chunks {
		if c.ID == "" {
			return fmt.Errorf("sqlite: upsertChunks: chunk missing id (ref_doc_id=%q)", c.RefDocID)
		}

		var embBytes []byte
		if len(c.Embedding) > 0 {
			if dimKnown && len(c.Embedding) != dim {
				return fmt.Errorf("%w: chunk %q embedding len=%d, index dim=%d", vector.ErrDimMismatch, c.ID, len(c.Embedding), dim)
			}
			embBytes = encodeVector(c.Embedding)
		}

		metaJSON, err := encodeMetadata(c.Metadata)
		if err != nil {
			return fmt.Errorf("sqlite: upsertChunks: chunk %q: encode metadata: %w", c.ID, err)
		}

		createdAt := c.CreatedAt
		if createdAt == "" {
			createdAt = time.Now().UTC().Format(time.RFC3339Nano)
		}

		if _, err := stmt.ExecContext(ctx, c.ID, c.RefDocID, c.Text, c.FilePath, c.FileName, c.Source, c.TokenCount, c.ChunkIndex, embBytes, metaJSON, createdAt, nullableStr(c.ValidTo), nullableStr(c.Replaces), nullableStr(c.SupersededBy)); err != nil {
			return fmt.Errorf("sqlite: upsertChunks: chunk %q: %w", c.ID, err)
		}
	}
	return nil
}

// SoftCloseByDoc sets valid_to on every currently active chunk of docID,
// keeping the rows as version history instead of deleting them. The chunk
// lifecycle columns of already-closed versions are left untouched.
func (s *VectorStore) SoftCloseByDoc(ctx context.Context, docID string) error {
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: SoftCloseByDoc: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE chunks SET valid_to = ? WHERE ref_doc_id = ? AND valid_to IS NULL
	`, time.Now().UTC().Format(time.RFC3339Nano), docID); err != nil {
		return fmt.Errorf("sqlite: SoftCloseByDoc: %w", err)
	}
	if err := bumpCorpusVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// SetSuperseded marks the given chunks as superseded by refDocID, softly
// invalidating them for ranking without removing them from retrieval.
func (s *VectorStore) SetSuperseded(ctx context.Context, chunkIDs []string, byRefDocID string) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: SetSuperseded: begin: %w", err)
	}
	defer tx.Rollback()

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunkIDs)), ",")
	args := make([]any, 0, len(chunkIDs)+1)
	args = append(args, byRefDocID)
	for _, id := range chunkIDs {
		args = append(args, id)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE chunks SET superseded_by = ? WHERE id IN (%s)
	`, placeholders), args...); err != nil {
		return fmt.Errorf("sqlite: SetSuperseded: %w", err)
	}
	if err := bumpCorpusVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearSupersededBy resets superseded_by on every chunk previously marked
// by refDocID, e.g. when the superseding document is removed.
func (s *VectorStore) ClearSupersededBy(ctx context.Context, refDocID string) error {
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: ClearSupersededBy: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE chunks SET superseded_by = NULL WHERE superseded_by = ?
	`, refDocID); err != nil {
		return fmt.Errorf("sqlite: ClearSupersededBy: %w", err)
	}
	if err := bumpCorpusVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *VectorStore) ClearSupersededOnDoc(ctx context.Context, docID string) error {
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: ClearSupersededOnDoc: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE chunks SET superseded_by = NULL
		WHERE ref_doc_id = ? AND valid_to IS NULL AND superseded_by IS NOT NULL
	`, docID); err != nil {
		return fmt.Errorf("sqlite: ClearSupersededOnDoc: %w", err)
	}
	if err := bumpCorpusVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *VectorStore) DeleteByDoc(ctx context.Context, docID string) error {
	tx, err := s.db.beginWriteTx(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: DeleteByDoc: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE ref_doc_id = ?`, docID); err != nil {
		return fmt.Errorf("sqlite: DeleteByDoc: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM doc_hashes WHERE ref_doc_id = ?`, docID); err != nil {
		return fmt.Errorf("sqlite: DeleteByDoc: delete hash: %w", err)
	}
	if err := bumpCorpusVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// DocHash returns the last-recorded content hash for docID, if any.
func (s *VectorStore) DocHash(ctx context.Context, docID string) (string, bool, error) {
	var hash string
	err := s.db.sql.QueryRowContext(ctx, `SELECT hash FROM doc_hashes WHERE ref_doc_id = ?`, docID).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sqlite: DocHash: %w", err)
	}
	return hash, true, nil
}

// SetDocHash records docID's content hash, so a future reindex with
// unchanged content can be skipped.
func (s *VectorStore) SetDocHash(ctx context.Context, docID, hash string) error {
	_, err := s.db.sql.ExecContext(ctx, `
		INSERT INTO doc_hashes (ref_doc_id, hash) VALUES (?, ?)
		ON CONFLICT(ref_doc_id) DO UPDATE SET hash = excluded.hash
	`, docID, hash)
	if err != nil {
		return fmt.Errorf("sqlite: SetDocHash: %w", err)
	}
	return nil
}

func (s *VectorStore) Query(ctx context.Context, vec []float32, k int, filter vector.Filter) ([]vector.ScoredChunk, error) {
	if k <= 0 {
		return nil, nil
	}

	query := `SELECT ` + chunkSelectCols + `
		FROM chunks WHERE embedding IS NOT NULL AND valid_to IS NULL`
	args := make([]any, 0, len(filter.Sources))
	if len(filter.Sources) > 0 {
		query += fmt.Sprintf(" AND source IN (%s)", strings.TrimSuffix(strings.Repeat("?,", len(filter.Sources)), ","))
		for _, src := range filter.Sources {
			args = append(args, src)
		}
	}

	rows, err := s.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: Query: %w", err)
	}
	defer rows.Close()

	var scored []vector.ScoredChunk
	for rows.Next() {
		c, err := scanChunk(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: Query: scan: %w", err)
		}
		if !filter.Matches(c.Source, c.Metadata) {
			continue
		}
		scored = append(scored, vector.ScoredChunk{
			Chunk: c,
			Score: cosineSimilarity(vec, c.Embedding),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: Query: %w", err)
	}

	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > k {
		scored = scored[:k]
	}
	return scored, nil
}

// QueryCandidates scores only the supplied candidate chunks instead of every
// active embedding. It is the ANN-prefilter path: the retriever builds a
// small candidate set from FTS5 plus entity-linking and then calls this to
// compute cosine similarity over O(K) rows rather than O(N).
func (s *VectorStore) QueryCandidates(ctx context.Context, vec []float32, k int, candidateIDs []string, filter vector.Filter) ([]vector.ScoredChunk, error) {
	if k <= 0 || len(candidateIDs) == 0 {
		return nil, nil
	}

	ids := dedupeStrings(candidateIDs)
	if len(ids) == 0 {
		return nil, nil
	}

	query := `SELECT ` + chunkSelectCols + `
		FROM chunks WHERE embedding IS NOT NULL AND valid_to IS NULL`
	args := make([]any, 0, len(ids)+len(filter.Sources))
	query += fmt.Sprintf(" AND id IN (%s)", strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","))
	for _, id := range ids {
		args = append(args, id)
	}
	if len(filter.Sources) > 0 {
		query += fmt.Sprintf(" AND source IN (%s)", strings.TrimSuffix(strings.Repeat("?,", len(filter.Sources)), ","))
		for _, src := range filter.Sources {
			args = append(args, src)
		}
	}

	rows, err := s.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: QueryCandidates: %w", err)
	}
	defer rows.Close()

	var scored []vector.ScoredChunk
	for rows.Next() {
		c, err := scanChunk(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: QueryCandidates: scan: %w", err)
		}
		if !filter.Matches(c.Source, c.Metadata) {
			continue
		}
		scored = append(scored, vector.ScoredChunk{
			Chunk: c,
			Score: cosineSimilarity(vec, c.Embedding),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: QueryCandidates: %w", err)
	}

	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > k {
		scored = scored[:k]
	}
	return scored, nil
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func (s *VectorStore) AllForBM25(ctx context.Context) ([]vector.Chunk, error) {
	rows, err := s.db.sql.QueryContext(ctx, `SELECT `+chunkSelectCols+` FROM chunks WHERE valid_to IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: AllForBM25: %w", err)
	}
	defer rows.Close()

	var chunks []vector.Chunk
	for rows.Next() {
		c, err := scanChunk(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: AllForBM25: scan: %w", err)
		}
		chunks = append(chunks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: AllForBM25: %w", err)
	}
	return chunks, nil
}

func (s *VectorStore) ChunksByDoc(ctx context.Context, docID string) ([]vector.Chunk, error) {
	rows, err := s.db.sql.QueryContext(ctx, `SELECT `+chunkSelectCols+` FROM chunks WHERE ref_doc_id = ? ORDER BY rowid`, docID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: ChunksByDoc: %w", err)
	}
	defer rows.Close()

	var chunks []vector.Chunk
	for rows.Next() {
		c, err := scanChunk(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: ChunksByDoc: scan: %w", err)
		}
		chunks = append(chunks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: ChunksByDoc: %w", err)
	}
	return chunks, nil
}

const chunkSelectCols = `id, ref_doc_id, text, file_path, file_name, source, token_count, chunk_index, embedding, metadata, created_at, valid_to, replaces, superseded_by`

func scanChunk(row scanner) (vector.Chunk, error) {
	var c vector.Chunk
	var embBytes []byte
	var metaJSON []byte
	var createdAt, validTo, replaces, supersededBy sql.NullString
	if err := row.Scan(&c.ID, &c.RefDocID, &c.Text, &c.FilePath, &c.FileName, &c.Source, &c.TokenCount, &c.ChunkIndex, &embBytes, &metaJSON, &createdAt, &validTo, &replaces, &supersededBy); err != nil {
		return vector.Chunk{}, err
	}
	c.Embedding = decodeVector(embBytes)
	c.CreatedAt = createdAt.String
	c.ValidTo = validTo.String
	c.Replaces = replaces.String
	c.SupersededBy = supersededBy.String
	m, err := decodeMetadata(metaJSON)
	if err != nil {
		return vector.Chunk{}, err
	}
	c.Metadata = m
	return c, nil
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func encodeMetadata(m map[string]string) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	return b, nil
}

func decodeMetadata(b []byte) (map[string]string, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	return m, nil
}

func encodeVector(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeVector(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
