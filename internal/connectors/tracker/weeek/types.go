package weeek

import "fmt"

type apiTask struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	BoardName   string `json:"boardName"`
	ColumnName  string `json:"columnName"`
	UpdatedAt   string `json:"updatedAt"`
	Responsible []int  `json:"responsibleIds"`
}

type apiTasksResponse struct {
	Success bool      `json:"success"`
	Tasks   []apiTask `json:"tasks"`
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("weeek: unexpected status %d", e.code)
}
