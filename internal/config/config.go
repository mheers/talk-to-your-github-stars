package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Config holds all runtime configuration.
type Config struct {
	GitHubToken    string
	OpenAIKey      string
	OpenAIBaseURL  string
	EmbeddingModel string
	ChatModel      string
	EmbeddingDim   int
	DataDir        string
	DBPath         string
}

// Load reads configuration from environment variables.
//
// GITHUB_TOKEN is optional here because only `sync` talks to the GitHub
// API. The other subcommands (`ingest`, `chat`, `ask`, `mcp`) only read
// the local database and work without a token; `sync` validates the
// token itself and fails with a clear message when it is missing.
func Load() (*Config, error) {
	gh := os.Getenv("GITHUB_TOKEN")

	dataDir := os.Getenv("TTYGS_DATA_HOME")
	if dataDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("could not determine working directory: %w", err)
		}
		dataDir = filepath.Join(cwd, "data")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("could not create data directory: %w", err)
	}

	dim, _ := strconv.Atoi(os.Getenv("TTYGS_EMBEDDING_DIM"))
	if dim == 0 {
		dim = 1536 // text-embedding-3-small
	}

	emb := os.Getenv("TTYGS_EMBEDDING_MODEL")
	if emb == "" {
		emb = "text-embedding-3-small"
	}

	chat := os.Getenv("TTYGS_CHAT_MODEL")
	if chat == "" {
		chat = "gpt-4o-mini"
	}

	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		base = "https://api.openai.com/v1"
	}

	return &Config{
		GitHubToken:    gh,
		OpenAIKey:      os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:  base,
		EmbeddingModel: emb,
		ChatModel:      chat,
		EmbeddingDim:   dim,
		DataDir:        dataDir,
		DBPath:         filepath.Join(dataDir, "stars.db"),
	}, nil
}
