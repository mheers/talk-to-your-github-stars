---
name: talk-to-github-stars
description: 'Query the user''s locally indexed GitHub stars (metadata + READMEs) via the ttygs MCP server. Use when the user asks for library recommendations from their stars, wants to find a starred repo for a specific use case (e.g. "find a Go OAuth library I starred"), wants to know what tools/frameworks/languages they have bookmarked, or wants the contents of a specific starred repository''s README.'
argument-hint: ''
user-invocable: true
disable-model-invocation: false
---

# talk-to-github-stars

A coding agent skill that teaches you to use the **ttygs MCP server**.
ttygs syncs the user's GitHub stars into a local SQLite database, indexes each
README, and exposes a small MCP server so you can answer questions like:

> "I'm building a static site generator in Go — what should I use?"
> "Which of my starred repos implement OAuth?"
> "Pull the README for `acme/widgets`."

## When to use

- The user mentions **stars**, **bookmarks**, **"things I've starred"**, or
  **"repos I follow / saved"**.
- The user wants a **library/tool/framework recommendation** and has GitHub
  stars (so prior taste is the corpus).
- The user wants to **read or summarize a specific starred repo's README**.
- The user wants a **list of their starred repos** matching some filter
  (language, topic, keyword, popularity).

If the user wants something *outside* their stars (e.g. "what's the best Go web
framework in general"), still use ttygs first — they may already have
something perfect saved — and broaden only if nothing matches.

## Prerequisites

The MCP server must be running and connected. Verify before invoking tools:

1. The `ttygs` binary is built: `go build -o ttygs ./cmd/ttygs` (or installed
   on `$PATH`).
2. The user has run `ttygs sync` at least once to populate the database.
3. The user has run `ttygs ingest` at least once if they want semantic
   (`vector_search`) results.
4. The MCP server is configured in the agent's MCP settings. The standard
   config is:

   ```json
   {
     "mcpServers": {
       "ttygs": {
         "command": "/absolute/path/to/ttygs",
         "args": ["mcp"],
         "env": { "TTYGS_DATA_HOME": "/absolute/path/to/data" }
       }
     }
   }
   ```

   `OPENAI_API_KEY` (or `OPENAI_BASE_URL` pointing at Ollama / similar) is
   only required for the `vector_search` tool. The other two tools work
   without it.

   See [mcp-config.md](./references/mcp-config.md) for full setup
   instructions including Ollama.

## Available tools

The MCP server advertises exactly three tools plus one resource template.
Use them in this order of preference.

### 1. `vector_search` — semantic search across README chunks (best for "find me a library for X")

| Field    | Type   | Notes |
|----------|--------|-------|
| `query`  | string | Natural-language description of the desired library / use case |
| `k`      | int    | Number of chunks to retrieve (default 10, max 50) |

Returns a list of `hits`. Each hit has `repo_full_name`, `repo_description`,
`text` (a README excerpt with a header showing the repo name), and `score` in
[0, 1] (cosine retrieval similarity, higher is better).

When the server runs with TypeSafe judging enabled (`TTYGS_RERANK=1`), the
response also carries `reranked: true` and an `answerable` probability, and
each hit carries a judged `relevance` in [0, 1]. In that case prefer
`relevance` over `score`, and treat a low `answerable` as "the user's stars
do not contain a match".

Requires an embedding model to be configured on the server.

### 2. `list_repos` — filter repos by keyword (best for "do I have anything in language X / topic Y?")

| Field   | Type   | Notes |
|---------|--------|-------|
| `query` | string | Case-insensitive substring matched against `owner/name`, description, topics, and README text |
| `limit` | int    | Default 50, max 500 |

Returns a list of compact repo summaries (`full_name`, `description`,
`language`, `stars`, `forks`, `topics`, `has_readme`, ...).

Use this when the query is a concrete filter (language, topic, exact name
fragment) rather than a use-case description.

### 3. `get_repo` — fetch a single repo's full README (best for "what does this repo do?")

| Field            | Type   | Notes |
|------------------|--------|-------|
| `full_name`      | string | `owner/name` (case-insensitive) |
| `include_readme` | bool   | If true, the response includes the full README markdown |
| `max_readme_chars` | int  | Truncate the README (0 = default 20000; negative values are rejected) |

Returns `{found, repo, readme, error}`. If `found` is `false`, the repo is
not in the user's stars.

### 4. Resource: `ttygs://repo/{owner}/{name}`

Read this when you only need the compact metadata as JSON (no README body).
Use it as a lightweight alternative to `get_repo` without `include_readme`.

## Standard procedure

Follow these steps for any question about the user's stars.

1. **Pick the right tool.**
   - "Find me a library for *X*" → `vector_search` with `k=10`.
   - "What do I have for language X / topic Y / keyword Z" → `list_repos`
     with a short query.
   - "What does repo `foo/bar` do?" → `get_repo` with `include_readme=true`.
   - "How popular is `foo/bar` in my stars?" → `list_repos` with `query="foo/bar"`
     and inspect the `stars` field.

2. **Read the response carefully.** Each `vector_search` hit includes a
   header line `Repository: <owner>/<name>` followed by the chunk text. Cite
   the repo by `full_name` in your reply.

3. **Deduplicate.** `vector_search` returns multiple chunks from the same
   repo. Group by `repo_full_name` and pick the highest-scoring chunk per
   repo. Mention at most 3–5 repos per answer unless the user asked for
   more.

4. **Cross-check with `get_repo`** when you need more context. If a hit
   sounds promising but you want to know stars / language / license /
   archived status, call `get_repo` *without* `include_readme` for a
   metadata-only fetch, then call it *with* `include_readme` only if you
   need the body.

5. **Be honest about gaps.** If the database has no matches:
   - Do NOT invent libraries. Tell the user you couldn't find a match in
     their stars and ask whether to broaden the search.
   - When `vector_search` reports a low `answerable` probability, say plainly
     that their stars do not contain a match instead of forcing a suggestion.
   - Suggest running `ttygs sync` if the database looks stale or empty
     (see [troubleshooting.md](./references/troubleshooting.md)).

6. **Cite every recommendation** with `owner/name` and (when relevant) the
   star count, language, and license. The user values brevity — one or two
   sentences per repo is enough.

## Example interaction

> **User:** I'm building a CLI in Go that needs OAuth. What do I have starred?

> **Agent:** Calls `vector_search({query: "OAuth library for Go CLI", k: 8})`,
> then groups the hits by repo and replies:

> Based on your stars, the most relevant options are:
> - **foo/go-oauth2** (⭐ 1.2k, Go, MIT) — a small OAuth2 client/server library
>   with PKCE support.
> - **acme/authkit** (⭐ 5k, TypeScript, Apache-2.0) — opinionated auth
>   framework; usable from Go via the SDK.
>
> I did not find a Go-native, MIT-licensed OAuth server. Want me to widen the
> search or fetch the full READMEs?

## Bundled resources

- [mcp-config.md](./references/mcp-config.md) — full client config snippets
  (Claude Desktop, VS Code, generic) and Ollama instructions.
- [tool-reference.md](./references/tool-reference.md) — exhaustive
  per-tool schema, input/output shape, and edge cases.
- [troubleshooting.md](./references/troubleshooting.md) — what to do when
  the server returns no data, errors, or unexpected results.
- [verify.sh](./scripts/verify.sh) — quick health check that the MCP
  server is configured correctly.
