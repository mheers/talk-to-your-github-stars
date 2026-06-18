# Talk to your GitHub stars

A small, fast Go TUI for chatting with your starred GitHub repositories. It downloads your stars, stores structured metadata + READMEs in SQLite, creates embeddings, and runs a local RAG-style chat so you can ask things like *“I’m building a Go OAuth server—what libraries do I have starred?”*.

> **Status:** working MVP. The vector search is currently a lightweight pure-Go cosine similarity search over embeddings stored in SQLite. `sqlite-vec` integration and an MCP server are planned next.

## Features

- One-command sync of all your GitHub stars (GraphQL for metadata, REST for READMEs).
- SQLite database with repo metadata, topics, languages, README text, and chunk embeddings.
- Mirror of each repo as `data/repos/orgname/reponame/metadata.json` + `readme.md` on disk.
- OpenAI-compatible embeddings + chat (works with OpenAI, Ollama, etc.).
- Interactive Bubble Tea chat TUI.
- One-shot `ask` command for terminal usage.
- Pure-Go build — no CGO or system SQLite headers required.

## Requirements

- [Go](https://go.dev/) 1.23+
- A GitHub personal access token (`GITHUB_TOKEN`)
- An OpenAI-compatible API key/endpoint (`OPENAI_API_KEY` and optional `OPENAI_BASE_URL`)

## Obtaining a GitHub Personal Access Token

This tool fetches your starred GitHub repositories. In order to access them without incurring rate limits it is required to use the GitHub Personal Access Token.

Read this to learn how to obtain it:

[Managing your personal access tokens - GitHub Docs](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)

For public repositories, no additional scopes are required. If you want to include **private** starred repositories, generate a classic token with the `repo` scope.

## Build

```bash
go build -o ttygs ./cmd/ttygs
./ttygs version
```

## Configuration

All settings are environment variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `GITHUB_TOKEN` | yes | — | GitHub personal access token |
| `OPENAI_API_KEY` | yes* | — | API key for embeddings + chat |
| `OPENAI_BASE_URL` | no | `https://api.openai.com/v1` | Compatible endpoint (e.g. Ollama) |
| `TTYGS_EMBEDDING_MODEL` | no | `text-embedding-3-small` | Embedding model |
| `TTYGS_CHAT_MODEL` | no | `gpt-4o-mini` | Chat model |
| `TTYGS_EMBEDDING_DIM` | no | `1536` | Embedding dimension |
| `TTYGS_DATA_HOME` | no | `./data` | Database/cache directory (relative to the working directory; gitignored by default). Each repo is also persisted here as `repos/orgname/reponame/metadata.json` and `readme.md`. |

\* Required for `sync` only via GitHub; required for `ingest`, `ask`, and `chat`.

## Usage

### 1. Sync your stars

```bash
export GITHUB_TOKEN=ghp_...
./ttygs sync
```

This fetches all starred repos (with stars, forks, issues, PRs, commit count, last commit/release, topics, languages, license, etc.) and downloads each README as Markdown. Each repo is saved both in SQLite and on disk under `data/repos/<org>/<repo>/` as `metadata.json` and `readme.md`.

### 2. Ingest into the vector store

```bash
export OPENAI_API_KEY=sk-...
./ttygs ingest
```

READMEs + metadata are chunked, embedded, and stored in SQLite.

#### 2.1 Ingesting with local ollama

```bash
ollama pull nomic-embed-text
export OPENAI_BASE_URL=http://localhost:11434/v1
export OPENAI_API_KEY=ollama                          # Ollama doesn't require a real key
export TTYGS_EMBEDDING_MODEL=nomic-embed-text         # or all-minilm, etc.\n
export TTYGS_EMBEDDING_DIM=768
./ttygs ingest
```

### 3. Chat

Interactive TUI:

```bash
export OPENAI_BASE_URL=http://localhost:11434/v1
export OPENAI_API_KEY=ollama
export TTYGS_EMBEDDING_MODEL=nomic-embed-text
export TTYGS_EMBEDDING_DIM=768
export TTYGS_CHAT_MODEL=qwen3.5
./ttygs chat
```

### 4. One-shot questions

```bash
./ttygs ask "I'm building a static site generator, what should I use?"
```

## Project layout

```
cmd/ttygs/         CLI entrypoint
internal/config/   Environment-based configuration
internal/github/   GitHub GraphQL/REST client
internal/store/    On-disk repo metadata + README persistence
internal/db/       SQLite schema + pure-Go vector search
internal/embed/    OpenAI-compatible embeddings
internal/llm/      OpenAI-compatible chat completions
internal/rag/      Chunking, ingestion, retrieval, prompts
internal/tui/      Bubble Tea chat interface
```

## Roadmap

- [ ] `sqlite-vec` integration (swap pure-Go search for the extension)
- [ ] MCP server exposing starred-repo search + README retrieval
- [ ] Incremental sync (only update changed repos)
- [ ] Custom per-repo notes/tags

## License

MIT
