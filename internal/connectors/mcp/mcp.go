package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
)

func init() {
	registry.Register("mcp", func() connector.Connector { return New() })
}

type Connector struct {
	name          string
	transportKind string
	command       string
	url           string
	token         string
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "mcp" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.transportKind = strings.ToLower(strings.TrimSpace(cfg.Config["transport"]))

	switch c.transportKind {
	case "stdio":
		c.command = strings.TrimSpace(cfg.Config["command"])
		if c.command == "" {
			return fmt.Errorf("mcp: source %q: config.command is required for stdio transport", cfg.Name)
		}
	case "http":
		c.url = strings.TrimSpace(cfg.Config["url"])
		if c.url == "" {
			return fmt.Errorf("mcp: source %q: config.url is required for http transport", cfg.Name)
		}
	default:
		return fmt.Errorf("mcp: source %q: config.transport must be %q or %q", cfg.Name, "stdio", "http")
	}

	c.token = ""
	if envName, ok := cfg.Secrets["token"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.token = v
		}
	}
	return nil
}

func (c *Connector) connect(ctx context.Context) (*sdk.ClientSession, error) {
	client := sdk.NewClient(&sdk.Implementation{Name: "kb", Version: "0.1.0"}, nil)

	switch c.transportKind {
	case "stdio":
		parts := strings.Fields(c.command)
		if len(parts) == 0 {
			return nil, fmt.Errorf("mcp: empty command")
		}
		cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
		return client.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	case "http":
		hc := &http.Client{}
		if c.token != "" {
			hc.Transport = &headerTransport{header: "Authorization", value: "Bearer " + c.token}
		}
		return client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: c.url, HTTPClient: hc}, nil)
	default:
		return nil, fmt.Errorf("mcp: unknown transport %q", c.transportKind)
	}
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	cs, err := c.connect(ctx)
	if err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("mcp: connect: %w", err)
	}
	defer cs.Close()

	newHashes := map[string]string{}
	count := 0
	readErrs := 0

	for res, rerr := range cs.Resources(ctx, nil) {
		if rerr != nil {
			return since, connector.FetchInfo{}, fmt.Errorf("mcp: list resources: %w", rerr)
		}
		rr, err := cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: res.URI})
		if err != nil {
			// Fail-open on individual reads, but a failed read means the
			// enumeration is incomplete: report no full reconcile so the
			// sink cannot prune the skipped resource on transient errors.
			readErrs++
			continue
		}
		body := extractText(rr.Contents)
		id := c.name + ":" + res.URI
		hash := hashBody(body)
		newHashes[id] = hash
		d := buildResourceDocument(c.name, res, body)
		if err := send(ctx, out, d); err != nil {
			return since, connector.FetchInfo{}, err
		}
		count++
	}

	for tool, terr := range cs.Tools(ctx, nil) {
		if terr != nil {
			return since, connector.FetchInfo{}, fmt.Errorf("mcp: list tools: %w", terr)
		}
		body := formatToolBody(tool)
		id := c.name + ":tool:" + tool.Name
		hash := hashBody(body)
		newHashes[id] = hash
		d := buildToolDocument(c.name, tool, body)
		if err := send(ctx, out, d); err != nil {
			return since, connector.FetchInfo{}, err
		}
		count++
	}

	newCursor := connector.Cursor{Value: cursorState{Hashes: newHashes}.encode()}
	// Every run enumerates the full current set, so the sink can prune
	// resources/tools removed upstream (the ingest layer prunes only when
	// FullReconcile is set). An incomplete enumeration (a read error)
	// suppresses pruning to avoid deleting live data.
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: readErrs == 0}, nil
}

func send(ctx context.Context, out chan<- connector.Document, d connector.Document) error {
	select {
	case out <- d:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func hashBody(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type headerTransport struct {
	base   http.RoundTripper
	header string
	value  string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(t.header, t.value)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}
