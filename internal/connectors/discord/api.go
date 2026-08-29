package discord

import "fmt"

type apiUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type apiMessage struct {
	ID                string      `json:"id"`
	ChannelID         string      `json:"channel_id"`
	Author            apiUser     `json:"author"`
	Content           string      `json:"content"`
	Timestamp         string      `json:"timestamp"`
	EditedTimestamp   *string     `json:"edited_timestamp"`
	ReferencedMessage *apiMessage `json:"referenced_message"`
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("discord: unexpected status %d", e.code)
}
