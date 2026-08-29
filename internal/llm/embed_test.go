package llm

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEmbed_BatchAndNormalize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "bge-m3" {
			t.Errorf("unexpected model %q", req.Model)
		}
		if len(req.Input) != 2 {
			t.Fatalf("expected 2 inputs, got %d", len(req.Input))
		}
		resp := embedResponse{Data: []embedData{
			{Embedding: []float32{3, 4}},
			{Embedding: []float32{1, 2, 2}},
		}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, DefaultEmbedModel: "bge-m3"})
	vecs, err := c.Embed(context.Background(), "bge-m3", []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(vecs))
	}
	for i, v := range vecs {
		var sumSq float64
		for _, x := range v {
			sumSq += float64(x) * float64(x)
		}
		norm := math.Sqrt(sumSq)
		if math.Abs(norm-1) > 1e-6 {
			t.Errorf("embedding %d not unit-normalized: norm=%v", i, norm)
		}
	}
}

func TestEmbed_EmptyTexts_ReturnsNil(t *testing.T) {
	c := NewClient(Config{BaseURL: "http://unused.invalid"})
	vecs, err := c.Embed(context.Background(), "bge-m3", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if vecs != nil {
		t.Fatalf("expected nil result, got %v", vecs)
	}
}

func TestEmbed_ServerError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := NewClient(Config{
		BaseURL:    srv.URL,
		MaxRetries: 1,
		BaseDelay:  time.Millisecond,
		MaxDelay:   2 * time.Millisecond,
	})
	vecs, err := c.Embed(context.Background(), "bge-m3", []string{"hello"})
	if err == nil {
		t.Fatal("expected error, got nil (batch failure should be surfaced to caller, not silently swallowed)")
	}
	if vecs != nil {
		t.Fatalf("expected nil vectors on error, got %v", vecs)
	}
}

func TestEmbed_MismatchedBatchSize_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(embedResponse{Data: []embedData{{Embedding: []float32{1, 0}}}})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	_, err := c.Embed(context.Background(), "bge-m3", []string{"hello", "world"})
	if err == nil {
		t.Fatal("expected error on embeddings/input count mismatch")
	}
}

func TestEmbed_Non200Status_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad model"}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	_, err := c.Embed(context.Background(), "bad-model", []string{"hello"})
	if err == nil {
		t.Fatal("expected error on non-200 status")
	}
}

func TestDim_ProbesEmbedAndReturnsLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "bge-m3" {
			t.Errorf("Dim should use configured embed model, got %q", req.Model)
		}
		json.NewEncoder(w).Encode(embedResponse{Data: []embedData{{Embedding: []float32{1, 2, 3, 4}}}})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, DefaultEmbedModel: "bge-m3"})
	dim, err := c.Dim(context.Background())
	if err != nil {
		t.Fatalf("Dim: %v", err)
	}
	if dim != 4 {
		t.Fatalf("expected dim 4, got %d", dim)
	}
}

func TestDim_ServerError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(Config{
		BaseURL:           srv.URL,
		DefaultEmbedModel: "bge-m3",
		MaxRetries:        1,
		BaseDelay:         time.Millisecond,
		MaxDelay:          2 * time.Millisecond,
	})
	_, err := c.Dim(context.Background())
	if err == nil {
		t.Fatal("expected error from Dim when embed server fails")
	}
}
