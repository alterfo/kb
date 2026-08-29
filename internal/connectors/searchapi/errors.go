package searchapi

import "fmt"

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("searchapi: unexpected status %d", e.code)
}
