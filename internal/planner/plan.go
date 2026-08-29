package planner

import (
	"regexp"
	"strings"
)

var (
	taskHeaderRe = regexp.MustCompile(`^###\s+(Task|Iteration)\s+(\d+)\s*:?\s*(.*)$`)
	checkItemRe  = regexp.MustCompile(`^(\s*)([-*])\s+\[([ xX])\]\s?(.*)$`)
	titleRe      = regexp.MustCompile(`^#\s+(.*)$`)
)

// Item is a single checkbox line inside a section.
type Item struct {
	Line int
	Text string
	Done bool
}

// Section is a contiguous block of checkboxes under a header. HeaderLine is
// the 0-based line index of the header. Kind is "task" for "### Task N:"
// / "### Iteration N:" sections and "other" for any other heading that
// contains checkboxes (Overview, Context, Success criteria, ...).
type Section struct {
	Header     string
	Title      string
	Kind       string
	HeaderLine int
	Items      []Item
}

// Pending returns the first unfinished checkbox in the section, or nil.
func (s *Section) Pending() *Item {
	for i := range s.Items {
		if !s.Items[i].Done {
			return &s.Items[i]
		}
	}
	return nil
}

// Plan is a parsed markdown plan file. It keeps the original lines so that
// checkbox state can be flipped in place and rendered back without losing
// any unrelated content.
type Plan struct {
	Title    string
	lines    []string
	Sections []Section
}

// ParsePlan parses a plan file. It recognises "### Task N:" / "### Iteration
// N:" sections (execution tasks) and any other heading containing checkboxes.
func ParsePlan(src []byte) *Plan {
	raw := strings.ReplaceAll(string(src), "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	p := &Plan{lines: lines}

	var cur *Section
	for i, line := range lines {
		if m := titleRe.FindStringSubmatch(line); m != nil {
			if p.Title == "" {
				p.Title = strings.TrimSpace(m[1])
			}
			continue
		}
		if m := taskHeaderRe.FindStringSubmatch(line); m != nil {
			p.Sections = append(p.Sections, Section{})
			cur = &p.Sections[len(p.Sections)-1]
			cur.Header = line
			cur.Title = strings.TrimSpace(m[3])
			cur.Kind = "task"
			cur.HeaderLine = i
			continue
		}
		if cm := checkItemRe.FindStringSubmatch(line); cm != nil {
			done := cm[3] == "x" || cm[3] == "X"
			item := Item{Line: i, Text: strings.TrimSpace(cm[4]), Done: done}
			if cur != nil {
				cur.Items = append(cur.Items, item)
			}
			continue
		}
		if isHeading(line) {
			p.Sections = append(p.Sections, Section{})
			cur = &p.Sections[len(p.Sections)-1]
			cur.Header = line
			cur.Title = headingTitle(line)
			cur.Kind = "other"
			cur.HeaderLine = i
		}
	}
	return p
}

func isHeading(line string) bool {
	return strings.HasPrefix(line, "##")
}

func headingTitle(line string) string {
	return strings.TrimSpace(strings.TrimLeft(line, "#"))
}

// FirstPending returns the first task section that still has unfinished
// checkboxes, or nil when every task section is complete.
func (p *Plan) FirstPending() *Section {
	for i := range p.Sections {
		s := &p.Sections[i]
		if s.Kind == "task" && s.Pending() != nil {
			return s
		}
	}
	return nil
}

// PendingOther returns non-task sections that still have unfinished
// checkboxes (e.g. "Success criteria"). These are informational for the
// runner: it must decide whether to satisfy or skip them.
func (p *Plan) PendingOther() []Section {
	var out []Section
	for i := range p.Sections {
		s := &p.Sections[i]
		if s.Kind == "other" && s.Pending() != nil {
			out = append(out, *s)
		}
	}
	return out
}

// SetDone marks every checkbox in the section as complete. It returns true
// when at least one line changed.
func (p *Plan) SetDone(s *Section) bool {
	changed := false
	for i := range s.Items {
		if s.Items[i].Done {
			continue
		}
		p.lines[s.Items[i].Line] = checkItemRe.ReplaceAllString(p.lines[s.Items[i].Line], "${1}${2} [x] ${4}")
		s.Items[i].Done = true
		changed = true
	}
	return changed
}

// Bytes renders the plan file with the current checkbox state.
func (p *Plan) Bytes() []byte {
	return []byte(strings.Join(p.lines, "\n"))
}

// RawSection returns the original text of a section (header plus everything
// below it up to the next section header), as it appears in the file.
func (p *Plan) RawSection(s *Section) string {
	start := s.HeaderLine
	end := len(p.lines)
	for i := range p.Sections {
		if p.Sections[i].HeaderLine > start {
			end = p.Sections[i].HeaderLine
			break
		}
	}
	return strings.Join(p.lines[start:end], "\n")
}
