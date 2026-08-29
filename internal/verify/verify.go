package verify

import (
	"sort"
	"strconv"
	"time"

	"github.com/alterfo/kb/internal/store/graphstore"
)

// Graph is the extraction output under verification: the entities and
// relations of a knowledge-graph slice, compared field-by-field by
// DiffGraph.
type Graph struct {
	Entities  []graphstore.Entity
	Relations []graphstore.Relation
}

// Report is the result of a golden-graph diff, grouped by category:
// missing (in want, not in got), extra (in got, not in want), and
// mismatched (same id, different fields). A mismatch carries the field
// name and both values.
type Report struct {
	MissingEntities     []string
	ExtraEntities       []string
	MismatchedEntities  []Mismatch
	MissingRelations    []string
	ExtraRelations      []string
	MismatchedRelations []Mismatch
}

// Mismatch describes one field-level difference between a got and a want
// graph element with the same id.
type Mismatch struct {
	ID    string
	Field string
	Got   string
	Want  string
}

// HasDifferences reports whether the diff found any discrepancy.
func (r Report) HasDifferences() bool {
	return len(r.MissingEntities) > 0 || len(r.ExtraEntities) > 0 ||
		len(r.MismatchedEntities) > 0 || len(r.MissingRelations) > 0 ||
		len(r.ExtraRelations) > 0 || len(r.MismatchedRelations) > 0
}

// DiffGraph compares the extracted graph (got) against the golden
// expectation (want) exactly, element by element. Entities are matched by
// id and compared on name, type and description; relations are matched by
// id and compared on src, dst, type, description, weight, valid_from and
// valid_to, so a wrong bi-temporal range surfaces as a mismatched
// relation, not a duplicate. Bookkeeping fields (source chunks, degree,
// created_at, expired_at) are not part of the golden contract.
func DiffGraph(got, want Graph) Report {
	var rep Report

	wantEntities := make(map[string]graphstore.Entity, len(want.Entities))
	for _, e := range want.Entities {
		wantEntities[e.ID] = e
	}
	wantRelations := make(map[string]graphstore.Relation, len(want.Relations))
	for _, r := range want.Relations {
		wantRelations[r.ID] = r
	}

	for _, e := range got.Entities {
		wantE, ok := wantEntities[e.ID]
		if !ok {
			rep.ExtraEntities = append(rep.ExtraEntities, e.ID)
			continue
		}
		rep.MismatchedEntities = append(rep.MismatchedEntities, diffEntity(e, wantE)...)
	}
	gotEntityIDs := entityIDs(got.Entities)
	for id := range wantEntities {
		if !gotEntityIDs[id] {
			rep.MissingEntities = append(rep.MissingEntities, id)
		}
	}

	for _, r := range got.Relations {
		wantR, ok := wantRelations[r.ID]
		if !ok {
			rep.ExtraRelations = append(rep.ExtraRelations, r.ID)
			continue
		}
		rep.MismatchedRelations = append(rep.MismatchedRelations, diffRelation(r, wantR)...)
	}
	gotRelationIDs := relationIDs(got.Relations)
	for id := range wantRelations {
		if !gotRelationIDs[id] {
			rep.MissingRelations = append(rep.MissingRelations, id)
		}
	}

	sort.Strings(rep.MissingEntities)
	sort.Strings(rep.ExtraEntities)
	sort.Strings(rep.MissingRelations)
	sort.Strings(rep.ExtraRelations)
	sort.Slice(rep.MismatchedEntities, func(i, j int) bool {
		return rep.MismatchedEntities[i].ID < rep.MismatchedEntities[j].ID
	})
	sort.Slice(rep.MismatchedRelations, func(i, j int) bool {
		return rep.MismatchedRelations[i].ID < rep.MismatchedRelations[j].ID
	})
	return rep
}

func diffEntity(got, want graphstore.Entity) []Mismatch {
	var out []Mismatch
	compare := func(field, gotV, wantV string) {
		if gotV != wantV {
			out = append(out, Mismatch{ID: got.ID, Field: field, Got: gotV, Want: wantV})
		}
	}
	compare("name", got.Name, want.Name)
	compare("type", got.Type, want.Type)
	compare("description", got.Description, want.Description)
	return out
}

func diffRelation(got, want graphstore.Relation) []Mismatch {
	var out []Mismatch
	compare := func(field, gotV, wantV string) {
		if gotV != wantV {
			out = append(out, Mismatch{ID: got.ID, Field: field, Got: gotV, Want: wantV})
		}
	}
	compare("src", got.Src, want.Src)
	compare("dst", got.Dst, want.Dst)
	compare("type", got.Type, want.Type)
	compare("description", got.Description, want.Description)
	if got.Weight != want.Weight {
		out = append(out, Mismatch{ID: got.ID, Field: "weight", Got: f64(got.Weight), Want: f64(want.Weight)})
	}
	compare("valid_from", timeString(got.ValidFrom), timeString(want.ValidFrom))
	compare("valid_to", timeString(got.ValidTo), timeString(want.ValidTo))
	return out
}

func timeString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func f64(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func entityIDs(entities []graphstore.Entity) map[string]bool {
	out := make(map[string]bool, len(entities))
	for _, e := range entities {
		out[e.ID] = true
	}
	return out
}

func relationIDs(relations []graphstore.Relation) map[string]bool {
	out := make(map[string]bool, len(relations))
	for _, r := range relations {
		out[r.ID] = true
	}
	return out
}
