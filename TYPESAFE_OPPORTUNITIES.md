# TypeSafe opportunities in talk-to-your-github-stars

Explored 2026-09-21 against the live TypeSafe docs and the real database
(1873 starred repos, 18995 embedded chunks). Two of the opportunities below
were exercised live through the TypeSafe HTTP API on real data; the others
are designs grounded in the same patterns.

The short version: this project has very little traditional parsing to
replace. Its fragility is **semantic selection encoded as fixed heuristics** —
cosine top-k, literal keyword search, blind byte truncation — and **prose
algorithms delegated to the agent** in `SKILL.md`. Those are exactly the spots
where calibrated judgements belong.

## What was validated live

A throwaway Go spike (`tmp/typesafe-spike/`, gitignored) reuses the project's
own `db` and `embed` packages, calls Ollama `nomic-embed-text` (the model the
database was ingested with), and sends judgements to `jev-latest`.

### 1. Re-ranking the vector shortlist + an answerability gate

Fast search: embed the query, `db.Search` the top 120 chunks, dedupe to 15
repos. Then one TypeSafe request with 15 per-candidate questions
(`Could candidates[i] help someone who needs need?`) plus one global
`any_direct_match` question. One request, ~6.5k input tokens, 0.3–1.5 s.

**Query A — "I want to build an HTTP API in Go - is there a web framework I starred?"**

| repo | cosine rank | noul | truth |
|---|---|---|---|
| `nlepage/go-wasm-http-server` | **1** (0.742) | 0.36 | service-worker shim, not a framework |
| `goforj/httpx` | **2** (0.739) | 0.09 | an HTTP *client* |
| `go-fuego/fuego` | 3 (0.721) | **0.96** | Go web framework |
| `goravel/framework` | outside top 5 | 0.82 | Go web framework |
| `livebud/bud` | outside top 5 | 0.80 | full-stack Go framework |
| `avelino/awesome-go` | 4 (0.709) | 0.27 | a curated list |
| **`any_direct_match`** | | **0.91** | |

Cosine put an HTTP client and a service-worker shim at the top and buried two
real frameworks outside the top 5. The judgement inverted that.

**Query B — "I'm building a Go OAuth server - which libraries do I have starred?"**

| repo | cosine rank | noul |
|---|---|---|
| `thecodearcher/limen` ("Modern, composable authentication for Go") | ~6 | **0.83** |
| `oauth2-proxy/oauth2-proxy` | 2 | 0.62 |
| `Skarlso/google-oauth-go-sample` | **1** (0.730) | 0.48 |
| `rakyll/openai-go` | 3 (0.703) | 0.05 |
| `oras-project/oras-go` | 5 (0.694) | 0.06 |
| **`any_direct_match`** | | **0.81** |

The best answer was not in the cosine top 5; the judgement found it and demoted
two cosine favourites to near zero.

**Query C — "I need a Haskell library for parsing JSON - do I have one starred?"**
(there are zero Haskell repos in the database)

| | |
|---|---|
| every candidate's noul | ≤ 0.09 |
| **`any_direct_match`** | **0.03** |

This is the most useful result. All three queries produced cosine scores in
the same 0.59–0.74 band — **no threshold on cosine similarity can separate
"here is your framework" from "nothing like this exists in your stars."** The
judgement separated them 0.91 / 0.81 / 0.03.

### 2. Reusable repo facets

One request over 8 real repos, one `kind` Choice per repo
("software / curated_list / learning_resource / other"), ~3.7k tokens, 0.3 s:

| repo | kind | confidence |
|---|---|---|
| `labstack/echo`, `uber-go/zap`, `ollama/ollama`, `kubernetes/kubernetes` | software | 1.00, 1.00, 1.00, 1.00 |
| `avelino/awesome-go`, `punkpeye/awesome-mcp-servers`, `trimstray/the-book-of-secret-knowledge` | curated_list | 1.00, 0.99, 0.94 |
| `Ebazhanov/linkedin-skill-assessments-quizzes` | learning_resource | 0.96 |

Why this matters, from the actual data: of the 12 repos that contribute the
most chunks to the vector index, 8 are curated lists or learning material —
`awesome-mcp-servers` (395 chunks), `linkedin-skill-assessments-quizzes`
(349), `best-of-ml-python` (342), `awesome-go` (304),
`the-book-of-secret-knowledge` (161), `awesome-public-datasets` (151).
467 repos have no topics and 46 no description, so keyword filters cannot
identify them either. Today the recommender happily treats any of these as a
candidate library.

## Opportunity catalogue

### O1 — Re-rank retrieval and gate on answerability (highest value)

**Status: implemented** — opt-in via `TTYGS_RERANK=1`, built on
[`mheers/typesafeai-systemone-jev-go`](https://github.com/mheers/typesafeai-systemone-jev-go)
(`internal/judge/`). Live check against the real database: the Go web
framework query reports `answerable=0.96` with `fuego`, `echo` and `goravel`
on top; the Go OAuth query reports `0.90` and promotes `thecodearcher/limen`
to first; the unanswerable Haskell query reports `0.04` and returns no hits.
Cosine scores for all three sit in the same 0.67–0.74 band.

**Fragile today:** `internal/rag/rag.go:88` returned raw cosine top-k; there
was no notion of relevance, no threshold, and no way to say "not in your
stars". `internal/mcpserver/server.go` exposed the same scores as
`score in [0,1]`, and `SKILL.md:123` told the *agent* to interpret them.
Query B shows a real match scoring 0.674 while unrelated repos score 0.676.

**Judgement:** shortlist 15–30 repos (existing `db.Search`, already ~0.14 s),
then one request of per-candidate Nouls — `direct_match` (and optionally
`is_offsetting_list`, `is_learning_resource`) — plus a global `answerable`
Noul. Code sorts, filters, and decides.

**Code owns:** embedding, shortlist, per-repo dedupe, thresholds, formatting,
the honest "I found nothing" response.

**Used by:** `ttygs ask`, the TUI chat (`internal/tui/tui.go:515`), and the MCP
`vector_search` tool. The MCP tool can then return `answerable` and
per-hit `relevance`, which removes the fragile score-interpretation prose from
the skill file.

### O2 — Repo facet judgements at ingest, reused by every filter

**Fragile today:** `list_repos` filters on GitHub's description/topics, which
are missing for 467/46 repos and do not say whether something is a library, a
list, or a course. The agent is told to do this filtering by reading
descriptions.

**Judgement:** at ingest, one request per repo with parallel questions:
`kind` (Choice), `readme_language` (Choice; Jev is English-first, so knowing
when a README is CJK matters), perhaps `has_quickstart_examples` (Noul).
Store as DB columns. ~1873 requests, one-time, parallelizable, and reusable
forever — "turn judgments into reusable data".

**Code owns:** the schema migration, when to refresh facets, and every filter
that consumes them (`list_repos(kind=software)`, "recommend only libraries").

**Note:** `last_pushed_at`, stars, archived, language are already deterministic
columns — no judgement needed for those.

### O3 — Natural language to typed repo filters

**Fragile today:** `internal/db/db.go:264` matches a literal substring. The
agent must translate "my Go auth libraries" into a magic keyword that happens
to appear in a README; `SKILL.md` instructs it how to phrase queries.

**Judgement:** the function-calling pattern. One request maps the request to
closed sets: function Choice (`search_repos` / `get_repo` / `semantic_search`),
`language` Choice over the database's actual distinct languages, `kind` Choice
(O2), `min_stars` Score or a `stated` Noul + code-side default, and Nouls for
candidate topics. Code executes SQL — the model never writes the query.

**Code owns:** option lists built from the DB, the SQL, and validation.

### O4 — Gate retrieved passages before the answer LLM

**Fragile today:** `BuildSystemPrompt` (`internal/rag/rag.go:100`) pastes raw
README chunks into the prompt with "Use only the repository information
below" and then truncates each at 900 bytes mid-sentence
(`internal/rag/rag.go:111`). READMEs are untrusted third-party text; a chunk
can contain instructions, contradiction, or noise.

**Judgement:** the four questions from the "Classifying RAG passages"
cookbook — relevant, contains evidence, contradicts the question's premise,
attempts to instruct the model — with thresholds in code. Accepted evidence
and conflicts go into separate prompt blocks; injections are dropped.

**Overlap:** this is O1's machinery applied to answering rather than ranking;
implement once.

### O5 — Verify the answer instead of trusting the prompt

**Fragile today:** "Cite repository names" is a prompt instruction with no
check. Nothing catches an answer that cites a repo it never received.

**Judgement:** after generation, one request checks each cited repo against
the claim it supports (Noul), and that the answer uses only supplied repos.
Code can append a warning or retry. The citation-check cookbook's confidence
gating applies directly.

**Status:** design only; lower priority than O1/O2.

### O6 — Select README sections for `get_repo` instead of truncating

**Fragile today:** `max_readme_chars` cuts at a byte boundary; the section that
answers the caller's question is as likely to be cut as kept.

**Judgement:** split the README into sections/line ranges in code (cheap
deterministic parsing), then use the line-by-line search pattern (one Choice
per candidate id) to select the relevant spans. Add an optional `question`
argument so callers get a reading guide rather than the first 20 000 bytes.

**Status:** design only; a smaller win.

## Where judgement is the wrong tool

Per the "use code when you can" rule, these stay deterministic:

- GitHub GraphQL/REST mapping, SQL, URI templates, flags, env parsing.
- **Per-repo dedupe** of retrieval hits — `SKILL.md:127` asks the agent to
  "pick the highest-scoring chunk per repo". That is a `seen` map, not a
  judgement; the MCP tool should just do it.
- Repo identity across renames — `node_id`/`id` already exist; key the upsert
  on them rather than `full_name` (a latent correctness bug, not an AI one).
- Recency/popularity filters — already in the database.
- Markdown-aware chunk boundaries (`internal/rag/chunk.go`) — deterministic
  parsing fixes code-block splitting; judgement would be slow across 19k
  chunks and is not needed if retrieval is gated (O1/O4).

Two incidental code findings from the same read:

- `internal/tui/tui.go:552` and `internal/rag/rag.go:111` truncate by bytes and
  can split a UTF-8 rune (same class of bug already fixed in the MCP server).
- `sync` upserts by `full_name`, so a renamed repo is duplicated rather than
  updated.

## Integration sketch

- New `internal/judge` package: a small HTTP client for
  `POST https://api.typesafe.ai/v1/systemone` (there is no Go SDK; Python and
  JS only). Request/response types for Noul, Choice, Score; retry on 429/529
  with backoff, as the docs recommend.
- Config: `TYPESAFE_API_KEY`, optional `TYPESAFE_MODEL` (default
  `jev-latest`), optional base URL. If the key is missing, every opportunity
  degrades to today's behavior — judgements are advisory, never a hard
  dependency.
- Add a thin seam so retrieval is testable without the network:
  `type Reranker interface { Rank(ctx, need string, candidates []Candidate) ([]Judgement, error) }`.
- Cache by content hash (query + candidate ids + question version) so a chat
  session does not pay twice for the same shortlist.
- Facts and limits from the docs: one request can carry many parallel
  questions; 64k context per request, 32k for state; English is the strongest
  language; price is $42 per billion input tokens and output is free
  (the three demo queries cost well under a cent); rate limits are dynamic.

## Recommendation

O1 is implemented (opt-in). Turn it on with `TYPESAFE_API_KEY` +
`TTYGS_RERANK=1` and calibrate `TTYGS_RERANK_MIN` on real queries. Next, do
**O2**, which turns one-time judgements into reusable columns and fixes the
"recommends awesome-lists as libraries" failure at the root.
