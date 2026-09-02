// Package askcache caches completed Ask (Graph-of-Thoughts) responses so a
// repeated question against an unchanged corpus and configuration returns
// the previous answer without paying LLM/retrieval cost again, including
// fail-open placeholder answers.
package askcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/alterfo/kb/internal/engine/got"
)

// CorpusVersioner reports the SQLite store's write-generation counter.
// Satisfied by *sqlite.DB.
type CorpusVersioner interface {
	CorpusVersion(ctx context.Context) (int, error)
}

// Store is the byte-oriented persistence seam for cache entries. It stores
// opaque JSON payloads keyed by a stable cache key, so the adapter above it
// stays free of any concrete storage dependency.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, corpusVersion int, value []byte) error
	Invalidate(ctx context.Context) error
	DeleteStale(ctx context.Context, corpusVersion int) error
}

// Cache adapts a byte Store into a got.AskCache by folding the current
// corpus version and config fingerprint into every cache key.
type Cache struct {
	Store      Store
	Versioner  CorpusVersioner
	ConfigHash string
}

func New(store Store, versioner CorpusVersioner, configHash string) *Cache {
	return &Cache{Store: store, Versioner: versioner, ConfigHash: configHash}
}

// Key derives the stable cache key from a query, the current corpus version
// and the effective-configuration fingerprint. NUL separators avoid
// ambiguity when inputs share prefixes.
func Key(query string, corpusVersion int, configHash string) string {
	h := sha256.New()
	h.Write([]byte(query))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(corpusVersion)))
	h.Write([]byte{0})
	h.Write([]byte(configHash))
	return hex.EncodeToString(h.Sum(nil))
}

var _ got.AskCache = (*Cache)(nil)

// Get implements got.AskCache. Any error reading the version or the store
// is fail-open: it reports a miss rather than blocking the ask.
func (c *Cache) Get(ctx context.Context, query string) (got.ThoughtGraph, bool, error) {
	if c == nil || c.Store == nil || c.Versioner == nil {
		return got.ThoughtGraph{}, false, nil
	}
	version, err := c.Versioner.CorpusVersion(ctx)
	if err != nil {
		return got.ThoughtGraph{}, false, err
	}
	raw, ok, err := c.Store.Get(ctx, Key(query, version, c.ConfigHash))
	if err != nil || !ok {
		return got.ThoughtGraph{}, false, err
	}
	var g got.ThoughtGraph
	if err := json.Unmarshal(raw, &g); err != nil {
		return got.ThoughtGraph{}, false, err
	}
	return g, true, nil
}

// Put implements got.AskCache. It never fails the ask: a marshal, version
// or store error is swallowed and reported through the error return only.
func (c *Cache) Put(ctx context.Context, query string, g got.ThoughtGraph) error {
	if c == nil || c.Store == nil || c.Versioner == nil {
		return nil
	}
	version, err := c.Versioner.CorpusVersion(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return c.Store.Put(ctx, Key(query, version, c.ConfigHash), version, raw)
}

// Invalidate drops every cached entry. It is the explicit invalidation
// hook for configuration or corpus changes that did not go through the
// normal corpus_version bump.
func (c *Cache) Invalidate(ctx context.Context) error {
	if c == nil || c.Store == nil {
		return nil
	}
	return c.Store.Invalidate(ctx)
}

// PruneStale removes entries recorded under any other corpus version, so
// the cache never grows without bound across reindex cycles.
func (c *Cache) PruneStale(ctx context.Context) error {
	if c == nil || c.Store == nil || c.Versioner == nil {
		return nil
	}
	version, err := c.Versioner.CorpusVersion(ctx)
	if err != nil {
		return err
	}
	return c.Store.DeleteStale(ctx, version)
}
