package github

import (
	"fmt"
	"time"
)

type apiRepo struct {
	FullName string `json:"full_name"`
}

type apiUser struct {
	Login string `json:"login"`
}

type apiLabel struct {
	Name string `json:"name"`
}

type apiPullRequestStub struct {
	MergedAt *time.Time `json:"merged_at"`
}

type apiIssue struct {
	Number      int                 `json:"number"`
	Title       string              `json:"title"`
	State       string              `json:"state"`
	HTMLURL     string              `json:"html_url"`
	UpdatedAt   time.Time           `json:"updated_at"`
	Body        string              `json:"body"`
	User        apiUser             `json:"user"`
	Labels      []apiLabel          `json:"labels"`
	PullRequest *apiPullRequestStub `json:"pull_request"`
}

type apiContentEntry struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	DownloadURL string `json:"download_url"`
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("github: unexpected status %d", e.code)
}
