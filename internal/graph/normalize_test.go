package graph

import (
	"testing"

	"github.com/alterfo/kb/internal/store/graphstore"
)

func TestEntityIDStableAndNormalized(t *testing.T) {
	a := EntityID("Alice Smith", "person")
	b := EntityID("  alice smith  ", "Person")
	if a != b {
		t.Fatalf("EntityID not normalized: %q != %q", a, b)
	}
	if a == "" {
		t.Fatalf("EntityID empty")
	}
}

func TestRelationIDStable(t *testing.T) {
	a := RelationID("alice", "bob", "knows")
	b := RelationID("alice", "bob", "Knows")
	if a != b {
		t.Fatalf("RelationID not normalized: %q != %q", a, b)
	}
}

func TestEntityAndRelationIDsNoBoundaryCollisions(t *testing.T) {
	a := EntityID("postgres", "db-entity")
	b := EntityID("postgres-db", "entity")
	if a == b {
		t.Fatalf("entity id collision across name/type boundary: %q", a)
	}
	c := RelationID("a-b", "c", "d")
	d := RelationID("a", "b-c", "d")
	if c == d {
		t.Fatalf("relation id collision across src/type boundary: %q", c)
	}
}

func TestBuildEntitiesDedupesByIDWithinBatch(t *testing.T) {
	raw := []RawEntity{
		{Name: "Alice", Type: "person", Description: ""},
		{Name: "alice", Type: "Person", Description: "the engineer"},
		{Name: "Bob", Type: "person"},
	}
	entities, nameToID := BuildEntities(raw)
	if len(entities) != 2 {
		t.Fatalf("got %d entities, want 2 (deduped): %+v", len(entities), entities)
	}
	var aliceDescription string
	var found bool
	for _, e := range entities {
		if e.Name == "Alice" {
			aliceDescription = e.Description
			found = true
		}
	}
	if !found || aliceDescription != "the engineer" {
		t.Fatalf("expected merged description from second occurrence, got %q (found=%v)", aliceDescription, found)
	}
	if _, ok := nameToID["alice"]; !ok {
		t.Fatalf("nameToID missing alice: %+v", nameToID)
	}
	if _, ok := nameToID["bob"]; !ok {
		t.Fatalf("nameToID missing bob: %+v", nameToID)
	}
}

func TestBuildEntitiesSkipsEmptyName(t *testing.T) {
	entities, _ := BuildEntities([]RawEntity{{Name: "  ", Type: "x"}})
	if len(entities) != 0 {
		t.Fatalf("got %+v, want empty", entities)
	}
}

func TestBuildEntitiesDefaultsMissingType(t *testing.T) {
	entities, _ := BuildEntities([]RawEntity{{Name: "X"}})
	if len(entities) != 1 || entities[0].Type != "entity" {
		t.Fatalf("got %+v, want default type 'entity'", entities)
	}
}

func TestBuildRelationsResolvesEndpointsAndDedupes(t *testing.T) {
	entities, nameToID := BuildEntities([]RawEntity{
		{Name: "Alice", Type: "person"},
		{Name: "Bob", Type: "person"},
	})
	if len(entities) != 2 {
		t.Fatalf("setup: got %d entities", len(entities))
	}

	relations := BuildRelations(nameToID, []RawRelation{
		{Source: "Alice", Target: "Bob", Type: "knows", Description: ""},
		{Source: "alice", Target: "bob", Type: "Knows", Description: "since college"},
	})
	if len(relations) != 1 {
		t.Fatalf("got %d relations, want 1 (deduped): %+v", len(relations), relations)
	}
	if relations[0].Description != "since college" {
		t.Fatalf("expected merged description, got %q", relations[0].Description)
	}
}

func TestBuildRelationsDropsUnknownEndpoints(t *testing.T) {
	_, nameToID := BuildEntities([]RawEntity{{Name: "Alice", Type: "person"}})
	relations := BuildRelations(nameToID, []RawRelation{
		{Source: "Alice", Target: "Ghost", Type: "knows"},
	})
	if len(relations) != 0 {
		t.Fatalf("got %+v, want empty (unknown target dropped)", relations)
	}
}

func TestBuildRelationsDropsSelfLoops(t *testing.T) {
	_, nameToID := BuildEntities([]RawEntity{{Name: "Alice", Type: "person"}})
	relations := BuildRelations(nameToID, []RawRelation{
		{Source: "Alice", Target: "alice", Type: "knows"},
	})
	if len(relations) != 0 {
		t.Fatalf("got %+v, want empty (self-loop dropped)", relations)
	}
}

func TestCommunityIDStableForSameMembersRegardlessOfOrder(t *testing.T) {
	rels := []graphstore.Relation{{Src: "a", Dst: "b", Type: "links"}}
	a := CommunityID(0, []string{"b", "a", "c"}, rels)
	b := CommunityID(0, []string{"c", "b", "a"}, rels)
	if a != b {
		t.Fatalf("CommunityID not order-independent: %q != %q", a, b)
	}

	c := CommunityID(0, []string{"a", "b"}, rels)
	if a == c {
		t.Fatalf("CommunityID collided for different member sets")
	}

	d := CommunityID(1, []string{"b", "a", "c"}, rels)
	if a == d {
		t.Fatalf("CommunityID collided across levels")
	}
}

func TestCommunityIDChangesWhenInternalRelationsChange(t *testing.T) {
	members := []string{"a", "b", "c"}
	base := CommunityID(0, members, []graphstore.Relation{{Src: "a", Dst: "b", Type: "links", Weight: 1}})
	weighted := CommunityID(0, members, []graphstore.Relation{{Src: "a", Dst: "b", Type: "links", Weight: 5}})
	if weighted == base {
		t.Fatalf("CommunityID did not change when a relation weight changed")
	}
	reordered := CommunityID(0, members, []graphstore.Relation{{Src: "b", Dst: "a", Type: "links", Weight: 1}})
	if reordered != base {
		t.Fatalf("CommunityID not order-independent over relations")
	}
	external := CommunityID(0, members, []graphstore.Relation{
		{Src: "a", Dst: "b", Type: "links", Weight: 1},
		{Src: "a", Dst: "outside", Type: "links", Weight: 1},
	})
	if external != base {
		t.Fatalf("CommunityID changed for a relation outside the community")
	}
}
