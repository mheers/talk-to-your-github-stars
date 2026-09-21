package rag

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/mheers/talk-to-your-github-stars/internal/db"
	"github.com/mheers/talk-to-your-github-stars/internal/embed"
	"github.com/mheers/talk-to-your-github-stars/internal/judge"
)

const (
	chunkSize    = 1500
	chunkOverlap = 200
)

// IngestRepo deletes old chunks for a repo, re-chunks its content and stores new embeddings.
func IngestRepo(ctx context.Context, database *db.DB, emb *embed.Client, repo *db.Repo) error {
	if err := database.DeleteChunksForRepo(repo.ID); err != nil {
		return fmt.Errorf("delete old chunks for %s: %w", repo.FullName, err)
	}

	chunks := BuildChunks(repo)
	if len(chunks) == 0 {
		return nil
	}
	log.Printf("[ingest] %s: building %d chunks", repo.FullName, len(chunks))

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}

	log.Printf("[ingest] %s: embedding %d chunks", repo.FullName, len(chunks))
	vecs, err := emb.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed %s: %w", repo.FullName, err)
	}
	if len(vecs) != len(chunks) {
		return fmt.Errorf("embedding count mismatch for %s", repo.FullName)
	}

	log.Printf("[ingest] %s: storing %d vectors", repo.FullName, len(chunks))
	for i, c := range chunks {
		rowid, err := database.InsertChunk(repo.ID, c.Text, c.Source)
		if err != nil {
			return fmt.Errorf("insert chunk for %s: %w", repo.FullName, err)
		}
		if err := database.InsertVec(rowid, vecs[i]); err != nil {
			return fmt.Errorf("insert vec for %s: %w", repo.FullName, err)
		}
	}
	log.Printf("[ingest] %s: done", repo.FullName)
	return nil
}

// BuildChunks creates searchable text fragments for a repository.
func BuildChunks(repo *db.Repo) []db.Chunk {
	header := fmt.Sprintf(
		"Repository: %s\nDescription: %s\nTopics: %s\nPrimary language: %s\nStars: %d\n",
		repo.FullName,
		repo.Description,
		strings.Join(repo.Topics, ", "),
		repo.Language,
		repo.Stars,
	)

	var chunks []db.Chunk

	readmeChunks := ChunkText(repo.ReadmeText, chunkSize, chunkOverlap)
	if len(readmeChunks) == 0 {
		// No readme: still index the metadata if there is any.
		if repo.Description != "" || len(repo.Topics) > 0 {
			chunks = append(chunks, db.Chunk{Text: header, Source: "meta"})
		}
		return chunks
	}

	for _, rc := range readmeChunks {
		text := header + "\n" + rc
		chunks = append(chunks, db.Chunk{Text: text, Source: "readme"})
	}
	return chunks
}

// NoMatchMessage is shown when retrieval finds nothing in the user's stars
// that matches the question. It is the honest answer when judged relevance
// leaves no candidate above the floor.
const NoMatchMessage = "Nothing in your starred repositories matches that. Try rephrasing the question, or run `ttygs sync` to refresh your stars."

// Retrieval is the outcome of a retrieval, optionally re-ranked by judged
// relevance.
type Retrieval struct {
	Results []db.SearchResult
	// Reranked reports whether the judge produced the ordering.
	Reranked bool
	// Answerable is the judged probability that the shortlist contains a
	// direct answer to the query. Only meaningful when Reranked is true.
	Answerable float64
	// RerankErr records a non-fatal judge failure. When it is set, Results
	// holds the plain vector shortlist and Reranked is false.
	RerankErr error
	// Relevance maps repository full names to their judged relevance when
	// Reranked is true.
	Relevance map[string]judge.RankedCandidate
}

const (
	// rerankShortlistChunks is how many vector hits are fetched before
	// per-repo dedupe. Judging needs a wider net than the final answer: the
	// cosine order can bury the best candidate.
	rerankShortlistChunks = 120
	// rerankMaxCandidates bounds the repositories judged in one request.
	rerankMaxCandidates = 30
)

// Retrieve embeds the query and returns the best matching chunks. With a nil
// reranker it returns the top-k vector hits unchanged. With a reranker it
// judges a wider shortlist, keeps one chunk per repository, and returns the
// repositories that pass the reranker's relevance floor. A failing reranker
// is non-fatal: the plain shortlist is returned with RerankErr set.
func Retrieve(ctx context.Context, database *db.DB, emb *embed.Client, reranker judge.Reranker, query string, k int) (*Retrieval, error) {
	vecs, err := emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 || vecs[0] == nil {
		return nil, fmt.Errorf("empty query embedding")
	}

	if reranker == nil {
		results, err := database.Search(vecs[0], k)
		if err != nil {
			return nil, err
		}
		return &Retrieval{Results: results}, nil
	}

	shortlistSize := k * 3
	if shortlistSize < rerankShortlistChunks {
		shortlistSize = rerankShortlistChunks
	}
	results, err := database.Search(vecs[0], shortlistSize)
	if err != nil {
		return nil, err
	}

	// Judge repositories, not chunks: duplicate chunks would spend tokens on
	// the same decision, and the best chunk per repo is the one worth showing.
	shortlist := dedupeByRepo(results)
	if len(shortlist) > rerankMaxCandidates {
		shortlist = shortlist[:rerankMaxCandidates]
	}
	candidates := make([]judge.Candidate, len(shortlist))
	for i, r := range shortlist {
		candidates[i] = judge.Candidate{
			ID:      r.RepoFullName,
			Title:   r.RepoDescription,
			Excerpt: r.Text,
		}
	}

	ranking, err := reranker.Rank(ctx, query, candidates)
	if err != nil {
		return &Retrieval{Results: limitResults(shortlist, k), RerankErr: err}, nil
	}

	byRepo := make(map[string]db.SearchResult, len(shortlist))
	for _, r := range shortlist {
		byRepo[r.RepoFullName] = r
	}
	out := &Retrieval{
		Reranked:   true,
		Answerable: ranking.Answerable,
		Relevance:  make(map[string]judge.RankedCandidate),
	}
	for _, item := range ranking.Top(reranker.MinRelevance()) {
		if len(out.Results) >= k {
			break
		}
		result, ok := byRepo[item.ID]
		if !ok {
			continue
		}
		out.Results = append(out.Results, result)
		out.Relevance[item.ID] = item
	}
	return out, nil
}

// dedupeByRepo keeps the first (highest-ranked) chunk per repository.
func dedupeByRepo(results []db.SearchResult) []db.SearchResult {
	seen := make(map[string]bool, len(results))
	out := make([]db.SearchResult, 0, len(results))
	for _, r := range results {
		if seen[r.RepoFullName] {
			continue
		}
		seen[r.RepoFullName] = true
		out = append(out, r)
	}
	return out
}

// limitResults caps results at k.
func limitResults(results []db.SearchResult, k int) []db.SearchResult {
	if len(results) > k {
		return results[:k]
	}
	return results
}

// BuildSystemPrompt turns retrieved chunks into a prompt for the LLM.
func BuildSystemPrompt(results []db.SearchResult) string {
	var b strings.Builder
	b.WriteString("You are a helpful assistant that recommends libraries from the user's GitHub stars.\n")
	b.WriteString("Use only the repository information below to answer. Cite repository names.\n\n")
	for _, r := range results {
		fmt.Fprintf(&b, "Repository: %s\nDescription: %s\nExcerpt:\n%s\n\n",
			r.RepoFullName, r.RepoDescription, truncate(r.Text, 900))
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
