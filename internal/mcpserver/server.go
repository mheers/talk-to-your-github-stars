// Package mcpserver exposes the local ttygs SQLite database as a
// Model Context Protocol (MCP) server. It lets any MCP-compatible coding
// agent query the user's starred GitHub repositories and READMEs
// (keyword or vector search) without going through the ttygs TUI.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mheers/talk-to-your-github-stars/internal/db"
	"github.com/mheers/talk-to-your-github-stars/internal/embed"
	"github.com/mheers/talk-to-your-github-stars/internal/judge"
	"github.com/mheers/talk-to-your-github-stars/internal/rag"
)

// ServerName is the MCP server name advertised to clients.
const ServerName = "ttygs"

// ServerVersion is the MCP server version advertised to clients.
const ServerVersion = "0.1.0"

// DefaultMaxReadmeChars is the README truncation length used when the
// caller does not specify max_readme_chars (or passes 0).
const DefaultMaxReadmeChars = 20000

// ReadmeTruncatedMarker is appended to a README when it is truncated.
const ReadmeTruncatedMarker = "\n\n…[truncated]"

// Server wires a *db.DB (and optional embedder) into an MCP server.
type Server struct {
	server   *mcp.Server
	database *db.DB
	emb      *embed.Client  // optional; nil disables semantic search
	reranker judge.Reranker // optional; nil disables TypeSafe judging
}

// New builds an MCP server backed by the given database.
//
// If emb is nil, the vector_search tool is still registered but returns a
// helpful error instructing the caller to configure an OpenAI-compatible
// endpoint.
func New(database *db.DB, emb *embed.Client) *Server {
	s := &Server{
		database: database,
		emb:      emb,
		server: mcp.NewServer(&mcp.Implementation{
			Name:    ServerName,
			Version: ServerVersion,
		}, nil),
	}
	s.registerTools()
	s.registerResources()
	return s
}

// Run serves MCP over stdio until ctx is cancelled or stdin is closed.
func (s *Server) Run(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}

// MCPServer returns the underlying *mcp.Server. It is exposed for tests
// that need to bind the server to a non-stdio transport (e.g. in-memory).
func (s *Server) MCPServer() *mcp.Server {
	return s.server
}

// WithReranker enables TypeSafe (System One / Jev) judgements over vector
// search results. A nil reranker keeps plain cosine ranking.
func (s *Server) WithReranker(r judge.Reranker) *Server {
	s.reranker = r
	return s
}

// ----- tool input/output types ----------------------------------------------

// listReposInput is the input for the list_repos tool.
type listReposInput struct {
	Query string `json:"query,omitempty" jsonschema:"optional case-insensitive substring matched against full name, description, topics, and README text"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of repositories to return (default 50, max 500)"`
}

// repoSummary is a compact representation of a starred repository.
type repoSummary struct {
	ID           int64    `json:"id"`
	FullName     string   `json:"full_name"`
	Owner        string   `json:"owner"`
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	Description  string   `json:"description,omitempty"`
	Language     string   `json:"language,omitempty"`
	Stars        int      `json:"stars"`
	Forks        int      `json:"forks"`
	Topics       []string `json:"topics,omitempty"`
	License      string   `json:"license,omitempty"`
	HasReadme    bool     `json:"has_readme"`
	IsArchived   bool     `json:"is_archived"`
	IsFork       bool     `json:"is_fork"`
	LastPushedAt string   `json:"last_pushed_at,omitempty"`
}

// listReposOutput is the output of the list_repos tool.
type listReposOutput struct {
	Count int           `json:"count"`
	Total int           `json:"total"`
	Query string        `json:"query"`
	Repos []repoSummary `json:"repos"`
}

// getRepoInput is the input for the get_repo tool.
type getRepoInput struct {
	FullName       string `json:"full_name" jsonschema:"the 'owner/name' identifier of the repository"`
	IncludeReadme  bool   `json:"include_readme,omitempty" jsonschema:"if true, include the full README markdown in the response"`
	MaxReadmeChars int    `json:"max_readme_chars,omitempty" jsonschema:"when include_readme=true, truncate the README to this many characters (0 = default 20000, must not be negative)"`
}

// getRepoOutput is the output of the get_repo tool.
type getRepoOutput struct {
	Found  bool         `json:"found"`
	Repo   *repoSummary `json:"repo,omitempty"`
	Readme string       `json:"readme,omitempty"`
	Error  string       `json:"error,omitempty"`
}

// vectorSearchInput is the input for the vector_search tool.
type vectorSearchInput struct {
	Query string `json:"query" jsonschema:"natural language query to search for in README chunks"`
	K     int    `json:"k,omitempty" jsonschema:"number of chunks to retrieve (default 10, max 50)"`
}

// vectorSearchHit is one semantic search hit.
type vectorSearchHit struct {
	ChunkID         int64   `json:"chunk_id"`
	RepoFullName    string  `json:"repo_full_name"`
	RepoDescription string  `json:"repo_description,omitempty"`
	Text            string  `json:"text"`
	Score           float64 `json:"score"`
	// Relevance is the judged relevance in [0,1] when TypeSafe judging is
	// enabled; absent otherwise. Confidence describes how peaked the judged
	// score distribution was.
	Relevance           *float64 `json:"relevance,omitempty"`
	RelevanceConfidence *float64 `json:"relevance_confidence,omitempty"`
}

// vectorSearchOutput is the output of the vector_search tool.
type vectorSearchOutput struct {
	Query string            `json:"query"`
	K     int               `json:"k"`
	Hits  []vectorSearchHit `json:"hits"`
	// Reranked reports whether TypeSafe judged the hits.
	Reranked bool `json:"reranked,omitempty"`
	// Answerable is the judged probability that the user's stars contain a
	// direct match for the query, in [0,1]. Only present when Reranked.
	Answerable *float64 `json:"answerable,omitempty"`
}

// ----- tool handlers --------------------------------------------------------

func (s *Server) handleListRepos(ctx context.Context, _ *mcp.CallToolRequest, in listReposInput) (*mcp.CallToolResult, listReposOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	repos, err := s.database.SearchRepos(in.Query)
	if err != nil {
		return nil, listReposOutput{}, fmt.Errorf("search repos: %w", err)
	}

	out := listReposOutput{
		Query: in.Query,
		Total: len(repos),
		Repos: make([]repoSummary, 0, min(len(repos), limit)),
	}
	for i, r := range repos {
		if i >= limit {
			break
		}
		out.Repos = append(out.Repos, toRepoSummary(r))
	}
	out.Count = len(out.Repos)
	return nil, out, nil
}

func (s *Server) handleGetRepo(ctx context.Context, _ *mcp.CallToolRequest, in getRepoInput) (*mcp.CallToolResult, getRepoOutput, error) {
	if strings.TrimSpace(in.FullName) == "" {
		return nil, getRepoOutput{Error: "full_name is required"}, nil
	}
	if in.MaxReadmeChars < 0 {
		return nil, getRepoOutput{}, fmt.Errorf("max_readme_chars must not be negative, got %d", in.MaxReadmeChars)
	}

	match, err := s.database.GetRepoByFullName(in.FullName)
	if err != nil {
		return nil, getRepoOutput{}, fmt.Errorf("lookup repo: %w", err)
	}
	if match == nil {
		return nil, getRepoOutput{Found: false, Error: fmt.Sprintf("repository %q not found", in.FullName)}, nil
	}

	out := getRepoOutput{
		Found: true,
		Repo:  ptrSummary(toRepoSummary(match)),
	}
	if in.IncludeReadme {
		max := in.MaxReadmeChars
		if max == 0 {
			max = DefaultMaxReadmeChars
		}
		out.Readme = truncateReadme(match.ReadmeText, max)
	}
	return nil, out, nil
}

// truncateReadme cuts text to at most max bytes, never splitting a UTF-8
// rune, and appends ReadmeTruncatedMarker when anything was removed.
func truncateReadme(text string, max int) string {
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + ReadmeTruncatedMarker
}

func (s *Server) handleVectorSearch(ctx context.Context, _ *mcp.CallToolRequest, in vectorSearchInput) (*mcp.CallToolResult, vectorSearchOutput, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, vectorSearchOutput{}, fmt.Errorf("query is required")
	}
	if s.emb == nil {
		return nil, vectorSearchOutput{}, fmt.Errorf("vector search is disabled: configure OPENAI_API_KEY (and optional OPENAI_BASE_URL / TTYGS_EMBEDDING_MODEL) before starting the MCP server")
	}

	k := in.K
	if k <= 0 {
		k = 10
	}
	if k > 50 {
		k = 50
	}

	retrieval, err := rag.Retrieve(ctx, s.database, s.emb, s.reranker, in.Query, k)
	if err != nil {
		return nil, vectorSearchOutput{}, err
	}

	out := vectorSearchOutput{
		Query:    in.Query,
		K:        k,
		Reranked: retrieval.Reranked,
		Hits:     make([]vectorSearchHit, 0, len(retrieval.Results)),
	}
	if retrieval.Reranked {
		answerable := retrieval.Answerable
		out.Answerable = &answerable
	}
	for _, r := range retrieval.Results {
		hit := vectorSearchHit{
			ChunkID:         r.ChunkID,
			RepoFullName:    r.RepoFullName,
			RepoDescription: r.RepoDescription,
			Text:            r.Text,
			Score:           1 - r.Distance, // cosine similarity
		}
		if judged, ok := retrieval.Relevance[r.RepoFullName]; ok {
			relevance, confidence := judged.Relevance, judged.Confidence
			hit.Relevance = &relevance
			hit.RelevanceConfidence = &confidence
		}
		out.Hits = append(out.Hits, hit)
	}
	return nil, out, nil
}

// ----- helpers --------------------------------------------------------------

func toRepoSummary(r *db.Repo) repoSummary {
	rs := repoSummary{
		ID:          r.ID,
		FullName:    r.FullName,
		Owner:       r.Owner,
		Name:        r.Name,
		URL:         r.URL,
		Description: r.Description,
		Language:    r.Language,
		Stars:       r.Stars,
		Forks:       r.Forks,
		Topics:      r.Topics,
		License:     r.License,
		HasReadme:   strings.TrimSpace(r.ReadmeText) != "",
		IsArchived:  r.IsArchived,
		IsFork:      r.IsFork,
	}
	if r.PushedAt != nil {
		rs.LastPushedAt = r.PushedAt.UTC().Format(time.RFC3339)
	}
	return rs
}

func ptrSummary(s repoSummary) *repoSummary { return &s }

func (s *Server) registerTools() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name: "list_repos",
		Description: "List the user's starred GitHub repositories. " +
			"Optionally filter by a case-insensitive substring matched against " +
			"full name, description, topics, and README text. " +
			"Returns compact metadata (stars, language, topics, description). " +
			"For full README content use get_repo with include_readme=true.",
	}, s.handleListRepos)

	mcp.AddTool(s.server, &mcp.Tool{
		Name: "get_repo",
		Description: "Fetch a single starred repository by its 'owner/name' identifier. " +
			"Returns the same metadata as list_repos plus, when include_readme=true, " +
			"the full README markdown (truncated to max_readme_chars, default 20000).",
	}, s.handleGetRepo)

	mcp.AddTool(s.server, &mcp.Tool{
		Name: "vector_search",
		Description: "Semantic search over README chunks. " +
			"Embeds the query with the configured OpenAI-compatible embedding model " +
			"and returns the top-k most similar chunks across all starred repos. " +
			"Each hit includes the repository it came from and the cosine retrieval score in [0,1]. " +
			"When the server has TypeSafe judging enabled, hits also carry a judged relevance in [0,1] " +
			"and the response carries an `answerable` probability: if `answerable` is low, tell the user " +
			"nothing in their stars matches instead of forcing a recommendation. " +
			"Requires OPENAI_API_KEY to be set when the server starts.",
	}, s.handleVectorSearch)
}

// ----- resources ------------------------------------------------------------

// repoResourceURI is the MCP resource template for a single repository.
// Example: ttygs://repo/octocat/Hello-World
const repoResourceTemplate = "ttygs://repo/{owner}/{name}"

func (s *Server) registerResources() {
	tpl := &mcp.ResourceTemplate{
		Name:        "ttygs_repo",
		URITemplate: repoResourceTemplate,
		Description: "A single starred GitHub repository, addressed by 'owner/name'. " +
			"Returns the repo's compact metadata as JSON.",
		MIMEType: "application/json",
	}
	s.server.AddResourceTemplate(tpl, s.handleRepoResource)
}

// handleRepoResource serves the compact JSON metadata for a single starred
// repository.
func (s *Server) handleRepoResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	owner, name, ok := splitRepoURI(uri)
	if !ok {
		return nil, fmt.Errorf("invalid repo resource URI: %q (expected %s)", uri, repoResourceTemplate)
	}

	fullName := owner + "/" + name
	match, err := s.database.GetRepoByFullName(fullName)
	if err != nil {
		return nil, fmt.Errorf("lookup repo: %w", err)
	}
	if match == nil {
		return nil, fmt.Errorf("repository %q not found", fullName)
	}

	payload, err := json.MarshalIndent(toRepoSummary(match), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal resource: %w", err)
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      uri,
				MIMEType: "application/json",
				Text:     string(payload),
			},
		},
	}, nil
}

func splitRepoURI(uri string) (owner, name string, ok bool) {
	const prefix = "ttygs://repo/"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(uri, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
