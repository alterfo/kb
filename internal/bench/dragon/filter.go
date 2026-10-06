package dragon

import (
	"strconv"
	"time"

	"github.com/alterfo/kb/internal/bench/corpus"
)

// LimitTexts keeps the first limit texts (0 or a value >= len(texts) keeps all).
func LimitTexts(texts []Text, limit int) []Text {
	if limit > 0 && limit < len(texts) {
		return texts[:limit]
	}
	return texts
}

// TextIDSet returns the string document IDs present in texts.
func InvertMapping(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[v] = k
	}
	return out
}

func TextIDSet(texts []Text) map[string]struct{} {
	ids := make(map[string]struct{}, len(texts))
	for _, t := range texts {
		ids[strconv.Itoa(t.ID)] = struct{}{}
	}
	return ids
}

// FilterQuestions returns the questions whose gold source documents are all
// contained in kept. Malformed gold TextIDs entries are skipped (fail-open)
// and counted so callers can report them; empty gold document sets are also
// excluded because they carry no retrievable source signal. Gold text_ids
// live in the private id space while kept holds public text ids, so
// privateToPublic translates between them (nil means the ids already agree).
func FilterQuestions(gold []GoldQA, questions []corpus.Question, kept map[string]struct{}, privateToPublic map[string]string) (matched []corpus.Question, malformed int) {
	allowed := make(map[string]struct{}, len(gold))
	for _, g := range gold {
		ids, err := flattenTextIDs(g.TextIDs)
		if err != nil {
			malformed++
			continue
		}
		if len(ids) == 0 {
			continue
		}
		full := true
		for _, id := range ids {
			if privateToPublic != nil {
				pub, ok := privateToPublic[id]
				if !ok {
					full = false
					break
				}
				id = pub
			}
			if _, ok := kept[id]; !ok {
				full = false
				break
			}
		}
		if full {
			allowed[strconv.Itoa(g.PublicID)] = struct{}{}
		}
	}
	for _, q := range questions {
		if _, ok := allowed[q.ID]; ok {
			matched = append(matched, q)
		}
	}
	return matched, malformed
}

// MaxDocLimit returns the largest doc-limit that keeps a full indexing pass
// under budget given a measured seconds-per-doc rate. Non-positive inputs
// yield zero (no recommendation) rather than an invalid count.
func MaxDocLimit(secondsPerDoc float64, budget time.Duration) int {
	if secondsPerDoc <= 0 {
		return 0
	}
	secs := budget.Seconds()
	if secs <= 0 {
		return 0
	}
	n := int(secs / secondsPerDoc)
	for n > 0 && float64(n)*secondsPerDoc > secs {
		n--
	}
	return n
}
