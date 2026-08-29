package importer

import (
	"fmt"
	"sync"

	"github.com/alterfo/kb/internal/connector"
)

type FileImporter interface {
	Ext() string
	Import(path string) ([]connector.Document, error)
}

type Factory func() FileImporter

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

func Register(ext string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := factories[ext]; exists {
		panic(fmt.Sprintf("importer: duplicate extension %q", ext))
	}
	factories[ext] = f
}

func New(ext string) (FileImporter, error) {
	mu.RLock()
	f, ok := factories[ext]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("importer: unsupported extension %q", ext)
	}
	return f(), nil
}
