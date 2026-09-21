# PLAN — finish, review and harden the ttygs MCP server

Date: 2026-09-21
Status: **complete** — all tasks below are implemented, validated and committed.
Scope: the uncommitted MCP-server + agent-skill work in this working tree.

## Current state

The working tree contained an in-progress feature: an MCP server (`internal/mcpserver/`),
a `ttygs mcp` subcommand, an agent skill under `.github/skills/talk-to-github-stars/`,
a README section, and a config change making `GITHUB_TOKEN` optional.

Validation performed before planning:

| Check | Result |
|---|---|
| `go build ./...` | pass |
| `go vet ./...` | pass |
| `go test ./...` | pass |
| `go mod tidy` | no unexpected changes |
| `gofmt -l .` | **`internal/mcpserver/server.go` was not formatted** |
| End-to-end JSON-RPC probe against the real 1873-repo / 18995-chunk DB | mostly pass, found the crash below |
| VS Code skill discovery in `.github/skills/` | confirmed (VS Code docs) |
| Claude Code discovery in `.github/skills/` | **not documented / unsupported** |

## Findings

### Bugs

**B1 — `get_repo` with a negative `max_readme_chars` crashed the server (confirmed).**
`handleGetRepo` sliced `text[:max]` after only replacing `max == 0`. Any negative
value panicked the process. Reproduced end-to-end before the fix: `alive=False`,
server exit code 2, stdout closed. Severity: high (remote crash / denial of service
for a long-lived MCP client).

**B2 — `max_readme_chars: 0` contradicted the documented API (confirmed).**
The jsonschema tag and `tool-reference.md` promised "0 = no truncation"; the code
mapped 0 to the 20000 default. Reproduced with `avelino/awesome-go` (394k-char
README). Decision: **0/omitted = default 20000, negative = validation error**, and
the docs were updated to match. This is the safer, LLM-friendly semantics.

**B3 — `internal/mcpserver/server.go` was not gofmt-clean.** Fixed.

**B4 — the "vector_search is disabled" error was unreachable in production.**
`embed.New(cfg)` always returns a non-nil client even with an empty API key, so the
real `ttygs mcp` process surfaced a confusing `401 Unauthorized` from the embeddings
API instead of the friendly disabled message promised by the README and the skill.
Found by the end-to-end probe. Fixed in `mcpCmd` by passing a nil embedder when
`OPENAI_API_KEY` is unset.

**B5 — truncation could split a UTF-8 rune.** `text[:max]` could cut a multi-byte
character in half; `encoding/json` then silently replaced the broken bytes with
U+FFFD. Fixed with a rune-boundary-aware `truncateReadme` (regression test added).

### Correctness / semantics

**C1 — `list_repos` treated user input as SQL `LIKE` wildcards.**
`db.SearchRepos` built `%query%` without escaping, so `query: "%"` matched all 1873
repos instead of a literal. Fixed with `ESCAPE '\'`; a `%` query now matches the
1060 repos that actually contain a literal `%` (mostly percentage signs in READMEs).

**C2 — dimension-mismatched embeddings silently scored -1.**
`vector_search` against a DB ingested with a different model/dimension returned hits
with `score: -1` (observed with a 1536-dim mock against the 768-dim real data),
breaking the documented `[0,1]` range. `db.Search` now skips chunks whose embedding
length differs from the query vector.

**C3 — `readme_path` output field was dead.** Always empty and omitted from JSON.
Removed (the MCP server intentionally has no filesystem coupling).

**C4 — `min` helper shadowed the Go 1.21+ builtin** (go.mod is go 1.25). Removed.

**C5 — `EnsureStdio` was dead code.** Removed.

**C6 — misleading comment** "Late import to avoid a hard dependency on the LLM/embed
packages" in `handleVectorSearch`; there was no late import. Removed.

**C7 — `get_repo` / resource lookup was a substring search plus a duplicated manual
filter loop**, loading candidate rows (with READMEs) that were then discarded.
Replaced with `db.GetRepoByFullName` (`WHERE full_name = ? COLLATE NOCASE`), which
also makes "substring alone must not match" testable and explicit.

**C8 — `vector_search` reads and JSON-parses all 18995 embeddings per call (~2.2 s).**
Acceptable for an agent tool; sqlite-vec is already on the roadmap. Not fixed,
recorded as a known limitation.

**C9 — `list_repos` ordering differs between empty and non-empty queries**
(alphabetical vs stars-descending). Not a bug; the tool reference now documents it.

### Documentation

**D1 — README contradicted itself on `GITHUB_TOKEN`.** Requirements table said
"required: yes", the footnote said "required for sync only", and the config comment
claimed `ask/chat/ingest` need it "transitively" (they never use it). Fixed: only
`sync` needs the token.

**D2 — README claimed Claude Code auto-discovers `.github/skills/`.** Claude Code's
documented locations are `.claude/skills/` and `~/.claude/skills/`; `.github/skills/`
is the VS Code/Copilot convention. The README now states this and gives a symlink
recipe.

**D3 — README said Go 1.23+; `go.mod` says `go 1.25.0`.** Fixed.

**D4 — `usage()` said ingest indexes "into sqlite-vec".** Fixed.

**D5 — skill reference drift:** `tool-reference.md` documented `readme_path`, the
`0 = no truncation` semantics, and looser `limit`/`k` clamping than the code
implements; `troubleshooting.md` claimed `ingest` needs `GITHUB_TOKEN`. All fixed;
the reference now also documents literal matching and dimension-mismatch skipping.

**D6 — cosmetic:** README project-layout block alignment; the empty leftover
directory `skills/talk-to-github-stars/` was removed.

## Tasks

- [x] T1 Fix `max_readme_chars`: 0/omitted → 20000, negative → tool error; update jsonschema text (B1, B2).
- [x] T2 `gofmt`, remove builtin `min` shadow, delete `EnsureStdio`, fix stale comment, drop `readme_path` (B3, C3, C4, C5, C6).
- [x] T3 Escape `%`, `_`, `\` in `db.SearchRepos` LIKE patterns; add db tests (C1).
- [x] T4 Skip dimension-mismatched embeddings in `db.Search`; add db test (C2).
- [x] T5 Add `db.GetRepoByFullName` and use it in both MCP handlers; add tests (C7).
- [x] T6 Add mcpserver tests: truncation/zero/negative, rune boundary, vector_search with a mock OpenAI-compatible embedder (hit ordering, score range, dimension mismatch, k clamping), unknown/malformed resource URI, substring must not match in `get_repo`, literal wildcards (B1, B5, C1, C2).
- [x] T7 Add `internal/config/config_test.go`: token optional, data-home handling, defaults (D1).
- [x] T8 Fix README (D1, D2, D3, D4, D6) and skill references (D5); remove the empty `skills/` dir.
- [x] T9 Fix the unreachable "disabled" error in `mcpCmd` (B4).
- [x] T10 Re-run full validation (results below).
- [x] T11 Commit all work as one commit.

## Final validation results

| Check | Result |
|---|---|
| `gofmt -l .` | clean |
| `go vet ./...` | pass |
| `go test -race ./... -count=1` | pass (config, db, mcpserver, rag, store) |
| `CGO_ENABLED=0 go build ./cmd/ttygs` | pass (pure-Go claim holds) |
| `make build` (`./ttygs`) | pass |
| `ttygs sync` without `GITHUB_TOKEN` | exits 1 with a clear message |
| `ttygs mcp` without `GITHUB_TOKEN` | starts and serves |
| End-to-end stdio JSON-RPC probe vs. the real DB (1873 repos, 18995 chunks) | **21/21 checks pass** |
| Crash regression (`max_readme_chars: -1`) | clean tool error, server stays alive |
| `vector_search` (mock embedder, 768-dim) | hits with scores in [0,1], ~2.2 s |
| Skill health check `scripts/verify.sh ./ttygs ./data` | all checks pass |

## Out of scope (follow-ups)

- sqlite-vec migration to replace the O(n) pure-Go scan (already on the roadmap).
- Populating `readme_path` from the on-disk mirror (removed instead).

## Follow-up implemented (2026-09-21): compact embedding storage

The ~2.2 s `vector_search` latency noted in C8 came from reading and
JSON-parsing every embedding on each query. Embeddings are now stored as raw
little-endian float32 blobs, and `Search` reads them directly.

- `db.InsertVec` writes blobs; `db.Search` accepts both the blob and the
  legacy JSON format.
- `db.Open` runs a one-time, `PRAGMA user_version`-guarded migration that
  rewrites legacy JSON embeddings in place (best-effort: unparsable values
  are left alone). No re-ingestion and no API calls are required.
- New tests cover the codec, the migration, and mixed-format reads; a
  `BenchmarkSearch` was added.

Measured on the real database (1873 repos, 18995 chunks, 768 dims):

| Metric | Before | After |
|---|---|---|
| `vector_search` (mock embedder) | ~2.20 s | ~0.14 s |
| Database size | 261 MB | 122 MB (after `VACUUM`) |
| One-time migration | — | 4.5 s |

Search scores before and after are identical, so the change is
behavior-preserving apart from the format.

Remaining follow-up: sqlite-vec remains the right answer at a much larger
scale, but the pure-Go scan is now fast enough for tens of thousands of
chunks.
