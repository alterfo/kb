package gitlab

import (
	"encoding/json"
	"time"
)

type cursorState struct {
	Since    time.Time `json:"since,omitempty"`
	Projects []string  `json:"projects,omitempty"`
}

func parseCursorState(v string) cursorState {
	if v == "" {
		return cursorState{}
	}
	var st cursorState
	if err := json.Unmarshal([]byte(v), &st); err != nil {
		return cursorState{}
	}
	return st
}

func (s cursorState) encode() string {
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(b)
}
