package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mheers/talk-to-your-github-stars/internal/config"
	"github.com/mheers/talk-to-your-github-stars/internal/db"
	"github.com/mheers/talk-to-your-github-stars/internal/embed"
	"github.com/mheers/talk-to-your-github-stars/internal/github"
	"github.com/mheers/talk-to-your-github-stars/internal/llm"
	"github.com/mheers/talk-to-your-github-stars/internal/mcpserver"
	"github.com/mheers/talk-to-your-github-stars/internal/rag"
	"github.com/mheers/talk-to-your-github-stars/internal/store"
	"github.com/mheers/talk-to-your-github-stars/internal/tui"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "sync":
		syncCmd(os.Args[2:])
	case "ingest":
		ingestCmd(os.Args[2:])
	case "chat":
		chatCmd()
	case "ask":
		askCmd(os.Args[2:])
	case "mcp":
		mcpCmd(os.Args[2:])
	case "version":
		fmt.Println("ttygs 0.1.0")
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: ttygs <sync|ingest|chat|ask|mcp|version>")
	fmt.Fprintln(os.Stderr, "\nCommands:")
	fmt.Fprintln(os.Stderr, "  sync [--graphql]  Fetch/refresh starred repositories from GitHub (REST by default)")
	fmt.Fprintln(os.Stderr, "  ingest      Chunk READMEs and store their embeddings in SQLite")
	fmt.Fprintln(os.Stderr, "  chat        Open the interactive TUI")
	fmt.Fprintln(os.Stderr, "  ask <query> Ask a single question from the terminal")
	fmt.Fprintln(os.Stderr, "  mcp         Run an MCP server (stdio) exposing the local stars database")
	fmt.Fprintln(os.Stderr, "  version     Print version")
}

func loadBase() (*config.Config, *db.DB, *embed.Client, *llm.Client) {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	database, err := db.Open(cfg.DBPath, cfg.EmbeddingDim)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	emb := embed.New(cfg)
	llmClient := llm.New(cfg)
	return cfg, database, emb, llmClient
}

func syncCmd(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	useGraphQL := fs.Bool("graphql", false, "use GitHub GraphQL API instead of REST")
	if err := fs.Parse(args); err != nil {
		log.Fatalf("parse flags: %v", err)
	}

	cfg, database, _, _ := loadBase()
	defer database.Close()

	if cfg.GitHubToken == "" {
		log.Fatal("GITHUB_TOKEN environment variable is required for `ttygs sync`")
	}

	log.Println("[sync] starting GitHub starred repository sync")
	gh := github.New(cfg, *useGraphQL)
	files := store.New(cfg.DataDir)
	ctx := context.Background()
	if err := gh.Sync(ctx, database, files); err != nil {
		log.Fatalf("sync failed: %v", err)
	}

	n, _ := database.RepoCount()
	log.Printf("[sync] finished. %d repositories in database.\n", n)
	fmt.Printf("Synced %d repositories.\n", n)
}

func ingestCmd(args []string) {
	_ = flag.NewFlagSet("ingest", flag.ExitOnError)
	flag.CommandLine.Parse(args)

	_, database, emb, _ := loadBase()
	defer database.Close()

	repos, err := database.ListRepos()
	if err != nil {
		log.Fatalf("list repos: %v", err)
	}
	log.Printf("[ingest] found %d repositories to index", len(repos))

	ctx := context.Background()
	var success, failed int
	for i, r := range repos {
		log.Printf("[ingest] %d/%d processing %s", i+1, len(repos), r.FullName)
		if err := rag.IngestRepo(ctx, database, emb, r); err != nil {
			failed++
			log.Printf("[ingest] failed %s: %v", r.FullName, err)
			continue
		}
		success++
		fmt.Printf("\ringested %d/%d", i+1, len(repos))
	}
	fmt.Println()
	log.Printf("[ingest] finished. succeeded: %d, failed: %d", success, failed)
}

func chatCmd() {
	cfg, database, emb, llmClient := loadBase()
	defer database.Close()

	m := tui.New(cfg, database, emb, llmClient)
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.SetProgram(p)
	if _, err := p.Run(); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

func askCmd(args []string) {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	k := fs.Int("k", 10, "number of repository chunks to retrieve")
	if err := fs.Parse(args); err != nil {
		log.Fatalf("parse flags: %v", err)
	}

	query := strings.Join(fs.Args(), " ")
	if query == "" {
		log.Fatal("ask requires a query argument")
	}

	_, database, emb, llmClient := loadBase()
	defer database.Close()

	ctx := context.Background()
	results, err := rag.Retrieve(ctx, database, emb, query, *k)
	if err != nil {
		log.Fatalf("retrieve: %v", err)
	}

	system := rag.BuildSystemPrompt(results)
	answer, err := llmClient.Complete(ctx, system, query)
	if err != nil {
		log.Fatalf("complete: %v", err)
	}

	fmt.Println(answer)
}

// mcpCmd runs the MCP server on stdio. It exposes the local stars database
// to any MCP-compatible coding agent. The server is read-only and safe to
// run concurrently with `ttygs chat` etc.
//
// Example client configuration (Claude Desktop / VS Code etc.):
//
//	{
//	  "mcpServers": {
//	    "ttygs": {
//	      "command": "/absolute/path/to/ttygs",
//	      "args": ["mcp"],
//	      "env": { "TTYGS_DATA_HOME": "/absolute/path/to/data" }
//	    }
//	  }
//	}
//
// The embedder is only needed if you want the vector_search tool. With no
// OPENAI_API_KEY the other two tools still work.
func mcpCmd(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		log.Fatalf("parse flags: %v", err)
	}

	cfg, database, emb, _ := loadBase()
	defer database.Close()

	// Pass a nil embedder when no API key is configured so the
	// vector_search tool returns a clear "disabled" message instead of a
	// confusing 401 from the embeddings API.
	if cfg.OpenAIKey == "" {
		emb = nil
		log.Println("[mcp] OPENAI_API_KEY is not set; the vector_search tool will return an error. The list_repos and get_repo tools still work.")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("[mcp] starting stdio server (name=%s, version=%s, db=%s)", mcpserver.ServerName, mcpserver.ServerVersion, cfg.DBPath)
	if err := mcpserver.New(database, emb).Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("mcp server: %v", err)
	}
}
