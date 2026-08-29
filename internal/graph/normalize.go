package graph

import (
	"crypto/sha1"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/alterfo/kb/internal/store/graphstore"
)

var slugNonAlnumRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugNonAlnumRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func normalizeName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func idHash(parts ...string) string {
	h := sha1.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s;", len(p), p)
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:4])
}

// EntityID derives a stable id from an entity's name and type:
// slug(lower(trim(name)) + "|" + type).
func EntityID(name, typ string) string {
	base := normalizeName(name) + "|" + normalizeName(typ)
	return slug(base) + "-" + idHash(normalizeName(name), normalizeName(typ))
}

// RelationID derives a stable id from a relation's endpoints and type.
func RelationID(srcID, dstID, typ string) string {
	base := srcID + "|" + normalizeName(typ) + "|" + dstID
	return slug(base) + "-" + idHash(srcID, normalizeName(typ), dstID)
}

// CommunityID derives a stable, content-addressed id from a community's
// level, member set, and internal relations: unchanged membership AND
// unchanged internal edges yield the same id across runs, so callers can
// detect "this community's content did not change" by comparing ids rather
// than diffing member lists. Relations whose endpoints both belong to the
// community are sorted deterministically before hashing.
func CommunityID(level int, members []string, relations []graphstore.Relation) string {
	sorted := append([]string(nil), members...)
	sort.Strings(sorted)
	h := sha1.New()
	fmt.Fprintf(h, "%d|%s", level, strings.Join(sorted, ","))
	for _, r := range sortedInternalRelations(members, relations) {
		fmt.Fprintf(h, "|%s|%s|%s|%f|%s", r.Src, r.Dst, r.Type, r.Weight, r.Description)
	}
	return fmt.Sprintf("c-%x", h.Sum(nil)[:8])
}

// sortedInternalRelations returns relations whose endpoints both belong to
// members, sorted deterministically by (src, dst, type).
func sortedInternalRelations(members []string, relations []graphstore.Relation) []graphstore.Relation {
	memberSet := make(map[string]struct{}, len(members))
	for _, m := range members {
		memberSet[m] = struct{}{}
	}
	var internal []graphstore.Relation
	for _, r := range relations {
		_, sOK := memberSet[r.Src]
		_, dOK := memberSet[r.Dst]
		if sOK && dOK {
			// The underlying community graph is undirected, so an edge is
			// hashed independent of its stored direction: a→b and b→a are
			// the same edge for summary-regeneration purposes.
			if r.Src > r.Dst {
				r.Src, r.Dst = r.Dst, r.Src
			}
			internal = append(internal, r)
		}
	}
	sort.Slice(internal, func(i, j int) bool {
		if internal[i].Src != internal[j].Src {
			return internal[i].Src < internal[j].Src
		}
		if internal[i].Dst != internal[j].Dst {
			return internal[i].Dst < internal[j].Dst
		}
		return internal[i].Type < internal[j].Type
	})
	return internal
}

// BuildEntities converts raw per-chunk extraction entities into graphstore
// entities, deduping by id within the batch (first non-empty description
// wins). It also returns a normalized-name -> id lookup for BuildRelations.
func BuildEntities(raw []RawEntity) ([]graphstore.Entity, map[string]string) {
	nameToID := map[string]string{}
	indexByID := map[string]int{}
	var out []graphstore.Entity

	for _, r := range raw {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		typ := strings.TrimSpace(r.Type)
		if typ == "" {
			typ = "entity"
		}
		id := EntityID(name, typ)
		nameToID[normalizeName(name)] = id

		if idx, ok := indexByID[id]; ok {
			if out[idx].Description == "" {
				out[idx].Description = strings.TrimSpace(r.Description)
			}
			continue
		}
		indexByID[id] = len(out)
		out = append(out, graphstore.Entity{
			ID:          id,
			Name:        name,
			Type:        typ,
			Description: strings.TrimSpace(r.Description),
		})
	}
	return out, nameToID
}

// BuildRelations converts raw per-chunk extraction relations into graphstore
// relations, resolving endpoint names via nameToID (produced by
// BuildEntities from the same chunk). Relations whose endpoints are not
// known entities, or that are self-loops, are dropped (fail-open).
func BuildRelations(nameToID map[string]string, raw []RawRelation) []graphstore.Relation {
	indexByID := map[string]int{}
	var out []graphstore.Relation

	for _, r := range raw {
		srcID, ok1 := nameToID[normalizeName(r.Source)]
		dstID, ok2 := nameToID[normalizeName(r.Target)]
		if !ok1 || !ok2 || srcID == dstID {
			continue
		}
		typ := strings.TrimSpace(r.Type)
		if typ == "" {
			typ = "related_to"
		}
		id := RelationID(srcID, dstID, typ)

		if idx, ok := indexByID[id]; ok {
			if out[idx].Description == "" {
				out[idx].Description = strings.TrimSpace(r.Description)
			}
			continue
		}
		indexByID[id] = len(out)
		out = append(out, graphstore.Relation{
			ID:              id,
			Src:             srcID,
			Dst:             dstID,
			Type:            typ,
			Description:     strings.TrimSpace(r.Description),
			Weight:          1,
			ValidFrom:       r.ValidFrom,
			ValidTo:         r.ValidTo,
			NoConflictClose: r.NoConflictClose,
		})
	}
	return out
}
