package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
)

type embedRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
}

type embedData struct {
	Embedding []float32 `json:"embedding"`
}

type embedResponse struct {
	Data []embedData `json:"data"`
}

func (c *Client) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	resp, err := c.postJSON(ctx, "/v1/embeddings", embedRequest{Model: model, Input: texts, EncodingFormat: "float"})
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embed: unexpected status %s: %s", resp.Status, string(b))
	}

	var parsed embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embed: expected %d embeddings, got %d", len(texts), len(parsed.Data))
	}

	out := make([][]float32, len(parsed.Data))
	for i := range parsed.Data {
		normalizeUnit(parsed.Data[i].Embedding)
		out[i] = parsed.Data[i].Embedding
	}
	return out, nil
}

func (c *Client) Dim(ctx context.Context) (int, error) {
	vecs, err := c.Embed(ctx, c.embedModel, []string{"dimension probe"})
	if err != nil {
		return 0, fmt.Errorf("dim probe: %w", err)
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return 0, fmt.Errorf("dim probe: empty embedding returned")
	}
	return len(vecs[0]), nil
}

func normalizeUnit(v []float32) {
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	if sumSq == 0 {
		return
	}
	norm := math.Sqrt(sumSq)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
}
