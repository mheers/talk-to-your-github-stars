// Package store persists repository metadata and READMEs on the local filesystem.
//
// Each starred repository is stored under DataDir/repos/owner/name/ as:
//   - metadata.json: structured repository metadata
//   - readme.md: the repository README in Markdown
//
// This complements the SQLite database with human-readable, version-control-friendly files.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mheers/talk-to-your-github-stars/internal/db"
)

// FileStore writes repository data to the filesystem.
type FileStore struct {
	DataDir string
}

// New creates a FileStore rooted at dataDir.
func New(dataDir string) *FileStore {
	return &FileStore{DataDir: dataDir}
}

// RepoDir returns the directory path for a given repository.
func (s *FileStore) RepoDir(owner, name string) string {
	return filepath.Join(s.DataDir, "repos", owner, name)
}

// SaveMetadata writes the repository metadata to DataDir/repos/owner/name/metadata.json.
func (s *FileStore) SaveMetadata(r *db.Repo) error {
	dir := s.RepoDir(r.Owner, r.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create repo dir %s: %w", dir, err)
	}

	path := filepath.Join(dir, "metadata.json")
	// Store metadata only; the README lives in readme.md.
	meta := *r
	meta.ReadmeText = ""

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata for %s: %w", r.FullName, err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write metadata %s: %w", path, err)
	}
	return nil
}

// SaveReadme writes the repository README to DataDir/repos/owner/name/readme.md.
func (s *FileStore) SaveReadme(owner, name, readme string) error {
	dir := s.RepoDir(owner, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create repo dir %s: %w", dir, err)
	}

	path := filepath.Join(dir, "readme.md")
	if err := os.WriteFile(path, []byte(readme), 0o644); err != nil {
		return fmt.Errorf("write readme %s: %w", path, err)
	}
	return nil
}
