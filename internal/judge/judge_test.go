package judge

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// systemOneServer stands in for the TypeSafe API. The handler receives the
// decoded request and returns the response body.
func systemOneServer(t *testing.T, handler func(req map[string]any) map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(handler(req))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func scoreAnswer(score float64) map[string]any {
	return map[string]any{
		"type":  "score",
		"score": score,
		"legend": map[string]string{
			"0": "Not related to the need",
			"1": "On a related topic, but does not help with the need",
			"2": "Useful context for someone with the need",
			"3": "Software that directly helps satisfy the need",
		},
		"probabilities": map[string]float64{"3": 1},
		"confidence":    0.9,
	}
}

func TestRankParsesAndNormalizes(t *testing.T) {
	var gotReq map[string]any
	srv := systemOneServer(t, func(req map[string]any) map[string]any {
		gotReq = req
		return map[string]any{
			"model": "jev-1.13.0",
			"answers": map[string]any{
				"answerable":  map[string]any{"type": "noul", "noul": 0.87},
				"candidate_0": scoreAnswer(3),   // -> 1.0
				"candidate_1": scoreAnswer(1.5), // -> 0.5
			},
			"usage": map[string]int{"input_tokens": 10, "output_tokens": 5},
		}
	})

	client, err := New(Options{APIKey: "test-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := client.MinRelevance(), DefaultMinRelevance; got != want {
		t.Fatalf("MinRelevance: got %v, want %v", got, want)
	}

	ranking, err := client.Rank(context.Background(), "build an OAuth server", []Candidate{
		{ID: "foo/bar", Title: "auth lib", Excerpt: "an authentication library"},
		{ID: "acme/list", Title: "links", Excerpt: "a curated list"},
	})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}

	if ranking.Answerable != 0.87 {
		t.Fatalf("Answerable: got %v, want 0.87", ranking.Answerable)
	}
	if len(ranking.Items) != 2 {
		t.Fatalf("items: got %d, want 2", len(ranking.Items))
	}
	if math.Abs(ranking.Items[0].Relevance-1.0) > 1e-9 || ranking.Items[0].ID != "foo/bar" {
		t.Fatalf("item 0: %+v", ranking.Items[0])
	}
	if math.Abs(ranking.Items[1].Relevance-0.5) > 1e-9 || ranking.Items[1].ID != "acme/list" {
		t.Fatalf("item 1: %+v", ranking.Items[1])
	}
	if ranking.Items[0].Confidence != 0.9 {
		t.Fatalf("confidence: got %v", ranking.Items[0].Confidence)
	}

	// The request must carry the need, the candidates, and one question per
	// candidate plus the answerability gate.
	state, _ := gotReq["state"].(map[string]any)
	if state["need"] != "build an OAuth server" {
		t.Fatalf("state.need: %v", state["need"])
	}
	if cands, ok := state["candidates"].([]any); !ok || len(cands) != 2 {
		t.Fatalf("state.candidates: %v", state["candidates"])
	} else {
		first, _ := cands[0].(map[string]any)
		if first["id"] != "foo/bar" || first["excerpt"] != "an authentication library" {
			t.Fatalf("candidate 0: %v", first)
		}
	}
	questions, _ := gotReq["questions"].(map[string]any)
	for _, id := range []string{"answerable", "candidate_0", "candidate_1"} {
		if _, ok := questions[id]; !ok {
			t.Fatalf("missing question %q in %v", id, questions)
		}
	}
	if q, _ := questions["answerable"].(map[string]any); q["type"] != "noul" {
		t.Fatalf("answerable type: %v", q["type"])
	}
	if q, _ := questions["candidate_0"].(map[string]any); q["type"] != "score" {
		t.Fatalf("candidate type: %v", q["type"])
	}
}

func TestRankTopFiltersAndSorts(t *testing.T) {
	ranking := Ranking{
		Items: []RankedCandidate{
			{ID: "a", Relevance: 0.2},
			{ID: "b", Relevance: 0.9},
			{ID: "c", Relevance: 0.5},
			{ID: "d", Relevance: 0.4},
		},
	}
	top := ranking.Top(0.5)
	if len(top) != 2 || top[0].ID != "b" || top[1].ID != "c" {
		t.Fatalf("Top(0.5): %+v", top)
	}
}

func TestRankRejectsBadInput(t *testing.T) {
	client, err := New(Options{APIKey: "test-key", BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := client.Rank(context.Background(), "   ", []Candidate{{ID: "a"}}); err == nil {
		t.Fatal("expected an error for an empty need")
	}

	ranking, err := client.Rank(context.Background(), "need", nil)
	if err != nil {
		t.Fatalf("Rank with no candidates: %v", err)
	}
	if len(ranking.Items) != 0 {
		t.Fatalf("expected an empty ranking, got %+v", ranking)
	}
}

func TestNewWithoutAPIKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected an error without an API key")
	}
}

func TestRankSurfacesAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusBadRequest) // not retried
	}))
	t.Cleanup(srv.Close)

	client, err := New(Options{APIKey: "test-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Rank(context.Background(), "need", []Candidate{{ID: "a", Excerpt: "x"}}); err == nil {
		t.Fatal("expected an error from a failing API")
	}
}
