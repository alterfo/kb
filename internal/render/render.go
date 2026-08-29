package render

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/alterfo/kb/internal/connector"
)

var reservedKeys = map[string]bool{
	"id": true, "source": true, "kind": true, "title": true,
	"url": true, "updated_at": true, "visibility": true, "summary": true,
}

func Render(d connector.Document) ([]byte, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}

	add := func(key string, val any) error {
		kNode := &yaml.Node{}
		if err := kNode.Encode(key); err != nil {
			return err
		}
		vNode := &yaml.Node{}
		if err := vNode.Encode(val); err != nil {
			return err
		}
		node.Content = append(node.Content, kNode, vNode)
		return nil
	}

	summary := summaryValue(d)
	fields := []struct {
		key string
		val any
		ok  bool
	}{
		{"id", d.ID, true},
		{"source", d.Source, true},
		{"kind", d.Kind, d.Kind != ""},
		{"title", d.Title, true},
		{"url", d.URL, d.URL != ""},
		{"updated_at", d.UpdatedAt.UTC().Format(time.RFC3339), !d.UpdatedAt.IsZero()},
		{"visibility", d.Visibility, d.Visibility != ""},
		{"summary", summary, summary != ""},
	}
	for _, f := range fields {
		if !f.ok {
			continue
		}
		if err := add(f.key, f.val); err != nil {
			return nil, fmt.Errorf("render: encode %s: %w", f.key, err)
		}
	}

	keys := make([]string, 0, len(d.Frontmatter))
	for k := range d.Frontmatter {
		if reservedKeys[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := add(k, d.Frontmatter[k]); err != nil {
			return nil, fmt.Errorf("render: encode frontmatter %s: %w", k, err)
		}
	}

	var yamlBuf bytes.Buffer
	enc := yaml.NewEncoder(&yamlBuf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return nil, fmt.Errorf("render: encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("render: close encoder: %w", err)
	}

	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(yamlBuf.Bytes())
	out.WriteString("---\n\n")
	out.WriteString(strings.TrimRight(d.Body, "\n"))
	out.WriteString("\n")
	return out.Bytes(), nil
}

// Parse reverses Render: it splits the YAML frontmatter from the body and
// lifts reserved keys back onto the Document's typed fields, leaving
// everything else in Frontmatter.
func Parse(data []byte) (connector.Document, error) {
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return connector.Document{}, fmt.Errorf("render: parse: missing frontmatter delimiter")
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return connector.Document{}, fmt.Errorf("render: parse: unterminated frontmatter")
	}
	fmRaw := rest[:end]
	body := strings.TrimPrefix(rest[end+len("\n---\n"):], "\n")

	var raw map[string]any
	if err := yaml.Unmarshal([]byte(fmRaw), &raw); err != nil {
		return connector.Document{}, fmt.Errorf("render: parse: decode frontmatter: %w", err)
	}

	d := connector.Document{Body: strings.TrimRight(body, "\n")}
	for k, v := range raw {
		switch k {
		case "id":
			d.ID = frontmatterString(v)
		case "source":
			d.Source = frontmatterString(v)
		case "kind":
			d.Kind = frontmatterString(v)
		case "title":
			d.Title = frontmatterString(v)
		case "url":
			d.URL = frontmatterString(v)
		case "visibility":
			d.Visibility = frontmatterString(v)
		case "updated_at":
			s := frontmatterString(v)
			if s == "" {
				continue
			}
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return connector.Document{}, fmt.Errorf("render: parse: updated_at: %w", err)
			}
			d.UpdatedAt = t
		case "summary":
			d.Summary = frontmatterString(v)
		default:
			if d.Frontmatter == nil {
				d.Frontmatter = map[string]any{}
			}
			d.Frontmatter[k] = v
		}
	}
	return d, nil
}

func summaryValue(d connector.Document) string {
	if v, ok := d.Frontmatter["summary"]; ok {
		s := fmt.Sprint(v)
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return d.Summary
}

func frontmatterString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
