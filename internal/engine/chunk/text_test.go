package chunk

import (
	"strings"
	"testing"
)

func TestTextChunker_Empty(t *testing.T) {
	c := NewTextChunker(512, 50)
	chunks, err := c.Chunk("")
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected no chunks for empty text, got %d", len(chunks))
	}
}

func TestTextChunker_ShortDocument(t *testing.T) {
	c := NewTextChunker(512, 50)
	chunks, err := c.Chunk("This is one sentence. This is another.")
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for a short document, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "This is one sentence.") {
		t.Fatalf("unexpected chunk text: %q", chunks[0].Text)
	}
}

func TestTextChunker_SentenceBoundaries(t *testing.T) {
	c := NewTextChunker(512, 50)
	text := "First sentence here. Second sentence here. Third sentence here."
	chunks, err := c.Chunk(text)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	for _, want := range []string{"First sentence here.", "Second sentence here.", "Third sentence here."} {
		if !strings.Contains(chunks[0].Text, want) {
			t.Errorf("chunk missing sentence %q; got %q", want, chunks[0].Text)
		}
	}
}

func TestTextChunker_SizeSplitsIntoMultipleChunks(t *testing.T) {
	c := NewTextChunker(20, 0)
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		sb.WriteString("This is a test sentence number filler words here. ")
	}
	chunks, err := c.Chunk(sb.String())
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, ch := range chunks {
		if ch.Index != i {
			t.Errorf("chunk %d has Index=%d", i, ch.Index)
		}
		if ch.TokenCount == 0 {
			t.Errorf("chunk %d has zero TokenCount", i)
		}
	}
}

func TestTextChunker_Overlap(t *testing.T) {
	c := NewTextChunker(15, 10)
	var sb strings.Builder
	sentences := []string{
		"Alpha bravo charlie delta echo foxtrot golf.",
		"Hotel india juliet kilo lima mike november.",
		"Oscar papa quebec romeo sierra tango uniform.",
		"Victor whiskey xray yankee zulu alpha bravo.",
	}
	for _, s := range sentences {
		sb.WriteString(s)
		sb.WriteString(" ")
	}
	chunks, err := c.Chunk(sb.String())
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks to observe overlap, got %d", len(chunks))
	}

	found := false
	for i := 1; i < len(chunks); i++ {
		firstSentenceOfPrev := strings.Split(chunks[i-1].Text, ".")[0]
		if firstSentenceOfPrev != "" && strings.Contains(chunks[i].Text, strings.TrimSpace(firstSentenceOfPrev)) {
			continue
		}
		lastSentenceOfPrev := lastNonEmpty(strings.Split(chunks[i-1].Text, "."))
		if lastSentenceOfPrev != "" && strings.Contains(chunks[i].Text, strings.TrimSpace(lastSentenceOfPrev)) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected some overlap between consecutive chunks, chunks=%#v", chunks)
	}
}

func lastNonEmpty(parts []string) string {
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.TrimSpace(parts[i]) != "" {
			return parts[i]
		}
	}
	return ""
}

func TestTextChunker_Cyrillic(t *testing.T) {
	c := NewTextChunker(512, 50)
	text := "Это первое предложение на русском. Это второе предложение здесь. А это третье."
	chunks, err := c.Chunk(text)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	for _, want := range []string{"первое предложение", "второе предложение", "третье"} {
		if !strings.Contains(chunks[0].Text, want) {
			t.Errorf("chunk missing %q; got %q", want, chunks[0].Text)
		}
	}
}

func TestTextChunker_DefaultsOnInvalidParams(t *testing.T) {
	c := NewTextChunker(0, -5)
	if c.Size != 512 {
		t.Errorf("expected default Size=512, got %d", c.Size)
	}
	if c.Overlap != 0 {
		t.Errorf("expected default Overlap=0, got %d", c.Overlap)
	}

	c2 := NewTextChunker(10, 100)
	if c2.Overlap != 0 {
		t.Errorf("expected Overlap reset to 0 when >= Size, got %d", c2.Overlap)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("EstimateTokens(\"\") = %d, want 0", got)
	}
	if got := EstimateTokens("a"); got != 1 {
		t.Errorf("EstimateTokens(single char) = %d, want 1", got)
	}
	if got := EstimateTokens(strings.Repeat("a", 400)); got != 100 {
		t.Errorf("EstimateTokens(400 chars) = %d, want 100", got)
	}
}
