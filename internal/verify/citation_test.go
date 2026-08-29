package verify

import "testing"

func TestCitationCheckerAllInContext(t *testing.T) {
	ctx := []Chunk{
		{FileName: "a.md", FilePath: "notes/a.md", ChunkID: "c1"},
		{FileName: "b.md", ChunkID: "c2"},
	}
	rep := CheckCitations("Claim one (a.md). Claim two [c2].", ctx)
	if rep.HasMissing() {
		t.Fatalf("unexpected missing citations: %+v", rep.Missing)
	}
	if len(rep.Citations) != 2 {
		t.Fatalf("got %d citations, want 2: %+v", len(rep.Citations), rep.Citations)
	}
}

func TestCitationCheckerMissingFlagged(t *testing.T) {
	ctx := []Chunk{{FileName: "a.md", ChunkID: "c1"}}
	rep := CheckCitations("Claim (missing.md) and (a.md).", ctx)
	if !rep.HasMissing() {
		t.Fatal("expected missing citations")
	}
	if len(rep.Missing) != 1 || rep.Missing[0].Raw != "missing.md" {
		t.Fatalf("Missing = %+v, want [missing.md]", rep.Missing)
	}
	if len(rep.Citations) != 1 || rep.Citations[0].FileName != "a.md" {
		t.Fatalf("Citations = %+v, want [a.md]", rep.Citations)
	}
}

func TestCitationCheckerChunkIDOnly(t *testing.T) {
	ctx := []Chunk{{ChunkID: "chunk-42"}}
	rep := CheckCitations("Per (chunk-42).", ctx)
	if rep.HasMissing() || len(rep.Citations) != 1 || rep.Citations[0].ChunkID != "chunk-42" {
		t.Fatalf("got %+v", rep)
	}
}

func TestCitationCheckerFilePathResolution(t *testing.T) {
	ctx := []Chunk{{FileName: "a.md", FilePath: "notes/a.md"}}
	rep := CheckCitations("See (notes/a.md).", ctx)
	if rep.HasMissing() || len(rep.Citations) != 1 {
		t.Fatalf("got %+v", rep)
	}
}

func TestCitationCheckerGroupedSources(t *testing.T) {
	ctx := []Chunk{{FileName: "a.md"}, {FileName: "b.md"}}
	rep := CheckCitations("Both (a.md, b.md).", ctx)
	if rep.HasMissing() || len(rep.Citations) != 2 {
		t.Fatalf("got %+v", rep)
	}
}

func TestCitationCheckerNoCitations(t *testing.T) {
	rep := CheckCitations("plain answer without citations", nil)
	if rep.HasMissing() || len(rep.Citations) != 0 {
		t.Fatalf("got %+v", rep)
	}
}

func TestCitationCheckerEmptyContextFlagsAll(t *testing.T) {
	rep := CheckCitations("Claim (a.md).", nil)
	if !rep.HasMissing() || len(rep.Missing) != 1 || rep.Missing[0].Raw != "a.md" {
		t.Fatalf("got %+v", rep)
	}
}

func TestCitationCheckerPartialGroupMissing(t *testing.T) {
	ctx := []Chunk{{FileName: "a.md"}}
	rep := CheckCitations("Both (a.md, ghost.md).", ctx)
	if !rep.HasMissing() || len(rep.Missing) != 1 || rep.Missing[0].Raw != "ghost.md" {
		t.Fatalf("got %+v", rep)
	}
}
