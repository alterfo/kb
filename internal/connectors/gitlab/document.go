package gitlab

import (
	"sort"
	"strconv"

	"github.com/alterfo/kb/internal/connector"
)

func sortedLabels(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, len(labels))
	copy(out, labels)
	sort.Strings(out)
	return out
}

func buildIssueDocument(sourceName, project string, it apiIssue) connector.Document {
	fm := map[string]any{
		"project": project,
		"iid":     it.IID,
		"state":   it.State,
		"author":  it.Author.Username,
	}
	if labels := sortedLabels(it.Labels); labels != nil {
		fm["labels"] = labels
	}
	return connector.Document{
		ID:          project + "#" + strconv.Itoa(it.IID),
		Source:      sourceName,
		Kind:        "issue",
		Title:       it.Title,
		URL:         it.WebURL,
		UpdatedAt:   it.UpdatedAt,
		Body:        it.Description,
		Visibility:  "public",
		Frontmatter: fm,
	}
}

func buildMergeRequestDocument(sourceName, project string, mr apiMergeRequest) connector.Document {
	fm := map[string]any{
		"project": project,
		"iid":     mr.IID,
		"state":   mr.State,
		"author":  mr.Author.Username,
	}
	if labels := sortedLabels(mr.Labels); labels != nil {
		fm["labels"] = labels
	}
	return connector.Document{
		ID:          project + "!" + strconv.Itoa(mr.IID),
		Source:      sourceName,
		Kind:        "mr",
		Title:       mr.Title,
		URL:         mr.WebURL,
		UpdatedAt:   mr.UpdatedAt,
		Body:        mr.Description,
		Visibility:  "public",
		Frontmatter: fm,
	}
}
