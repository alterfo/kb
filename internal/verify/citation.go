package verify

import (
	"regexp"
	"strings"
	"unicode"
)

type Chunk struct {
	FileName string
	FilePath string
	ChunkID  string
	Text     string
}

type Citation struct {
	Raw       string
	FileName  string
	ChunkID   string
	InContext bool
}

type CitationReport struct {
	Citations []Citation
	Missing   []Citation
}

func (r CitationReport) HasMissing() bool {
	return len(r.Missing) > 0
}

var citationGroupRe = regexp.MustCompile(`[\[(]([^\[\]()]+)[\])]`)

// CheckCitations verifies that every citation in the answer resolves to
// one of the provided context chunks. Citations that resolve are listed in
// Citations; unresolvable ones land in Missing.
func CheckCitations(answer string, context []Chunk) CitationReport {
	var rep CitationReport
	for _, m := range citationGroupRe.FindAllStringSubmatch(answer, -1) {
		token := cleanCitation(strings.TrimSpace(m[1]))
		if token == "" {
			continue
		}
		if cit, ok := resolveCitation(token, context); ok {
			rep.Citations = append(rep.Citations, cit)
			continue
		}
		parts := strings.FieldsFunc(token, func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		})
		if len(parts) == 1 {
			rep.Missing = append(rep.Missing, Citation{Raw: token})
			continue
		}
		for _, part := range parts {
			part = cleanCitation(part)
			if cit, ok := resolveCitation(part, context); ok {
				rep.Citations = append(rep.Citations, cit)
				continue
			}
			rep.Missing = append(rep.Missing, Citation{Raw: part})
		}
	}
	return rep
}

func cleanCitation(s string) string {
	return strings.Trim(s, `"'“”`)
}

func resolveCitation(token string, chunks []Chunk) (Citation, bool) {
	if token == "" {
		return Citation{}, false
	}
	for _, ch := range chunks {
		if token == ch.FileName || token == ch.ChunkID || (ch.FilePath != "" && token == ch.FilePath) {
			return Citation{Raw: token, FileName: ch.FileName, ChunkID: ch.ChunkID, InContext: true}, true
		}
	}
	return Citation{}, false
}
