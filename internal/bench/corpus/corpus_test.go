package corpus

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadQuestions_Success(t *testing.T) {
	qs, warns, err := LoadQuestions(filepath.Join("testdata", "questions-sample.jsonl"))
	if err != nil {
		t.Fatalf("LoadQuestions() error = %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("LoadQuestions() warnings = %d, want 1 (%v)", len(warns), warns)
	}
	if len(qs) != 3 {
		t.Fatalf("LoadQuestions() count = %d, want 3", len(qs))
	}

	q := qs[0]
	if q.ID != "qst_0001" {
		t.Errorf("ID = %q, want qst_0001", q.ID)
	}
	if q.Type != "basic" {
		t.Errorf("Type = %q, want basic", q.Type)
	}
	if !reflect.DeepEqual(q.SourceTypes, []string{"github"}) {
		t.Errorf("SourceTypes = %v, want [github]", q.SourceTypes)
	}
	if !strings.Contains(q.Text, "default size limits") {
		t.Errorf("Text = %q, want mention of size limits", q.Text)
	}
	if !reflect.DeepEqual(q.ExpectedDocIDs, []string{"dsid_ae068ee4aa9640159427cd941bef0238"}) {
		t.Errorf("ExpectedDocIDs = %v", q.ExpectedDocIDs)
	}
	if q.GoldAnswer == "" {
		t.Error("GoldAnswer is empty")
	}
	if len(q.AnswerFacts) != 2 {
		t.Errorf("AnswerFacts = %d, want 2", len(q.AnswerFacts))
	}

	hl := qs[1]
	if hl.Type != "high_level" {
		t.Errorf("second Type = %q, want high_level", hl.Type)
	}
	if len(hl.ExpectedDocIDs) != 0 {
		t.Errorf("high_level ExpectedDocIDs = %v, want empty", hl.ExpectedDocIDs)
	}

	nf := qs[2]
	if nf.Type != "info_not_found" {
		t.Errorf("third Type = %q, want info_not_found", nf.Type)
	}
}

func TestLoadQuestions_LanguageField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "languages.jsonl")
	content := "{\"question_id\":\"q1\",\"question_type\":\"basic\",\"question\":\"ru?\",\"language\":\"ru\"}\n" +
		"{\"question_id\":\"q2\",\"question_type\":\"basic\",\"question\":\"en missing?\"}\n" +
		"{\"question_id\":\"q3\",\"question_type\":\"basic\",\"question\":\"en?\",\"language\":\"en\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	qs, warns, err := LoadQuestions(path)
	if err != nil {
		t.Fatalf("LoadQuestions() error = %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("LoadQuestions() warnings = %v, want none", warns)
	}
	if len(qs) != 3 {
		t.Fatalf("LoadQuestions() count = %d, want 3", len(qs))
	}
	if qs[0].Language != "ru" {
		t.Errorf("first Language = %q, want ru", qs[0].Language)
	}
	if qs[1].Language != "" {
		t.Errorf("second Language = %q, want empty", qs[1].Language)
	}
	if qs[2].Language != "en" {
		t.Errorf("third Language = %q, want en", qs[2].Language)
	}
}

func TestLoadQuestions_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadQuestions(path); err == nil {
		t.Fatal("LoadQuestions(empty) expected error, got nil")
	}
}

func TestLoadQuestions_MissingFile(t *testing.T) {
	if _, _, err := LoadQuestions(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatal("LoadQuestions(missing) expected error, got nil")
	}
}

func TestLoadCorpus_TXT(t *testing.T) {
	docs, warns, err := LoadCorpus(filepath.Join("testdata", "txt-corpus"))
	if err != nil {
		t.Fatalf("LoadCorpus(txt) error = %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("LoadCorpus(txt) warnings = %v, want none", warns)
	}
	if len(docs) != 2 {
		t.Fatalf("LoadCorpus(txt) count = %d, want 2", len(docs))
	}

	byID := map[string]Doc{}
	for _, d := range docs {
		byID[d.ID] = d
	}

	d1 := byID["dsid_11111111111111111111111111111111"]
	if d1.ID == "" {
		t.Fatal("slack doc not found")
	}
	if d1.SourceType != "slack" {
		t.Errorf("SourceType = %q, want slack", d1.SourceType)
	}
	if d1.Title != "Team Update: Q3 Roadmap Review" {
		t.Errorf("Title = %q", d1.Title)
	}
	wantBody := "Summary:\nThe platform team reviewed the Q3 roadmap.\n\nAction items:\n- finalize capacity plan\n- ship billing beta"
	if d1.Body != wantBody {
		t.Errorf("Body = %q, want %q", d1.Body, wantBody)
	}
	if d1.FileName != "dsid_11111111111111111111111111111111__team-update.txt" {
		t.Errorf("FileName = %q", d1.FileName)
	}
	if !strings.HasSuffix(d1.RelPath, filepath.Join("general", d1.FileName)) &&
		!strings.Contains(d1.RelPath, "team-update.txt") {
		t.Errorf("RelPath = %q, want path under slack/", d1.RelPath)
	}
	if d1.Noise {
		t.Error("Noise = true, want false")
	}

	d2 := byID["dsid_22222222222222222222222222222222"]
	if d2.ID == "" {
		t.Fatal("confluence doc not found")
	}
	if d2.SourceType != "confluence" {
		t.Errorf("SourceType = %q, want confluence", d2.SourceType)
	}
	if d2.Title != "Runbook: Audit Exporter Delivery Failures" {
		t.Errorf("Title = %q", d2.Title)
	}
}

func TestLoadCorpus_JSON(t *testing.T) {
	docs, warns, err := LoadCorpus(filepath.Join("testdata", "json-corpus"))
	if err != nil {
		t.Fatalf("LoadCorpus(json) error = %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("LoadCorpus(json) warnings = %v, want exactly 1 (broken.json)", warns)
	}
	if len(docs) != 3 {
		t.Fatalf("LoadCorpus(json) count = %d, want 3", len(docs))
	}

	byID := map[string]Doc{}
	for _, d := range docs {
		byID[d.ID] = d
	}

	fb := byID["dsid_33333333333333333333333333333333"]
	if fb.ID == "" {
		t.Fatal("confluence json doc not found")
	}
	if fb.Title != "Asynchronous Feedback Framework" {
		t.Errorf("Title = %q", fb.Title)
	}
	if fb.Body != "Summary:\nThis page documents Redwood's asynchronous feedback framework." {
		t.Errorf("Body = %q", fb.Body)
	}
	if fb.Meta["author"] != "Maya Chen" {
		t.Errorf("Meta[author] = %v, want Maya Chen", fb.Meta["author"])
	}
	if fb.Meta["created_at"] != "2026-01-12" {
		t.Errorf("Meta[created_at] = %v", fb.Meta["created_at"])
	}
	if _, ok := fb.Meta["dataset_doc_uuid"]; ok {
		t.Error("Meta still has reserved dataset_doc_uuid")
	}
	if _, ok := fb.Meta["title_field_name"]; ok {
		t.Error("Meta still has reserved title_field_name")
	}
	if _, ok := fb.Meta["content_field_names"]; ok {
		t.Error("Meta still has reserved content_field_names")
	}

	multi := byID["dsid_44444444444444444444444444444444"]
	if multi.ID == "" {
		t.Fatal("github multi-field doc not found")
	}
	if multi.Body != "Ann\nBob\nDiscussed rollout." {
		t.Errorf("Body = %q, want joined list+field", multi.Body)
	}

	noise := byID["dsid_55555555555555555555555555555555"]
	if noise.ID == "" {
		t.Fatal("noise doc not found")
	}
	if !noise.Noise {
		t.Error("Noise = false, want true")
	}
	if _, ok := noise.Meta["dataset_noise_document"]; ok {
		t.Error("Meta still has dataset_noise_document")
	}
}

func TestLoadCorpus_EmptyDir(t *testing.T) {
	if _, _, err := LoadCorpus(t.TempDir()); err == nil {
		t.Fatal("LoadCorpus(empty dir) expected error, got nil")
	}
}

func TestLoadCorpus_MixedAndJunk(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "gmail", "inbox")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	txt := "Memo: Billing Beta Dates\n\nThe billing beta ships on 2026-03-01.\n"
	if err := os.WriteFile(filepath.Join(src, "dsid_66666666666666666666666666666666__billing-beta.txt"), []byte(txt), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "agents.md"), []byte("instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".DS_Store"), []byte{0x00}, 0o644); err != nil {
		t.Fatal(err)
	}

	docs, warns, err := LoadCorpus(root)
	if err != nil {
		t.Fatalf("LoadCorpus(mixed) error = %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(docs))
	}
	if docs[0].SourceType != "gmail" {
		t.Errorf("SourceType = %q, want gmail", docs[0].SourceType)
	}
	if len(warns) != 2 {
		t.Fatalf("warnings = %v, want 2 (agents.md, .DS_Store)", warns)
	}
}

func TestLoadCorpus_RootLevelFilesSkipped(t *testing.T) {
	root := t.TempDir()
	txt := "Loose Note\n\nNo source directory here.\n"
	if err := os.WriteFile(filepath.Join(root, "dsid_77777777777777777777777777777777__loose.txt"), []byte(txt), 0o644); err != nil {
		t.Fatal(err)
	}
	docs, warns, err := LoadCorpus(root)
	if err == nil {
		t.Fatal("expected error for corpus without loadable docs, got nil")
	}
	if len(docs) != 0 {
		t.Fatalf("docs = %d, want 0", len(docs))
	}
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want 1", warns)
	}
}

func TestToDocument_Mapping(t *testing.T) {
	d := Doc{
		ID:         "dsid_ae068ee4aa9640159427cd941bef0238",
		SourceType: "confluence",
		Title:      "Framework",
		Body:       "Body text",
		Meta:       map[string]any{"author": "Maya Chen"},
	}
	got := d.ToDocument()
	if got.ID != d.ID {
		t.Errorf("ID = %q, want %q", got.ID, d.ID)
	}
	if got.Source != "confluence" {
		t.Errorf("Source = %q, want confluence", got.Source)
	}
	if got.Title != d.Title || got.Body != d.Body {
		t.Errorf("Title/Body mismatch: %q/%q", got.Title, got.Body)
	}
	if !reflect.DeepEqual(got.Frontmatter, d.Meta) {
		t.Errorf("Frontmatter = %v, want %v", got.Frontmatter, d.Meta)
	}
	if !got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt = %v, want zero without meta dates", got.UpdatedAt)
	}
}

func TestToDocument_UpdatedAtVariants(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
		want time.Time
	}{
		{
			name: "last_updated preferred over created_at",
			meta: map[string]any{"last_updated": "2026-02-03", "created_at": "2026-01-12"},
			want: time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "created_at fallback",
			meta: map[string]any{"created_at": "2026-01-12"},
			want: time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "rfc3339",
			meta: map[string]any{"last_updated": "2026-02-03T10:20:30Z"},
			want: time.Date(2026, 2, 3, 10, 20, 30, 0, time.UTC),
		},
		{
			name: "go default layout",
			meta: map[string]any{"last_updated": "2026-02-03 10:20:30 +0000 UTC"},
			want: time.Date(2026, 2, 3, 10, 20, 30, 0, time.UTC),
		},
		{
			name: "unparseable ignored",
			meta: map[string]any{"last_updated": "not-a-date"},
			want: time.Time{},
		},
		{
			name: "numeric timestamp ignored",
			meta: map[string]any{"created_at": 1774728005},
			want: time.Time{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Doc{ID: "dsid_x", Meta: tt.meta}
			got := d.ToDocument()
			if !got.UpdatedAt.Equal(tt.want) {
				t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, tt.want)
			}
		})
	}
}

func TestParseTXT_FilenameWithoutSeparatorSkipped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "jira", "tickets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain-name.txt"), []byte("No separator"), 0o644); err != nil {
		t.Fatal(err)
	}
	docs, warns, err := LoadCorpus(root)
	if err == nil {
		t.Fatal("expected error when nothing loads, got nil")
	}
	if len(docs) != 0 || len(warns) != 1 {
		t.Fatalf("docs=%d warns=%v, want 0 docs and 1 warning", len(docs), warns)
	}
}

func TestLoadQuestions_BOMAndCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bom.jsonl")
	content := "\xEF\xBB\xBF{\"question_id\": \"qst_0001\", \"question_type\": \"basic\", \"question\": \"BOM question?\", \"expected_doc_ids\": [], \"gold_answer\": \"x\", \"answer_facts\": []}\r\n\r\n{\"question_id\": \"qst_0002\", \"question_type\": \"basic\", \"question\": \"CRLF question?\", \"expected_doc_ids\": [], \"gold_answer\": \"y\", \"answer_facts\": []}\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	qs, warns, err := LoadQuestions(path)
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("warnings = %v, want none", warns)
	}
	if len(qs) != 2 {
		t.Fatalf("questions = %d, want 2 (BOM and blank CRLF lines tolerated)", len(qs))
	}
	if qs[0].ID != "qst_0001" || strings.HasSuffix(qs[0].ID, "\uFEFF") {
		t.Errorf("BOM not stripped from first id: %q", qs[0].ID)
	}
}

func TestLoadCorpus_TXTWithCRLF(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "slack", "general")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "CRLF Title\r\n\r\nLine one\r\nLine two\r\n"
	if err := os.WriteFile(filepath.Join(dir, "dsid_88888888888888888888888888888888__crlf.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	docs, warns, err := LoadCorpus(root)
	if err != nil || len(warns) != 0 {
		t.Fatalf("err=%v warns=%v", err, warns)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(docs))
	}
	if docs[0].Title != "CRLF Title" {
		t.Errorf("Title = %q, want CR stripped", docs[0].Title)
	}
	if strings.Contains(docs[0].Body, "\r") {
		t.Errorf("Body contains CR: %q", docs[0].Body)
	}
}
