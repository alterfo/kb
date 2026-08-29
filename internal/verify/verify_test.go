package verify

import (
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/store/graphstore"
)

func ts(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func ent(id, name, typ, desc string) graphstore.Entity {
	return graphstore.Entity{ID: id, Name: name, Type: typ, Description: desc}
}

func rel(id, src, dst, typ, desc string) graphstore.Relation {
	return graphstore.Relation{ID: id, Src: src, Dst: dst, Type: typ, Description: desc, Weight: 1}
}

func TestDiffGraphIdenticalEmpty(t *testing.T) {
	rep := DiffGraph(Graph{}, Graph{})
	if rep.HasDifferences() {
		t.Fatalf("identical empty graphs must diff clean, got %+v", rep)
	}
}

func TestDiffGraphIdenticalGraphsClean(t *testing.T) {
	got := Graph{
		Entities: []graphstore.Entity{
			ent("e1", "Alice", "person", "engineer"),
			ent("e2", "Acme", "company", "corp"),
		},
		Relations: []graphstore.Relation{
			rel("r1", "e1", "e2", "works_at", "employment"),
		},
	}
	rep := DiffGraph(got, got)
	if rep.HasDifferences() {
		t.Fatalf("identical graphs must diff clean, got %+v", rep)
	}
}

func TestDiffGraphMissingAndExtraEntity(t *testing.T) {
	want := Graph{Entities: []graphstore.Entity{ent("e1", "Alice", "person", ""), ent("e2", "Bob", "person", "")}}
	got := Graph{Entities: []graphstore.Entity{ent("e2", "Bob", "person", ""), ent("e3", "Carol", "person", "")}}

	rep := DiffGraph(got, want)
	if !rep.HasDifferences() {
		t.Fatal("expected differences")
	}
	if len(rep.MissingEntities) != 1 || rep.MissingEntities[0] != "e1" {
		t.Fatalf("MissingEntities = %v, want [e1]", rep.MissingEntities)
	}
	if len(rep.ExtraEntities) != 1 || rep.ExtraEntities[0] != "e3" {
		t.Fatalf("ExtraEntities = %v, want [e3]", rep.ExtraEntities)
	}
	if len(rep.MismatchedEntities) != 0 {
		t.Fatalf("MismatchedEntities = %+v, want none", rep.MismatchedEntities)
	}
}

func TestDiffGraphEntityMismatchReportsFields(t *testing.T) {
	want := Graph{Entities: []graphstore.Entity{ent("e1", "Alice", "person", "engineer")}}
	got := Graph{Entities: []graphstore.Entity{ent("e1", "Alicia", "person", "manager")}}

	rep := DiffGraph(got, want)
	if len(rep.MismatchedEntities) != 2 {
		t.Fatalf("MismatchedEntities = %+v, want name+description", rep.MismatchedEntities)
	}
	fields := map[string]bool{}
	for _, m := range rep.MismatchedEntities {
		if m.ID != "e1" {
			t.Fatalf("mismatch id = %q, want e1", m.ID)
		}
		fields[m.Field] = true
	}
	if !fields["name"] || !fields["description"] {
		t.Fatalf("fields = %v, want name and description", fields)
	}
}

func TestDiffGraphMissingAndExtraRelation(t *testing.T) {
	want := Graph{Relations: []graphstore.Relation{rel("r1", "e1", "e2", "knows", ""), rel("r2", "e2", "e3", "knows", "")}}
	got := Graph{Relations: []graphstore.Relation{rel("r2", "e2", "e3", "knows", ""), rel("r3", "e3", "e4", "works_at", "")}}

	rep := DiffGraph(got, want)
	if len(rep.MissingRelations) != 1 || rep.MissingRelations[0] != "r1" {
		t.Fatalf("MissingRelations = %v, want [r1]", rep.MissingRelations)
	}
	if len(rep.ExtraRelations) != 1 || rep.ExtraRelations[0] != "r3" {
		t.Fatalf("ExtraRelations = %v, want [r3]", rep.ExtraRelations)
	}
}

func TestDiffGraphRelationTemporalMismatch(t *testing.T) {
	want := Graph{Relations: []graphstore.Relation{{
		ID: "r1", Src: "e1", Dst: "e2", Type: "amends", Weight: 1,
		ValidFrom: ts(2012, 12, 30), ValidTo: nil,
	}}}
	got := Graph{Relations: []graphstore.Relation{{
		ID: "r1", Src: "e1", Dst: "e2", Type: "amends", Weight: 1,
		ValidFrom: ts(2012, 12, 30), ValidTo: ts(2015, 3, 8),
	}}}

	rep := DiffGraph(got, want)
	if len(rep.MismatchedRelations) != 1 {
		t.Fatalf("MismatchedRelations = %+v, want one temporal mismatch", rep.MismatchedRelations)
	}
	m := rep.MismatchedRelations[0]
	if m.ID != "r1" || m.Field != "valid_to" {
		t.Fatalf("mismatch = %+v, want valid_to on r1", m)
	}
	if m.Got == "" || m.Want != "" {
		t.Fatalf("got=%q want=%q, want closed-vs-open values", m.Got, m.Want)
	}
}

func TestDiffGraphRelationFieldMismatch(t *testing.T) {
	want := Graph{Relations: []graphstore.Relation{rel("r1", "e1", "e2", "knows", "colleagues")}}
	got := Graph{Relations: []graphstore.Relation{rel("r1", "e1", "e2", "knows", "friends")}}

	rep := DiffGraph(got, want)
	if len(rep.MismatchedRelations) != 1 || rep.MismatchedRelations[0].Field != "description" {
		t.Fatalf("MismatchedRelations = %+v, want description mismatch", rep.MismatchedRelations)
	}
}

func TestDiffGraphRelationWeightMismatch(t *testing.T) {
	want := Graph{Relations: []graphstore.Relation{rel("r1", "e1", "e2", "knows", "")}}
	got := Graph{Relations: []graphstore.Relation{{ID: "r1", Src: "e1", Dst: "e2", Type: "knows", Weight: 3}}}

	rep := DiffGraph(got, want)
	if len(rep.MismatchedRelations) != 1 || rep.MismatchedRelations[0].Field != "weight" {
		t.Fatalf("MismatchedRelations = %+v, want weight mismatch", rep.MismatchedRelations)
	}
}

func TestDiffGraphBookkeepingFieldsIgnored(t *testing.T) {
	want := Graph{Entities: []graphstore.Entity{{
		ID: "e1", Name: "Alice", Type: "person", Description: "engineer", Degree: 1,
	}}}
	got := Graph{Entities: []graphstore.Entity{{
		ID: "e1", Name: "Alice", Type: "person", Description: "engineer", Degree: 5,
		SourceChunks: []string{"c1"},
	}}}
	rep := DiffGraph(got, want)
	if rep.HasDifferences() {
		t.Fatalf("bookkeeping fields must not diff, got %+v", rep)
	}
}

func TestDiffGraphReportSortedAndDeterministic(t *testing.T) {
	want := Graph{Entities: []graphstore.Entity{ent("e1", "Alice", "person", ""), ent("e2", "Bob", "person", "")}}
	got := Graph{Entities: []graphstore.Entity{ent("e2", "Bob", "person", ""), ent("e3", "Carol", "person", "")}}

	a := DiffGraph(got, want)
	b := DiffGraph(got, want)
	if strings.Join(a.MissingEntities, ",") != strings.Join(b.MissingEntities, ",") {
		t.Fatalf("report must be deterministic: %v vs %v", a, b)
	}
	if len(a.MissingEntities) == 0 || a.MissingEntities[0] != "e1" {
		t.Fatalf("missing entities unsorted: %v", a.MissingEntities)
	}
}
