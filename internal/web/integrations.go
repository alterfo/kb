package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/sink"
	"github.com/alterfo/kb/internal/state"
)

type secretPresence struct {
	Name    string
	Present bool
}

type integrationSource struct {
	Name        string
	Type        string
	ConfigKeys  []string
	Secrets     []secretPresence
	LastSync    string
	NeverSynced bool
	Stale       bool
	LastError   string
	EditURL     string
}

type integrationFormData struct {
	Name    string
	Type    string
	Config  string
	Secrets string
	Editing string
}

type integrationsData struct {
	Note               string
	Form               integrationFormData
	Sources            []integrationSource
	VirtualCollections map[string][]string
}

func (s *Server) handleIntegrations(w http.ResponseWriter, r *http.Request) {
	data := s.integrationView(r.Context())
	var alerts []Alert
	if edit := strings.TrimSpace(r.URL.Query().Get("edit")); edit != "" {
		data.Form = s.integrationForm(r.Context(), edit)
	}
	if deleted := strings.TrimSpace(r.URL.Query().Get("deleted")); deleted != "" {
		msg := "source " + deleted + " removed from sources.yaml"
		if related := r.URL.Query().Get("related"); related != "" && related != "0" {
			msg += "; " + related + " document(s) in the corpus still reference it and will no longer be synced"
		}
		alerts = append(alerts, Alert{Kind: "success", Message: msg})
	}
	s.render(w, "page-integrations", http.StatusOK, page{
		Title:  "Integrations",
		Alerts: alerts,
		Data:   data,
	})
}

func (s *Server) integrationView(ctx context.Context) integrationsData {
	data := integrationsData{VirtualCollections: map[string][]string{}}
	cfg, err := s.loadSources(ctx)
	if err != nil {
		data.Note = "sources config unavailable: " + err.Error()
		return data
	}
	data.VirtualCollections = cfg.VirtualCollections
	data.Sources = s.integrationRows(ctx)
	return data
}

func (s *Server) integrationForm(ctx context.Context, editName string) integrationFormData {
	form := integrationFormData{Editing: editName}
	cfg, err := s.loadSources(ctx)
	if err != nil {
		return form
	}
	for _, src := range cfg.Sources {
		if src.Name == editName {
			return sourceToForm(src, editName)
		}
	}
	return form
}

func sourceToForm(src config.SourceInstance, editing string) integrationFormData {
	return integrationFormData{
		Name:    src.Name,
		Type:    src.Type,
		Config:  keyValueLines(src.Config),
		Secrets: keyValueLines(src.Secrets),
		Editing: editing,
	}
}

func keyValueLines(m map[string]string) string {
	keys := sortedKeys(m)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, m[k])
	}
	return b.String()
}

func (s *Server) loadSources(ctx context.Context) (config.SourcesConfig, error) {
	cfg, err := config.LoadSourcesFile(s.deps.SourcesPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return config.SourcesConfig{}, nil
		}
		return config.SourcesConfig{}, err
	}
	return cfg, nil
}

func (s *Server) integrationRows(ctx context.Context) []integrationSource {
	cfg, err := s.loadSources(ctx)
	if err != nil {
		return nil
	}
	var st *state.Store
	if _, err := os.Stat(s.deps.StatePath); err == nil {
		if opened, err := state.OpenStore(s.deps.StatePath); err == nil {
			st = opened
		}
	}

	now := s.deps.Now()
	var rows []integrationSource
	for _, src := range cfg.Sources {
		row := integrationSource{
			Name:       src.Name,
			Type:       src.Type,
			ConfigKeys: sortedKeys(src.Config),
			EditURL:    "/integrations?edit=" + url.QueryEscape(src.Name),
		}
		for field, envName := range src.Secrets {
			_, present := s.deps.EnvLookup(envName)
			row.Secrets = append(row.Secrets, secretPresence{Name: field + " (" + envName + ")", Present: present})
		}
		sort.Slice(row.Secrets, func(i, j int) bool { return row.Secrets[i].Name < row.Secrets[j].Name })

		if st != nil {
			if st, ok := st.Get(src.Type + ":" + src.Name); ok {
				if !st.LastSyncAt.IsZero() {
					row.LastSync = st.LastSyncAt.Format(time.RFC3339)
					row.Stale = now.Sub(st.LastSyncAt) > s.deps.StaleAfter
				} else {
					row.NeverSynced = true
					row.Stale = true
				}
				row.LastError = st.LastError
			} else {
				row.NeverSynced = true
				row.Stale = true
			}
		} else {
			row.NeverSynced = true
			row.Stale = true
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Type != rows[j].Type {
			return rows[i].Type < rows[j].Type
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (s *Server) handleIntegrationSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderIntegrationFormError(w, r, http.StatusBadRequest, "invalid form: "+err.Error(), integrationFormData{})
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	typ := strings.TrimSpace(r.FormValue("type"))
	configText := r.FormValue("config")
	secretsText := r.FormValue("secrets")
	original := strings.TrimSpace(r.FormValue("original"))
	form := integrationFormData{Name: name, Type: typ, Config: configText, Secrets: secretsText, Editing: original}

	fail := func(status int, msg string) {
		s.renderIntegrationFormError(w, r, status, msg, form)
	}

	if name == "" {
		fail(http.StatusBadRequest, "name is required")
		return
	}
	if typ == "" {
		fail(http.StatusBadRequest, "type is required")
		return
	}
	if !registry.Known(typ) {
		fail(http.StatusBadRequest, "unknown connector type: "+typ)
		return
	}
	cfgMap, err := parseKeyValueLines(configText)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	secretMap, err := parseKeyValueLines(secretsText)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}

	instance := config.SourceInstance{Name: name, Type: typ, Config: cfgMap, Secrets: secretMap}

	cfg, err := s.loadSources(r.Context())
	if err != nil {
		fail(http.StatusInternalServerError, "sources config unavailable: "+err.Error())
		return
	}

	if original != "" {
		idx := findSource(cfg.Sources, original)
		if idx < 0 {
			fail(http.StatusNotFound, "source not found: "+original)
			return
		}
		if instance.Name != original {
			fail(http.StatusBadRequest, "renaming a source is not supported; delete it and re-add it under the new name")
			return
		}
		if instance.Type != cfg.Sources[idx].Type {
			fail(http.StatusBadRequest, "changing a source type is not supported; delete it and re-add it with the new type")
			return
		}
		cfg.Sources[idx] = instance
	} else {
		cfg.Sources = append(cfg.Sources, instance)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		fail(http.StatusInternalServerError, "marshal sources failed: "+err.Error())
		return
	}
	if _, err := config.ParseSources(data); err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	if err := s.writeSourcesFile(data); err != nil {
		fail(http.StatusInternalServerError, "writing sources.yaml failed: "+err.Error())
		return
	}

	http.Redirect(w, r, "/integrations", http.StatusSeeOther)
}

func (s *Server) handleIntegrationDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form: "+err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	cfg, err := s.loadSources(r.Context())
	if err != nil {
		http.Error(w, "sources config unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}
	idx := findSource(cfg.Sources, name)
	if idx < 0 {
		http.Error(w, "source not found: "+name, http.StatusNotFound)
		return
	}
	cfg.Sources = append(cfg.Sources[:idx], cfg.Sources[idx+1:]...)

	data, err := yaml.Marshal(cfg)
	if err != nil {
		http.Error(w, "marshal sources failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := config.ParseSources(data); err != nil {
		http.Error(w, "validating sources failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.writeSourcesFile(data); err != nil {
		http.Error(w, "writing sources.yaml failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	related := s.relatedDocumentCount(name)
	http.Redirect(w, r, "/integrations?deleted="+url.QueryEscape(name)+"&related="+strconv.Itoa(related), http.StatusSeeOther)
}

func (s *Server) relatedDocumentCount(source string) int {
	entries, err := s.scanDocuments()
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Source == source {
			n++
		}
	}
	return n
}

func (s *Server) renderIntegrationFormError(w http.ResponseWriter, r *http.Request, status int, msg string, form integrationFormData) {
	data := s.integrationView(r.Context())
	data.Form = form
	s.render(w, "page-integrations", status, page{
		Title:  "Integrations",
		Alerts: []Alert{{Kind: "error", Message: msg}},
		Data:   data,
	})
}

func findSource(sources []config.SourceInstance, name string) int {
	for i := range sources {
		if sources[i].Name == name {
			return i
		}
	}
	return -1
}

func parseKeyValueLines(text string) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid line %q: expected key=value", line)
		}
		out[key] = strings.TrimSpace(val)
	}
	return out, nil
}

func (s *Server) writeSourcesFile(data []byte) error {
	return sink.WriteFileAtomic(s.deps.SourcesPath, data, 0o644)
}
