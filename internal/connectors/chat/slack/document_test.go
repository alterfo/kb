package slack

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
	m := apiMessage{
		Type: "message",
		User: "U1",
		Text: "Deploy finished successfully.",
		Ts:   "1700000010.000100",
	}

	d := buildDocument("main-workspace", "C1", m)
	if d.Kind != "message" {
		t.Fatalf("Kind = %q, want message", d.Kind)
	}
	if d.ID != "slack:C1:1700000010.000100" {
		t.Fatalf("ID = %q, want slack:C1:1700000010.000100", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "message.md", got)
}

func TestBuildDocument_ReplyFrontmatterGolden(t *testing.T) {
	m := apiMessage{
		Type:     "message",
		User:     "U2",
		Text:     "Thanks for the update!",
		Ts:       "1700000011.000100",
		ThreadTs: "1700000010.000100",
	}

	d := buildDocument("main-workspace", "C1", m)
	if d.Frontmatter["thread"] != "1700000010.000100" {
		t.Fatalf("thread = %v, want 1700000010.000100", d.Frontmatter["thread"])
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "reply.md", got)
}

func TestBuildDocument_ThreadRootJoinsGroup(t *testing.T) {
	m := apiMessage{
		Type:     "message",
		User:     "U1",
		Text:     "parent",
		Ts:       "1700000010.000100",
		ThreadTs: "1700000010.000100",
	}
	d := buildDocument("main-workspace", "C1", m)
	if d.Frontmatter["thread"] != "1700000010.000100" {
		t.Fatalf("thread root frontmatter = %v, want own ts so the chain glues into one chunk", d.Frontmatter["thread"])
	}
}

func TestBuildDocument_EditedFrontmatter(t *testing.T) {
	m := apiMessage{
		Type:   "message",
		User:   "U1",
		Text:   "Fixed text",
		Ts:     "1700000010.000100",
		Edited: &apiEdited{User: "U1", Ts: "1700000015.000200"},
	}
	d := buildDocument("main-workspace", "C1", m)
	if d.Frontmatter["edit_at"] != "2023-11-14T22:13:35Z" {
		t.Fatalf("edit_at = %v, want 2023-11-14T22:13:35Z", d.Frontmatter["edit_at"])
	}
	if d.Body != "Fixed text" {
		t.Fatalf("Body = %q, want edited text", d.Body)
	}
}

func TestBuildDocument_NoEditedFieldSkipsEditAt(t *testing.T) {
	m := apiMessage{Type: "message", User: "U1", Text: "plain", Ts: "1700000010.000100"}
	d := buildDocument("main-workspace", "C1", m)
	if _, ok := d.Frontmatter["edit_at"]; ok {
		t.Fatalf("edit_at present without edited field: %v", d.Frontmatter["edit_at"])
	}
}

func TestMessageTitle_TruncatesAndDefaults(t *testing.T) {
	if got := messageTitle(""); got != "Slack message" {
		t.Fatalf("messageTitle(empty) = %q", got)
	}
	if got := messageTitle("line one\nline two"); got != "line one" {
		t.Fatalf("messageTitle(multiline) = %q, want first line only", got)
	}
}

func TestCompareTs(t *testing.T) {
	if compareTs("2.0", "") <= 0 {
		t.Fatal("compareTs against empty baseline should be > 0")
	}
	if compareTs("1.0", "2.0") >= 0 {
		t.Fatal("compareTs(1.0, 2.0) should be < 0")
	}
	if compareTs("2.0", "2.0") != 0 {
		t.Fatal("compareTs(2.0, 2.0) should be 0")
	}
}
