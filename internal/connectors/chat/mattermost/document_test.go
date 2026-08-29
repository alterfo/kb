package mattermost

import (
	"os"
	"path/filepath"
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
	p := apiPost{
		ID:        "abc123",
		ChannelID: "C1",
		UserID:    "u1",
		CreateAt:  1700000010000,
		Message:   "Deploy finished successfully.",
	}

	d := buildDocument("main-team", "https://mm.example", "acme", "C1", p)
	if d.Kind != "message" {
		t.Fatalf("Kind = %q, want message", d.Kind)
	}
	if d.ID != "mattermost:C1:abc123" {
		t.Fatalf("ID = %q, want mattermost:C1:abc123", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "message.md", got)
}

func TestBuildDocument_ReplyFrontmatterGolden(t *testing.T) {
	p := apiPost{
		ID:        "def456",
		RootID:    "abc123",
		ChannelID: "C1",
		UserID:    "u2",
		CreateAt:  1700000011000,
		Message:   "Thanks for the update!",
	}

	d := buildDocument("main-team", "https://mm.example", "acme", "C1", p)
	if d.Frontmatter["thread"] != "abc123" {
		t.Fatalf("thread = %v, want abc123", d.Frontmatter["thread"])
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "reply.md", got)
}

func TestBuildDocument_NoTeamMeansNoURL(t *testing.T) {
	p := apiPost{ID: "x", ChannelID: "C1", UserID: "u1", CreateAt: 1700000010000, Message: "hi"}
	d := buildDocument("main-team", "https://mm.example", "", "C1", p)
	if d.URL != "" {
		t.Fatalf("URL = %q, want empty when team is unset", d.URL)
	}
}

func TestBuildDocument_EditAtFrontmatter(t *testing.T) {
	p := apiPost{
		ID:        "abc123",
		ChannelID: "C1",
		UserID:    "u1",
		CreateAt:  1700000010000,
		EditAt:    1700000015000,
		Message:   "Fixed text",
	}
	d := buildDocument("main-team", "https://mm.example", "acme", "C1", p)
	if d.Frontmatter["edit_at"] != "2023-11-14T22:13:35Z" {
		t.Fatalf("edit_at = %v, want 2023-11-14T22:13:35Z", d.Frontmatter["edit_at"])
	}
	if d.Body != "Fixed text" {
		t.Fatalf("Body = %q, want edited text", d.Body)
	}
}

func TestBuildDocument_NoEditAtSkipsFrontmatter(t *testing.T) {
	p := apiPost{ID: "x", ChannelID: "C1", UserID: "u1", CreateAt: 1700000010000, Message: "hi"}
	d := buildDocument("main-team", "https://mm.example", "acme", "C1", p)
	if _, ok := d.Frontmatter["edit_at"]; ok {
		t.Fatalf("edit_at present without EditAt: %v", d.Frontmatter["edit_at"])
	}
}

func TestMessageTitle_TruncatesAndDefaults(t *testing.T) {
	if got := messageTitle(""); got != "Mattermost message" {
		t.Fatalf("messageTitle(empty) = %q", got)
	}
	if got := messageTitle("line one\nline two"); got != "line one" {
		t.Fatalf("messageTitle(multiline) = %q, want first line only", got)
	}
}
