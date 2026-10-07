package report

import (
	"fmt"
	"strings"

	"github.com/alterfo/kb/internal/store/vector"
)

// SupersessionBlock renders the superseded-vs-replacement pairs found in
// chunks as an instruction block for synthesis prompts. It is empty when no
// superseded chunk has its replacement in the same set. The block tells the
// model to prefer the newer document and to name the conflict explicitly
// instead of silently picking a side.
func SupersessionBlock(chunks []vector.ScoredChunk) string {
	byDoc := make(map[string]vector.ScoredChunk, len(chunks))
	for _, c := range chunks {
		if _, ok := byDoc[c.Chunk.RefDocID]; !ok {
			byDoc[c.Chunk.RefDocID] = c
		}
	}

	var lines []string
	for _, c := range chunks {
		if c.Chunk.SupersededBy == "" {
			continue
		}
		newer, ok := byDoc[c.Chunk.SupersededBy]
		if !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("- OLD: %s (%s) superseded by NEW: %s (%s)",
			c.Chunk.FileName, chunkDocDate(c.Chunk),
			newer.Chunk.FileName, chunkDocDate(newer.Chunk)))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Superseded documents detected. This is a background note, not the question: use it only when the " +
		"excerpts above give different values for the question being asked (then prefer the newer document and " +
		"state both values). Never answer about this note itself and do not mention it otherwise:\n" +
		strings.Join(lines, "\n")
}

func chunkDocDate(c vector.Chunk) string {
	for _, key := range []string{"last_updated", "created_at", "updated_at"} {
		if v, ok := c.Metadata[key]; ok && v != "" {
			return v
		}
	}
	return "date unknown"
}
