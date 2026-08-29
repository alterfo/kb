package slack

import "fmt"

type apiHistoryResponse struct {
	OK               bool         `json:"ok"`
	Error            string       `json:"error"`
	Messages         []apiMessage `json:"messages"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

type apiMessage struct {
	Type     string     `json:"type"`
	User     string     `json:"user"`
	Text     string     `json:"text"`
	Ts       string     `json:"ts"`
	ThreadTs string     `json:"thread_ts"`
	Edited   *apiEdited `json:"edited"`
}

type apiEdited struct {
	User string `json:"user"`
	Ts   string `json:"ts"`
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("slack: unexpected status %d", e.code)
}
