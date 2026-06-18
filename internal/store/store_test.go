package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mheers/talk-to-your-github-stars/internal/db"
)

func TestSaveMetadataAndReadme(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	repo := &db.Repo{
		ID:           42,
		NodeID:       "MDEw",
		Owner:        "testorg",
		Name:         "testrepo",
		FullName:     "testorg/testrepo",
		Description:  "a test repo",
		Homepage:     "https://example.com",
		URL:          "https://github.com/testorg/testrepo",
		Language:     "Go",
		Stars:        100,
		Watchers:     50,
		Forks:        10,
		OpenIssues:   3,
		OpenPRs:      1,
		Commits:      25,
		Contributors: 2,
		Topics:       []string{"go", "test"},
		Languages:    map[string]int{"Go": 1234},
		License:      "MIT",
		IsFork:       false,
		IsArchived:   false,
		IsPrivate:    false,
		CreatedAt:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		ReadmeText:   "should not appear in metadata",
	}

	if err := s.SaveMetadata(repo); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	metaPath := filepath.Join(dir, "repos", "testorg", "testrepo", "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read metadata.json: %v", err)
	}

	var got db.Repo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if got.FullName != repo.FullName {
		t.Fatalf("expected full_name %q, got %q", repo.FullName, got.FullName)
	}
	if got.ReadmeText != "" {
		t.Fatalf("metadata should not contain readme_text, got %q", got.ReadmeText)
	}
	if got.Stars != repo.Stars {
		t.Fatalf("expected stars %d, got %d", repo.Stars, got.Stars)
	}

	readme := "# Hello\n\nThis is the README.\n"
	if err := s.SaveReadme(repo.Owner, repo.Name, readme); err != nil {
		t.Fatalf("SaveReadme: %v", err)
	}

	readmePath := filepath.Join(dir, "repos", "testorg", "testrepo", "readme.md")
	gotReadme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read readme.md: %v", err)
	}
	if string(gotReadme) != readme {
		t.Fatalf("expected readme %q, got %q", readme, string(gotReadme))
	}
}
