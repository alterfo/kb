package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/alterfo/kb/internal/config"
)

type listSourcesIn struct{}

type sourceSummary struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	ConfigKeys []string `json:"config_keys,omitempty"`
	SecretEnvs []string `json:"secret_envs,omitempty"`
}

type listSourcesOut struct {
	Sources            []sourceSummary     `json:"sources"`
	VirtualCollections map[string][]string `json:"virtual_collections,omitempty"`
}

func (s *Server) listSources(_ context.Context, _ *sdk.CallToolRequest, _ listSourcesIn) (*sdk.CallToolResult, listSourcesOut, error) {
	cfg, err := loadSourcesConfig(s.deps.SourcesPath)
	if err != nil {
		return nil, listSourcesOut{}, err
	}

	out := listSourcesOut{VirtualCollections: cfg.VirtualCollections}
	for _, src := range cfg.Sources {
		summary := sourceSummary{Name: src.Name, Type: src.Type}
		for k := range src.Config {
			summary.ConfigKeys = append(summary.ConfigKeys, k)
		}
		for _, envName := range src.Secrets {
			summary.SecretEnvs = append(summary.SecretEnvs, envName)
		}
		out.Sources = append(out.Sources, summary)
	}
	return nil, out, nil
}

type addSourceIn struct {
	Name    string            `json:"name" jsonschema:"unique instance name for this source"`
	Type    string            `json:"type" jsonschema:"connector type, e.g. github, gitlab, wiki, mcp, telegram, slack, mattermost, yandex-tracker, youtrack, kaiten, weeek, searchapi, file"`
	Config  map[string]string `json:"config,omitempty" jsonschema:"non-secret connector configuration"`
	Secrets map[string]string `json:"secrets,omitempty" jsonschema:"map of secret field name to the ENV_VAR_NAME holding its value (never a literal secret)"`
}

type addSourceOut struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (s *Server) addSource(_ context.Context, _ *sdk.CallToolRequest, in addSourceIn) (*sdk.CallToolResult, addSourceOut, error) {
	cfg, err := loadSourcesConfig(s.deps.SourcesPath)
	if err != nil {
		return nil, addSourceOut{}, err
	}

	instance := config.SourceInstance{Name: in.Name, Type: in.Type, Config: in.Config, Secrets: in.Secrets}
	cfg.Sources = append(cfg.Sources, instance)

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, addSourceOut{}, fmt.Errorf("mcp: add_source: marshal: %w", err)
	}
	if _, err := config.ParseSources(data); err != nil {
		return nil, addSourceOut{}, fmt.Errorf("mcp: add_source: %w", err)
	}

	if err := writeSourcesConfig(s.deps.SourcesPath, data); err != nil {
		return nil, addSourceOut{}, err
	}
	return nil, addSourceOut{Name: in.Name, Type: in.Type}, nil
}

func loadSourcesConfig(path string) (config.SourcesConfig, error) {
	cfg, err := config.LoadSourcesFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return config.SourcesConfig{}, nil
		}
		return config.SourcesConfig{}, err
	}
	return cfg, nil
}

func writeSourcesConfig(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mcp: add_source: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("mcp: add_source: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("mcp: add_source: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("mcp: add_source: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("mcp: add_source: %w", err)
	}
	return nil
}
