package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type SourceInstance struct {
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"`
	Config  map[string]string `yaml:"config"`
	Secrets map[string]string `yaml:"secrets"`
}

type SourcesConfig struct {
	Sources            []SourceInstance    `yaml:"sources"`
	VirtualCollections map[string][]string `yaml:"virtual_collections"`
}

var envVarNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func LoadSourcesFile(path string) (SourcesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SourcesConfig{}, fmt.Errorf("reading sources file %q: %w", path, err)
	}
	return ParseSources(data)
}

func ParseSources(data []byte) (SourcesConfig, error) {
	var cfg SourcesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return SourcesConfig{}, fmt.Errorf("parsing sources yaml: %w", err)
	}

	seen := make(map[string]bool)
	names := make(map[string]string)
	for _, src := range cfg.Sources {
		if src.Name == "" {
			return SourcesConfig{}, fmt.Errorf("source with empty name (type %q)", src.Type)
		}
		if src.Type == "" {
			return SourcesConfig{}, fmt.Errorf("source %q: empty type", src.Name)
		}
		if prevType, ok := names[src.Name]; ok {
			return SourcesConfig{}, fmt.Errorf(
				"source name %q is used by both %q and %q sources; names must be unique across types (sink directories, pruning, and retrieval key on the name alone)",
				src.Name, prevType, src.Type,
			)
		}
		names[src.Name] = src.Type
		key := src.Type + ":" + src.Name
		if seen[key] {
			return SourcesConfig{}, fmt.Errorf("duplicate source instance %q", key)
		}
		seen[key] = true

		for field, envName := range src.Secrets {
			if !envVarNamePattern.MatchString(envName) {
				return SourcesConfig{}, fmt.Errorf(
					"source %q: secret %q value %q must be an env var NAME (uppercase, digits, underscore), not a literal secret",
					src.Name, field, envName,
				)
			}
		}
	}

	for collection, globs := range cfg.VirtualCollections {
		for _, glob := range globs {
			typ, name, ok := strings.Cut(strings.TrimSpace(glob), ":")
			if !ok || typ == "" || name == "" || name == "*" {
				continue
			}
			found := false
			for _, src := range cfg.Sources {
				if src.Type == typ && src.Name == name {
					found = true
					break
				}
			}
			if !found {
				return SourcesConfig{}, fmt.Errorf(
					"virtual collection %q references unknown source %q", collection, glob,
				)
			}
		}
	}

	return cfg, nil
}

// SourceNamesFor expands a selector into the concrete source instance names
// it covers for retrieval filtering. A selector that names a virtual
// collection is expanded through its globs (`type:*` matches every source
// of that connector type, `type:name` a single instance); anything else is
// returned unchanged, so exact source names keep working as-is.
func (c SourcesConfig) SourceNamesFor(selector string) []string {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil
	}
	globs, ok := c.VirtualCollections[selector]
	if !ok {
		return []string{selector}
	}
	var out []string
	for _, g := range globs {
		out = append(out, c.expandGlob(g)...)
	}
	return out
}

func (c SourcesConfig) expandGlob(glob string) []string {
	typ, name, ok := strings.Cut(strings.TrimSpace(glob), ":")
	if !ok || typ == "" {
		return nil
	}
	var out []string
	for _, src := range c.Sources {
		if src.Type != typ {
			continue
		}
		if name == "*" || name == src.Name {
			out = append(out, src.Name)
		}
	}
	return out
}
