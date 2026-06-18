package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestOpenAndVectorSearch(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.db"), 3)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	repo := &Repo{
		ID:          123,
		Owner:       "foo",
		Name:        "bar",
		FullName:    "foo/bar",
		Description: "hello",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := d.UpsertRepo(repo); err != nil {
		t.Fatalf("upsert repo: %v", err)
	}

	chunkID, err := d.InsertChunk(repo.ID, "foo bar baz", "readme")
	if err != nil {
		t.Fatalf("insert chunk: %v", err)
	}

	if err := d.InsertVec(chunkID, []float32{1, 2, 3}); err != nil {
		t.Fatalf("insert vec: %v", err)
	}

	results, err := d.Search([]float32{1, 2, 3}, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].RepoFullName != "foo/bar" {
		t.Fatalf("unexpected repo: %s", results[0].RepoFullName)
	}
}

func TestGetRepoAndSearchRepos(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "search.db"), 3)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	repos := []*Repo{
		{ID: 1, NodeID: "MDEw1", Owner: "foo", Name: "bar", FullName: "foo/bar", Description: "a cli tool", Language: "Go", Stars: 42, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: 2, NodeID: "MDEw2", Owner: "baz", Name: "qux", FullName: "baz/qux", Description: "a web framework", Language: "Python", Stars: 10, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: 3, NodeID: "MDEw3", Owner: "acme", Name: "widgets", FullName: "acme/widgets", Description: "widget library", Language: "Go", Stars: 100, CreatedAt: time.Now(), UpdatedAt: time.Now(), ReadmeText: "useful cli helpers"},
	}
	for _, r := range repos {
		if err := d.UpsertRepo(r); err != nil {
			t.Fatalf("upsert repo %s: %v", r.FullName, err)
		}
	}

	got, err := d.GetRepo(2)
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if got.FullName != "baz/qux" {
		t.Fatalf("expected baz/qux, got %s", got.FullName)
	}

	searches := []struct {
		query string
		want  []string
	}{
		{"", []string{"acme/widgets", "baz/qux", "foo/bar"}},
		{"cli", []string{"acme/widgets", "foo/bar"}},
		{"web", []string{"baz/qux"}},
		{"helpers", []string{"acme/widgets"}},
		{"nonexistent", nil},
	}

	for _, tc := range searches {
		results, err := d.SearchRepos(tc.query)
		if err != nil {
			t.Fatalf("search repos %q: %v", tc.query, err)
		}
		if len(results) != len(tc.want) {
			t.Fatalf("search repos %q: expected %d results, got %d", tc.query, len(tc.want), len(results))
		}
		for i, r := range results {
			if r.FullName != tc.want[i] {
				t.Fatalf("search repos %q: result %d expected %s, got %s", tc.query, i, tc.want[i], r.FullName)
			}
		}
	}
}
