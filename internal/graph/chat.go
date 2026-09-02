package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

// KindChatMessage is the document kind produced by chat connectors
// (telegram/slack/mattermost); the indexer carries it into chunk metadata,
// and the graph pipeline routes such chunks to the ChatExtractor.
const KindChatMessage = "message"

const chatUserEntityType = "chat-user"

// Chat decision edge types: the only relation types the chat extraction
// path emits. They are stored uppercase regardless of the LLM's casing.
const (
	EdgeDecided  = "DECIDED"
	EdgeProposed = "PROPOSED"
	EdgeAgreed   = "AGREED"
)

var chatEdgeTypes = map[string]struct{}{
	EdgeDecided:  {},
	EdgeProposed: {},
	EdgeAgreed:   {},
}

const defaultMinChatLength = 20

const chatDecisionSystemPrompt = `You extract decisions from a chat thread chunk (Slack/Telegram/Mattermost conversation). ` +
	`Ignore small talk, greetings, and single-word acknowledgments. ` +
	`Respond with JSON: {"entities":[{"name":"","type":"","description":""}],"relations":[{"source":"","target":"","type":"","description":""}]}. ` +
	`Entity types: "topic" (a subject, technology, task, or decision object), "person" (a named participant). ` +
	`Relation types: "DECIDED" (a decision was made), "PROPOSED" (a proposal was made), "AGREED" (participants agreed on something). ` +
	`Each relation's "source" is the person who proposed/decided/agreed and "target" is the topic entity it concerns. ` +
	`Relations must NOT include valid_from/valid_to — the message timestamp is attached by the system. ` +
	`"source"/"target" must reference entity names listed in "entities". No prose, no markdown fences.`

const chatSmallTalkSystemPrompt = `You classify whether a chat message is small talk: greetings, single-word acknowledgments, emoji, off-topic filler. ` +
	`Work-relevant proposals, decisions, questions, and technical statements are NOT small talk. ` +
	`Respond with JSON: {"is_smalltalk": true|false}. No prose, no markdown fences.`

// ChatExtractor builds a thread-scope mini-graph from ChatChunker chunks
// (see internal/engine/chunk). The deterministic phase attributes each
// chunk to its speaker (frontmatter "user"), filters small talk, and stamps
// decision edges with the message timestamp (frontmatter "ts"); the LLM
// phase extracts topic entities and DECIDED/PROPOSED/AGREED edges. Same
// fail-open contract as Extractor: on transport error, empty input or
// unparseable response the deterministic contribution survives and no error
// is returned.
type ChatExtractor struct {
	Chat      ChatClient
	Model     string
	Classify  bool
	MinLength int
}

func NewChatExtractor(chat ChatClient, model string) *ChatExtractor {
	return &ChatExtractor{Chat: chat, Model: model}
}

// ExtractThread builds the thread-scope mini-graph for one document's chat
// chunks (a glued thread or a single message): one chat-user entity per
// distinct speaker, LLM topic entities, and decision edges from speaker to
// topic with ValidFrom = the message timestamp. Chunks filtered as small
// talk contribute nothing. Always returns a nil error (fail-open).
func (e *ChatExtractor) ExtractThread(ctx context.Context, chunks []vector.Chunk) ([]graphstore.Entity, []graphstore.Relation, error) {
	if e == nil {
		return nil, nil, nil
	}

	type contribution struct {
		chunkID  string
		entities []RawEntity
		rels     []RawRelation
	}
	var contribs []contribution

	for _, c := range chunks {
		if e.isSmallTalk(c.Text) {
			continue
		}
		if e.Classify && e.Chat != nil {
			small, err := e.classifySmallTalk(ctx, c.Text)
			if err == nil && small {
				// LLM verdict wins; on transport/parse error keep the
				// chunk (fail-open).
				continue
			}
		}

		users, userTS := speakerInfo(c.Metadata)
		ents := make([]RawEntity, 0, len(users))
		for _, u := range users {
			ents = append(ents, RawEntity{Name: u, Type: chatUserEntityType, Description: "Участник чата"})
		}

		extraction, err := e.extractDecisions(ctx, c.Text)
		if err != nil {
			// Fail-open: the deterministic speaker contribution still
			// merges into the graph.
			extraction = Extraction{}
		}
		ents = append(ents, extraction.Entities...)
		chunkTS := chatTimestamp(c.Metadata["ts"])
		var rels []RawRelation
		for _, r := range extraction.Relations {
			typ := strings.ToUpper(strings.TrimSpace(r.Type))
			if _, ok := chatEdgeTypes[typ]; !ok {
				continue
			}
			src := strings.TrimSpace(r.Source)
			if len(users) > 1 {
				// A glued thread chunk names several speakers: trust the
				// LLM's attribution only when it names a real participant,
				// otherwise fall back to the chunk-level speaker (the
				// thread's first author).
				if !containsUser(users, src) {
					src = users[0]
				}
			} else {
				// Single-speaker chunk: the chunk-level speaker is the
				// author of every message, so the LLM's source is ignored.
				src = users[0]
			}
			if normalizeName(r.Target) == normalizeName(src) {
				continue
			}
			ts := userTS[normalizeName(src)]
			if ts == nil {
				ts = chunkTS
			}
			rels = append(rels, RawRelation{
				Source:          src,
				Target:          r.Target,
				Type:            typ,
				Description:     r.Description,
				ValidFrom:       ts,
				NoConflictClose: true,
			})
		}
		contribs = append(contribs, contribution{chunkID: c.ID, entities: ents, rels: rels})
	}

	var rawEnts []RawEntity
	entChunks := map[string]map[string]struct{}{}
	for _, co := range contribs {
		for _, re := range co.entities {
			id := EntityID(re.Name, re.Type)
			rawEnts = append(rawEnts, re)
			if entChunks[id] == nil {
				entChunks[id] = map[string]struct{}{}
			}
			entChunks[id][co.chunkID] = struct{}{}
		}
	}

	entities, nameToID := BuildEntities(rawEnts)
	for i := range entities {
		entities[i].SourceChunks = setToSlice(entChunks[entities[i].ID])
	}

	var rawRels []RawRelation
	relChunks := map[string]map[string]struct{}{}
	for _, co := range contribs {
		for _, rr := range co.rels {
			rawRels = append(rawRels, rr)
			id := relationKeyForChunks(nameToID, rr)
			if id == "" {
				continue
			}
			if relChunks[id] == nil {
				relChunks[id] = map[string]struct{}{}
			}
			relChunks[id][co.chunkID] = struct{}{}
		}
	}

	relations := BuildRelations(nameToID, rawRels)
	for i := range relations {
		relations[i].SourceChunks = setToSlice(relChunks[relations[i].ID])
	}
	return entities, relations, nil
}

// isSmallTalk applies the deterministic heuristics: empty or punctuation/
// emoji-only text, a short acknowledgment word, or text below MinLength.
func (e *ChatExtractor) isSmallTalk(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || smallTalkOnlyPunctRe.MatchString(trimmed) {
		return true
	}
	if _, ok := smallTalkExact[strings.ToLower(trimmed)]; ok {
		return true
	}
	minLen := e.MinLength
	if minLen <= 0 {
		minLen = defaultMinChatLength
	}
	return len([]rune(trimmed)) < minLen
}

var smallTalkOnlyPunctRe = regexp.MustCompile(`^[\p{P}\p{S}\p{Z}]*$`)

var smallTalkExact = map[string]struct{}{
	"ok": {}, "ок": {}, "спасибо": {}, "спс": {}, "благодарю": {},
	"thanks": {}, "thx": {}, "ty": {}, "lol": {}, "+1": {},
	"👍": {}, "👌": {}, "✅": {}, "🙏": {},
}

// classifySmallTalk asks the LLM whether the chunk is small talk. Transport
// errors and unparseable responses are returned as errors so the caller can
// fail open (keep the chunk).
func (e *ChatExtractor) classifySmallTalk(ctx context.Context, text string) (bool, error) {
	resp, err := e.Chat.Chat(ctx, llm.ChatRequest{
		Model: e.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: chatSmallTalkSystemPrompt},
			{Role: "user", Content: text, Untrusted: true},
		},
	})
	if err != nil {
		return false, err
	}
	var verdict struct {
		IsSmallTalk bool `json:"is_smalltalk"`
	}
	if err := json.Unmarshal([]byte(stripCodeFence(resp.Content)), &verdict); err != nil {
		return false, fmt.Errorf("chat: small-talk verdict: %w", err)
	}
	return verdict.IsSmallTalk, nil
}

// extractDecisions runs the chat decision prompt. Fail-open: any transport
// error or unparseable response yields a zero Extraction and a nil error.
func (e *ChatExtractor) extractDecisions(ctx context.Context, text string) (Extraction, error) {
	if e == nil || e.Chat == nil || strings.TrimSpace(text) == "" {
		return Extraction{}, nil
	}
	resp, err := e.Chat.Chat(ctx, llm.ChatRequest{
		Model: e.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: chatDecisionSystemPrompt},
			{Role: "user", Content: text, Untrusted: true},
		},
	})
	if err != nil {
		return Extraction{}, nil
	}
	result, ok := parseExtraction(resp.Content)
	if !ok {
		return Extraction{}, nil
	}
	return result, nil
}

// chatTimestamp parses the message timestamp stored in chat chunk metadata
// ("ts" frontmatter): RFC3339, a date, or unix seconds/milliseconds (slack
// and telegram use seconds, mattermost milliseconds). Returns nil when
// unparseable (fail-open — the edge is then not time-stamped).
func chatTimestamp(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t
		}
	}

	var f float64
	if strings.Contains(raw, ".") {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil
		}
		f = v
	} else {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil
		}
		f = float64(n)
	}
	if f > 1e11 {
		t := time.UnixMilli(int64(f)).UTC()
		return &t
	}
	t := time.Unix(int64(f), 0).UTC()
	return &t
}

func setToSlice(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// speakerInfo returns the distinct speakers of a chunk in message order and
// each speaker's first message timestamp. Multi-speaker chunks (a glued
// thread) carry a JSON "speakers" metadata list written by ChatChunker;
// single-speaker chunks fall back to the chunk-level "user"/"ts" keys.
func speakerInfo(meta map[string]string) ([]string, map[string]*time.Time) {
	if raw := strings.TrimSpace(meta["speakers"]); raw != "" {
		var entries []struct {
			User string `json:"user"`
			TS   string `json:"ts"`
		}
		if err := json.Unmarshal([]byte(raw), &entries); err == nil && len(entries) > 0 {
			users := make([]string, 0, len(entries))
			ts := make(map[string]*time.Time, len(entries))
			for _, e := range entries {
				u := strings.TrimSpace(e.User)
				if u == "" {
					continue
				}
				users = append(users, u)
				if t := chatTimestamp(e.TS); t != nil {
					ts[normalizeName(u)] = t
				}
			}
			if len(users) > 0 {
				return users, ts
			}
		}
	}
	user := strings.TrimSpace(meta["user"])
	if user == "" {
		user = "unknown"
	}
	return []string{user}, nil
}

func containsUser(users []string, name string) bool {
	want := normalizeName(name)
	for _, u := range users {
		if normalizeName(u) == want {
			return true
		}
	}
	return false
}

// relationKeyForChunks mirrors BuildRelations' ID computation so the
// per-chunk attribution pass and the built relations stay keyed alike.
// Relations BuildRelations will drop (unknown or self-loop endpoints) get
// no chunk attribution and are simply absent from the final lookup.
func relationKeyForChunks(nameToID map[string]string, rr RawRelation) string {
	srcID, ok1 := nameToID[normalizeName(rr.Source)]
	dstID, ok2 := nameToID[normalizeName(rr.Target)]
	if !ok1 || !ok2 || srcID == dstID {
		return ""
	}
	return RelationID(srcID, dstID, rr.Type)
}
