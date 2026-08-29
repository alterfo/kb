package chunk

import (
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return tm
}

func TestChatChunker_Empty(t *testing.T) {
	c := NewChatChunker(time.Hour)
	if chunks := c.Chunk(nil); chunks != nil {
		t.Fatalf("expected nil chunks, got %#v", chunks)
	}
}

func TestChatChunker_ThreadNotSplitBySentences(t *testing.T) {
	c := NewChatChunker(time.Hour)
	msgs := []ChatMessage{
		{Text: "Hello. How are you?", ThreadID: "t1", Timestamp: mustTime(t, "2026-08-19T10:00:00Z")},
		{Text: "I am fine. Thanks for asking!", ThreadID: "t1", Timestamp: mustTime(t, "2026-08-19T10:01:00Z")},
		{Text: "Great to hear.", ThreadID: "t1", Timestamp: mustTime(t, "2026-08-19T10:02:00Z")},
	}
	chunks := c.Chunk(msgs)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for a single thread, got %d", len(chunks))
	}
	for _, want := range []string{"Hello. How are you?", "I am fine. Thanks for asking!", "Great to hear."} {
		if !strings.Contains(chunks[0].Text, want) {
			t.Errorf("chunk missing message %q; got %q", want, chunks[0].Text)
		}
	}
	if chunks[0].Metadata["thread_id"] != "t1" {
		t.Errorf("expected thread_id metadata t1, got %q", chunks[0].Metadata["thread_id"])
	}
}

func TestChatChunker_ThreadChangeCreatesBoundary(t *testing.T) {
	c := NewChatChunker(time.Hour)
	msgs := []ChatMessage{
		{Text: "msg in thread 1", ThreadID: "t1", Timestamp: mustTime(t, "2026-08-19T10:00:00Z")},
		{Text: "another in thread 1", ThreadID: "t1", Timestamp: mustTime(t, "2026-08-19T10:01:00Z")},
		{Text: "msg in thread 2", ThreadID: "t2", Timestamp: mustTime(t, "2026-08-19T10:02:00Z")},
	}
	chunks := c.Chunk(msgs)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks (one per thread), got %d: %#v", len(chunks), chunks)
	}
	if chunks[0].Metadata["thread_id"] != "t1" {
		t.Errorf("chunk 0 thread_id = %q, want t1", chunks[0].Metadata["thread_id"])
	}
	if chunks[1].Metadata["thread_id"] != "t2" {
		t.Errorf("chunk 1 thread_id = %q, want t2", chunks[1].Metadata["thread_id"])
	}
	if strings.Contains(chunks[0].Text, "thread 2") {
		t.Errorf("chunk 0 should not contain thread 2 content: %q", chunks[0].Text)
	}
}

func TestChatChunker_LargeTimeGapCreatesBoundary(t *testing.T) {
	c := NewChatChunker(time.Hour)
	msgs := []ChatMessage{
		{Text: "morning message", ThreadID: "t1", Timestamp: mustTime(t, "2026-08-19T09:00:00Z")},
		{Text: "evening message same thread", ThreadID: "t1", Timestamp: mustTime(t, "2026-08-19T22:00:00Z")},
	}
	chunks := c.Chunk(msgs)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks due to large time gap, got %d", len(chunks))
	}
	if strings.Contains(chunks[0].Text, "evening message") {
		t.Errorf("chunk 0 should not contain the message after the gap")
	}
}

func TestChatChunker_ReplyChainKeptWholeEvenIfLarge(t *testing.T) {
	c := NewChatChunker(time.Hour)
	var msgs []ChatMessage
	base := mustTime(t, "2026-08-19T10:00:00Z")
	for i := 0; i < 50; i++ {
		msgs = append(msgs, ChatMessage{
			Text:      strings.Repeat("word ", 20),
			ThreadID:  "t1",
			ParentID:  "root",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}
	chunks := c.Chunk(msgs)
	if len(chunks) != 1 {
		t.Fatalf("expected the whole reply chain to stay in 1 chunk, got %d", len(chunks))
	}
}

func TestChatChunker_SkipsEmptyMessages(t *testing.T) {
	c := NewChatChunker(time.Hour)
	msgs := []ChatMessage{
		{Text: "real content", ThreadID: "t1", Timestamp: mustTime(t, "2026-08-19T10:00:00Z")},
		{Text: "   ", ThreadID: "t1", Timestamp: mustTime(t, "2026-08-19T10:01:00Z")},
	}
	chunks := c.Chunk(msgs)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if strings.TrimSpace(chunks[0].Text) != "real content" {
		t.Errorf("expected only real content, got %q", chunks[0].Text)
	}
}

func TestChatChunker_DefaultMaxGap(t *testing.T) {
	c := NewChatChunker(0)
	if c.MaxGap != 24*time.Hour {
		t.Errorf("expected default MaxGap=24h, got %v", c.MaxGap)
	}
}

func TestChatChunker_MultiSpeakerChunkKeepsAttribution(t *testing.T) {
	c := NewChatChunker(time.Hour)
	msgs := []ChatMessage{
		{Text: "Предлагаю перейти на Postgres", User: "alice", ThreadID: "t1", Timestamp: mustTime(t, "2026-01-10T10:00:00Z")},
		{Text: "Согласен, переходим", User: "bob", ThreadID: "t1", Timestamp: mustTime(t, "2026-01-10T10:05:00Z")},
	}
	chunks := c.Chunk(msgs)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 glued chunk, got %d", len(chunks))
	}
	for _, want := range []string{"alice: Предлагаю перейти на Postgres", "bob: Согласен, переходим"} {
		if !strings.Contains(chunks[0].Text, want) {
			t.Errorf("chunk text missing %q; got %q", want, chunks[0].Text)
		}
	}
	raw := chunks[0].Metadata["speakers"]
	if raw == "" {
		t.Fatal("multi-speaker chunk must carry speakers metadata")
	}
	for _, want := range []string{`"user":"alice"`, `"user":"bob"`, `"ts":"2026-01-10T10:00:00Z"`, `"ts":"2026-01-10T10:05:00Z"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("speakers metadata missing %s; got %s", want, raw)
		}
	}
}

func TestChatChunker_SingleSpeakerChunkUnchanged(t *testing.T) {
	c := NewChatChunker(time.Hour)
	msgs := []ChatMessage{
		{Text: "Просто сообщение", User: "alice", ThreadID: "t1", Timestamp: mustTime(t, "2026-01-10T10:00:00Z")},
	}
	chunks := c.Chunk(msgs)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Text != "Просто сообщение" {
		t.Errorf("single-speaker text must not gain a prefix; got %q", chunks[0].Text)
	}
	if _, ok := chunks[0].Metadata["speakers"]; ok {
		t.Error("single-speaker chunk must not carry speakers metadata")
	}
}

func TestChatChunker_ChunkIndexSequential(t *testing.T) {
	c := NewChatChunker(time.Hour)
	msgs := []ChatMessage{
		{Text: "a", ThreadID: "t1", Timestamp: mustTime(t, "2026-08-19T10:00:00Z")},
		{Text: "b", ThreadID: "t2", Timestamp: mustTime(t, "2026-08-19T10:01:00Z")},
		{Text: "c", ThreadID: "t3", Timestamp: mustTime(t, "2026-08-19T10:02:00Z")},
	}
	chunks := c.Chunk(msgs)
	for i, ch := range chunks {
		if ch.Index != i {
			t.Errorf("chunk %d has Index=%d", i, ch.Index)
		}
	}
}
