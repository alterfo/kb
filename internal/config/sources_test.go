package config

import (
	"os"
	"testing"
)

func TestParseSources_Valid_MultipleInstances(t *testing.T) {
	data, err := os.ReadFile("testdata/sources_valid.yaml")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	cfg, err := ParseSources(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 3 {
		t.Fatalf("expected 3 source instances, got %d", len(cfg.Sources))
	}

	githubCount := 0
	for _, s := range cfg.Sources {
		if s.Type == "github" {
			githubCount++
		}
	}
	if githubCount != 2 {
		t.Errorf("expected 2 github instances, got %d", githubCount)
	}

	if got := cfg.Sources[0].Secrets["token"]; got != "GITHUB_TOKEN" {
		t.Errorf("secrets[token] = %q, want GITHUB_TOKEN", got)
	}

	if len(cfg.VirtualCollections["chats"]) != 3 {
		t.Errorf("expected 3 chats patterns, got %v", cfg.VirtualCollections["chats"])
	}
	if len(cfg.VirtualCollections["requirements"]) != 2 {
		t.Errorf("expected 2 requirements patterns, got %v", cfg.VirtualCollections["requirements"])
	}
	if len(cfg.VirtualCollections["code"]) != 2 {
		t.Errorf("expected 2 code patterns, got %v", cfg.VirtualCollections["code"])
	}
}

func TestParseSources_PresenceOnly_NoLiteralSecrets(t *testing.T) {
	data, err := os.ReadFile("testdata/sources_secret_leak.yaml")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	_, err = ParseSources(data)
	if err == nil {
		t.Fatal("expected error for literal secret value in sources.yaml, got nil")
	}
}

func TestParseSources_DuplicateInstance(t *testing.T) {
	data, err := os.ReadFile("testdata/sources_duplicate.yaml")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	_, err = ParseSources(data)
	if err == nil {
		t.Fatal("expected error for duplicate source instance, got nil")
	}
}

func TestParseSources_RejectsSameNameAcrossTypes(t *testing.T) {
	data, err := os.ReadFile("testdata/sources_same_name.yaml")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	_, err = ParseSources(data)
	if err == nil {
		t.Fatal("expected error for same source name reused by different types, got nil")
	}
}

func TestParseSources_EmptyNameOrType(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"empty name", "sources:\n  - name: \"\"\n    type: github\n"},
		{"empty type", "sources:\n  - name: foo\n    type: \"\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSources([]byte(tc.yaml))
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestParseSources_EmptyDocument(t *testing.T) {
	cfg, err := ParseSources([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("expected no sources, got %d", len(cfg.Sources))
	}
}

func TestLoadSourcesFile_MissingFile(t *testing.T) {
	_, err := LoadSourcesFile("testdata/does_not_exist.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadSourcesFile_Valid(t *testing.T) {
	cfg, err := LoadSourcesFile("testdata/sources_valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 3 {
		t.Errorf("expected 3 sources, got %d", len(cfg.Sources))
	}
}

func TestSourceNamesForExpandsVirtualCollections(t *testing.T) {
	cfg, err := ParseSources([]byte(`
sources:
  - name: tg-main
    type: telegram
  - name: tg-archive
    type: telegram
  - name: gh
    type: github
virtual_collections:
  chats: [telegram:*]
  main-chat: [telegram:tg-main]
`))
	if err != nil {
		t.Fatalf("ParseSources: %v", err)
	}

	got := cfg.SourceNamesFor("chats")
	want := []string{"tg-main", "tg-archive"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("SourceNamesFor(chats) = %v, want %v", got, want)
	}
	got = cfg.SourceNamesFor("main-chat")
	if len(got) != 1 || got[0] != "tg-main" {
		t.Fatalf("SourceNamesFor(main-chat) = %v, want [tg-main]", got)
	}
	if got := cfg.SourceNamesFor("gh"); len(got) != 1 || got[0] != "gh" {
		t.Fatalf("SourceNamesFor(gh) = %v, want passthrough [gh]", got)
	}
	if got := cfg.SourceNamesFor(""); got != nil {
		t.Fatalf("SourceNamesFor(\"\") = %v, want nil", got)
	}
	if got := cfg.SourceNamesFor("missing"); len(got) != 1 || got[0] != "missing" {
		t.Fatalf("SourceNamesFor(missing) = %v, want passthrough", got)
	}
}

func TestParseSources_RejectsDanglingVirtualCollectionReference(t *testing.T) {
	_, err := ParseSources([]byte(`
sources:
  - name: tg
    type: telegram
virtual_collections:
  main-chat: [telegram:tg-main]
`))
	if err == nil {
		t.Fatal("expected error for virtual collection referencing an unknown source, got nil")
	}
}

func TestParseSources_AllowsExactAndWildcardCollectionReferences(t *testing.T) {
	_, err := ParseSources([]byte(`
sources:
  - name: tg
    type: telegram
virtual_collections:
  chats: [telegram:*]
  main-chat: [telegram:tg]
`))
	if err != nil {
		t.Fatalf("expected valid collection references to parse, got %v", err)
	}
}

func TestParseSources_LeonOnly_NoDemoDocs(t *testing.T) {
	data, err := os.ReadFile("testdata/sources_leon_only.yaml")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	cfg, err := ParseSources(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("expected 1 source instance, got %d", len(cfg.Sources))
	}
	src := cfg.Sources[0]
	if src.Name != "leon-ai" {
		t.Errorf("source name = %q, want leon-ai", src.Name)
	}
	if src.Type != "github" {
		t.Errorf("source type = %q, want github", src.Type)
	}
	if got := src.Config["repos"]; got != "leon-ai/leon" {
		t.Errorf("config[repos] = %q, want leon-ai/leon", got)
	}
	for _, s := range cfg.Sources {
		if s.Name == "demo-docs" {
			t.Errorf("demo-docs source still present in config: %+v", s)
		}
	}
}

func TestParseSources_LeonFull_Golden(t *testing.T) {
	data, err := os.ReadFile("testdata/sources_leon_full.yaml")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	cfg, err := ParseSources(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 4 {
		t.Fatalf("expected 4 source instances, got %d", len(cfg.Sources))
	}

	byName := make(map[string]string)
	for _, src := range cfg.Sources {
		if prev, ok := byName[src.Name]; ok {
			t.Fatalf("duplicate source name %q used by %q and %q", src.Name, prev, src.Type)
		}
		byName[src.Name] = src.Type
		if src.Name == "demo-docs" {
			t.Errorf("demo-docs source still present in config")
		}
	}

	if byName["leon-ai"] != "github" {
		t.Fatalf("leon-ai type = %q, want github", byName["leon-ai"])
	}
	if byName["leon-code"] != "file" {
		t.Fatalf("leon-code type = %q, want file", byName["leon-code"])
	}
	if byName["leon-wiki"] != "github" {
		t.Fatalf("leon-wiki type = %q, want github", byName["leon-wiki"])
	}
	if byName["leon-discord"] != "discord" {
		t.Fatalf("leon-discord type = %q, want discord", byName["leon-discord"])
	}

	code := cfg.Sources[1]
	if code.Name != "leon-code" || code.Config["path"] != "kb_root/.persist/leon-repo" {
		t.Fatalf("leon-code source = %+v", code)
	}
	wiki := cfg.Sources[2]
	if wiki.Config["include_wiki"] != "true" || wiki.Config["repos"] != "leon-ai/leon" {
		t.Fatalf("leon-wiki source = %+v", wiki)
	}
	discord := cfg.Sources[3]
	if discord.Config["guild_id"] != "587078057634824222" {
		t.Errorf("guild_id = %q", discord.Config["guild_id"])
	}
	if discord.Config["channels"] != "587078058130014240" {
		t.Errorf("channels = %q", discord.Config["channels"])
	}
	if discord.Secrets["token"] != "KB_DISCORD_TOKEN" {
		t.Errorf("discord secret token = %q, want KB_DISCORD_TOKEN", discord.Secrets["token"])
	}
}

func TestParseSources_LeonFull_RejectsDuplicateLeonName(t *testing.T) {
	_, err := ParseSources([]byte(`
sources:
  - name: leon-ai
    type: github
    config:
      repos: leon-ai/leon
  - name: leon-ai
    type: github
    config:
      repos: leon-ai/leon
`))
	if err == nil {
		t.Fatal("expected error for duplicate source name in Leon config, got nil")
	}
}

func TestParseSources_LeonFull_RejectsLiteralSecret(t *testing.T) {
	_, err := ParseSources([]byte(`
sources:
  - name: leon-discord
    type: discord
    config:
      guild_id: "587078057634824222"
      channels: "587078058130014240"
    secrets:
      token: actual_bot_token
`))
	if err == nil {
		t.Fatal("expected error for literal secret in Leon config, got nil")
	}
}
