package github

import (
	"sort"
	"strconv"

	"github.com/alterfo/kb/internal/connector"
)

func labelNames(labels []apiLabel) []string {
	if len(labels) == 0 {
		return nil
	}
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		names = append(names, l.Name)
	}
	sort.Strings(names)
	return names
}

func buildIssueDocument(sourceName, repoFullName string, it apiIssue) connector.Document {
	kind := "issue"
	fm := map[string]any{
		"repo":   repoFullName,
		"number": it.Number,
		"state":  it.State,
		"author": it.User.Login,
	}
	if labels := labelNames(it.Labels); labels != nil {
		fm["labels"] = labels
	}
	if it.PullRequest != nil {
		kind = "pr"
		fm["merged"] = it.PullRequest.MergedAt != nil
	}

	return connector.Document{
		ID:          repoFullName + "#" + strconv.Itoa(it.Number),
		Source:      sourceName,
		Kind:        kind,
		Title:       it.Title,
		URL:         it.HTMLURL,
		UpdatedAt:   it.UpdatedAt,
		Body:        it.Body,
		Visibility:  "public",
		Frontmatter: fm,
	}
}
