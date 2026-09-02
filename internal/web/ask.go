package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/sink"
	"github.com/alterfo/kb/internal/state"
	"github.com/alterfo/kb/internal/store/history"
	"github.com/alterfo/kb/internal/verify"
)

type askRun struct {
	id      string
	query   string
	created time.Time
	done    bool
	g       got.ThoughtGraph
	subs    map[chan got.ThoughtGraph]bool
}

// maxAskRuns bounds how many finished runs the manager keeps around for
// status/approve polling; older finished runs are evicted on new starts.
const maxAskRuns = 64

// maxConcurrentAsks bounds how many GoT orchestrations can run at once; a
// burst of asks would otherwise pile up unbounded LLM/retrieval work.
const maxConcurrentAsks = 4

type askManager struct {
	mu    sync.Mutex
	runs  map[string]*askRun
	spawn func(func())
}

func newAskManager(spawn func(func())) *askManager {
	if spawn == nil {
		spawn = func(f func()) { go f() }
	}
	return &askManager{runs: map[string]*askRun{}, spawn: spawn}
}

func (m *askManager) start(query string, run func(id string) got.ThoughtGraph) string {
	id := randID()
	m.mu.Lock()
	m.runs[id] = &askRun{id: id, query: query, created: time.Now(), subs: map[chan got.ThoughtGraph]bool{}}
	m.evictFinishedLocked()
	m.mu.Unlock()
	m.spawn(func() {
		m.finish(id, run(id))
	})
	return id
}

// evictFinishedLocked drops the oldest finished runs once the map exceeds
// maxAskRuns, so long-running servers don't accumulate every ask forever.
func (m *askManager) evictFinishedLocked() {
	if len(m.runs) <= maxAskRuns {
		return
	}
	var finished []*askRun
	for _, r := range m.runs {
		if r.done {
			finished = append(finished, r)
		}
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].created.Before(finished[j].created) })
	for _, r := range finished {
		delete(m.runs, r.id)
		if len(m.runs) <= maxAskRuns {
			return
		}
	}
}

func (m *askManager) progress(id string, g got.ThoughtGraph) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runs[id]
	if r == nil || r.done {
		return
	}
	r.g = g
	m.broadcastLocked(r, g)
}

func (m *askManager) finish(id string, g got.ThoughtGraph) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runs[id]
	if r == nil {
		return
	}
	r.g = g
	r.done = true
	m.broadcastLocked(r, g)
}

func (m *askManager) broadcastLocked(r *askRun, g got.ThoughtGraph) {
	for ch := range r.subs {
		select {
		case ch <- g:
		default:
		}
	}
}

func (m *askManager) get(id string) (got.ThoughtGraph, bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return got.ThoughtGraph{}, false, false
	}
	return r.g, r.done, true
}

// subscribe registers a channel for run id and reports whether the run had
// already finished under the same lock, closing the check-then-subscribe
// race where a completion between get and subscribe would leave the
// subscriber waiting forever.
func (m *askManager) subscribe(id string) (<-chan got.ThoughtGraph, bool, func()) {
	ch := make(chan got.ThoughtGraph, 16)
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		close(ch)
		return ch, true, func() {}
	}
	r.subs[ch] = true
	return ch, r.done, func() {
		m.mu.Lock()
		delete(r.subs, ch)
		m.mu.Unlock()
	}
}

func randID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (s *Server) startAsk(query string) (string, bool) {
	select {
	case s.asksSem <- struct{}{}:
	default:
		return "", false
	}
	s.refreshBM25(s.baseCtx)
	createdAt := s.deps.Now()
	id := s.asks.start(query, func(id string) got.ThoughtGraph {
		defer func() { <-s.asksSem }()
		s.persistAskRun(id, query, history.AskRunStatusRunning, got.ThoughtGraph{Query: query}, createdAt, nil)
		orch := got.New(got.Config{
			Retriever:             retriever.Adapter{Retriever: s.retriever},
			Chat:                  s.deps.Chat,
			Model:                 s.deps.LLMModel,
			ContradictionDetector: verify.NewContradictionDetector(s.deps.Chat, s.deps.LLMModel),
			DetectContradictions:  s.deps.DetectContradictions,
			ExtractQualifiers:     s.deps.QualifierFilter,
			AbstainThreshold:      s.deps.AbstainThreshold,
			RollingMemory:         s.deps.RollingMemory,
			AskCache:              s.deps.AskCache,
			Progress: func(g got.ThoughtGraph) {
				s.asks.progress(id, g)
				s.persistAskRun(id, query, history.AskRunStatusRunning, g, createdAt, nil)
			},
		})
		g := orch.Run(s.baseCtx, query)
		finished := s.deps.Now()
		s.persistAskRun(id, query, history.AskRunStatusDone, g, createdAt, &finished)
		return g
	})
	return id, true
}

// persistAskRun is fail-open: the in-memory askManager is the source of
// truth for a live run, this table only backs the history page and
// post-restart recovery, so a write failure must never affect the ask
// itself.
func (s *Server) persistAskRun(id, query, status string, g got.ThoughtGraph, createdAt time.Time, finishedAt *time.Time) {
	if s.deps.History == nil {
		return
	}
	graphJSON, err := json.Marshal(g)
	if err != nil {
		return
	}
	_ = s.deps.History.SaveAskRun(s.baseCtx, history.AskRunEntry{
		ID:         id,
		Query:      query,
		Status:     status,
		GraphJSON:  string(graphJSON),
		CreatedAt:  createdAt,
		FinishedAt: finishedAt,
	})
}

type askData struct {
	Query     string
	RunID     string
	Status    string
	GraphJSON template.JS
}

// handleAskPage resolves the run's current state from the live askManager
// first (the source of truth while the process is up), falling back to the
// persisted history store for a run started before a restart — without the
// fallback, navigating back to a run's URL after `kb serve` restarted would
// silently show an empty page instead of the run's last known state.
func (s *Server) handleAskPage(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run")
	query := r.URL.Query().Get("q")
	data := askData{Query: query, RunID: runID, GraphJSON: "null"}
	if runID != "" {
		if g, done, exists := s.asks.get(runID); exists {
			data.Query = g.Query
			data.Status = history.AskRunStatusRunning
			if done {
				data.Status = history.AskRunStatusDone
			}
			data.GraphJSON = graphToJS(g)
		} else if s.deps.History != nil {
			if e, ok, err := s.deps.History.AskRun(r.Context(), runID); err == nil && ok {
				data.Query = e.Query
				data.Status = e.Status
				data.GraphJSON = template.JS(e.GraphJSON)
			}
		}
	}
	s.render(w, "page-ask", http.StatusOK, page{
		Title: "Ask",
		Data:  data,
	})
}

// graphToJS marshals g for safe embedding in a <script> block; a marshal
// failure (which cannot happen for this JSON-serializable type in practice)
// degrades to "null" rather than breaking the page.
func graphToJS(g got.ThoughtGraph) template.JS {
	b, err := json.Marshal(g)
	if err != nil {
		return "null"
	}
	return template.JS(b)
}

type askHistoryEntryView struct {
	ID         string
	Query      string
	Status     string
	CreatedAt  string
	FinishedAt string
}

type askHistoryData struct {
	Runs []askHistoryEntryView
}

const askHistoryLimit = 100

func (s *Server) handleAskHistory(w http.ResponseWriter, r *http.Request) {
	var views []askHistoryEntryView
	if s.deps.History != nil {
		if entries, err := s.deps.History.AskRuns(r.Context(), askHistoryLimit); err == nil {
			views = make([]askHistoryEntryView, 0, len(entries))
			for _, e := range entries {
				v := askHistoryEntryView{ID: e.ID, Query: e.Query, Status: e.Status, CreatedAt: e.CreatedAt.Format(time.RFC3339)}
				if e.FinishedAt != nil {
					v.FinishedAt = e.FinishedAt.Format(time.RFC3339)
				}
				views = append(views, v)
			}
		}
	}
	s.render(w, "page-ask-history", http.StatusOK, page{
		Title: "Ask History",
		Data:  askHistoryData{Runs: views},
	})
}

func (s *Server) handleAskStart(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("q"))
	if q == "" {
		s.render(w, "page-ask", http.StatusBadRequest, page{
			Title:  "Ask",
			Alerts: []Alert{{Kind: "error", Message: "question is required"}},
			Data:   askData{Query: q},
		})
		return
	}
	id, ok := s.startAsk(q)
	if !ok {
		s.render(w, "page-ask", http.StatusTooManyRequests, page{
			Title:  "Ask",
			Alerts: []Alert{{Kind: "error", Message: "too many concurrent asks, retry in a moment"}},
			Data:   askData{Query: q},
		})
		return
	}
	http.Redirect(w, r, "/ask?run="+id, http.StatusFound)
}

func (s *Server) handleAskStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	g, done, ok := s.asks.get(id)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown run: " + id})
		return
	}
	writeJSON(w, http.StatusOK, askStatusView{ID: id, Done: done, Graph: g, ContractVersion: webResponseContractVersion})
}

type askStatusView struct {
	ID              string           `json:"id"`
	Done            bool             `json:"done"`
	Graph           got.ThoughtGraph `json:"graph"`
	ContractVersion int              `json:"contract_version"`
}

// webResponseContractVersion tracks JSON response shape changes for the
// dashboard. Version 2 adds metrics and degraded observability fields to
// the ask status response (and to the graph payload embedded in it).
const webResponseContractVersion = 2

func (s *Server) handleAskEvents(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	g, done, ok := s.asks.get(id)
	if !ok {
		http.Error(w, "unknown run: "+id, http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if done {
		writeSSE(w, "done", g)
		flusher.Flush()
		return
	}

	ch, subDone, unsubscribe := s.asks.subscribe(id)
	defer unsubscribe()
	if subDone {
		if latest, _, ok := s.asks.get(id); ok {
			g = latest
		} else if s.deps.History != nil {
			if e, found, err := s.deps.History.AskRun(r.Context(), id); err == nil && found {
				var latest got.ThoughtGraph
				if json.Unmarshal([]byte(e.GraphJSON), &latest) == nil {
					g = latest
				}
			}
		}
		writeSSE(w, "done", g)
		flusher.Flush()
		return
	}
	// The final done snapshot is delivered through the subscriber channel,
	// but a slow client can drop it when the non-blocking broadcast finds
	// the buffer full; re-polling here guarantees the stream always
	// terminates with a done event.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			g, isDone, _ := s.asks.get(id)
			if isDone {
				// The run finished between ticks: flush any snapshots
				// still queued as progress (the done snapshot is always
				// broadcast last), then terminate with the authoritative
				// done event so a slow client doesn't miss the last
				// steps before the stream closes.
				drainAskProgress(w, flusher, ch)
				writeSSE(w, "done", g)
				flusher.Flush()
				return
			}
		case snapshot := <-ch:
			// The snapshot is a progress event by construction: finish()
			// broadcasts the done snapshot last, so a read from the
			// subscriber channel can never be the terminal snapshot while
			// a run is still broadcasting.
			writeSSE(w, "progress", snapshot)
			flusher.Flush()
			if g, isDone, _ := s.asks.get(id); isDone {
				drainAskProgress(w, flusher, ch)
				writeSSE(w, "done", g)
				flusher.Flush()
				return
			}
		}
	}
}

// drainAskProgress flushes snapshots queued before the final done
// broadcast as progress events. finish() always broadcasts the done
// snapshot last, so anything still buffered here is a progress snapshot
// (or, at worst, the done snapshot itself, which the caller re-emits as
// the authoritative done event right after).
func drainAskProgress(w io.Writer, flusher http.Flusher, ch <-chan got.ThoughtGraph) {
	for {
		select {
		case snapshot := <-ch:
			writeSSE(w, "progress", snapshot)
			flusher.Flush()
		default:
			return
		}
	}
}

func writeSSE(w io.Writer, event string, g got.ThoughtGraph) {
	data, err := json.Marshal(g)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleAskApprove(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	title := strings.TrimSpace(r.FormValue("title"))
	g, done, ok := s.asks.get(id)
	if !ok {
		s.render(w, "page-ask", http.StatusBadRequest, page{
			Title:  "Ask",
			Alerts: []Alert{{Kind: "error", Message: "unknown run: " + id}},
			Data:   askData{Query: g.Query},
		})
		return
	}
	if !done {
		s.render(w, "page-ask", http.StatusBadRequest, page{
			Title:  "Ask",
			Alerts: []Alert{{Kind: "error", Message: "run " + id + " is still in progress"}},
			Data:   askData{Query: g.Query, RunID: id},
		})
		return
	}
	answer := strings.TrimSpace(g.FinalAnswer)
	if answer == "" {
		s.render(w, "page-ask", http.StatusBadRequest, page{
			Title:  "Ask",
			Alerts: []Alert{{Kind: "error", Message: "run " + id + " produced no answer"}},
			Data:   askData{Query: g.Query, RunID: id},
		})
		return
	}
	if title == "" {
		title = g.Query
	}
	slug := sanitizeID(truncate(g.Query, 60))
	if len([]rune(g.Query)) > 60 {
		sum := sha256.Sum256([]byte(g.Query))
		slug += "-" + hex.EncodeToString(sum[:4])
	}
	relPath, docID := uniqueApprovedPath(s.deps.Root, slug)
	body := answer + "\n\n## Sources\n\n" + sourceList(g.Sources)
	doc := connector.Document{
		ID:        docID,
		Source:    "notes/approved",
		Title:     title,
		Body:      body,
		UpdatedAt: s.deps.Now(),
	}
	data, err := render.Render(doc)
	if err != nil {
		s.render(w, "page-ask", http.StatusOK, page{
			Title:  "Ask",
			Alerts: []Alert{{Kind: "error", Message: "rendering approved note failed: " + err.Error()}},
			Data:   askData{Query: g.Query, RunID: id},
		})
		return
	}
	if err := sink.WritePath(s.deps.Root, relPath, data); err != nil {
		s.render(w, "page-ask", http.StatusOK, page{
			Title:  "Ask",
			Alerts: []Alert{{Kind: "error", Message: "saving approved note failed: " + err.Error()}},
			Data:   askData{Query: g.Query, RunID: id},
		})
		return
	}
	if s.deps.Indexer != nil {
		if err := s.deps.Indexer.AddOrUpdateDocument(r.Context(), relPath); err != nil {
			s.render(w, "page-ask", http.StatusOK, page{
				Title:  "Ask",
				Alerts: []Alert{{Kind: "error", Message: "note saved but indexing failed: " + err.Error()}},
				Data:   askData{Query: g.Query, RunID: id},
			})
			return
		}
	}
	s.refreshBM25(r.Context())
	http.Redirect(w, r, "/documents/view?path="+relPath, http.StatusFound)
}

// uniqueApprovedPath picks a notes/approved/<slug>.md path that does not
// exist yet, so re-approving the same query never overwrites an earlier
// note. It returns the final root-relative path and the document id
// (filename stem) derived from slug.
func uniqueApprovedPath(root, slug string) (string, string) {
	for i := 1; ; i++ {
		name := slug
		if i > 1 {
			name = fmt.Sprintf("%s-%d", slug, i)
		}
		rel := "notes/approved/" + name + ".md"
		full, err := resolveWithin(root, rel)
		if err != nil {
			return rel, name
		}
		if _, err := os.Stat(full); os.IsNotExist(err) {
			return rel, name
		}
	}
}

func sourceList(sources []got.Source) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, src := range sources {
		if src.FilePath == "" || seen[src.FilePath] {
			continue
		}
		seen[src.FilePath] = true
		fmt.Fprintf(&b, "- %s\n", src.FilePath)
	}
	return b.String()
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func (s *Server) handleAskPromote(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	g, done, ok := s.asks.get(id)
	fail := func(msg string) {
		s.render(w, "page-ask", http.StatusOK, page{
			Title:  "Ask",
			Alerts: []Alert{{Kind: "error", Message: msg}},
			Data:   askData{Query: g.Query, RunID: id},
		})
	}
	if !ok {
		fail("unknown run: " + id)
		return
	}
	if !done {
		fail("run " + id + " is still in progress")
		return
	}
	if s.deps.Governance == nil {
		fail("governance is not configured")
		return
	}
	paths := citedPaths(g.Sources)
	if len(paths) == 0 {
		fail("run " + id + " cited no sources to promote")
		return
	}
	plan := s.deps.Governance.ProposeRetirement(paths, "")
	ts, err := state.OpenTombstoneStore(filepath.Join(s.deps.PersistDir, ".tombstones.json"))
	if err != nil {
		fail("opening tombstones failed: " + err.Error())
		return
	}
	results := s.deps.Governance.ApplyRetirement(r.Context(), ts, plan.Notes, plan.Upstream)
	okCount := 0
	for _, res := range results {
		if res.OK {
			okCount++
		}
	}
	s.render(w, "page-ask", http.StatusOK, page{
		Title:  "Ask",
		Alerts: []Alert{{Kind: "success", Message: fmt.Sprintf("promoted %d of %d cited source(s)", okCount, len(results))}},
		Data:   askData{Query: g.Query, RunID: id},
	})
}

func citedPaths(sources []got.Source) []string {
	seen := map[string]bool{}
	var out []string
	for _, src := range sources {
		if src.FilePath == "" || seen[src.FilePath] {
			continue
		}
		seen[src.FilePath] = true
		out = append(out, src.FilePath)
	}
	return out
}
