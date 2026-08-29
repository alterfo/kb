package got

import (
	"context"
	"fmt"

	"github.com/alterfo/kb/internal/store/vector"
	"github.com/alterfo/kb/internal/verify"
)

type ContradictionDetector interface {
	Detect(ctx context.Context, query string, chunks []verify.Chunk) (verify.ContradictionReport, error)
}

func (o *Orchestrator) detectContradictions(ctx context.Context, query string, chunks []vector.ScoredChunk) []string {
	if o.cfg.ContradictionDetector == nil || !o.cfg.DetectContradictions || len(chunks) == 0 {
		return nil
	}
	rep, err := o.cfg.ContradictionDetector.Detect(ctx, query, toVerifyChunks(chunks))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rep.Contradictions))
	for _, c := range rep.Contradictions {
		out = append(out, fmt.Sprintf("%s <-> %s: %s", c.ChunkA, c.ChunkB, c.Reason))
	}
	return out
}

func toVerifyChunks(chunks []vector.ScoredChunk) []verify.Chunk {
	out := make([]verify.Chunk, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, verify.Chunk{
			FileName: c.FileName,
			FilePath: c.FilePath,
			ChunkID:  c.ID,
			Text:     c.Text,
		})
	}
	return out
}
