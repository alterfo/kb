package trello

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("trello", func() connector.Connector { return New() })
}

const cardFields = "name,desc,due,closed,shortUrl,idList,labels"

type Connector struct {
	name       string
	boardID    string
	apiBase    string
	publicBase string
	key        string
	token      string
	client     *transport.Client
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "trello" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.boardID = strings.TrimSpace(cfg.Config["board_id"])
	if c.boardID == "" {
		return fmt.Errorf("trello: source %q: config.board_id is required", cfg.Name)
	}

	c.apiBase = strings.TrimRight(defaultStr(cfg.Config["api_base"], "https://api.trello.com"), "/")
	c.publicBase = strings.TrimRight(defaultStr(cfg.Config["public_base"], "https://trello.com"), "/")

	keyName, hasKey := cfg.Secrets["key"]
	tokenName, hasToken := cfg.Secrets["token"]
	if hasKey != hasToken {
		return fmt.Errorf("trello: source %q: secrets.key and secrets.token must be configured together", cfg.Name)
	}
	if hasKey {
		if keyName == "" || tokenName == "" {
			return fmt.Errorf("trello: source %q: secrets.key and secrets.token must both name env vars", cfg.Name)
		}
		var ok bool
		c.key, ok = env(keyName)
		if !ok || c.key == "" {
			return fmt.Errorf("trello: source %q: secrets.key env %q is not set", cfg.Name, keyName)
		}
		c.token, ok = env(tokenName)
		if !ok || c.token == "" {
			return fmt.Errorf("trello: source %q: secrets.token env %q is not set", cfg.Name, tokenName)
		}
	}

	if c.client == nil {
		c.client = transport.NewClient(transport.Config{})
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	count := 0
	if err := c.fetchCards(ctx, out, &count); err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("trello: %w", err)
	}
	return connector.Cursor{}, connector.FetchInfo{ItemCount: count, FullReconcile: true}, nil
}

func (c *Connector) fetchCards(ctx context.Context, out chan<- connector.Document, count *int) error {
	if c.key != "" && c.token != "" {
		return c.fetchAPICards(ctx, out, count)
	}
	return c.fetchPublicBoard(ctx, out, count)
}

func (c *Connector) fetchPublicBoard(ctx context.Context, out chan<- connector.Document, count *int) error {
	u := c.publicBase + "/b/" + c.boardID + ".json"
	body, err := c.get(ctx, u)
	if err != nil {
		return err
	}

	var board trelloBoard
	if err := json.Unmarshal(body, &board); err != nil {
		return fmt.Errorf("decode board json: %w", err)
	}

	listNames := make(map[string]string, len(board.Lists))
	for _, list := range board.Lists {
		listNames[list.ID] = list.Name
	}
	for _, card := range board.Cards {
		if card.Closed {
			continue
		}
		if err := send(ctx, out, buildDocument(c.name, c.boardID, listName(listNames, card.IDList), card)); err != nil {
			return err
		}
		*count++
	}
	return nil
}

func (c *Connector) fetchAPICards(ctx context.Context, out chan<- connector.Document, count *int) error {
	listNames, err := c.fetchAPILists(ctx)
	if err != nil {
		return err
	}

	q := url.Values{}
	q.Set("key", c.key)
	q.Set("token", c.token)
	q.Set("fields", cardFields)
	u := c.apiBase + "/1/boards/" + c.boardID + "/cards?" + q.Encode()
	body, err := c.get(ctx, u)
	if err != nil {
		return err
	}

	var cards []trelloCard
	if err := json.Unmarshal(body, &cards); err != nil {
		return fmt.Errorf("decode cards json: %w", err)
	}
	for _, card := range cards {
		if card.Closed {
			continue
		}
		if err := send(ctx, out, buildDocument(c.name, c.boardID, listName(listNames, card.IDList), card)); err != nil {
			return err
		}
		*count++
	}
	return nil
}

func (c *Connector) fetchAPILists(ctx context.Context) (map[string]string, error) {
	q := url.Values{}
	q.Set("key", c.key)
	q.Set("token", c.token)
	q.Set("fields", "id,name")
	u := c.apiBase + "/1/boards/" + c.boardID + "/lists?" + q.Encode()
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}

	var lists []trelloList
	if err := json.Unmarshal(body, &lists); err != nil {
		return nil, fmt.Errorf("decode lists json: %w", err)
	}
	names := make(map[string]string, len(lists))
	for _, list := range lists {
		names[list.ID] = list.Name
	}
	return names, nil
}

func (c *Connector) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(ctx, req.Clone(ctx))
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &statusError{resp.StatusCode}
	}
	return body, nil
}

func send(ctx context.Context, out chan<- connector.Document, d connector.Document) error {
	select {
	case out <- d:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func listName(names map[string]string, idList string) string {
	if name := names[idList]; name != "" {
		return name
	}
	return idList
}

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
