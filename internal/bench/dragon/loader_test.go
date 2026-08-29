package dragon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchTexts_Paginates(t *testing.T) {
	var gotOffsets []string
	mux := http.NewServeMux()
	mux.HandleFunc("/rows", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("dataset") != TextsDataset {
			t.Errorf("dataset = %q, want %q", r.URL.Query().Get("dataset"), TextsDataset)
		}
		offset := r.URL.Query().Get("offset")
		gotOffsets = append(gotOffsets, offset)
		switch offset {
		case "0":
			fmt.Fprint(w, `{"rows":[{"row_idx":0,"row":{"id":0,"text":"first"}},{"row_idx":1,"row":{"id":1,"text":"second"}}],"num_rows_total":3}`)
		case "2":
			fmt.Fprint(w, `{"rows":[{"row_idx":2,"row":{"id":2,"text":"third"}}],"num_rows_total":3}`)
		default:
			t.Errorf("unexpected offset %q", offset)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	texts, err := FetchTexts(context.Background(), srv.Client(), srv.URL, TextsDataset)
	if err != nil {
		t.Fatalf("FetchTexts: %v", err)
	}
	if len(texts) != 3 {
		t.Fatalf("len(texts) = %d, want 3", len(texts))
	}
	want := []Text{{ID: 0, Text: "first"}, {ID: 1, Text: "second"}, {ID: 2, Text: "third"}}
	for i, w := range want {
		if texts[i] != w {
			t.Errorf("texts[%d] = %+v, want %+v", i, texts[i], w)
		}
	}
	if strings.Join(gotOffsets, ",") != "0,2" {
		t.Errorf("offsets requested = %v, want [0 2]", gotOffsets)
	}
}

func TestFetchTexts_UsesGivenDataset(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rows", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("dataset"); got != HistTextsDataset {
			t.Errorf("dataset = %q, want %q", got, HistTextsDataset)
		}
		fmt.Fprint(w, `{"rows":[{"row_idx":0,"row":{"id":0,"text":"hist"}}],"num_rows_total":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	texts, err := FetchTexts(context.Background(), srv.Client(), srv.URL, HistTextsDataset)
	if err != nil {
		t.Fatalf("FetchTexts: %v", err)
	}
	if len(texts) != 1 || texts[0].Text != "hist" {
		t.Fatalf("texts = %+v", texts)
	}
}

func TestFetchQuestions_SinglePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rows", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("dataset") != QuestionsDataset {
			t.Errorf("dataset = %q, want %q", r.URL.Query().Get("dataset"), QuestionsDataset)
		}
		fmt.Fprint(w, `{"rows":[{"row_idx":0,"row":{"id":0,"question":"Кто?"}}],"num_rows_total":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	questions, err := FetchQuestions(context.Background(), srv.Client(), srv.URL, QuestionsDataset)
	if err != nil {
		t.Fatalf("FetchQuestions: %v", err)
	}
	if len(questions) != 1 || questions[0] != (Question{ID: 0, Question: "Кто?"}) {
		t.Fatalf("questions = %+v", questions)
	}
}

func TestFetchGoldQA_ParsesFields(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rows", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("dataset"); got != HistGoldDataset {
			t.Errorf("dataset = %q, want %q", got, HistGoldDataset)
		}
		fmt.Fprint(w, `{"rows":[{"row_idx":0,"row":{"id":0,"public_id":110,"text_ids":"[144]","question":"В каком городе?","answer":"Новосибирск","type":"simple"}}],"num_rows_total":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gold, err := FetchGoldQA(context.Background(), srv.Client(), srv.URL, HistGoldDataset)
	if err != nil {
		t.Fatalf("FetchGoldQA: %v", err)
	}
	if len(gold) != 1 {
		t.Fatalf("len(gold) = %d, want 1", len(gold))
	}
	want := GoldQA{ID: 0, PublicID: 110, TextIDs: "[144]", Question: "В каком городе?", Answer: "Новосибирск", Type: "simple"}
	if gold[0] != want {
		t.Errorf("gold[0] = %+v, want %+v", gold[0], want)
	}
}

func TestFetchTexts_EmptyRowsStopsPagination(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/rows", func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"rows":[],"num_rows_total":5}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	texts, err := FetchTexts(context.Background(), srv.Client(), srv.URL, TextsDataset)
	if err != nil {
		t.Fatalf("FetchTexts: %v", err)
	}
	if len(texts) != 0 {
		t.Fatalf("len(texts) = %d, want 0", len(texts))
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestFetchTexts_ErrorStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rows", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := FetchTexts(context.Background(), srv.Client(), srv.URL, TextsDataset)
	if err == nil {
		t.Fatal("FetchTexts: expected error, got nil")
	}
}

func TestFetchTexts_InvalidJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rows", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := FetchTexts(context.Background(), srv.Client(), srv.URL, TextsDataset)
	if err == nil {
		t.Fatal("FetchTexts: expected error, got nil")
	}
}

func TestFetchTexts_DefaultBaseURL(t *testing.T) {
	if !strings.HasPrefix(DefaultBaseURL, "https://") {
		t.Fatalf("DefaultBaseURL = %q, want https URL", DefaultBaseURL)
	}
}
