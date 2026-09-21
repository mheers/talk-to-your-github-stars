package mcpserver_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mheers/talk-to-your-github-stars/internal/config"
	"github.com/mheers/talk-to-your-github-stars/internal/db"
	"github.com/mheers/talk-to-your-github-stars/internal/embed"
	"github.com/mheers/talk-to-your-github-stars/internal/judge"
	"github.com/mheers/talk-to-your-github-stars/internal/mcpserver"
)

func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "test.db"), 4)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func seedRepos(t *testing.T, d *db.DB) {
	t.Helper()
	now := time.Now().UTC()
	repos := []*db.Repo{
		{
			ID:          101,
			NodeID:      "n1",
			Owner:       "foo",
			Name:        "bar",
			FullName:    "foo/bar",
			Description: "A small CLI for working with csv files",
			URL:         "https://github.com/foo/bar",
			Language:    "Go",
			Stars:       1234,
			Forks:       42,
			Topics:      []string{"cli", "csv"},
			License:     "MIT",
			ReadmeText:  "# foo/bar\nA small CLI for working with csv files.\n\n## Install\ngo install",
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          102,
			NodeID:      "n2",
			Owner:       "acme",
			Name:        "widgets",
			FullName:    "acme/widgets",
			Description: "Widget library for building dashboards",
			URL:         "https://github.com/acme/widgets",
			Language:    "TypeScript",
			Stars:       5000,
			Forks:       300,
			Topics:      []string{"widgets", "ui", "dashboards"},
			License:     "Apache-2.0",
			ReadmeText:  "# acme/widgets\nA widget library for building dashboards.",
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          103,
			NodeID:      "n3",
			Owner:       "rusty",
			Name:        "ferrite",
			FullName:    "rusty/ferrite",
			Description: "Embedded database written in Rust",
			URL:         "https://github.com/rusty/ferrite",
			Language:    "Rust",
			Stars:       9,
			Forks:       1,
			Topics:      []string{"database", "embedded"},
			License:     "MIT",
			ReadmeText:  "# rusty/ferrite\nEmbedded database.",
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          104,
			NodeID:      "n4",
			Owner:       "long",
			Name:        "readme",
			FullName:    "long/readme",
			Description: "A repository with a very long README",
			URL:         "https://github.com/long/readme",
			Language:    "Go",
			Stars:       1,
			CreatedAt:   now,
			UpdatedAt:   now,
			ReadmeText:  strings.Repeat("word ", 6000), // 30000 chars
		},
	}
	for _, r := range repos {
		if err := d.UpsertRepo(r); err != nil {
			t.Fatalf("upsert %s: %v", r.FullName, err)
		}
	}
}

// startTestSession spins up a real mcp.Client connected to the in-process
// server and returns the session. emb may be nil (vector_search disabled).
func startTestSession(t *testing.T, database *db.DB, emb *embed.Client) *mcp.ClientSession {
	t.Helper()
	return startSessionWith(t, database, emb, nil)
}

// startSessionWith is startTestSession with an optional judge.
func startSessionWith(t *testing.T, database *db.DB, emb *embed.Client, reranker judge.Reranker) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	server := mcpserver.New(database, emb).WithReranker(reranker).MCPServer()

	st, ct := mcp.NewInMemoryTransports()

	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// mockEmbedder returns an embed.Client backed by a fake OpenAI-compatible
// embeddings endpoint. fn maps a text to its embedding vector.
func mockEmbedder(t *testing.T, dim int, fn func(text string) []float32) *embed.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		type item struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		resp := struct {
			Object string `json:"object"`
			Data   []item `json:"data"`
			Model  string `json:"model"`
		}{Object: "list", Model: req.Model}
		for i, text := range req.Input {
			resp.Data = append(resp.Data, item{Object: "embedding", Index: i, Embedding: fn(text)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	return embed.New(&config.Config{
		OpenAIKey:      "test-key",
		OpenAIBaseURL:  srv.URL,
		EmbeddingModel: "mock-embed",
		EmbeddingDim:   dim,
	})
}

// structured decodes the structuredContent of a tool result into T.
func structured[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	if res == nil {
		t.Fatal("nil tool result")
	}
	if res.IsError {
		t.Fatalf("tool error: %v", toolText(res))
	}
	if res.StructuredContent == nil {
		t.Fatalf("no structured content: %v", toolText(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return v
}

func toolText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

const (
	defaultMaxReadmeChars = mcpserver.DefaultMaxReadmeChars
	readmeTruncatedMarker = mcpserver.ReadmeTruncatedMarker
)

// repoOut mirrors the get_repo output shape.
type repoOut struct {
	Found bool `json:"found"`
	Repo  *struct {
		FullName  string `json:"full_name"`
		HasReadme bool   `json:"has_readme"`
	} `json:"repo,omitempty"`
	Readme string `json:"readme,omitempty"`
	Error  string `json:"error,omitempty"`
}

// listOut mirrors the list_repos output shape.
type listOut struct {
	Count int `json:"count"`
	Total int `json:"total"`
	Repos []struct {
		FullName string `json:"full_name"`
	} `json:"repos"`
}

// vectorOut mirrors the vector_search output shape.
type vectorOut struct {
	Query      string   `json:"query"`
	K          int      `json:"k"`
	Reranked   bool     `json:"reranked"`
	Answerable *float64 `json:"answerable"`
	Hits       []struct {
		ChunkID      int64    `json:"chunk_id"`
		RepoFullName string   `json:"repo_full_name"`
		Text         string   `json:"text"`
		Score        float64  `json:"score"`
		Relevance    *float64 `json:"relevance"`
	} `json:"hits"`
}

// testReranker is a canned judge for MCP tests.
type testReranker struct {
	ranking judge.Ranking
	err     error
}

func (r *testReranker) Rank(context.Context, string, []judge.Candidate) (judge.Ranking, error) {
	return r.ranking, r.err
}

func (r *testReranker) MinRelevance() float64 { return 0.5 }

func TestListRepos_AllAndFiltered(t *testing.T) {
	d := newTestDB(t)
	seedRepos(t, d)
	cs := startTestSession(t, d, nil)

	t.Run("no filter", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "list_repos",
			Arguments: map[string]any{
				"limit": 10,
			},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if res.IsError {
			t.Fatalf("tool error: %+v", res)
		}
		if len(res.Content) == 0 {
			t.Fatal("no content")
		}
		tc, ok := res.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("unexpected content type: %T", res.Content[0])
		}
		if !contains(tc.Text, `"acme/widgets"`) || !contains(tc.Text, `"foo/bar"`) {
			t.Fatalf("missing expected repos: %s", tc.Text)
		}
	})

	t.Run("filter on 'csv' matches foo/bar", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "list_repos",
			Arguments: map[string]any{"query": "csv", "limit": 10},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if res.IsError {
			t.Fatalf("tool error: %+v", res)
		}
		tc := res.Content[0].(*mcp.TextContent)
		if !contains(tc.Text, `"foo/bar"`) {
			t.Fatalf("expected foo/bar in result: %s", tc.Text)
		}
		if contains(tc.Text, `"acme/widgets"`) {
			t.Fatalf("did not expect acme/widgets in result: %s", tc.Text)
		}
	})

	t.Run("limit caps result size", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "list_repos",
			Arguments: map[string]any{"limit": 1},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		tc := res.Content[0].(*mcp.TextContent)
		if !contains(tc.Text, `"count":1`) {
			t.Fatalf("expected count=1, got: %s", tc.Text)
		}
	})

	t.Run("LIKE wildcards are matched literally", func(t *testing.T) {
		for _, q := range []string{"%", "_"} {
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "list_repos",
				Arguments: map[string]any{"query": q, "limit": 10},
			})
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			out := structured[listOut](t, res)
			if out.Total != 0 {
				t.Fatalf("query %q matched %d repos; expected literal matching", q, out.Total)
			}
		}
	})
}

func TestGetRepo(t *testing.T) {
	d := newTestDB(t)
	seedRepos(t, d)
	cs := startTestSession(t, d, nil)

	t.Run("found without readme", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{"full_name": "foo/bar"},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if res.IsError {
			t.Fatalf("tool error: %+v", res)
		}
		tc := res.Content[0].(*mcp.TextContent)
		if !contains(tc.Text, `"found":true`) {
			t.Fatalf("expected found=true: %s", tc.Text)
		}
		if contains(tc.Text, `"readme":`) {
			t.Fatalf("did not request readme but got one: %s", tc.Text)
		}
		if !contains(tc.Text, `"has_readme":true`) {
			t.Fatalf("expected has_readme=true: %s", tc.Text)
		}
	})

	t.Run("found with readme", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{"full_name": "foo/bar", "include_readme": true},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		tc := res.Content[0].(*mcp.TextContent)
		if !contains(tc.Text, `"# foo/bar`) {
			t.Fatalf("expected README in result: %s", tc.Text)
		}
	})

	t.Run("case-insensitive match", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{"full_name": "FOO/Bar"},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		tc := res.Content[0].(*mcp.TextContent)
		if !contains(tc.Text, `"found":true`) {
			t.Fatalf("expected found=true for case-insensitive match: %s", tc.Text)
		}
	})

	t.Run("not found", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{"full_name": "missing/repo"},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		// Not-found is reported via structured content with found=false.
		if res.IsError {
			t.Fatalf("unexpected transport error: %+v", res)
		}
		tc := res.Content[0].(*mcp.TextContent)
		if !contains(tc.Text, `"found":false`) {
			t.Fatalf("expected found=false: %s", tc.Text)
		}
		if !contains(tc.Text, `not found`) {
			t.Fatalf("expected 'not found' in body: %s", tc.Text)
		}
	})

	t.Run("missing arg", func(t *testing.T) {
		_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{},
		})
		if err == nil {
			t.Fatal("expected error for missing full_name")
		}
	})

	t.Run("substring alone is not a match", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{"full_name": "foo/ba"},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		out := structured[repoOut](t, res)
		if out.Found {
			t.Fatalf("foo/ba must not match foo/bar: %+v", out)
		}
	})

	t.Run("max_readme_chars truncates", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{"full_name": "long/readme", "include_readme": true, "max_readme_chars": 100},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		out := structured[repoOut](t, res)
		wantLen := 100 + len(readmeTruncatedMarker)
		if len(out.Readme) != wantLen {
			t.Fatalf("expected %d chars, got %d", wantLen, len(out.Readme))
		}
		if !strings.HasSuffix(out.Readme, readmeTruncatedMarker) {
			t.Fatalf("expected truncation marker, got tail: %q", out.Readme[len(out.Readme)-20:])
		}
	})

	t.Run("zero max_readme_chars means the default", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{"full_name": "long/readme", "include_readme": true, "max_readme_chars": 0},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		out := structured[repoOut](t, res)
		wantLen := defaultMaxReadmeChars + len(readmeTruncatedMarker)
		if len(out.Readme) != wantLen {
			t.Fatalf("expected the %d-char default, got %d chars", wantLen, len(out.Readme))
		}
	})

	t.Run("omitted max_readme_chars means the default", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{"full_name": "long/readme", "include_readme": true},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		out := structured[repoOut](t, res)
		wantLen := defaultMaxReadmeChars + len(readmeTruncatedMarker)
		if len(out.Readme) != wantLen {
			t.Fatalf("expected the %d-char default, got %d chars", wantLen, len(out.Readme))
		}
	})

	t.Run("negative max_readme_chars is a tool error, not a crash", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{"full_name": "long/readme", "include_readme": true, "max_readme_chars": -1},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if !res.IsError {
			t.Fatalf("expected a tool error for a negative max_readme_chars: %+v", res)
		}
		if !contains(toolText(res), "must not be negative") {
			t.Fatalf("expected a clear error message, got: %s", toolText(res))
		}

		// The server must still serve requests after the rejected input.
		res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_repo",
			Arguments: map[string]any{"full_name": "foo/bar"},
		})
		if err != nil {
			t.Fatalf("follow-up call: %v", err)
		}
		if res.IsError {
			t.Fatalf("server became unhealthy after a bad request: %+v", res)
		}
	})
}

func TestGetRepoTruncatesOnRuneBoundary(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC()
	repo := &db.Repo{
		ID:         201,
		NodeID:     "rb1",
		Owner:      "utf",
		Name:       "eight",
		FullName:   "utf/eight",
		ReadmeText: strings.Repeat("é", 100), // 2 bytes per rune
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := d.UpsertRepo(repo); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	cs := startTestSession(t, d, nil)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_repo",
		Arguments: map[string]any{"full_name": "utf/eight", "include_readme": true, "max_readme_chars": 11},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	out := structured[repoOut](t, res)
	if !utf8.ValidString(out.Readme) {
		t.Fatalf("truncated README is not valid UTF-8: %q", out.Readme)
	}
	if !strings.HasSuffix(out.Readme, readmeTruncatedMarker) {
		t.Fatalf("expected truncation marker, got: %q", out.Readme)
	}
	if len(out.Readme) > 11+len(readmeTruncatedMarker) {
		t.Fatalf("expected at most %d bytes, got %d", 11+len(readmeTruncatedMarker), len(out.Readme))
	}
}

func TestVectorSearch_Reranked(t *testing.T) {
	d := newTestDB(t)
	seedRepos(t, d)

	chunks := []struct {
		repoID int64
		text   string
		vec    []float32
	}{
		{101, "foo/bar chunk", []float32{1, 0, 0}},
		{102, "acme/widgets chunk", []float32{0, 1, 0}},
		{103, "rusty/ferrite chunk", []float32{0.5, 0.5, 0}},
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

	emb := mockEmbedder(t, 3, func(string) []float32 { return []float32{1, 0, 0} })
	reranker := &testReranker{ranking: judge.Ranking{
		Answerable: 0.88,
		Items: []judge.RankedCandidate{
			{ID: "foo/bar", Relevance: 0.9, Confidence: 0.9},
			{ID: "acme/widgets", Relevance: 0.2, Confidence: 0.6},
			{ID: "rusty/ferrite", Relevance: 0.7, Confidence: 0.8},
		},
	}}
	cs := startSessionWith(t, d, emb, reranker)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "vector_search",
		Arguments: map[string]any{"query": "a Go library", "k": 5},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	out := structured[vectorOut](t, res)

	if !out.Reranked {
		t.Fatal("expected reranked=true")
	}
	if out.Answerable == nil || *out.Answerable != 0.88 {
		t.Fatalf("answerable: %v", out.Answerable)
	}
	if len(out.Hits) != 2 {
		t.Fatalf("expected the two hits above the relevance floor, got %d", len(out.Hits))
	}
	if out.Hits[0].RepoFullName != "foo/bar" || out.Hits[1].RepoFullName != "rusty/ferrite" {
		t.Fatalf("expected judged order, got %v, %v", out.Hits[0].RepoFullName, out.Hits[1].RepoFullName)
	}
	if out.Hits[0].Relevance == nil || *out.Hits[0].Relevance != 0.9 {
		t.Fatalf("first hit relevance: %v", out.Hits[0].Relevance)
	}
	for _, hit := range out.Hits {
		if hit.RepoFullName == "acme/widgets" {
			t.Fatal("a candidate below the relevance floor leaked into the results")
		}
	}
}

func TestVectorSearch_NoEmbedderReturnsError(t *testing.T) {
	d := newTestDB(t)
	seedRepos(t, d)
	cs := startTestSession(t, d, nil)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "vector_search",
		Arguments: map[string]any{"query": "anything"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error when no embedder is configured: %+v", res)
	}
	tc, _ := res.Content[0].(*mcp.TextContent)
	if tc == nil || !contains(tc.Text, "vector search is disabled") {
		t.Fatalf("expected friendly error, got: %+v", res)
	}
}

func TestVectorSearch_WithEmbedder(t *testing.T) {
	d := newTestDB(t)
	seedRepos(t, d)

	// foo/bar and rusty/ferrite are identical to the query vector,
	// acme/widgets is orthogonal, and long/readme was embedded with a
	// different model/dimension and must be skipped.
	chunks := []struct {
		repoID int64
		text   string
		vec    []float32
	}{
		{101, "foo/bar chunk", []float32{1, 0, 0}},
		{102, "acme/widgets chunk", []float32{0, 1, 0}},
		{103, "rusty/ferrite chunk", []float32{1, 0, 0}},
		{104, "long/readme chunk", []float32{1, 0}}, // dimension mismatch
	}
	for _, c := range chunks {
		id, err := d.InsertChunk(c.repoID, c.text, "readme")
		if err != nil {
			t.Fatalf("insert chunk for %d: %v", c.repoID, err)
		}
		if err := d.InsertVec(id, c.vec); err != nil {
			t.Fatalf("insert vec for %d: %v", c.repoID, err)
		}
	}

	emb := mockEmbedder(t, 3, func(string) []float32 { return []float32{1, 0, 0} })
	cs := startTestSession(t, d, emb)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "vector_search",
		Arguments: map[string]any{"query": "oauth library", "k": 10},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	out := structured[vectorOut](t, res)
	if len(out.Hits) != 3 {
		t.Fatalf("expected 3 comparable chunks, got %d", len(out.Hits))
	}
	for _, h := range out.Hits {
		if h.RepoFullName == "long/readme" {
			t.Fatalf("dimension-mismatched chunk leaked into the results: %+v", h)
		}
		if h.Score < -1e-6 || h.Score > 1+1e-6 {
			t.Fatalf("score out of [0,1]: %f", h.Score)
		}
	}
	if math.Abs(out.Hits[0].Score-1) > 1e-6 || math.Abs(out.Hits[1].Score-1) > 1e-6 {
		t.Fatalf("expected the two identical vectors to score 1, got %f and %f", out.Hits[0].Score, out.Hits[1].Score)
	}
	if math.Abs(out.Hits[2].Score) > 1e-6 {
		t.Fatalf("expected the orthogonal vector to score 0, got %f", out.Hits[2].Score)
	}

	t.Run("k is clamped", func(t *testing.T) {
		for _, tc := range []struct{ k, want int }{{999, 50}, {-5, 10}} {
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "vector_search",
				Arguments: map[string]any{"query": "oauth library", "k": tc.k},
			})
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			out := structured[vectorOut](t, res)
			if out.K != tc.want {
				t.Fatalf("k=%d was clamped to %d, want %d", tc.k, out.K, tc.want)
			}
		}
	})

	t.Run("empty query is rejected", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "vector_search",
			Arguments: map[string]any{"query": "   "},
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if !res.IsError {
			t.Fatalf("expected an error for an empty query: %+v", res)
		}
	})
}

func TestSplitRepoURI(t *testing.T) {
	// exercise splitRepoURI indirectly through the public resource API
	d := newTestDB(t)
	seedRepos(t, d)
	cs := startTestSession(t, d, nil)

	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{
		URI: "ttygs://repo/acme/widgets",
	})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) == 0 {
		t.Fatal("no contents")
	}
	rc := res.Contents[0]
	if rc.MIMEType != "application/json" {
		t.Fatalf("unexpected mime type: %q", rc.MIMEType)
	}
	if !contains(rc.Text, `"full_name": "acme/widgets"`) {
		t.Fatalf("unexpected body: %s", rc.Text)
	}

	t.Run("unknown repo", func(t *testing.T) {
		if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{
			URI: "ttygs://repo/missing/repo",
		}); err == nil {
			t.Fatal("expected an error for an unknown repo")
		}
	})

	t.Run("malformed URI", func(t *testing.T) {
		if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{
			URI: "ttygs://repo/onlyowner",
		}); err == nil {
			t.Fatal("expected an error for a malformed URI")
		}
	})
}

func TestListTools(t *testing.T) {
	d := newTestDB(t)
	cs := startTestSession(t, d, nil)

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string]bool{
		"list_repos":    false,
		"get_repo":      false,
		"vector_search": false,
	}
	for _, t := range tools.Tools {
		if _, ok := want[t.Name]; ok {
			want[t.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q was not advertised", name)
		}
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
