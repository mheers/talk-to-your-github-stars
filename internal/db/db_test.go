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

func TestSearchReposEscapesLikeWildcards(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "wildcards.db"), 3)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	repos := []*Repo{
		{ID: 1, NodeID: "w1", Owner: "acme", Name: "percent", FullName: "acme/percent", Description: "reports 100% cpu usage", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: 2, NodeID: "w2", Owner: "acme", Name: "plain", FullName: "acme/plain", Description: "an ordinary library", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	for _, r := range repos {
		if err := d.UpsertRepo(r); err != nil {
			t.Fatalf("upsert %s: %v", r.FullName, err)
		}
	}

	cases := []struct {
		query string
		want  int
	}{
		{"%", 1},     // literal percent, not "match everything"
		{"100%", 1},  // literal
		{"100_", 0},  // underscore is not a single-character wildcard
		{"_", 0},     // literal underscore only
		{"plain", 1}, // ordinary substring still works
		{"missing", 0},
	}
	for _, tc := range cases {
		got, err := d.SearchRepos(tc.query)
		if err != nil {
			t.Fatalf("search %q: %v", tc.query, err)
		}
		if len(got) != tc.want {
			t.Fatalf("search %q: expected %d results, got %d", tc.query, tc.want, len(got))
		}
	}
}

func TestGetRepoByFullName(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "byfullname.db"), 3)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	repo := &Repo{ID: 1, NodeID: "g1", Owner: "Foo", Name: "Bar", FullName: "Foo/Bar", Description: "x", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := d.UpsertRepo(repo); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := d.GetRepoByFullName("Foo/Bar")
	if err != nil {
		t.Fatalf("exact lookup: %v", err)
	}
	if got == nil || got.FullName != "Foo/Bar" {
		t.Fatalf("exact lookup: got %+v", got)
	}

	got, err = d.GetRepoByFullName("foo/bar")
	if err != nil {
		t.Fatalf("case-insensitive lookup: %v", err)
	}
	if got == nil {
		t.Fatal("case-insensitive lookup: expected a match")
	}

	// A substring of a real full name must not match.
	got, err = d.GetRepoByFullName("Foo/Ba")
	if err != nil {
		t.Fatalf("substring lookup: %v", err)
	}
	if got != nil {
		t.Fatalf("substring lookup: expected nil, got %s", got.FullName)
	}

	got, err = d.GetRepoByFullName("nope/nope")
	if err != nil {
		t.Fatalf("missing lookup: %v", err)
	}
	if got != nil {
		t.Fatalf("missing lookup: expected nil, got %s", got.FullName)
	}
}

func TestSearchSkipsDimensionMismatch(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "dims.db"), 3)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	repo := &Repo{ID: 1, NodeID: "d1", Owner: "foo", Name: "bar", FullName: "foo/bar", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := d.UpsertRepo(repo); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	matching, err := d.InsertChunk(repo.ID, "matching", "readme")
	if err != nil {
		t.Fatalf("insert matching chunk: %v", err)
	}
	if err := d.InsertVec(matching, []float32{1, 0, 0}); err != nil {
		t.Fatalf("insert matching vec: %v", err)
	}

	other, err := d.InsertChunk(repo.ID, "other model", "readme")
	if err != nil {
		t.Fatalf("insert other chunk: %v", err)
	}
	if err := d.InsertVec(other, []float32{1, 0}); err != nil {
		t.Fatalf("insert other vec: %v", err)
	}

	results, err := d.Search([]float32{1, 0, 0}, 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected only the dimension-matching chunk, got %d results", len(results))
	}
	if results[0].ChunkID != matching {
		t.Fatalf("unexpected chunk: %d", results[0].ChunkID)
	}
	if score := 1 - results[0].Distance; score < 0.99 || score > 1.01 {
		t.Fatalf("unexpected score: %f", score)
	}
}
