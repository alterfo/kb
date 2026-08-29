package kaiten

import (
	"fmt"
	"time"
)

const (
	timeLayout       = "2006-01-02T15:04:05.999Z"
	timeFormatLayout = "2006-01-02T15:04:05.000Z"
)

type apiColumn struct {
	Title string `json:"title"`
}

type apiBoard struct {
	Title string `json:"title"`
}

type apiUser struct {
	FullName string `json:"full_name"`
}

type apiCard struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	UpdatedAt   string    `json:"updated"`
	Column      apiColumn `json:"column"`
	Board       apiBoard  `json:"board"`
	Owner       *apiUser  `json:"owner"`
}

func (c apiCard) updated() time.Time {
	t, err := time.Parse(timeLayout, c.UpdatedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("kaiten: unexpected status %d", e.code)
}
