# Troubleshooting

Quick fixes for the most common failure modes when the agent tries to talk
to the ttygs MCP server.

## "Tool not found" / tools not advertised

The MCP client did not start the server, or failed to negotiate the
session.

1. **Check the binary path.** It must be absolute. Run it once from a
   terminal:
   ```bash
   /absolute/path/to/ttygs mcp
   ```
   You should see the server log `[mcp] starting stdio server ...` and the
   process should stay alive (no errors). If it exits immediately, there is
   a config error — see step 3.
2. **Check `TTYGS_DATA_HOME`.** The directory must exist and contain
   `stars.db`. If you ran `ttygs sync` from a different working directory,
   set this env var to that directory.
3. **Check `GITHUB_TOKEN`.** Only `ttygs sync` needs this; the MCP server
   never calls the GitHub API, so it is fine to leave it unset here.
4. **Restart the client** (Claude Desktop / VS Code / etc.) after editing
   the config.

## "vector_search is disabled"

The server started, but no embedding model is configured.

Set these env vars in the MCP config:

```json
"env": {
  "OPENAI_API_KEY": "sk-...",
  "OPENAI_EMBEDDING_MODEL": "text-embedding-3-small"
}
```

For Ollama, see [mcp-config.md](./mcp-config.md#ollama-local-free).

## "Found 0 results" / empty list

The database has no stars indexed yet. Tell the user to run, in order:

```bash
export GITHUB_TOKEN=ghp_...
ttygs sync     # downloads metadata + READMEs
ttygs ingest   # embeds README chunks for semantic search
```

After that, re-run the query. `list_repos` and `get_repo` need only step 1
(`sync`); `vector_search` needs both.

## Stale results after starring a new repo

`ttygs sync` is **not** incremental. The user must re-run it to pick up
newly starred repos. Mention this and offer to walk them through it.

## Permission / privacy

- The MCP server runs locally on the user's machine and only reads the
  local SQLite database. It does **not** make outbound GitHub API calls.
- It does make outbound calls to the configured embedding API (OpenAI /
  Ollama) **only** when `vector_search` is called, and only the user's
  query text is sent.
- `get_repo` and `list_repos` make **no** network calls.

If the user is concerned about sending the query to a third-party LLM,
suggest they use a local embedding model via Ollama.
