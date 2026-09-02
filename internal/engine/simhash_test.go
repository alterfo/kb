package engine

import (
	"strings"
	"testing"
)

func TestSimhashFingerprintIdenticalTextHasZeroDistance(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog while the cat watches from the window and the sun sets slowly."
	if got := hammingDistance(simhashFingerprint(text), simhashFingerprint(text)); got != 0 {
		t.Fatalf("hamming distance of identical text = %d, want 0", got)
	}
}

func TestSimhashFingerprintNearDuplicateHasSmallDistance(t *testing.T) {
	base := "The quick brown fox jumps over the lazy dog while the cat watches from the window and the sun sets slowly over the hills."
	modified := strings.Replace(base, "quick", "fast", 1)
	distance := hammingDistance(simhashFingerprint(base), simhashFingerprint(modified))
	if distance > nearDuplicateMaxBits {
		t.Fatalf("hamming distance for near duplicate = %d, want <= %d", distance, nearDuplicateMaxBits)
	}
}

func TestSimhashFingerprintUnrelatedTextHasLargeDistance(t *testing.T) {
	a := "Project alpha ships a graph database with vector retrieval, citation synthesis, and local SQLite persistence."
	b := "The bakery opens at sunrise and sells warm croissants, sourdough loaves, and honey cakes every weekday morning."
	if got := hammingDistance(simhashFingerprint(a), simhashFingerprint(b)); got <= nearDuplicateMaxBits {
		t.Fatalf("hamming distance for unrelated text = %d, want > %d", got, nearDuplicateMaxBits)
	}
}
