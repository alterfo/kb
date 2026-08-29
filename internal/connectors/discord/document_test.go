package discord

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/render"
)

func goldenPath(name string) string {
	return filepath.Join("testdata", name)
}

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(goldenPath(name))
	if err != nil {
		t.Fatalf("reading golden %s: %v", name, err)
	}
	if string(got) != string(want) {
		t.Fatalf("render mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestBuildDocument_MessageFrontmatterGolden(t *testing.T) {
	m := apiMessage{
		ID:        "101",
		ChannelID: "C1",
		Author:    apiUser{ID: "u1", Username: "alice"},
		Content:   "Deploy finished successfully.",
		Timestamp: "2026-08-22T10:00:00+00:00",
	}
	d := buildDocument("leon-discord", "G1", "https://discord.com", "C1", m, "")
	if d.Kind != "message" {
		t.Fatalf("Kind = %q, want message", d.Kind)
	}
	if d.ID != "C1-101" {
		t.Fatalf("ID = %q, want C1-101", d.ID)
	}
	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "message.md", got)
}

func TestBuildDocument_ReplyAndThreadTopicGolden(t *testing.T) {
	ref := &apiMessage{ID: "101", ChannelID: "C1", Author: apiUser{ID: "u1", Username: "alice"}, Content: "Deploy finished successfully.", Timestamp: "2026-08-22T10:00:00+00:00"}
	m := apiMessage{
		ID:                "102",
		ChannelID:         "C1",
		Author:            apiUser{ID: "u2", Username: "bob"},
		Content:           "Thanks for the update!",
		Timestamp:         "2026-08-22T10:01:00+00:00",
		ReferencedMessage: ref,
	}
	d := buildDocument("leon-discord", "G1", "https://discord.com", "C1", m, "Deploy finished successfully.")
	if d.Frontmatter["thread"] != "101" {
		t.Fatalf("thread = %v, want 101", d.Frontmatter["thread"])
	}
	if d.Frontmatter["parent_id"] != "101" {
		t.Fatalf("parent_id = %v, want 101", d.Frontmatter["parent_id"])
	}
	if d.Frontmatter["thread_topic"] != "Deploy finished successfully." {
		t.Fatalf("thread_topic = %v", d.Frontmatter["thread_topic"])
	}
	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "reply.md", got)
}

func TestBuildDocument_EmptyContentGetsDefaultTitle(t *testing.T) {
	m := apiMessage{ID: "1", ChannelID: "C1", Timestamp: "2026-08-22T10:00:00+00:00"}
	d := buildDocument("ds", "", "https://discord.com", "C1", m, "")
	if d.Title != "Discord message" {
		t.Fatalf("Title = %q, want Discord message", d.Title)
	}
	if d.URL != "" {
		t.Fatalf("URL = %q, want empty without guild_id", d.URL)
	}
}

func TestParseTimestampInvalidReturnsZero(t *testing.T) {
	if !parseTimestamp("not-a-time").IsZero() {
		t.Fatal("expected zero time for invalid timestamp")
	}
}

func TestMessageTitle_TruncatesByRunes(t *testing.T) {
	text := strings.Repeat("я", 100)
	got := messageTitle(text)
	if len([]rune(got)) != 80 {
		t.Fatalf("title runes = %d, want 80", len([]rune(got)))
	}
	if !strings.HasPrefix(text, got) {
		t.Fatalf("title = %q, want prefix of input", got)
	}
}
