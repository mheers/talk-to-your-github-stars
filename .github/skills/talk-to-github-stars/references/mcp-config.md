# MCP server configuration

The `ttygs mcp` subcommand runs the MCP server on stdio. Configure your agent
to launch it. The exact JSON shape depends on the client; the table below
shows the most common ones.

## Path to the binary

Use an **absolute** path. Relative paths and `~` are interpreted relative to
the client process, which usually isn't what you want.

```bash
# Build once
go build -o /usr/local/bin/ttygs ./cmd/ttygs
# or, if you prefer a project-local build
go build -o "$PWD/ttygs" ./cmd/ttygs
```

## `TTYGS_DATA_HOME`

If the user runs `ttygs` from a different working directory than where they
originally ran `ttygs sync`, set `TTYGS_DATA_HOME` to the directory that
contains `stars.db`. Otherwise the server will open a fresh empty database.

```bash
# Example: shared data dir on macOS
export TTYGS_DATA_HOME="$HOME/Library/Application Support/ttygs"
```

## Client configurations

### Claude Desktop (`claude_desktop_config.json`)

macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
Linux: `~/.config/Claude/claude_desktop_config.json`

```json
{
  "mcpServers": {
    "ttygs": {
      "command": "/usr/local/bin/ttygs",
      "args": ["mcp"],
      "env": {
        "TTYGS_DATA_HOME": "/Users/you/ttygs-data"
      }
    }
  }
}
```

### VS Code (`.vscode/mcp.json` at workspace or user level)

```json
{
  "servers": {
    "ttygs": {
      "command": "/usr/local/bin/ttygs",
      "args": ["mcp"],
      "env": {
        "TTYGS_DATA_HOME": "${userHome}/ttygs-data"
      }
    }
  }
}
```

### Generic MCP client (stdio)

| Field  | Value |
|--------|-------|
| `command` | absolute path to `ttygs` |
| `args`    | `["mcp"]` |
| `env`     | (optional) `TTYGS_DATA_HOME`, `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `TTYGS_EMBEDDING_MODEL`, `TTYGS_EMBEDDING_DIM`, `TTYGS_RERANK`, `TTYGS_RERANK_MIN`, `TYPESAFE_API_KEY` |

## Enabling vector search (optional)

`list_repos` and `get_repo` work without any API key. To enable
`vector_search`, set at least `OPENAI_API_KEY` (and optionally
`OPENAI_BASE_URL` for Ollama / OpenRouter / etc.).

### OpenAI

```json
"env": {
  "OPENAI_API_KEY": "sk-...",
  "TTYGS_EMBEDDING_MODEL": "text-embedding-3-small",
  "TTYGS_EMBEDDING_DIM": "1536"
}
```

### Ollama (local, free)

```bash
ollama pull nomic-embed-text
```

```json
"env": {
  "OPENAI_API_KEY": "ollama",
  "OPENAI_BASE_URL": "http://localhost:11434/v1",
  "TTYGS_EMBEDDING_MODEL": "nomic-embed-text",
  "TTYGS_EMBEDDING_DIM": "768"
}
```

If `OPENAI_API_KEY` is empty when the server starts, the server logs a
warning and the `vector_search` tool returns a friendly error explaining how
to fix it. The other two tools continue to work.

## Judged results with TypeSafe (optional)

Set these to have `vector_search` results judged by TypeSafe's Jev instead of
trusting cosine similarity alone:

```json
"env": {
  "TYPESAFE_API_KEY": "...",
  "TTYGS_RERANK": "1",
  "TTYGS_RERANK_MIN": "0.5"
}
```

With judging enabled, `vector_search` returns `reranked`, an `answerable`
probability, and a per-hit `relevance`. If the API key is missing or a
judgement call fails, the server logs a warning and falls back to plain
vector search — no configuration error and no failed tool call.

## Verifying the connection

After restarting the agent, check that the `ttygs` MCP server is listed in
the available tools/prompts panel. Then run a one-shot query from the agent
chat:

> "List the first 3 of my GitHub stars."

If you get results, the wiring is correct. If the agent reports the tools
are not available, see [troubleshooting.md](./troubleshooting.md).
