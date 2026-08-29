package web

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/sink"
	"github.com/alterfo/kb/internal/store/graphstore"

	"gopkg.in/yaml.v3"
)

type docEntry struct {
	Path      string
	Title     string
	Source    string
	Summary   string
	UpdatedAt string
	EditURL   string
	DeleteURL string
}

type documentsData struct {
	Documents []docEntry
	Sources   []string
	Source    string
	Limit     int
	Offset    int
	Total     int
	From      int
	To        int
	HasNext   bool
	HasPrev   bool
	NextURL   string
	PrevURL   string
}

func (s *Server) handleDocuments(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	entries, err := s.scanDocuments()
	if err != nil {
		s.render(w, "page-documents", http.StatusOK, page{
			Title:  "Documents",
			Alerts: []Alert{{Kind: "error", Message: "listing documents failed: " + err.Error()}},
			Data:   documentsData{},
		})
		return
	}

	distinct := map[string]bool{}
	for _, e := range entries {
		if e.Source != "" {
			distinct[e.Source] = true
		}
	}
	var sources []string
	for src := range distinct {
		sources = append(sources, src)
	}
	sort.Strings(sources)

	var filtered []docEntry
	if source == "" {
		filtered = entries
	} else {
		for _, e := range entries {
			if e.Source == source {
				filtered = append(filtered, e)
			}
		}
	}

	limit := parseLimit(r)
	offset := parseOffset(r)
	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	data := documentsData{
		Documents: filtered[offset:end],
		Sources:   sources,
		Source:    source,
		Limit:     limit,
		Offset:    offset,
		Total:     total,
		HasNext:   end < total,
		HasPrev:   offset > 0,
		NextURL:   documentsURL(source, limit, offset+limit),
		PrevURL:   documentsURL(source, limit, max(0, offset-limit)),
	}
	if total > 0 {
		data.From = offset + 1
		data.To = end
	}

	if isHtmx(r) {
		s.render(w, "documents-table", http.StatusOK, page{Title: "Documents", Data: data})
		return
	}
	s.render(w, "page-documents", http.StatusOK, page{
		Title: "Documents",
		Data:  data,
	})
}

func (s *Server) scanDocuments() ([]docEntry, error) {
	var entries []docEntry
	err := filepath.WalkDir(s.deps.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != s.deps.Root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(s.deps.Root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		doc := docEntry{
			Path:      rel,
			EditURL:   "/documents/edit?path=" + url.QueryEscape(rel),
			DeleteURL: "/documents?path=" + url.QueryEscape(rel),
		}
		if data, err := os.ReadFile(path); err == nil {
			if parsed, err := render.Parse(data); err == nil {
				doc.Title = parsed.Title
				doc.Source = parsed.Source
				doc.Summary = parsed.Summary
				if !parsed.UpdatedAt.IsZero() {
					doc.UpdatedAt = parsed.UpdatedAt.Format(time.RFC3339)
				}
			}
		}
		entries = append(entries, doc)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt != entries[j].UpdatedAt {
			return entries[i].UpdatedAt > entries[j].UpdatedAt
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

const documentsDefaultLimit = 50

func parseLimit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return documentsDefaultLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return documentsDefaultLimit
	}
	if n > 200 {
		return 200
	}
	return n
}

func parseOffset(r *http.Request) int {
	raw := r.URL.Query().Get("offset")
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func documentsURL(source string, limit, offset int) string {
	v := url.Values{}
	if source != "" {
		v.Set("source", source)
	}
	v.Set("limit", strconv.Itoa(limit))
	v.Set("offset", strconv.Itoa(offset))
	return "/documents?" + v.Encode()
}

func isHtmx(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

type renderDoc struct {
	Path      string
	ID        string
	Title     string
	Source    string
	Summary   string
	URL       string
	UpdatedAt string
	EditURL   string
	DeleteURL string
	Body      template.HTML
	Fields    map[string]string
	Relations docRelationsView
}

// docRelationsView is the "document relationships" section on
// /documents/view: graph entities/relations whose source chunks overlap
// this document's chunks. Indexed distinguishes "not indexed yet" (no
// chunks found for this document) from "indexed, but the graph found no
// entities in it" — the two look identical as an empty Entities slice
// otherwise, and a user needs to tell them apart.
type docRelationsView struct {
	Indexed   bool
	Entities  []docRelatedEntity
	Relations []docRelatedRelation
}

type docRelatedEntity struct {
	ID   string
	Name string
	Type string
	URL  string
}

type docRelatedRelation struct {
	SrcName string
	SrcURL  string
	Type    string
	DstName string
	DstURL  string
}

func (s *Server) readDoc(relPath string) (renderDoc, error) {
	abs, err := resolveWithin(s.deps.Root, relPath)
	if err != nil {
		return renderDoc{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return renderDoc{}, err
	}
	doc, err := render.Parse(data)
	if err != nil {
		return renderDoc{}, fmt.Errorf("parsing %s: %w", relPath, err)
	}
	fields := map[string]string{}
	for k, v := range doc.Frontmatter {
		fields[k] = fmt.Sprint(v)
	}
	return renderDoc{
		Path:      filepath.ToSlash(relPath),
		ID:        doc.ID,
		Title:     doc.Title,
		Source:    doc.Source,
		Summary:   doc.Summary,
		URL:       doc.URL,
		UpdatedAt: doc.UpdatedAt.Format(time.RFC3339),
		EditURL:   "/documents/edit?path=" + url.QueryEscape(filepath.ToSlash(relPath)),
		DeleteURL: "/documents?path=" + url.QueryEscape(filepath.ToSlash(relPath)),
		Body:      renderMarkdown(doc.Body),
		Fields:    fields,
	}, nil
}

// documentRelations resolves the graph entities/relations backed by
// relPath's chunks. Fail-open throughout: a missing Vector/Graph dependency
// or any store error yields a zero-value (Indexed: false) view rather than
// breaking the document page.
func (s *Server) documentRelations(ctx context.Context, relPath string) docRelationsView {
	if s.deps.Vector == nil || s.deps.Graph == nil {
		return docRelationsView{}
	}
	chunks, err := s.deps.Vector.ChunksByDoc(ctx, engine.DocRefID(relPath))
	if err != nil || len(chunks) == 0 {
		return docRelationsView{}
	}
	chunkIDs := make(map[string]struct{}, len(chunks))
	for _, c := range chunks {
		chunkIDs[c.ID] = struct{}{}
	}

	entities, err := s.deps.Graph.AllEntities(ctx)
	if err != nil {
		return docRelationsView{}
	}
	matched := map[string]graphstore.Entity{}
	entityByID := make(map[string]graphstore.Entity, len(entities))
	for _, e := range entities {
		entityByID[e.ID] = e
		if entityTouchesChunks(e, chunkIDs) {
			matched[e.ID] = e
		}
	}

	view := docRelationsView{Indexed: true}
	for _, e := range matched {
		view.Entities = append(view.Entities, docRelatedEntity{
			ID: e.ID, Name: e.Name, Type: e.Type, URL: graphEntityURL(e.ID),
		})
	}
	sort.Slice(view.Entities, func(i, j int) bool { return view.Entities[i].Name < view.Entities[j].Name })

	if len(matched) > 0 {
		if relations, err := s.deps.Graph.AllRelations(ctx); err == nil {
			for _, r := range relations {
				if !relationTouchesChunks(r, chunkIDs) {
					continue
				}
				view.Relations = append(view.Relations, docRelatedRelation{
					SrcName: entityDisplayName(entityByID, r.Src), SrcURL: graphEntityURL(r.Src),
					Type:    r.Type,
					DstName: entityDisplayName(entityByID, r.Dst), DstURL: graphEntityURL(r.Dst),
				})
			}
			sort.Slice(view.Relations, func(i, j int) bool {
				if view.Relations[i].SrcName != view.Relations[j].SrcName {
					return view.Relations[i].SrcName < view.Relations[j].SrcName
				}
				return view.Relations[i].DstName < view.Relations[j].DstName
			})
		}
	}
	return view
}

func entityTouchesChunks(e graphstore.Entity, chunkIDs map[string]struct{}) bool {
	for _, cid := range e.SourceChunks {
		if _, ok := chunkIDs[cid]; ok {
			return true
		}
	}
	return false
}

func relationTouchesChunks(r graphstore.Relation, chunkIDs map[string]struct{}) bool {
	for _, cid := range r.SourceChunks {
		if _, ok := chunkIDs[cid]; ok {
			return true
		}
	}
	return false
}

func entityDisplayName(byID map[string]graphstore.Entity, id string) string {
	if e, ok := byID[id]; ok {
		return e.Name
	}
	return id
}

func (s *Server) handleDocumentView(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		s.render(w, "page-document", http.StatusBadRequest, page{
			Title:  "Document",
			Alerts: []Alert{{Kind: "error", Message: "missing path parameter"}},
		})
		return
	}
	doc, err := s.readDoc(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.render(w, "page-document", http.StatusNotFound, page{
				Title:  "Document",
				Alerts: []Alert{{Kind: "error", Message: "document not found: " + path}},
			})
			return
		}
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "escapes root") {
			status = http.StatusBadRequest
		}
		s.render(w, "page-document", status, page{
			Title:  "Document",
			Alerts: []Alert{{Kind: "error", Message: err.Error()}},
		})
		return
	}
	doc.Relations = s.documentRelations(r.Context(), path)
	s.render(w, "page-document", http.StatusOK, page{Title: doc.Title, Data: doc})
}

func (s *Server) handleDocumentDelete(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		http.Error(w, "missing path parameter", http.StatusBadRequest)
		return
	}
	if !strings.HasSuffix(path, ".md") {
		http.Error(w, "only markdown documents can be deleted", http.StatusBadRequest)
		return
	}
	abs, err := resolveWithin(s.deps.Root, path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if info, statErr := os.Stat(abs); statErr != nil || info.IsDir() {
		http.Error(w, "document not found: "+path, http.StatusNotFound)
		return
	}
	if s.deps.Governance == nil {
		http.Error(w, "governance is not configured", http.StatusServiceUnavailable)
		return
	}
	results := s.deps.Governance.Apply(r.Context(), []string{"trash:" + path})
	if len(results) == 0 || !results[0].OK {
		detail := "delete failed"
		if len(results) > 0 {
			detail = results[0].Detail
		}
		http.Error(w, detail, http.StatusInternalServerError)
		return
	}
	s.refreshBM25(r.Context())
	if isHtmx(r) {
		w.Header().Set("HX-Redirect", "/documents")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/documents", http.StatusSeeOther)
}

type editData struct {
	Path        string
	Title       string
	Summary     string
	Body        string
	Frontmatter string
}

func (s *Server) handleDocumentEditForm(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		s.render(w, "page-document-edit", http.StatusBadRequest, page{
			Title:  "Edit document",
			Alerts: []Alert{{Kind: "error", Message: "missing path parameter"}},
			Data:   editData{},
		})
		return
	}
	doc, err := s.readDocRaw(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.render(w, "page-document-edit", http.StatusNotFound, page{
				Title:  "Edit document",
				Alerts: []Alert{{Kind: "error", Message: "document not found: " + path}},
				Data:   editData{Path: path},
			})
			return
		}
		s.render(w, "page-document-edit", http.StatusBadRequest, page{
			Title:  "Edit document",
			Alerts: []Alert{{Kind: "error", Message: err.Error()}},
			Data:   editData{Path: path},
		})
		return
	}
	data := editData{
		Path:        filepath.ToSlash(path),
		Title:       doc.Title,
		Summary:     doc.Summary,
		Body:        doc.Body,
		Frontmatter: frontmatterYAML(doc.Frontmatter),
	}
	s.render(w, "page-document-edit", http.StatusOK, page{Title: "Edit " + data.Path, Data: data})
}

func (s *Server) handleDocumentEdit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "page-document-edit", http.StatusBadRequest, page{
			Title:  "Edit document",
			Alerts: []Alert{{Kind: "error", Message: "invalid form: " + err.Error()}},
			Data:   editData{},
		})
		return
	}
	path := strings.TrimSpace(r.FormValue("path"))
	title := strings.TrimSpace(r.FormValue("title"))
	summary := strings.TrimSpace(r.FormValue("summary"))
	body := r.FormValue("body")
	fmText := r.FormValue("frontmatter")
	data := editData{Path: path, Title: title, Summary: summary, Body: body, Frontmatter: fmText}

	fail := func(status int, msg string) {
		s.render(w, "page-document-edit", status, page{
			Title:  "Edit document",
			Alerts: []Alert{{Kind: "error", Message: msg}},
			Data:   data,
		})
	}

	if path == "" {
		fail(http.StatusBadRequest, "path is required")
		return
	}
	if _, err := resolveWithin(s.deps.Root, path); err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body) == "" {
		fail(http.StatusBadRequest, "body is required")
		return
	}
	extra, err := parseFrontmatterYAML(fmText)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	doc, err := s.readDocRaw(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fail(http.StatusNotFound, "document not found: "+path)
			return
		}
		fail(http.StatusBadRequest, err.Error())
		return
	}

	doc.Title = title
	doc.Summary = summary
	doc.Body = body
	doc.Frontmatter = extra
	doc.UpdatedAt = s.deps.Now()

	raw, err := render.Render(doc)
	if err != nil {
		fail(http.StatusInternalServerError, "rendering document failed: "+err.Error())
		return
	}
	if err := sink.WritePath(s.deps.Root, path, raw); err != nil {
		fail(http.StatusInternalServerError, "saving document failed: "+err.Error())
		return
	}
	if s.deps.Indexer != nil {
		if err := s.deps.Indexer.AddOrUpdateDocument(r.Context(), path); err != nil {
			fail(http.StatusInternalServerError, "document saved but indexing failed: "+err.Error())
			return
		}
	}
	s.refreshBM25(r.Context())
	http.Redirect(w, r, "/documents/view?path="+url.QueryEscape(filepath.ToSlash(path)), http.StatusSeeOther)
}

func (s *Server) readDocRaw(relPath string) (connector.Document, error) {
	abs, err := resolveWithin(s.deps.Root, relPath)
	if err != nil {
		return connector.Document{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return connector.Document{}, err
	}
	return render.Parse(data)
}

func frontmatterYAML(fm map[string]any) string {
	if len(fm) == 0 {
		return ""
	}
	data, err := yaml.Marshal(fm)
	if err != nil {
		return ""
	}
	return string(data)
}

func parseFrontmatterYAML(text string) (map[string]any, error) {
	out := map[string]any{}
	if strings.TrimSpace(text) == "" {
		return out, nil
	}
	if err := yaml.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("invalid frontmatter YAML: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

type addData struct {
	Path    string
	Title   string
	Content string
}

func (s *Server) handleAddForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "page-add", http.StatusOK, page{Title: "Add note", Data: addData{}})
}

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	path := r.FormValue("path")
	title := r.FormValue("title")
	content := r.FormValue("content")
	data := addData{Path: path, Title: title, Content: content}

	fail := func(status int, msg string) {
		s.render(w, "page-add", status, page{
			Title:  "Add note",
			Alerts: []Alert{{Kind: "error", Message: msg}},
			Data:   data,
		})
	}

	if strings.TrimSpace(content) == "" {
		fail(http.StatusBadRequest, "content is required")
		return
	}
	if strings.TrimSpace(path) == "" {
		fail(http.StatusBadRequest, "path is required")
		return
	}
	relPath := path
	if !strings.HasSuffix(relPath, ".md") {
		relPath += ".md"
	}
	if _, err := resolveWithin(s.deps.Root, relPath); err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	relPath = filepath.ToSlash(relPath)

	source := engine.InferSource(relPath)
	if source == "" {
		source = "notes"
	}
	id := noteID(relPath, source)
	// A bare filename has no source directory yet; canonicalize it to the
	// source dir the file will actually land in.
	if !strings.Contains(relPath, "/") {
		relPath = source + "/" + id + ".md"
	}
	doc := connector.Document{
		ID:        id,
		Source:    source,
		Title:     title,
		Body:      content,
		UpdatedAt: s.deps.Now(),
	}
	raw, err := render.Render(doc)
	if err != nil {
		fail(http.StatusInternalServerError, "rendering note failed: "+err.Error())
		return
	}
	if err := sink.WritePath(s.deps.Root, relPath, raw); err != nil {
		fail(http.StatusInternalServerError, "saving note failed: "+err.Error())
		return
	}
	if s.deps.Indexer != nil {
		if err := s.deps.Indexer.AddOrUpdateDocument(r.Context(), relPath); err != nil {
			fail(http.StatusInternalServerError, "note saved but indexing failed: "+err.Error())
			return
		}
	}
	s.refreshBM25(r.Context())
	http.Redirect(w, r, "/documents/view?path="+url.QueryEscape(relPath), http.StatusFound)
}

// noteID derives the frontmatter id from a validated relative note path:
// the path minus its .md extension and the leading source directory
// (notes/approved/foo.md -> approved/foo). Keeping the sub-path in the id
// makes nested notes distinct, so notes/name.md is never overwritten by
// notes/sub/name.md.
func noteID(relPath, source string) string {
	id := strings.TrimSuffix(relPath, ".md")
	if source != "" && strings.HasPrefix(id, source+"/") {
		id = strings.TrimPrefix(id, source+"/")
	}
	return id
}

func sanitizeID(id string) string {
	var b strings.Builder
	replaced := false
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
			replaced = true
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	// Must match internal/sink/filesink.go's sanitizeID: distinct ids whose
	// sanitized forms collapse together would overwrite each other on
	// disk, so a short hash of the raw id keeps colliding ids distinct.
	if replaced {
		sum := sha256.Sum256([]byte(id))
		fmt.Fprintf(&b, "-%x", sum[:4])
	}
	return b.String()
}
