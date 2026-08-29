package legaleval

import (
	"strings"
	"testing"
)

func TestParsePlenumPoints(t *testing.T) {
	src := `# Title
Some preamble.

## Пункт 1
First point body.
More lines.

## Пункт 2
Second point body.
`
	points, err := ParsePlenumPoints(strings.NewReader(src), "вс-рф/пленум/пост-25")
	if err != nil {
		t.Fatalf("ParsePlenumPoints: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2", len(points))
	}
	if points[0].ID != "вс-рф/пленум/пост-25/п1" {
		t.Fatalf("point 0 id = %q", points[0].ID)
	}
	if points[0].Body != "First point body.\nMore lines." {
		t.Fatalf("point 0 body = %q", points[0].Body)
	}
	if points[1].ID != "вс-рф/пленум/пост-25/п2" || points[1].Body != "Second point body." {
		t.Fatalf("point 1 = %+v", points[1])
	}
}

func TestParsePlenumPointsNoPrefix(t *testing.T) {
	points, err := ParsePlenumPoints(strings.NewReader("## Пункт 7\nbody\n"), "")
	if err != nil {
		t.Fatalf("ParsePlenumPoints: %v", err)
	}
	if len(points) != 1 || points[0].ID != "п7" {
		t.Fatalf("points = %+v", points)
	}
}

func TestPlenumLookup(t *testing.T) {
	p := NewPlenum([]PlenumPoint{{ID: "p/п1", Body: "b"}})
	if pt, ok := p.Point("p/п1"); !ok || pt.Body != "b" {
		t.Fatalf("Point(p/п1) = %+v, %v", pt, ok)
	}
	if _, ok := p.Point("p/п9"); ok {
		t.Fatal("Point(p/п9) must not exist")
	}
}

func TestLoadPlenumPointsGold(t *testing.T) {
	points, err := LoadPlenumPoints("../../../internal/importer/legalru/testdata/gold/plenum-25-2015.md", "вс-рф/пленум/пост-25")
	if err != nil {
		t.Fatalf("LoadPlenumPoints: %v", err)
	}
	want := map[string]bool{"1": true, "2": true, "4": true, "7": true, "11": true, "12": true, "13": true, "15": true}
	if len(points) != len(want) {
		t.Fatalf("gold plenum has %d points, want %d", len(points), len(want))
	}
	seen := map[string]bool{}
	for _, pt := range points {
		ptNum := pt.ID[len("вс-рф/пленум/пост-25/п"):]
		if !want[ptNum] || seen[ptNum] {
			t.Fatalf("unexpected or duplicate point %s", pt.ID)
		}
		seen[ptNum] = true
		if pt.Body == "" {
			t.Fatalf("point %s has empty body", pt.ID)
		}
	}
}
