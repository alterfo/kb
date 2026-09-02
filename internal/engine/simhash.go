package engine

import (
	"context"
	"hash/fnv"
	"log"
	"math/bits"
	"strings"
	"unicode"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/store/vector"
)

const nearDuplicateMaxBits = 12

type documentFingerprintStore interface {
	SetDocumentFingerprint(ctx context.Context, docID string, fingerprint uint64, duplicateOf string) error
	DocumentFingerprint(ctx context.Context, docID string) (vector.DocumentFingerprint, bool, error)
	ListDocumentFingerprints(ctx context.Context) ([]vector.DocumentFingerprint, error)
}

type nearDuplicateDecision struct {
	available   bool
	skip        bool
	duplicateOf string
	fingerprint uint64
}

func (ix *Indexer) nearDuplicateDecisionFor(ctx context.Context, doc connector.Document, refDocID string) nearDuplicateDecision {
	store, ok := ix.vector.(documentFingerprintStore)
	if !ok || doc.Kind == "message" {
		return nearDuplicateDecision{}
	}
	fingerprint := simhashFingerprint(doc.Body)
	maxBits := nearDuplicateMaxBits
	if len(simhashWords(doc.Body)) < 3 {
		maxBits = 0
	}
	decision := nearDuplicateDecision{available: true, fingerprint: fingerprint}
	rows, err := store.ListDocumentFingerprints(ctx)
	if err != nil {
		return decision
	}
	byRefDocID := make(map[string]vector.DocumentFingerprint, len(rows))
	for _, row := range rows {
		byRefDocID[row.RefDocID] = row
	}
	// A previous run may have flagged this document as a duplicate of some
	// target. Re-validate rather than trusting the stored flag: the target
	// may since have been deleted, or this document's own content may have
	// changed enough that it no longer resembles the target. Either way,
	// falling through to the corpus scan below re-derives the correct
	// decision from the current body instead of leaving a stale document
	// permanently unindexed.
	if own, ok := byRefDocID[refDocID]; ok && own.DuplicateOf != "" {
		if target, ok := byRefDocID[own.DuplicateOf]; ok && target.DuplicateOf == "" && hammingDistance(fingerprint, target.Fingerprint) <= maxBits {
			decision.skip = true
			decision.duplicateOf = own.DuplicateOf
			return decision
		}
	}
	for _, row := range rows {
		if row.RefDocID == refDocID || row.DuplicateOf != "" || InferSource(row.RefDocID) != doc.Source {
			continue
		}
		if hammingDistance(fingerprint, row.Fingerprint) <= maxBits {
			decision.skip = true
			decision.duplicateOf = row.RefDocID
			return decision
		}
	}
	return decision
}

func (ix *Indexer) recordDocumentFingerprint(ctx context.Context, refDocID string, decision nearDuplicateDecision) {
	if !decision.available {
		return
	}
	store, ok := ix.vector.(documentFingerprintStore)
	if !ok {
		return
	}
	if err := store.SetDocumentFingerprint(ctx, refDocID, decision.fingerprint, decision.duplicateOf); err != nil {
		log.Printf("engine: record fingerprint %q: %v (continuing)", refDocID, err)
	}
}

func simhashFingerprint(text string) uint64 {
	features := simhashFeatures(text)
	var weights [64]int
	for feature, count := range features {
		for bit := 0; bit < 64; bit++ {
			if feature&(uint64(1)<<bit) != 0 {
				weights[bit] += count
			} else {
				weights[bit] -= count
			}
		}
	}
	var fingerprint uint64
	for bit, weight := range weights {
		if weight > 0 {
			fingerprint |= uint64(1) << bit
		}
	}
	return fingerprint
}

func simhashFeatures(text string) map[uint64]int {
	words := simhashWords(text)

	features := make(map[uint64]int)
	if len(words) == 0 {
		return features
	}
	if len(words) < 3 {
		features[featureHash(strings.Join(words, " "))]++
		return features
	}
	for i := 0; i+3 <= len(words); i++ {
		features[featureHash(strings.Join(words[i:i+3], " "))]++
	}
	return features
}

func simhashWords(text string) []string {
	words := make([]string, 0)
	var builder strings.Builder
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		words = append(words, builder.String())
		builder.Reset()
	}
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return words
}

func featureHash(token string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(token))
	return h.Sum64()
}

func hammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}
