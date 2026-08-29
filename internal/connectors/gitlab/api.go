package gitlab

import (
	"fmt"
	"time"
)

type apiProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
}

type apiUser struct {
	Username string `json:"username"`
}

type apiIssue struct {
	IID         int       `json:"iid"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	WebURL      string    `json:"web_url"`
	UpdatedAt   time.Time `json:"updated_at"`
	Description string    `json:"description"`
	Author      apiUser   `json:"author"`
	Labels      []string  `json:"labels"`
}

type apiMergeRequest struct {
	IID         int       `json:"iid"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	WebURL      string    `json:"web_url"`
	UpdatedAt   time.Time `json:"updated_at"`
	Description string    `json:"description"`
	Author      apiUser   `json:"author"`
	Labels      []string  `json:"labels"`
}

type apiWikiPage struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type apiTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Name string `json:"name"`
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("gitlab: unexpected status %d", e.code)
}
