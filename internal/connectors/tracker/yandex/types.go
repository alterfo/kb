package yandex

import (
	"fmt"
	"time"
)

const timeLayout = "2006-01-02T15:04:05.000-0700"

type apiStatus struct {
	Display string `json:"display"`
}

type apiUser struct {
	Display string `json:"display"`
}

type apiIssue struct {
	Key         string    `json:"key"`
	Summary     string    `json:"summary"`
	Description string    `json:"description"`
	Status      apiStatus `json:"status"`
	Assignee    *apiUser  `json:"assignee"`
	UpdatedAt   string    `json:"updatedAt"`
}

func (it apiIssue) updated() time.Time {
	t, err := time.Parse(timeLayout, it.UpdatedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("yandex-tracker: unexpected status %d", e.code)
}
