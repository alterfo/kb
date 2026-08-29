package mcp

import (
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExtractText_JoinsTextContents(t *testing.T) {
	contents := []*sdk.ResourceContents{
		{URI: "file:///a", Text: "hello"},
		{URI: "file:///a", Text: "world"},
	}
	if got, want := extractText(contents), "hello\n\nworld"; got != want {
		t.Errorf("extractText() = %q, want %q", got, want)
	}
}

func TestExtractText_SkipsBlobOnly(t *testing.T) {
	contents := []*sdk.ResourceContents{
		{URI: "file:///a", Blob: []byte{1, 2, 3}},
	}
	if got := extractText(contents); got != "" {
		t.Errorf("extractText() = %q, want empty", got)
	}
}

func TestFormatToolBody_IncludesDescriptionAndSchema(t *testing.T) {
	tool := &sdk.Tool{
		Name:        "echo",
		Description: "Echoes input",
		InputSchema: map[string]any{"type": "object"},
	}
	body := formatToolBody(tool)
	if !strings.Contains(body, "Echoes input") {
		t.Errorf("body missing description: %q", body)
	}
	if !strings.Contains(body, `"type": "object"`) {
		t.Errorf("body missing schema: %q", body)
	}
}

func TestBuildResourceDocument_TitleFallback(t *testing.T) {
	r := &sdk.Resource{URI: "file:///hello.txt", MIMEType: "text/plain"}
	d := buildResourceDocument("src", r, "content")
	if d.Title != "file:///hello.txt" {
		t.Errorf("Title = %q, want fallback to URI", d.Title)
	}
	if d.ID != "src:file:///hello.txt" {
		t.Errorf("ID = %q", d.ID)
	}
	if d.Kind != "resource" {
		t.Errorf("Kind = %q, want resource", d.Kind)
	}
	if d.Frontmatter["mime_type"] != "text/plain" {
		t.Errorf("Frontmatter[mime_type] = %v", d.Frontmatter["mime_type"])
	}
	if d.Frontmatter["mcp_server"] != "src" {
		t.Errorf("Frontmatter[mcp_server] = %v", d.Frontmatter["mcp_server"])
	}
}

func TestBuildResourceDocument_PrefersTitleThenName(t *testing.T) {
	r := &sdk.Resource{URI: "file:///hello.txt", Name: "hello", Title: "Hello Doc"}
	d := buildResourceDocument("src", r, "content")
	if d.Title != "Hello Doc" {
		t.Errorf("Title = %q, want Hello Doc", d.Title)
	}

	r2 := &sdk.Resource{URI: "file:///hello.txt", Name: "hello"}
	d2 := buildResourceDocument("src", r2, "content")
	if d2.Title != "hello" {
		t.Errorf("Title = %q, want hello", d2.Title)
	}
}

func TestBuildToolDocument(t *testing.T) {
	tool := &sdk.Tool{Name: "echo", Description: "Echoes input"}
	d := buildToolDocument("src", tool, formatToolBody(tool))
	if d.ID != "src:tool:echo" {
		t.Errorf("ID = %q", d.ID)
	}
	if d.Kind != "tool" {
		t.Errorf("Kind = %q, want tool", d.Kind)
	}
	if d.Frontmatter["resource_uri"] != "tool:echo" {
		t.Errorf("Frontmatter[resource_uri] = %v", d.Frontmatter["resource_uri"])
	}
	if !strings.Contains(d.Body, "Echoes input") {
		t.Errorf("Body missing description: %q", d.Body)
	}
}

func TestHashBody_Stable(t *testing.T) {
	h1 := hashBody("hello")
	h2 := hashBody("hello")
	h3 := hashBody("world")
	if h1 != h2 {
		t.Error("hashBody not stable for same input")
	}
	if h1 == h3 {
		t.Error("hashBody collided for different input")
	}
}
