package registry

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

var testNameSeq atomic.Int64

func testConnectorName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, testNameSeq.Add(1))
}

type fakeConnector struct{ typ string }

func (f *fakeConnector) Type() string { return f.typ }
func (f *fakeConnector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	return nil
}
func (f *fakeConnector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	return since, connector.FetchInfo{}, nil
}

func TestRegisterAndNew(t *testing.T) {
	typ := testConnectorName("regtest-a")
	Register(typ, func() connector.Connector { return &fakeConnector{typ: typ} })

	c, err := New(typ)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Type() != typ {
		t.Fatalf("Type() = %q, want %q", c.Type(), typ)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	typ := testConnectorName("regtest-b")
	Register(typ, func() connector.Connector { return &fakeConnector{typ: typ} })

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	Register(typ, func() connector.Connector { return &fakeConnector{typ: typ} })
}

func TestNewUnknownType(t *testing.T) {
	_, err := New("regtest-does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown connector type")
	}
}

func TestTypesSorted(t *testing.T) {
	typX := testConnectorName("regtest-x")
	typZ := testConnectorName("regtest-z")
	Register(typX, func() connector.Connector { return &fakeConnector{typ: typX} })
	Register(typZ, func() connector.Connector { return &fakeConnector{typ: typZ} })

	types := Types()
	foundX, foundZ := -1, -1
	for i, ty := range types {
		if ty == typX {
			foundX = i
		}
		if ty == typZ {
			foundZ = i
		}
	}
	if foundX == -1 || foundZ == -1 {
		t.Fatalf("expected %q and %q in %v", typX, typZ, types)
	}
	if foundX > foundZ {
		t.Fatalf("expected sorted order, got %v", types)
	}
}
