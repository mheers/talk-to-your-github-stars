# Talk to your GitHub stars

A small, fast Go TUI for chatting with your starred GitHub repositories. It downloads your stars, stores structured metadata + READMEs in SQLite, creates embeddings, and runs a local RAG-style chat so you can ask things like *“I’m building a Go OAuth server—what libraries do I have starred?”*.

It also ships an **MCP server** so any coding agent (Claude Desktop, VS Code, etc.) can query the same database through a small set of tools, plus a ready-to-import **agent skill** that teaches the agent when and how to use them.

> **Status:** working MVP. The vector search is a lightweight pure-Go cosine similarity search over embeddings stored as compact float32 blobs in SQLite; databases written by older builds are migrated automatically on first open, and `sqlite3 data/stars.db VACUUM` reclaims the space the old JSON representation used. `sqlite-vec` integration is still planned for much larger corpora.

## Features

- One-command sync of all your GitHub stars (GraphQL for metadata, REST for READMEs).
- SQLite database with repo metadata, topics, languages, README text, and chunk embeddings (compact float32 blobs).
- Mirror of each repo as `data/repos/orgname/reponame/metadata.json` + `readme.md` on disk.
- OpenAI-compatible embeddings + chat (works with OpenAI, Ollama, etc.).
- Interactive Bubble Tea chat TUI.
- One-shot `ask` command for terminal usage.
- **MCP server** (`ttygs mcp`) exposing `list_repos`, `get_repo`, and `vector_search` over stdio, plus a `ttygs://repo/{owner}/{name}` resource template.
- **Agent skill** at `.github/skills/talk-to-github-stars/` (auto-discovered by VS Code / GitHub Copilot) that teaches a coding agent how to use those tools.
- **Optional TypeSafe (System One / Jev) re-ranking** — judges whether retrieved repositories actually satisfy the question instead of trusting cosine similarity alone (`TTYGS_RERANK=1`).
- Pure-Go build — no CGO or system SQLite headers required.

## Requirements

- [Go](https://go.dev/) 1.25+
- A GitHub personal access token (`GITHUB_TOKEN`) — required for `sync`
- An OpenAI-compatible API key/endpoint (`OPENAI_API_KEY` and optional `OPENAI_BASE_URL`) — required for `ingest`, `ask`, `chat`, and `vector_search`
- Optional: a TypeSafe API key (`TYPESAFE_API_KEY`) for judged re-ranking (`TTYGS_RERANK=1`)

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
| `GITHUB_TOKEN` | for `sync` | — | GitHub personal access token |
| `OPENAI_API_KEY` | for `ingest`/`ask`/`chat`/`vector_search` | — | API key for embeddings + chat |
| `OPENAI_BASE_URL` | no | `https://api.openai.com/v1` | Compatible endpoint (e.g. Ollama) |
| `TTYGS_EMBEDDING_MODEL` | no | `text-embedding-3-small` | Embedding model |
| `TTYGS_CHAT_MODEL` | no | `gpt-4o-mini` | Chat model |
| `TTYGS_EMBEDDING_DIM` | no | `1536` | Embedding dimension |
| `TTYGS_RERANK` | no | `0` | Opt in to TypeSafe (System One / Jev) judging of retrieval results (`1`/`true`) |
| `TTYGS_RERANK_MIN` | no | `0.5` | Lowest judged relevance (0–1) that still reaches the answering model |
| `TYPESAFE_API_KEY` | for `TTYGS_RERANK` | — | TypeSafe API key, read by the TypeSafe SDK |
| `TTYGS_DATA_HOME` | no | `./data` | Database/cache directory (relative to the working directory; gitignored by default). Each repo is also persisted here as `repos/orgname/reponame/metadata.json` and `readme.md`. |

`GITHUB_TOKEN` is only read by `ttygs sync`. The other subcommands open the
local database and never call the GitHub API, so `ttygs mcp`, `ttygs chat`,
`ttygs ask`, and `ttygs ingest` work without a token.

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

### 5. MCP server (for coding agents)

`ttygs mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io/)
server on stdio. It exposes the local database to any MCP-compatible
client (Claude Desktop, VS Code, Continue, etc.) and is read-only.

The server registers three tools and one resource template:

| Name | Purpose |
|---|---|
| `list_repos` | List/filter starred repos by keyword (`query`, `limit`). |
| `get_repo` | Fetch one repo by `owner/name`, optionally with its full README. |
| `vector_search` | Semantic search over README chunks (needs an embedding model). |
| Resource `ttygs://repo/{owner}/{name}` | Compact JSON metadata for one repo. |

The `vector_search` tool needs an embedding model (`OPENAI_API_KEY`, or
`OPENAI_BASE_URL` pointing at Ollama + `TTYGS_EMBEDDING_MODEL`). The other
two tools work without it. When `TTYGS_RERANK=1` is set, `vector_search`
additionally returns `answerable` (the judged probability that your stars
contain a direct match) and a per-hit `relevance` in [0,1].

#### Configure Claude Desktop

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "ttygs": {
      "command": "/absolute/path/to/ttygs",
      "args": ["mcp"],
      "env": {
        "TTYGS_DATA_HOME": "/absolute/path/to/data",
        "OPENAI_BASE_URL": "http://localhost:11434/v1",
        "OPENAI_API_KEY": "ollama",
        "TTYGS_EMBEDDING_MODEL": "nomic-embed-text",
        "TTYGS_EMBEDDING_DIM": "768"
      }
    }
  }
}
```

`TTYGS_DATA_HOME` must point at the directory that contains `stars.db`
(created by `ttygs sync`).

#### Configure VS Code

Create `.vscode/mcp.json` in your workspace (or user-level):

```json
{
  "servers": {
    "ttygs": {
      "command": "/absolute/path/to/ttygs",
      "args": ["mcp"],
      "env": {
        "TTYGS_DATA_HOME": "${userHome}/ttygs-data"
      }
    }
  }
}
```

#### Import the agent skill

This repository ships a ready-made agent skill at
[`.github/skills/talk-to-github-stars/`](.github/skills/talk-to-github-stars/).
VS Code and GitHub Copilot auto-discover skills in `.github/skills/`; other
runtimes use different locations (Claude Code reads `.claude/skills/` and
`~/.claude/skills/`, and several clients also read `.agents/skills/`).

For Claude Code, copy or symlink the skill into one of its directories:

```bash
mkdir -p .claude/skills
ln -s ../../.github/skills/talk-to-github-stars .claude/skills/talk-to-github-stars
```

The skill teaches the agent:

- when to use the MCP tools (vs. searching the web),
- which tool fits which kind of question,
- a standard procedure to deduplicate and cite results,
- how to recover when the database is empty or stale,
- a `scripts/verify.sh` health check.

For agent runtimes that read skills from a different location, copy the
folder to the appropriate place — for example
`~/.claude/skills/talk-to-github-stars/` (Claude Code) or
`~/.copilot/skills/talk-to-github-stars/` (Copilot CLI).

### 6. Optional: re-rank results with TypeSafe (System One / Jev)

Vector search ranks chunks by cosine similarity, which answers "what text is
close to the query" — not "does this repository satisfy the need". With a
[TypeSafe](https://docs.typesafe.ai/) API key you can have Jev judge the
shortlist:

```bash
export TYPESAFE_API_KEY=...
export TTYGS_RERANK=1
```

`ttygs ask`, `ttygs chat`, and the MCP `vector_search` tool then:

- widen the shortlist and send one request judging up to 30 repositories,
- re-order results by judged relevance, keeping one chunk per repository,
- drop candidates below `TTYGS_RERANK_MIN` (default `0.5`),
- report `answerable` — the probability that your stars contain a direct
  match — so the tools can say "nothing in your stars matches" instead of
  forcing a recommendation,
- fall back to plain vector search if the key is missing or the call fails.

MCP clients additionally receive a per-hit `relevance` (and
`relevance_confidence`) whenever re-ranking is active.

Re-ranking sends the query and short README excerpts to TypeSafe's API. Leave
`TTYGS_RERANK` unset to keep all retrieval local.

Implementation: [github.com/mheers/typesafeai-systemone-jev-go](https://github.com/mheers/typesafeai-systemone-jev-go)
under `internal/judge/`.

## Project layout

```
cmd/ttygs/              CLI entrypoint
internal/config/        Environment-based configuration
internal/github/        GitHub GraphQL/REST client
internal/store/         On-disk repo metadata + README persistence
internal/db/            SQLite schema + pure-Go vector search
internal/embed/         OpenAI-compatible embeddings
internal/llm/           OpenAI-compatible chat completions
internal/rag/           Chunking, ingestion, retrieval, prompts
internal/mcpserver/     MCP server (stdio) exposing the local DB
internal/judge/         Optional TypeSafe System One (Jev) retrieval judgements
internal/tui/           Bubble Tea chat interface
.github/skills/         Copilot/VS Code agent skills (auto-discovered)
  talk-to-github-stars/   Skill for using the ttygs MCP server
```

## Roadmap

- [ ] `sqlite-vec` integration (swap pure-Go search for the extension)
- [x] MCP server exposing starred-repo search + README retrieval
- [x] Agent skill (`.github/skills/talk-to-github-stars/`) for using it
- [ ] Incremental sync (only update changed repos)
- [ ] Custom per-repo notes/tags

## License

MIT
