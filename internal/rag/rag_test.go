package rag

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mheers/talk-to-your-github-stars/internal/config"
	"github.com/mheers/talk-to-your-github-stars/internal/db"
	"github.com/mheers/talk-to-your-github-stars/internal/embed"
	"github.com/mheers/talk-to-your-github-stars/internal/judge"
)

// fakeReranker returns a canned ranking and records what it was asked.
type fakeReranker struct {
	ranking  judge.Ranking
	err      error
	min      float64
	gotNeed  string
	gotCands []judge.Candidate
}

func (f *fakeReranker) Rank(_ context.Context, need string, candidates []judge.Candidate) (judge.Ranking, error) {
	f.gotNeed = need
	f.gotCands = candidates
	if f.err != nil {
		return judge.Ranking{}, f.err
	}
	return f.ranking, nil
}

func (f *fakeReranker) MinRelevance() float64 {
	if f.min == 0 {
		return 0.5
	}
	return f.min
}

// mockEmbedder serves an OpenAI-compatible embeddings endpoint that always
// returns the given vector.
func mockEmbedder(t *testing.T, vec []float32) *embed.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"object": "embedding", "index": 0, "embedding": vec},
			},
			"model": "mock",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return embed.New(&config.Config{
		OpenAIKey:      "test",
		OpenAIBaseURL:  srv.URL,
		EmbeddingModel: "mock",
		EmbeddingDim:   len(vec),
	})
}

// seedRetrievalDB inserts three repos where beta/two has two chunks.
func seedRetrievalDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "rag.db"), 3)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	now := time.Now().UTC()
	repos := []*db.Repo{
		{ID: 1, NodeID: "r1", Owner: "alpha", Name: "one", FullName: "alpha/one", Description: "first", CreatedAt: now, UpdatedAt: now},
		{ID: 2, NodeID: "r2", Owner: "beta", Name: "two", FullName: "beta/two", Description: "second", CreatedAt: now, UpdatedAt: now},
		{ID: 3, NodeID: "r3", Owner: "gamma", Name: "three", FullName: "gamma/three", Description: "third", CreatedAt: now, UpdatedAt: now},
	}
	for _, r := range repos {
		if err := d.UpsertRepo(r); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	chunks := []struct {
		repoID int64
		text   string
		vec    []float32
	}{
		{1, "alpha readme", []float32{1, 0, 0}},
		{2, "beta first chunk", []float32{0.99, 0.01, 0}},
		{2, "beta second chunk", []float32{0.9, 0.1, 0}},
		{3, "gamma readme", []float32{0.5, 0.5, 0}},
	}
	for _, c := range chunks {
		id, err := d.InsertChunk(c.repoID, c.text, "readme")
		if err != nil {
			t.Fatalf("insert chunk: %v", err)
		}
		if err := d.InsertVec(id, c.vec); err != nil {
			t.Fatalf("insert vec: %v", err)
		}
	}
	return d
}

func TestRetrieveWithoutReranker(t *testing.T) {
	d := seedRetrievalDB(t)
	emb := mockEmbedder(t, []float32{1, 0, 0})

	retrieval, err := Retrieve(context.Background(), d, emb, nil, "anything", 2)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if retrieval.Reranked {
		t.Fatal("expected plain retrieval")
	}
	if len(retrieval.Results) != 2 {
		t.Fatalf("expected the top 2 chunks, got %d", len(retrieval.Results))
	}
	if retrieval.Results[0].RepoFullName != "alpha/one" || retrieval.Results[1].RepoFullName != "beta/two" {
		t.Fatalf("unexpected order: %+v", retrieval.Results)
	}
}

func TestRetrieveWithReranker(t *testing.T) {
	d := seedRetrievalDB(t)
	emb := mockEmbedder(t, []float32{1, 0, 0})
	reranker := &fakeReranker{
		ranking: judge.Ranking{
			Answerable: 0.77,
			Items: []judge.RankedCandidate{
				{ID: "alpha/one", Relevance: 0.2, Confidence: 0.8},
				{ID: "beta/two", Relevance: 0.4, Confidence: 0.7},
				{ID: "gamma/three", Relevance: 0.9, Confidence: 0.95},
			},
		},
	}

	retrieval, err := Retrieve(context.Background(), d, emb, reranker, "a need", 10)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if !retrieval.Reranked {
		t.Fatal("expected a judged retrieval")
	}
	if retrieval.Answerable != 0.77 {
		t.Fatalf("Answerable: got %v", retrieval.Answerable)
	}
	if len(retrieval.Results) != 1 || retrieval.Results[0].RepoFullName != "gamma/three" {
		t.Fatalf("expected only gamma/three above the floor, got %+v", retrieval.Results)
	}
	if judged := retrieval.Relevance["gamma/three"]; judged.Relevance != 0.9 {
		t.Fatalf("relevance lookup: %+v", judged)
	}

	// The judge must see one candidate per repository (beta/two deduped).
	if reranker.gotNeed != "a need" {
		t.Fatalf("need: %q", reranker.gotNeed)
	}
	if len(reranker.gotCands) != 3 {
		t.Fatalf("expected 3 deduped candidates, got %d", len(reranker.gotCands))
	}
	if reranker.gotCands[1].ID != "beta/two" || reranker.gotCands[1].Excerpt != "beta first chunk" {
		t.Fatalf("dedupe kept the wrong chunk: %+v", reranker.gotCands[1])
	}
}

func TestRetrieveRerankerFailureFallsBack(t *testing.T) {
	d := seedRetrievalDB(t)
	emb := mockEmbedder(t, []float32{1, 0, 0})
	reranker := &fakeReranker{err: errors.New("typesafe is down")}

	retrieval, err := Retrieve(context.Background(), d, emb, reranker, "a need", 2)
	if err != nil {
		t.Fatalf("Retrieve must not fail when the judge fails: %v", err)
	}
	if retrieval.Reranked {
		t.Fatal("expected fallback to plain retrieval")
	}
	if retrieval.RerankErr == nil {
		t.Fatal("expected RerankErr to be set")
	}
	if len(retrieval.Results) != 2 {
		t.Fatalf("expected the deduped shortlist capped at k, got %d", len(retrieval.Results))
	}
}
