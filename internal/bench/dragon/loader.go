package dragon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	DefaultBaseURL       = "https://datasets-server.huggingface.co"
	TextsDataset         = "ai-forever/rag-bench-public-texts"
	QuestionsDataset     = "ai-forever/rag-bench-public-questions"
	HistTextsDataset     = "ai-forever/hist-rag-bench-public-texts"
	HistQuestionsDataset = "ai-forever/hist-rag-bench-public-questions"
	HistGoldDataset      = "ai-forever/hist-rag-bench-private-qa"
)

type Text struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

type Question struct {
	ID       int    `json:"id"`
	Question string `json:"question"`
}

type GoldQA struct {
	ID       int    `json:"id"`
	PublicID int    `json:"public_id"`
	TextIDs  string `json:"text_ids"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Type     string `json:"type"`
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type rowsResponse struct {
	Rows []struct {
		RowIdx int             `json:"row_idx"`
		Row    json.RawMessage `json:"row"`
	} `json:"rows"`
	NumRowsTotal int `json:"num_rows_total"`
}

func FetchTexts(ctx context.Context, doer HTTPDoer, baseURL, dataset string) ([]Text, error) {
	var out []Text
	err := fetchRows(ctx, doer, baseURL, dataset, func(raw json.RawMessage) error {
		var t Text
		if err := json.Unmarshal(raw, &t); err != nil {
			return err
		}
		out = append(out, t)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func FetchQuestions(ctx context.Context, doer HTTPDoer, baseURL, dataset string) ([]Question, error) {
	var out []Question
	err := fetchRows(ctx, doer, baseURL, dataset, func(raw json.RawMessage) error {
		var q Question
		if err := json.Unmarshal(raw, &q); err != nil {
			return err
		}
		out = append(out, q)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func FetchGoldQA(ctx context.Context, doer HTTPDoer, baseURL, dataset string) ([]GoldQA, error) {
	var out []GoldQA
	err := fetchRows(ctx, doer, baseURL, dataset, func(raw json.RawMessage) error {
		var g GoldQA
		if err := json.Unmarshal(raw, &g); err != nil {
			return err
		}
		out = append(out, g)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func fetchRows(ctx context.Context, doer HTTPDoer, baseURL, dataset string, handle func(json.RawMessage) error) error {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	offset := 0
	total := -1
	for total < 0 || offset < total {
		body, status, err := getRowsPage(ctx, doer, baseURL, dataset, offset)
		if err != nil {
			return fmt.Errorf("dragon: fetch %s offset %d: %w", dataset, offset, err)
		}
		if status != http.StatusOK {
			return fmt.Errorf("dragon: fetch %s offset %d: status %d", dataset, offset, status)
		}
		var parsed rowsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return fmt.Errorf("dragon: parse %s offset %d: %w", dataset, offset, err)
		}
		if len(parsed.Rows) == 0 {
			break
		}
		for _, r := range parsed.Rows {
			if err := handle(r.Row); err != nil {
				return fmt.Errorf("dragon: decode %s row %d: %w", dataset, r.RowIdx, err)
			}
		}
		total = parsed.NumRowsTotal
		offset += len(parsed.Rows)
	}
	return nil
}

func getRowsPage(ctx context.Context, doer HTTPDoer, baseURL, dataset string, offset int) ([]byte, int, error) {
	u := fmt.Sprintf("%s/rows?dataset=%s&config=default&split=train&offset=%d&length=100", baseURL, dataset, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := doer.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return body, resp.StatusCode, nil
}
