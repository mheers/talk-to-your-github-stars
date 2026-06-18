package github

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/google/go-github/v69/github"
	"github.com/mheers/talk-to-your-github-stars/internal/config"
	"github.com/mheers/talk-to-your-github-stars/internal/db"
	"github.com/mheers/talk-to-your-github-stars/internal/store"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
	"golang.org/x/sync/errgroup"
)

// Client wraps both the GitHub GraphQL (v4) and REST (v3) clients.
type Client struct {
	v4         *githubv4.Client
	v3         *github.Client
	tok        string
	useGraphQL bool
}

// New creates a GitHub client from configuration.
// If useGraphQL is true, starred repositories are fetched via the GraphQL API
// (falling back to REST on failure); otherwise the REST API is used by default.
func New(cfg *config.Config, useGraphQL bool) *Client {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: cfg.GitHubToken})
	httpClient := &http.Client{
		Timeout:   120 * time.Second,
		Transport: &oauth2.Transport{Source: src, Base: newRetryTransport(http.DefaultTransport)},
	}
	return &Client{
		v4:         githubv4.NewClient(httpClient),
		v3:         github.NewClient(httpClient),
		tok:        cfg.GitHubToken,
		useGraphQL: useGraphQL,
	}
}

type gqlRepository struct {
	DatabaseID      int64
	ID              string
	NameWithOwner   string
	Description     string
	URL             string
	HomepageURL     *string
	StargazerCount  int
	Watchers        struct{ TotalCount int }
	ForkCount       int
	Issues          struct{ TotalCount int } `graphql:"issues(states: OPEN)"`
	PullRequests    struct{ TotalCount int } `graphql:"pullRequests(states: OPEN)"`
	PrimaryLanguage *struct{ Name string }
	Languages       struct {
		Edges []struct {
			Node struct{ Name string }
			Size int
		}
	} `graphql:"languages(first: 10)"`
	RepositoryTopics struct {
		Nodes []struct {
			Topic struct{ Name string }
		}
	} `graphql:"repositoryTopics(first: 20)"`
	DefaultBranchRef *struct {
		Target struct {
			Commit struct {
				CommittedDate githubv4.DateTime
				History       struct{ TotalCount int } `graphql:"history(first: 1)"`
			} `graphql:"... on Commit"`
		}
	}
	Releases struct {
		Nodes []struct {
			PublishedAt *githubv4.DateTime
			TagName     string
		}
	} `graphql:"releases(first: 1)"`
	MentionableUsers struct{ TotalCount int }
	LicenseInfo      *struct{ SpdxID string }
	IsFork           bool
	IsArchived       bool
	IsPrivate        bool
	CreatedAt        githubv4.DateTime
	UpdatedAt        githubv4.DateTime
	PushedAt         *githubv4.DateTime
}

// Sync downloads starred repository metadata and READMEs into the database and the filesystem.
func (c *Client) Sync(ctx context.Context, database *db.DB, files *store.FileStore) error {
	log.Println("[sync] fetching starred repositories from GitHub")
	repos, err := c.fetchStarred(ctx)
	if err != nil {
		return fmt.Errorf("fetch starred repos: %w", err)
	}
	log.Printf("[sync] fetched %d starred repositories", len(repos))

	log.Println("[sync] upserting repository metadata")
	for i, r := range repos {
		if err := database.UpsertRepo(r); err != nil {
			return fmt.Errorf("upsert %s: %w", r.FullName, err)
		}
		if files != nil {
			if err := files.SaveMetadata(r); err != nil {
				log.Printf("[sync] save metadata %s: %v", r.FullName, err)
				// non-fatal: continue with database sync
			}
		}
		if (i+1)%100 == 0 || i == len(repos)-1 {
			log.Printf("[sync] upserted %d/%d repositories", i+1, len(repos))
		}
	}

	const workers = 10
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)

	log.Printf("[sync] fetching readmes with %d workers", workers)
	for _, r := range repos {
		r := r
		g.Go(func() error {
			readme, err := c.fetchReadme(ctx, r.Owner, r.Name)
			if err != nil {
				log.Printf("[sync] readme %s: %v", r.FullName, err)
				return nil // non-fatal
			}
			if err := database.UpdateReadme(r.ID, readme); err != nil {
				log.Printf("[sync] update readme %s: %v", r.FullName, err)
				return nil // non-fatal
			}
			if files != nil {
				if err := files.SaveReadme(r.Owner, r.Name, readme); err != nil {
					log.Printf("[sync] save readme %s: %v", r.FullName, err)
					// non-fatal: already stored in the database
				}
			}
			log.Printf("[sync] fetched readme %s", r.FullName)
			return nil
		})
	}
	return g.Wait()
}

func (c *Client) fetchStarred(ctx context.Context) ([]*db.Repo, error) {
	if c.useGraphQL {
		repos, err := c.fetchStarredGraphQL(ctx)
		if err == nil {
			return repos, nil
		}
		log.Printf("[sync] GraphQL starred fetch failed: %v; falling back to REST API", err)
	}
	return c.fetchStarredREST(ctx)
}

func (c *Client) fetchStarredGraphQL(ctx context.Context) ([]*db.Repo, error) {
	var all []*db.Repo
	var cursor *githubv4.String

	for {
		var q struct {
			Viewer struct {
				StarredRepositories struct {
					PageInfo struct {
						HasNextPage bool
						EndCursor   githubv4.String
					}
					Edges []struct {
						Node      gqlRepository
						StarredAt githubv4.DateTime
					}
				} `graphql:"starredRepositories(first: 50, after: $cursor)"`
			}
		}

		vars := map[string]interface{}{"cursor": cursor}
		if err := c.v4.Query(ctx, &q, vars); err != nil {
			return nil, err
		}

		for _, e := range q.Viewer.StarredRepositories.Edges {
			all = append(all, gqlToRepo(e.Node))
		}

		if !q.Viewer.StarredRepositories.PageInfo.HasNextPage {
			break
		}
		cursor = &q.Viewer.StarredRepositories.PageInfo.EndCursor
	}

	return all, nil
}

func (c *Client) fetchStarredREST(ctx context.Context) ([]*db.Repo, error) {
	var all []*db.Repo
	opts := &github.ActivityListStarredOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		starred, resp, err := c.v3.Activity.ListStarred(ctx, "", opts)
		if err != nil {
			return nil, err
		}
		for _, s := range starred {
			if s.Repository == nil {
				continue
			}
			all = append(all, restToRepo(s.Repository))
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return all, nil
}

func restToRepo(r *github.Repository) *db.Repo {
	owner := ""
	if r.Owner != nil {
		owner = r.Owner.GetLogin()
	}
	license := ""
	if r.License != nil {
		license = r.GetLicense().GetSPDXID()
	}
	var pushed *time.Time
	if r.PushedAt != nil {
		pushed = &r.PushedAt.Time
	}
	return &db.Repo{
		ID:          r.GetID(),
		NodeID:      r.GetNodeID(),
		Owner:       owner,
		Name:        r.GetName(),
		FullName:    r.GetFullName(),
		Description: r.GetDescription(),
		Homepage:    r.GetHomepage(),
		URL:         r.GetHTMLURL(),
		Language:    r.GetLanguage(),
		Stars:       r.GetStargazersCount(),
		Watchers:    r.GetWatchersCount(),
		Forks:       r.GetForksCount(),
		OpenIssues:  r.GetOpenIssuesCount(),
		Topics:      r.Topics,
		License:     license,
		IsFork:      r.GetFork(),
		IsArchived:  r.GetArchived(),
		IsPrivate:   r.GetPrivate(),
		CreatedAt:   r.GetCreatedAt().Time,
		UpdatedAt:   r.GetUpdatedAt().Time,
		PushedAt:    pushed,
	}
}

func (c *Client) fetchReadme(ctx context.Context, owner, repo string) (string, error) {
	// GetReadme returns the raw repository README content (base64-encoded in the
	// API response). GetContent decodes it, giving us the Markdown source.
	rc, _, err := c.v3.Repositories.GetReadme(ctx, owner, repo, nil)
	if err != nil {
		return "", err
	}
	content, err := rc.GetContent()
	if err != nil {
		return "", err
	}
	return content, nil
}

func gqlToRepo(g gqlRepository) *db.Repo {
	parts := strings.SplitN(g.NameWithOwner, "/", 2)
	owner, name := parts[0], parts[1]

	langs := make(map[string]int)
	for _, e := range g.Languages.Edges {
		langs[e.Node.Name] = e.Size
	}

	topics := make([]string, 0, len(g.RepositoryTopics.Nodes))
	for _, n := range g.RepositoryTopics.Nodes {
		topics = append(topics, n.Topic.Name)
	}

	var lang string
	if g.PrimaryLanguage != nil {
		lang = g.PrimaryLanguage.Name
	}

	r := &db.Repo{
		ID:           g.DatabaseID,
		NodeID:       g.ID,
		Owner:        owner,
		Name:         name,
		FullName:     g.NameWithOwner,
		Description:  g.Description,
		Homepage:     deref(g.HomepageURL),
		URL:          g.URL,
		Language:     lang,
		Stars:        g.StargazerCount,
		Watchers:     g.Watchers.TotalCount,
		Forks:        g.ForkCount,
		OpenIssues:   g.Issues.TotalCount,
		OpenPRs:      g.PullRequests.TotalCount,
		Contributors: g.MentionableUsers.TotalCount,
		Topics:       topics,
		Languages:    langs,
		IsFork:       g.IsFork,
		IsArchived:   g.IsArchived,
		IsPrivate:    g.IsPrivate,
		CreatedAt:    g.CreatedAt.Time,
		UpdatedAt:    g.UpdatedAt.Time,
	}

	if g.DefaultBranchRef != nil {
		commit := g.DefaultBranchRef.Target.Commit
		r.LastCommitAt = &commit.CommittedDate.Time
		r.Commits = commit.History.TotalCount
	}
	if len(g.Releases.Nodes) > 0 && g.Releases.Nodes[0].PublishedAt != nil {
		r.LastReleaseAt = &g.Releases.Nodes[0].PublishedAt.Time
	}
	if g.LicenseInfo != nil {
		r.License = g.LicenseInfo.SpdxID
	}
	if g.PushedAt != nil {
		r.PushedAt = &g.PushedAt.Time
	}

	return r
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// retryTransport retries HTTP requests on transient server/network errors.
type retryTransport struct {
	base http.RoundTripper
}

func newRetryTransport(base http.RoundTripper) *retryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &retryTransport{base: base}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	const maxAttempts = 5
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	var lastResp *http.Response
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if lastResp != nil {
				lastResp.Body.Close()
				lastResp = nil
			}
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}

		clone := req.Clone(req.Context())
		if req.Body != nil && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			clone.Body = body
		}

		resp, err := t.base.RoundTrip(clone)
		if err != nil {
			if isTemporaryError(err) {
				lastErr = err
				log.Printf("[github] transient error on attempt %d/%d: %v; retrying...", attempt, maxAttempts, err)
				continue
			}
			return nil, err
		}

		if !shouldRetryStatus(resp.StatusCode) {
			return resp, nil
		}

		lastResp = resp
		lastErr = nil
		log.Printf("[github] HTTP %s on attempt %d/%d; retrying...", resp.Status, attempt, maxAttempts)
	}

	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

func shouldRetryStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isTemporaryError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	return false
}
