package telegram

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
	msg := apiMessage{
		MessageID: 42,
		From:      &apiUser{Username: "alice"},
		Chat:      apiChat{ID: -100123, Title: "Team Chat", Username: "teamchat"},
		Date:      time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC).Unix(),
		Text:      "Hello team, deployment is done.",
	}

	d := buildDocument("main-chat", msg)
	if d.Kind != "message" {
		t.Fatalf("Kind = %q, want message", d.Kind)
	}
	if d.ID != "telegram:-100123:42" {
		t.Fatalf("ID = %q, want telegram:-100123:42", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "message.md", got)
}

func TestBuildDocument_ReplyFrontmatterGolden(t *testing.T) {
	msg := apiMessage{
		MessageID:      43,
		From:           &apiUser{FirstName: "Bob"},
		Chat:           apiChat{ID: -100123, Title: "Team Chat", Username: "teamchat"},
		Date:           time.Date(2026, 3, 4, 5, 10, 0, 0, time.UTC).Unix(),
		Text:           "Thanks!",
		ReplyToMessage: &apiReplyRef{MessageID: 42},
	}

	d := buildDocument("main-chat", msg)
	if d.Frontmatter["thread"] != int64(42) {
		t.Fatalf("thread = %v, want 42", d.Frontmatter["thread"])
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "reply.md", got)
}

func TestMessageTitle_TruncatesAndDefaults(t *testing.T) {
	if got := messageTitle(""); got != "Telegram message" {
		t.Fatalf("messageTitle(empty) = %q", got)
	}
	if got := messageTitle("line one\nline two"); got != "line one" {
		t.Fatalf("messageTitle(multiline) = %q, want first line only", got)
	}
}
