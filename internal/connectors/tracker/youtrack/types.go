package youtrack

import (
	"fmt"
	"strings"
	"time"
)

type apiProject struct {
	ShortName string `json:"shortName"`
}

type apiFieldValue struct {
	Name  string `json:"name"`
	Login string `json:"login"`
}

type apiCustomField struct {
	Name  string         `json:"name"`
	Value *apiFieldValue `json:"value"`
}

type apiIssue struct {
	IDReadable  string           `json:"idReadable"`
	Summary     string           `json:"summary"`
	Description string           `json:"description"`
	Updated     int64            `json:"updated"`
	Project     apiProject       `json:"project"`
	CustomField []apiCustomField `json:"customFields"`
}

func (it apiIssue) updated() time.Time {
	if it.Updated <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(it.Updated).UTC()
}

func (it apiIssue) customField(name string) string {
	for _, f := range it.CustomField {
		if !strings.EqualFold(f.Name, name) || f.Value == nil {
			continue
		}
		if f.Value.Login != "" {
			return f.Value.Login
		}
		return f.Value.Name
	}
	return ""
}

func (it apiIssue) status() string   { return it.customField("State") }
func (it apiIssue) assignee() string { return it.customField("Assignee") }

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("youtrack: unexpected status %d", e.code)
}
