package actualize

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
)

type slackFixtureMessage struct {
	Type string `json:"type"`
	User string `json:"user"`
	Text string `json:"text"`
	Ts   string `json:"ts"`
}

type slackFixtureResponse struct {
	OK       bool                  `json:"ok"`
	Messages []slackFixtureMessage `json:"messages"`
}

func NewFixtureHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		channel := r.URL.Query().Get("channel")
		oldest := r.URL.Query().Get("oldest")

		corrections := append([]ChatCorrection(nil), ChatCorrections()...)
		sort.SliceStable(corrections, func(i, j int) bool {
			return parseSlackFixtureTS(corrections[i].TS) < parseSlackFixtureTS(corrections[j].TS)
		})

		msgs := make([]slackFixtureMessage, 0, len(corrections))
		for _, c := range corrections {
			if channel != "" && c.Channel != channel {
				continue
			}
			if oldest != "" && parseSlackFixtureTS(c.TS) <= parseSlackFixtureTS(oldest) {
				continue
			}
			msgs = append(msgs, slackFixtureMessage{
				Type: "message",
				User: c.User,
				Text: c.Text,
				Ts:   c.TS,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(slackFixtureResponse{OK: true, Messages: msgs}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}

func parseSlackFixtureTS(ts string) float64 {
	f, _ := strconv.ParseFloat(ts, 64)
	return f
}
