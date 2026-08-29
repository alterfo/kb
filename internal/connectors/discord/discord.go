package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("discord", func() connector.Connector { return New() })
}

const pageLimit = 100

var maxPages = 1000

type Connector struct {
	name     string
	apiBase  string
	webBase  string
	guildID  string
	token    string
	channels []string
	client   *transport.Client
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "discord" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.apiBase = strings.TrimRight(defaultStr(cfg.Config["base_url"], "https://discord.com/api/v10"), "/")
	c.webBase = strings.TrimRight(defaultStr(cfg.Config["web_base_url"], "https://discord.com"), "/")
	c.guildID = strings.TrimSpace(cfg.Config["guild_id"])

	c.channels = nil
	if v := strings.TrimSpace(cfg.Config["channels"]); v != "" {
		for _, ch := range strings.Split(v, ",") {
			ch = strings.TrimSpace(ch)
			if ch != "" {
				c.channels = append(c.channels, ch)
			}
		}
	}
	if len(c.channels) == 0 {
		return fmt.Errorf("discord: source %q: config.channels is required", cfg.Name)
	}

	c.token = ""
	if envName, ok := cfg.Secrets["token"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.token = v
		}
	}
	if c.token == "" {
		return fmt.Errorf("discord: source %q: secrets.token is required", cfg.Name)
	}

	if c.client == nil {
		clientCfg := transport.Config{}
		if raw, ok := env("KB_SOCKS_PROXY"); ok && strings.TrimSpace(raw) != "" {
			dialContext, err := transport.SOCKS5DialContext(raw)
			if err != nil {
				return fmt.Errorf("discord: source %q: %w", cfg.Name, err)
			}
			clientCfg.Doer = &http.Client{Transport: transport.NewProxyBypassTransportWithDialContext(nil, dialContext)}
		}
		c.client = transport.NewClient(clientCfg)
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	st := parseCursorState(since.Value)
	fullReconcile := len(st.Channels) == 0 || !channelSetsEqual(st.Channels, c.channels)

	// On a full reconcile every configured channel is re-enumerated from
	// scratch so the seen set below covers the whole corpus; otherwise
	// Sink.Prune (triggered by FullReconcile) would delete the history of
	// unchanged channels fetched incrementally. Building newChannels from the
	// configured channels only also drops cursors of removed channels.
	newChannels := make(map[string]string, len(c.channels))
	count := 0
	for _, channel := range c.channels {
		cursor := st.Channels[channel]
		if fullReconcile {
			cursor = ""
		}
		docs, newest, err := c.fetchChannel(ctx, channel, cursor)
		if err != nil {
			return since, connector.FetchInfo{}, fmt.Errorf("discord: channel %s: %w", channel, err)
		}
		for _, d := range docs {
			if err := send(ctx, out, d); err != nil {
				return since, connector.FetchInfo{}, err
			}
			count++
		}
		if newest != "" {
			newChannels[channel] = newest
		} else {
			newChannels[channel] = cursor
		}
	}

	newState := cursorState{Channels: newChannels}
	newCursor := connector.Cursor{Value: newState.encode()}
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: fullReconcile}, nil
}

func (c *Connector) fetchChannel(ctx context.Context, channel, cursor string) ([]connector.Document, string, error) {
	key := "before"
	if cursor != "" {
		key = "after"
	}
	messages, err := c.fetchMessages(ctx, channel, key, cursor)
	if err != nil {
		return nil, "", err
	}
	if len(messages) == 0 {
		return nil, "", nil
	}

	// The API returns newest-first, so the last message is the oldest (first)
	// message of this fetched window and provides the thread topic.
	threadTopic := messageTitle(messages[len(messages)-1].Content)
	newest := messages[0].ID

	docs := make([]connector.Document, 0, len(messages))
	for _, m := range messages {
		docs = append(docs, buildDocument(c.name, c.guildID, c.webBase, channel, m, threadTopic))
	}
	return docs, newest, nil
}

func (c *Connector) fetchMessages(ctx context.Context, channel, key, value string) ([]apiMessage, error) {
	var out []apiMessage
	stopID := ""
	if key == "after" {
		stopID = value
	}
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(pageLimit))
		if key != "" && value != "" {
			q.Set(key, value)
		}
		req, err := c.newRequest(ctx, c.apiBase+"/channels/"+url.PathEscape(channel)+"/messages?"+q.Encode())
		if err != nil {
			return nil, err
		}

		resp, err := c.client.Do(ctx, req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode == http.StatusNotFound {
			return out, nil
		}
		if resp.StatusCode != http.StatusOK {
			return nil, &statusError{code: resp.StatusCode}
		}

		var pageMessages []apiMessage
		if err := json.Unmarshal(body, &pageMessages); err != nil {
			return nil, err
		}
		for _, m := range pageMessages {
			if stopID != "" && !messageIDAfter(m.ID, stopID) {
				return out, nil
			}
			out = append(out, m)
		}

		if len(pageMessages) == 0 || len(pageMessages) < pageLimit {
			return out, nil
		}
		oldest := pageMessages[len(pageMessages)-1].ID
		if oldest == "" {
			return out, nil
		}
		if key == "after" {
			key = "before"
		}
		value = oldest
	}
	return nil, fmt.Errorf("discord: channel %s: exceeded %d pages; history truncated", channel, maxPages)
}

func channelSetsEqual(state map[string]string, configured []string) bool {
	if len(state) != len(configured) {
		return false
	}
	for _, ch := range configured {
		if _, ok := state[ch]; !ok {
			return false
		}
	}
	return true
}

func messageIDAfter(a, b string) bool {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a > b
}

func send(ctx context.Context, out chan<- connector.Document, d connector.Document) error {
	select {
	case out <- d:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Connector) newRequest(ctx context.Context, u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	return req, nil
}

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
