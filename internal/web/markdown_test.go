package web

import (
	"strings"
	"testing"
)

func TestRenderMarkdown_EscapesRawHTML(t *testing.T) {
	out := string(renderMarkdown("hello <script>alert(1)</script>"))
	if strings.Contains(out, "<script>") {
		t.Errorf("raw HTML leaked into output: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected text preserved, got %q", out)
	}
}

func TestRenderMarkdown_BasicBlocks(t *testing.T) {
	out := string(renderMarkdown("# Title\n\nSome **bold** text.\n\n- one\n- two"))
	for _, want := range []string{"<h1", "<strong>bold</strong>", "<ul>", "<li>one</li>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

func TestRenderMarkdown_CodeBlock(t *testing.T) {
	out := string(renderMarkdown("```go\npackage main\n```"))
	if !strings.Contains(out, "package main") {
		t.Errorf("code block content missing: %q", out)
	}
}

func TestRenderMarkdown_BlocksUnsafeLinkSchemes(t *testing.T) {
	out := string(renderMarkdown("[click me](javascript:alert(1))"))
	if strings.Contains(out, "href=\"javascript:") {
		t.Errorf("javascript: link rendered as active href: %q", out)
	}
	if !strings.Contains(out, "click me") {
		t.Errorf("link text lost: %q", out)
	}
}

func TestRenderMarkdown_BlocksUnsafeImageSchemes(t *testing.T) {
	out := string(renderMarkdown("![img](javascript:alert(1))"))
	if strings.Contains(out, "<img") {
		t.Errorf("unsafe image rendered: %q", out)
	}
	out = string(renderMarkdown("![ok](https://example.com/x.png)"))
	if !strings.Contains(out, "<img") {
		t.Errorf("safe image suppressed: %q", out)
	}
}
