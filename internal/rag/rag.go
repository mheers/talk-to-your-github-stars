package rag

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/mheers/talk-to-your-github-stars/internal/db"
	"github.com/mheers/talk-to-your-github-stars/internal/embed"
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

// Retrieve embeds the query and returns the top-k matching chunks.
func Retrieve(ctx context.Context, database *db.DB, emb *embed.Client, query string, k int) ([]db.SearchResult, error) {
	vecs, err := emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 || vecs[0] == nil {
		return nil, fmt.Errorf("empty query embedding")
	}
	return database.Search(vecs[0], k)
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
