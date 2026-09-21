package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWithoutGitHubToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("TTYGS_DATA_HOME", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load must succeed without GITHUB_TOKEN: %v", err)
	}
	if cfg.GitHubToken != "" {
		t.Fatalf("expected an empty token, got %q", cfg.GitHubToken)
	}
}

func TestLoadCreatesDataHomeAndUsesDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	t.Setenv("TTYGS_DATA_HOME", dir)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("TTYGS_EMBEDDING_MODEL", "")
	t.Setenv("TTYGS_CHAT_MODEL", "")
	t.Setenv("TTYGS_EMBEDDING_DIM", "")
	t.Setenv("TTYGS_RERANK", "")
	t.Setenv("TTYGS_RERANK_MIN", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != dir {
		t.Fatalf("DataDir: got %q, want %q", cfg.DataDir, dir)
	}
	if cfg.DBPath != filepath.Join(dir, "stars.db") {
		t.Fatalf("DBPath: got %q", cfg.DBPath)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("data dir was not created: %v", err)
	}
	if cfg.OpenAIBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("OpenAIBaseURL: got %q", cfg.OpenAIBaseURL)
	}
	if cfg.EmbeddingModel != "text-embedding-3-small" {
		t.Fatalf("EmbeddingModel: got %q", cfg.EmbeddingModel)
	}
	if cfg.ChatModel != "gpt-4o-mini" {
		t.Fatalf("ChatModel: got %q", cfg.ChatModel)
	}
	if cfg.EmbeddingDim != 1536 {
		t.Fatalf("EmbeddingDim: got %d", cfg.EmbeddingDim)
	}
	if cfg.Rerank {
		t.Fatal("reranking must be opt-in; expected Rerank to be false by default")
	}
	if cfg.RerankMin != 0.5 {
		t.Fatalf("RerankMin: got %v, want 0.5", cfg.RerankMin)
	}
}

func TestLoadRerankOptions(t *testing.T) {
	t.Setenv("TTYGS_DATA_HOME", t.TempDir())
	t.Setenv("TTYGS_RERANK", "1")
	t.Setenv("TTYGS_RERANK_MIN", "0.7")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Rerank {
		t.Fatal("expected Rerank to be enabled by TTYGS_RERANK=1")
	}
	if cfg.RerankMin != 0.7 {
		t.Fatalf("RerankMin: got %v, want 0.7", cfg.RerankMin)
	}

	t.Setenv("TTYGS_RERANK_MIN", "not-a-number")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RerankMin != 0.5 {
		t.Fatalf("RerankMin with invalid input: got %v, want the 0.5 default", cfg.RerankMin)
	}

	t.Setenv("TTYGS_RERANK", "0")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Rerank {
		t.Fatal("expected Rerank to be disabled by TTYGS_RERANK=0")
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("TTYGS_DATA_HOME", t.TempDir())
	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("TTYGS_EMBEDDING_MODEL", "nomic-embed-text")
	t.Setenv("TTYGS_CHAT_MODEL", "qwen3")
	t.Setenv("TTYGS_EMBEDDING_DIM", "768")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAIKey != "sk-test" {
		t.Fatalf("OpenAIKey: got %q", cfg.OpenAIKey)
	}
	if cfg.OpenAIBaseURL != "http://localhost:11434/v1" {
		t.Fatalf("OpenAIBaseURL: got %q", cfg.OpenAIBaseURL)
	}
	if cfg.EmbeddingModel != "nomic-embed-text" {
		t.Fatalf("EmbeddingModel: got %q", cfg.EmbeddingModel)
	}
	if cfg.ChatModel != "qwen3" {
		t.Fatalf("ChatModel: got %q", cfg.ChatModel)
	}
	if cfg.EmbeddingDim != 768 {
		t.Fatalf("EmbeddingDim: got %d", cfg.EmbeddingDim)
	}
}

func TestLoadDefaultDataDirIsWorkingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TTYGS_DATA_HOME", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if want := filepath.Join(wd, "data"); cfg.DataDir != want {
		t.Fatalf("DataDir: got %q, want %q", cfg.DataDir, want)
	}
}
