package trello

import "fmt"

type trelloBoard struct {
	Lists []trelloList `json:"lists"`
	Cards []trelloCard `json:"cards"`
}

type trelloList struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type trelloCard struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Desc     string        `json:"desc"`
	Due      string        `json:"due"`
	Closed   bool          `json:"closed"`
	ShortURL string        `json:"shortUrl"`
	IDList   string        `json:"idList"`
	Labels   []trelloLabel `json:"labels"`
}

type trelloLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("trello: unexpected status %d", e.code)
}
