package legaleval

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

type PlenumPoint struct {
	ID   string
	Body string
}

func ParsePlenumPoints(r io.Reader, prefix string) ([]PlenumPoint, error) {
	var points []PlenumPoint
	var curNum, curBody []string
	sc := bufio.NewScanner(r)
	flush := func() {
		if len(curNum) == 0 {
			return
		}
		id := "п" + strings.Join(curNum, "")
		if prefix != "" {
			id = prefix + "/п" + strings.Join(curNum, "")
		}
		points = append(points, PlenumPoint{
			ID:   id,
			Body: strings.TrimSpace(strings.Join(curBody, "\n")),
		})
	}
	for sc.Scan() {
		trimmed := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(trimmed, "## Пункт ") {
			flush()
			curNum = []string{strings.TrimSpace(strings.TrimPrefix(trimmed, "## Пункт "))}
			curBody = nil
			continue
		}
		if len(curNum) > 0 && !strings.HasPrefix(trimmed, "#") {
			curBody = append(curBody, sc.Text())
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("legaleval: scan plenum: %w", err)
	}
	return points, nil
}

func LoadPlenumPoints(path, prefix string) ([]PlenumPoint, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("legaleval: open %s: %w", path, err)
	}
	defer f.Close()
	return ParsePlenumPoints(f, prefix)
}

type Plenum struct {
	byID map[string]PlenumPoint
}

func NewPlenum(points []PlenumPoint) *Plenum {
	p := &Plenum{byID: make(map[string]PlenumPoint, len(points))}
	for _, pt := range points {
		p.byID[pt.ID] = pt
	}
	return p
}

func (p *Plenum) Point(id string) (PlenumPoint, bool) {
	pt, ok := p.byID[id]
	return pt, ok
}

// Known reports whether id names a plenum point or the resolution document
// itself. A citation may point at the whole Постановление (the chunk id of
// a plenum document) rather than one of its points; such a citation is not
// a statute claim and must not count as a hallucinated statute.
func (p *Plenum) Known(id string) bool {
	if _, ok := p.byID[id]; ok {
		return true
	}
	for pid := range p.byID {
		if strings.HasPrefix(pid, id+"/") {
			return true
		}
	}
	return false
}
