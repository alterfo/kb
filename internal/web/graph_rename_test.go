package web

import (
	"context"
	"net/http"
	"testing"
)

func TestGraph_EntityRenameRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()

	rr := postJSON(t, te.server.Handler(), "/graph/entities", map[string]any{
		"name": "Alpha", "type": "org",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	entities, err := te.graph.AllEntities(ctx)
	if err != nil || len(entities) != 1 {
		t.Fatalf("entities after create = %+v, %v", entities, err)
	}

	rr = postJSON(t, te.server.Handler(), "/graph/entities", map[string]any{
		"id": entities[0].ID, "name": "Renamed", "type": "org",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("rename status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	entities, err = te.graph.AllEntities(ctx)
	if err != nil || len(entities) != 1 {
		t.Fatalf("entities after rejected rename = %+v, %v", entities, err)
	}
	if entities[0].Name != "Alpha" {
		t.Fatalf("entity name = %q, want unchanged Alpha", entities[0].Name)
	}
}
