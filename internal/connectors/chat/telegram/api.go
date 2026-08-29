package telegram

import "fmt"

type apiUpdatesResponse struct {
	OK          bool        `json:"ok"`
	Description string      `json:"description"`
	Result      []apiUpdate `json:"result"`
}

type apiUpdate struct {
	UpdateID          int64       `json:"update_id"`
	Message           *apiMessage `json:"message"`
	ChannelPost       *apiMessage `json:"channel_post"`
	EditedMessage     *apiMessage `json:"edited_message"`
	EditedChannelPost *apiMessage `json:"edited_channel_post"`
}

type apiMessage struct {
	MessageID      int64        `json:"message_id"`
	From           *apiUser     `json:"from"`
	Chat           apiChat      `json:"chat"`
	Date           int64        `json:"date"`
	EditDate       int64        `json:"edit_date"`
	Text           string       `json:"text"`
	ReplyToMessage *apiReplyRef `json:"reply_to_message"`
}

type apiReplyRef struct {
	MessageID int64 `json:"message_id"`
}

type apiUser struct {
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type apiChat struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Username string `json:"username"`
	Type     string `json:"type"`
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("telegram: unexpected status %d", e.code)
}
