package actualize

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

func getFixtureMessages(t *testing.T, channel, oldest string) []slackFixtureMessage {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/conversations.history", nil)
	q := req.URL.Query()
	if channel != "" {
		q.Set("channel", channel)
	}
	if oldest != "" {
		q.Set("oldest", oldest)
	}
	req.URL.RawQuery = q.Encode()

	rr := httptest.NewRecorder()
	NewFixtureHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp slackFixtureResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatal("response ok = false")
	}
	return resp.Messages
}

func TestFixtureServerFullFetch(t *testing.T) {
	msgs := getFixtureMessages(t, "C-AVRORA", "")
	if len(msgs) != 5 {
		t.Fatalf("messages = %d, want 5", len(msgs))
	}
	for _, m := range msgs {
		if m.Type != "message" {
			t.Errorf("type = %q, want message", m.Type)
		}
		if m.User == "" || m.Text == "" || m.Ts == "" {
			t.Errorf("message has empty field: %+v", m)
		}
	}
	if !sort.SliceIsSorted(msgs, func(i, j int) bool {
		return parseSlackFixtureTS(msgs[i].Ts) < parseSlackFixtureTS(msgs[j].Ts)
	}) {
		t.Errorf("messages not ordered oldest-first: %v", messageTSs(msgs))
	}
}

func TestFixtureServerOldestFilter(t *testing.T) {
	corrections := ChatCorrections()
	if len(corrections) < 3 {
		t.Fatalf("need at least 3 corrections, got %d", len(corrections))
	}
	third := corrections[2].TS

	msgs := getFixtureMessages(t, "C-AVRORA", third)
	if len(msgs) != 2 {
		t.Fatalf("messages with oldest=%s = %d, want 2", third, len(msgs))
	}
	for _, m := range msgs {
		if parseSlackFixtureTS(m.Ts) <= parseSlackFixtureTS(third) {
			t.Errorf("message ts %s is not newer than oldest %s", m.Ts, third)
		}
	}
}

func TestFixtureServerUnknownChannel(t *testing.T) {
	msgs := getFixtureMessages(t, "C-UNKNOWN", "")
	if len(msgs) != 0 {
		t.Fatalf("messages for unknown channel = %d, want 0", len(msgs))
	}
}

func messageTSs(msgs []slackFixtureMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Ts
	}
	return out
}
