package markdown

import (
	"net/url"
	"testing"
)

func renderHTML(t *testing.T, in, base string) string {
	t.Helper()
	n, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse(%q): %v", in, err)
	}
	var b *url.URL
	if base != "" {
		b, err = url.Parse(base)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", base, err)
		}
	}
	return Render(n, b)
}

func TestRenderElements(t *testing.T) {
	cases := []struct {
		name string
		in   string
		base string
		want string
	}{
		{"heading", "<h2>Hello</h2>", "", "## Hello"},
		{"inline bold", "<p>a <strong>b</strong> c</p>", "", "a **b** c"},
		{"relative link", `<p><a href="/x">X</a></p>`, "https://example.com/docs", "[X](https://example.com/x)"},
		{"unordered list", "<ul><li>one</li><li>two</li></ul>", "", "- one\n- two"},
		{"ordered list", "<ol><li>a</li><li>b</li></ol>", "", "1. a\n2. b"},
		{"code block", "<pre><code>a\nb</code></pre>", "", "```\na\nb\n```"},
		{"image", `<p><img src="pic.png" alt="P"></p>`, "https://example.com/docs/", "![P](https://example.com/docs/pic.png)"},
		{"blockquote", "<blockquote>quote</blockquote>", "", "> quote"},
		{"hr", "<hr>", "", "---"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderHTML(t, tc.in, tc.base); got != tc.want {
				t.Fatalf("Render(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderEmpty(t *testing.T) {
	if got := renderHTML(t, "", ""); got != "" {
		t.Fatalf("Render(empty) = %q, want empty", got)
	}
}

func TestNormalizeSpace(t *testing.T) {
	if got := NormalizeSpace("  a\t\n b  "); got != "a b" {
		t.Fatalf("NormalizeSpace = %q, want %q", got, "a b")
	}
}

func TestTextContent(t *testing.T) {
	n, err := Parse([]byte("<div>a<span>b</span>c</div>"))
	if err != nil {
		t.Fatal(err)
	}
	if got := TextContent(n); got != "abc" {
		t.Fatalf("TextContent = %q, want abc", got)
	}
}
