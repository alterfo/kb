package ruimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
)

func TestDocIDFromSourcePath(t *testing.T) {
	a := DocIDFromSourcePath("tools/git-cli/index.md")
	b := DocIDFromSourcePath("tools/code-review/index.md")
	if !strings.HasPrefix(a, "dsid_ru") {
		t.Fatalf("id %q missing dsid_ prefix", a)
	}
	if a == b {
		t.Fatalf("ids collide: %q", a)
	}
	if DocIDFromSourcePath("tools/git-cli/index.md") != a {
		t.Fatalf("id not deterministic")
	}
}

func TestSlugFromSourcePath(t *testing.T) {
	if got := SlugFromSourcePath("tools/git-cli/index.md"); got != "git-cli" {
		t.Fatalf("index slug = %q", got)
	}
	if got := SlugFromSourcePath("a11y/README.md"); got != "README" {
		t.Fatalf("file slug = %q", got)
	}
}

func TestSplitFrontmatter(t *testing.T) {
	meta, body, ok := splitFrontmatter("---\ntitle: \"Git CLI\"\n---\n## Кратко\nТело.")
	if !ok || meta != "title: \"Git CLI\"" {
		t.Fatalf("split ok=%v meta=%q", ok, meta)
	}
	if !strings.Contains(body, "## Кратко") {
		t.Fatalf("body = %q", body)
	}
	if _, _, ok := splitFrontmatter("## Без фронтматтера"); ok {
		t.Fatalf("no-frontmatter split should be false")
	}
}

func TestFrontmatterTitle(t *testing.T) {
	src := "---\ntitle: \"Git CLI\"\nauthors:\n  - foo\n---\n## Кратко\nТело."
	title, body := frontmatterTitle(src)
	if title != "Git CLI" {
		t.Fatalf("title = %q", title)
	}
	if !strings.Contains(body, "## Кратко") {
		t.Fatalf("body missing content: %q", body)
	}
	if title, _ := frontmatterTitle("## Нет фронтматтера"); title != "" {
		t.Fatalf("no-frontmatter title = %q", title)
	}
}

func TestMarkdownToText(t *testing.T) {
	src := "## Кратко\n\nТекст с [ссылкой](/tools/git/).\n\n```bash\ngit clone x\n```\n\n![Картинка](images/a.png) и **жирный** и _курсив_ и `код`.\n\n- пункт один\n- пункт два"
	got := MarkdownToText(src)
	for _, want := range []string{"Кратко", "Текст с ссылкой.", "пункт один", "пункт два", "жирный", "курсив", "код"} {
		if !strings.Contains(got, want) {
			t.Fatalf("text missing %q:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"```", "![", "](", "**", "__", "`", "[ссылка", "git clone x"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("text still contains %q:\n%s", notWant, got)
		}
	}
}

func TestImportFixture(t *testing.T) {
	root := t.TempDir()
	out := t.TempDir()
	write(t, root, "tools/git-cli/index.md", "---\ntitle: \"Git CLI\"\n---\n## Кратко\nРабота с git.")
	write(t, root, "tools/code-review/index.md", "---\ntitle: \"Код-ревью\"\n---\nПроцесс проверки кода.")
	write(t, root, "tools/code-review/practice/deep.md", "Практика не импортируется")
	write(t, root, "README.md", "не статья")
	write(t, root, "tools/empty-doc/index.md", "---\ntitle: \"Пустой\"\n---\n\n")
	write(t, root, "a11y/screenreaders/index.md", "---\ntitle: \"Скринридеры\"\n---\nЧтение с экрана.")

	res, err := Import(Options{
		SourceType: "doka",
		Root:       root,
		OutDir:     out,
		Sections:   []string{"tools", "a11y"},
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Imported != 3 {
		t.Fatalf("imported = %d, want 3 (warnings: %v)", res.Imported, res.Warnings)
	}
	if res.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (warnings: %v)", res.Skipped, res.Warnings)
	}

	docs, warns, err := corpus.LoadCorpus(out)
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("loaded docs = %d, want 3", len(docs))
	}
	for _, w := range warns {
		if strings.Contains(w, "outside a source directory") {
			t.Fatalf("unexpected warning: %s", w)
		}
	}
	byTitle := map[string]string{}
	for _, d := range docs {
		if !strings.HasPrefix(d.ID, "dsid_ru") {
			t.Fatalf("doc id %q missing dsid_ prefix", d.ID)
		}
		if d.SourceType != "doka" {
			t.Fatalf("doc %q source type = %q", d.ID, d.SourceType)
		}
		if d.Title == "" || d.Body == "" {
			t.Fatalf("doc %q has empty title or body", d.ID)
		}
		byTitle[d.Title] = d.ID
	}
	for _, title := range []string{"Git CLI", "Код-ревью", "Скринридеры"} {
		if _, ok := byTitle[title]; !ok {
			t.Fatalf("missing doc %q in %v", title, byTitle)
		}
	}
}

func TestImportIdempotent(t *testing.T) {
	root := t.TempDir()
	out := t.TempDir()
	write(t, root, "tools/git-cli/index.md", "---\ntitle: \"Git CLI\"\n---\nРабота с git.")

	if _, err := Import(Options{SourceType: "doka", Root: root, OutDir: out}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := Import(Options{SourceType: "doka", Root: root, OutDir: out}); err != nil {
		t.Fatalf("second import: %v", err)
	}
	docs, _, err := corpus.LoadCorpus(out)
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs after double import = %d, want 1", len(docs))
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
