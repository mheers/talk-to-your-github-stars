# Tool reference

Detailed schema, semantics, and edge cases for each tool the ttygs MCP
server exposes.

## `list_repos`

List starred GitHub repositories, optionally filtered by a substring.

### Input

```json
{
  "query": "go oauth",        // optional, default ""
  "limit": 50                  // optional, default 50, max 500
}
```

### Output

```json
{
  "count": 12,
  "total": 248,
  "query": "go oauth",
  "repos": [
    {
      "id": 12345,
      "full_name": "foo/go-oauth2",
      "owner": "foo",
      "name": "go-oauth2",
      "url": "https://github.com/foo/go-oauth2",
      "description": "OAuth2 client/server for Go.",
      "language": "Go",
      "stars": 1234,
      "forks": 42,
      "topics": ["oauth", "go"],
      "license": "MIT",
      "has_readme": true,
      "is_archived": false,
      "is_fork": false,
      "last_pushed_at": "2025-09-12T10:11:12Z"
    }
  ]
}
```

### Semantics

- `query` is matched **case-insensitively** against `owner/name`,
  `description`, `topics`, and `readme_text` using SQL `LIKE %query%`.
  The match is literal: `%` and `_` in the query are escaped, not treated
  as wildcards.
- An empty `query` returns every repository in alphabetical order
  (subject to `limit`); a non-empty query returns matches ordered by
  `stars DESC, full_name ASC`.
- `limit` values of `0` or less use the default (50); values above 500 are
  capped at 500.
- `count` is the number returned; `total` is the number of matches in the
  database.

### When NOT to use

- If the user wants a *natural-language* search ("find me something like
  Auth0"), use `vector_search` instead. `list_repos` is keyword-only.

## `get_repo`

Fetch one repository by `owner/name`.

### Input

```json
{
  "full_name": "foo/bar",     // required
  "include_readme": true,     // optional, default false
  "max_readme_chars": 20000   // optional, 0 = default 20000, negative is rejected
}
```

### Output (found)

```json
{
  "found": true,
  "repo": { /* same shape as list_repos.repos[0] */ },
  "readme": "# foo/bar\n..."
}
```

### Output (not found)

```json
{
  "found": false,
  "error": "repository \"missing/repo\" not found"
}
```

### Semantics

- `full_name` is matched **case-insensitively** but must be a single,
  specific `owner/name` pair. Substrings do not match; `foo/ba` will not
  return `foo/bar`.
- `max_readme_chars` only applies when `include_readme=true`. `0` (or
  omitting the field) uses the 20000-character default; a negative value
  is rejected with a tool error. When truncation happens the server
  appends `\n\n…[truncated]`.
- The server is **read-only**. Calling `get_repo` cannot modify the
  database.

### When NOT to use

- If you only need the metadata, prefer the resource
  `ttygs://repo/{owner}/{name}` — it is cheaper and keeps the README
  payload out of context.

## `vector_search`

Semantic search over README chunks.

### Input

```json
{
  "query": "small Go CLI for working with CSV files",
  "k": 10
}
```

### Output

```json
{
  "query": "small Go CLI for working with CSV files",
  "k": 10,
  "hits": [
    {
      "chunk_id": 42,
      "repo_full_name": "foo/bar",
      "repo_description": "A small CLI for working with csv files",
      "text": "Repository: foo/bar\nDescription: ...\nExcerpt: ...",
      "score": 0.83
    }
  ]
}
```

### Semantics

- The query is embedded with the configured embedding model (e.g.
  `text-embedding-3-small` or `nomic-embed-text`).
- Chunks are stored with a metadata header
  `Repository: ...\nDescription: ...\nTopics: ...\nPrimary language: ...\nStars: ...`
  prepended to the README excerpt, so the embedding captures the repo
  context as well as the README text.
- `score` is **cosine similarity in [0, 1]**; higher is more relevant.
- Hits are ordered by descending score. `k` values of `0` or less use the
  default (10); values above 50 are capped at 50.
- Chunks that were embedded with a different model or vector dimension
  than the query embedding are skipped, so a database ingested with
  another embedding model simply yields no hits (re-run `ttygs ingest`
  with the configured model to index it).

### When the embedder is not configured

The tool returns a tool error with a message like:

> vector search is disabled: configure OPENAI_API_KEY (and optional
> OPENAI_BASE_URL / TTYGS_EMBEDDING_MODEL) before starting the MCP server

Tell the user how to fix it (see
[mcp-config.md](./mcp-config.md#enabling-vector-search-optional)) and fall
back to `list_repos` / `get_repo` for the current question.

### When the query returns no hits

This can happen when:
- The user has no stars yet (`ttygs sync` not run).
- The user has stars but no embeddings yet (`ttygs ingest` not run).
- The query is wildly out-of-distribution for the corpus.

In any of these cases, fall back to `list_repos` with a concrete filter,
or report the gap honestly.
