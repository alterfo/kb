package mattermost

import "fmt"

type apiPostsResponse struct {
	Order []string           `json:"order"`
	Posts map[string]apiPost `json:"posts"`
}

type apiPost struct {
	ID        string `json:"id"`
	RootID    string `json:"root_id"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	CreateAt  int64  `json:"create_at"`
	EditAt    int64  `json:"edit_at"`
	Message   string `json:"message"`
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("mattermost: unexpected status %d", e.code)
}
