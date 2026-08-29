package registry

import (
	"fmt"
	"sort"
	"sync"

	"github.com/alterfo/kb/internal/connector"
)

type Factory func() connector.Connector

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

func Register(typ string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := factories[typ]; exists {
		panic(fmt.Sprintf("registry: duplicate connector type %q", typ))
	}
	factories[typ] = f
}

func New(typ string) (connector.Connector, error) {
	mu.RLock()
	f, ok := factories[typ]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("registry: unknown connector type %q", typ)
	}
	return f(), nil
}

func Known(typ string) bool {
	mu.RLock()
	_, ok := factories[typ]
	mu.RUnlock()
	return ok
}

func Types() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(factories))
	for typ := range factories {
		out = append(out, typ)
	}
	sort.Strings(out)
	return out
}
