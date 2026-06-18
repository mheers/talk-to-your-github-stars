package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// Repo mirrors a starred GitHub repository in the database.
type Repo struct {
	ID            int64
	NodeID        string
	Owner         string
	Name          string
	FullName      string
	Description   string
	Homepage      string
	URL           string
	Language      string
	Stars         int
	Watchers      int
	Forks         int
	OpenIssues    int
	OpenPRs       int
	Commits       int
	Contributors  int
	Topics        []string
	Languages     map[string]int
	License       string
	IsFork        bool
	IsArchived    bool
	IsPrivate     bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
	PushedAt      *time.Time
	LastCommitAt  *time.Time
	LastReleaseAt *time.Time
	ReadmeText    string
	SyncedAt      time.Time
}

// Chunk is a searchable text fragment from a repository.
type Chunk struct {
	ID        int64
	RepoID    int64
	Text      string
	Source    string
	CreatedAt time.Time
}

// SearchResult represents one vector search hit.
type SearchResult struct {
	ChunkID         int64
	RepoID          int64
	RepoFullName    string
	RepoDescription string
	Text            string
	Distance        float64
}

// DB wraps the SQLite connection.
type DB struct {
	db *sql.DB
}

type rowScanner interface {
	Scan(dest ...any) error
}

// Open opens (and migrates) the SQLite database.
func Open(path string, vecDim int) (*DB, error) {
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	if err := raw.Ping(); err != nil {
		return nil, err
	}

	for _, s := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := raw.Exec(s); err != nil {
			return nil, fmt.Errorf("sqlite pragma %q: %w", s, err)
		}
	}

	d := &DB{db: raw}
	if err := d.migrate(vecDim); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate(vecDim int) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS repos (
			id INTEGER PRIMARY KEY,
			node_id TEXT UNIQUE,
			owner TEXT NOT NULL,
			name TEXT NOT NULL,
			full_name TEXT UNIQUE NOT NULL,
			description TEXT,
			homepage TEXT,
			url TEXT,
			language TEXT,
			stars INTEGER,
			watchers INTEGER,
			forks INTEGER,
			open_issues INTEGER,
			open_prs INTEGER,
			commits INTEGER,
			contributors INTEGER,
			topics TEXT,
			languages TEXT,
			license TEXT,
			is_fork INTEGER,
			is_archived INTEGER,
			is_private INTEGER,
			created_at TEXT,
			updated_at TEXT,
			pushed_at TEXT,
			last_commit_at TEXT,
			last_release_at TEXT,
			readme_text TEXT,
			synced_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id INTEGER PRIMARY KEY,
			repo_id INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
			text TEXT NOT NULL,
			embedding TEXT,
			source TEXT,
			created_at TEXT
		)`,
	}

	for _, s := range stmts {
		if _, err := d.db.Exec(s); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// UpsertRepo inserts a new repo or updates an existing one keyed by full_name.
func (d *DB) UpsertRepo(r *Repo) error {
	topics, _ := json.Marshal(r.Topics)
	langs, _ := json.Marshal(r.Languages)
	now := time.Now().UTC()

	_, err := d.db.Exec(`
		INSERT INTO repos (
			id, node_id, owner, name, full_name, description, homepage, url, language,
			stars, watchers, forks, open_issues, open_prs, commits, contributors,
			topics, languages, license, is_fork, is_archived, is_private,
			created_at, updated_at, pushed_at, last_commit_at, last_release_at, readme_text, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(full_name) DO UPDATE SET
			node_id=excluded.node_id,
			description=excluded.description,
			homepage=excluded.homepage,
			url=excluded.url,
			language=excluded.language,
			stars=excluded.stars,
			watchers=excluded.watchers,
			forks=excluded.forks,
			open_issues=excluded.open_issues,
			open_prs=excluded.open_prs,
			commits=excluded.commits,
			contributors=excluded.contributors,
			topics=excluded.topics,
			languages=excluded.languages,
			license=excluded.license,
			is_fork=excluded.is_fork,
			is_archived=excluded.is_archived,
			is_private=excluded.is_private,
			updated_at=excluded.updated_at,
			pushed_at=excluded.pushed_at,
			last_commit_at=excluded.last_commit_at,
			last_release_at=excluded.last_release_at,
			synced_at=excluded.synced_at
	`, r.ID, r.NodeID, r.Owner, r.Name, r.FullName, r.Description, r.Homepage, r.URL, r.Language,
		r.Stars, r.Watchers, r.Forks, r.OpenIssues, r.OpenPRs, r.Commits, r.Contributors,
		string(topics), string(langs), r.License, boolInt(r.IsFork), boolInt(r.IsArchived), boolInt(r.IsPrivate),
		timeString(r.CreatedAt), timeString(r.UpdatedAt), timePtrString(r.PushedAt),
		timePtrString(r.LastCommitAt), timePtrString(r.LastReleaseAt), r.ReadmeText, timeString(now))

	if err != nil {
		return fmt.Errorf("upsert repo %s: %w", r.FullName, err)
	}
	return nil
}

// UpdateReadme updates the readme text for a repo.
func (d *DB) UpdateReadme(id int64, readme string) error {
	_, err := d.db.Exec(`UPDATE repos SET readme_text=? WHERE id=?`, readme, id)
	return err
}

// RepoCount returns the number of stored repositories.
func (d *DB) RepoCount() (int, error) {
	var n int
	if err := d.db.QueryRow(`SELECT count(*) FROM repos`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ListRepos returns all stored repositories.
func (d *DB) ListRepos() ([]*Repo, error) {
	rows, err := d.db.Query(`
		SELECT id, node_id, owner, name, full_name, description, homepage, url, language,
			stars, watchers, forks, open_issues, open_prs, commits, contributors,
			topics, languages, license, is_fork, is_archived, is_private,
			created_at, updated_at, pushed_at, last_commit_at, last_release_at, readme_text, synced_at
		FROM repos
		ORDER BY full_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []*Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

// GetRepo returns a single repository by its ID.
func (d *DB) GetRepo(id int64) (*Repo, error) {
	row := d.db.QueryRow(`
		SELECT id, node_id, owner, name, full_name, description, homepage, url, language,
			stars, watchers, forks, open_issues, open_prs, commits, contributors,
			topics, languages, license, is_fork, is_archived, is_private,
			created_at, updated_at, pushed_at, last_commit_at, last_release_at, readme_text, synced_at
		FROM repos
		WHERE id = ?
	`, id)
	return scanRepo(row)
}

// SearchRepos returns repositories whose metadata matches the query.
// It searches the full name, description, topics and README text.
func (d *DB) SearchRepos(query string) ([]*Repo, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return d.ListRepos()
	}
	pattern := "%" + q + "%"
	rows, err := d.db.Query(`
		SELECT id, node_id, owner, name, full_name, description, homepage, url, language,
			stars, watchers, forks, open_issues, open_prs, commits, contributors,
			topics, languages, license, is_fork, is_archived, is_private,
			created_at, updated_at, pushed_at, last_commit_at, last_release_at, readme_text, synced_at
		FROM repos
		WHERE full_name LIKE ? OR description LIKE ? OR topics LIKE ? OR readme_text LIKE ?
		ORDER BY stars DESC, full_name
	`, pattern, pattern, pattern, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []*Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

// DeleteChunksForRepo removes all chunks for a repo.
func (d *DB) DeleteChunksForRepo(repoID int64) error {
	_, err := d.db.Exec(`DELETE FROM chunks WHERE repo_id=?`, repoID)
	return err
}

// InsertChunk inserts a text chunk and returns its rowid.
func (d *DB) InsertChunk(repoID int64, text, source string) (int64, error) {
	res, err := d.db.Exec(
		`INSERT INTO chunks(repo_id, text, source, created_at) VALUES (?, ?, ?, ?)`,
		repoID, text, source, timeString(time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertVec stores an embedding for a chunk rowid.
func (d *DB) InsertVec(rowid int64, vec []float32) error {
	data, _ := json.Marshal(vec)
	_, err := d.db.Exec(`UPDATE chunks SET embedding=? WHERE id=?`, string(data), rowid)
	return err
}

// Search performs a cosine-similarity vector search over chunks and returns the top-k results.
func (d *DB) Search(vec []float32, k int) ([]SearchResult, error) {
	rows, err := d.db.Query(`
		SELECT c.id, c.repo_id, r.full_name, r.description, c.text, c.embedding
		FROM chunks c
		JOIN repos r ON r.id = c.repo_id
		WHERE c.embedding IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scored []SearchResult
	for rows.Next() {
		var sr SearchResult
		var embStr string
		if err := rows.Scan(&sr.ChunkID, &sr.RepoID, &sr.RepoFullName, &sr.RepoDescription, &sr.Text, &embStr); err != nil {
			return nil, err
		}
		var emb []float32
		if err := json.Unmarshal([]byte(embStr), &emb); err != nil {
			continue
		}
		// Store distance as 1 - cosine_similarity so lower is closer.
		sr.Distance = 1 - cosine(vec, emb)
		scored = append(scored, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Distance < scored[j].Distance
	})
	if len(scored) > k {
		scored = scored[:k]
	}
	return scored, nil
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return -1
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return -1
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func scanRepo(rows rowScanner) (*Repo, error) {
	r := &Repo{}
	var topicsJSON, langsJSON string
	var created, updated, pushed, lastCommit, lastRelease, synced string
	var ifork, iarch, ipriv int

	err := rows.Scan(
		&r.ID, &r.NodeID, &r.Owner, &r.Name, &r.FullName, &r.Description, &r.Homepage, &r.URL, &r.Language,
		&r.Stars, &r.Watchers, &r.Forks, &r.OpenIssues, &r.OpenPRs, &r.Commits, &r.Contributors,
		&topicsJSON, &langsJSON, &r.License, &ifork, &iarch, &ipriv,
		&created, &updated, &pushed, &lastCommit, &lastRelease, &r.ReadmeText, &synced,
	)
	if err != nil {
		return nil, err
	}

	r.IsFork = ifork == 1
	r.IsArchived = iarch == 1
	r.IsPrivate = ipriv == 1
	r.CreatedAt = parseTime(created)
	r.UpdatedAt = parseTime(updated)
	r.PushedAt = parseTimePtr(pushed)
	r.LastCommitAt = parseTimePtr(lastCommit)
	r.LastReleaseAt = parseTimePtr(lastRelease)
	r.SyncedAt = parseTime(synced)

	_ = json.Unmarshal([]byte(topicsJSON), &r.Topics)
	_ = json.Unmarshal([]byte(langsJSON), &r.Languages)

	return r, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func timeString(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func timePtrString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func parseTimePtr(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
