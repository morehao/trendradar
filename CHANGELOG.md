# Changelog

## Unreleased

Optional external-context enrichment (Track B) for more accurate summaries.

### Added
- `internal/enrich`: best-effort external context for pending repos. `repo` mode
  adds GitHub topics; `readme` mode also adds an 800-rune README excerpt. Topics
  come from the repository HTML and README from `raw.githubusercontent.com`, so
  the REST 60/h limit is avoided. Results are cached for 7 days under
  `<repo>/.git/trendradar-enrich-cache.json`; concurrency, per-request timeout and
  a whole-run budget bound the work.
- Per-repository failure isolation: 404 / timeout / 429 are recorded in an
  `enrich` report and degrade to description-only summaries; a rate-limit signal
  stops new requests. `--dry-run` only reads the enrich cache and never hits the
  network.
- Prompt now emits a dedicated `TOPICS=` line and `README_EXCERPT=` line alongside
  the untouched `REPO=/DESC=` first line. README/topics are treated as untrusted
  reference data (cleaned and truncated) and never executed.
- Summary cache namespace includes the enrich mode, so A/B modes and the
  no-context baseline never reuse each other's prose.
- Config: `summarize.enrich` (`none|repo|readme`, default `none`),
  `readmeChars`, `enrichCacheTTLHours`, `enrichConcurrency`,
  `enrichTimeoutSeconds`, `enrichBudgetSeconds`.
- Observability: `run --json` gains optional `stages` and `enrich` fields;
  `trendradar-usage.jsonl` records `durationMs`, `summaryDegraded` and the enrich
  counters. Digests stay `schema:2`; `RepoSnapshot`/`Digest` are unchanged.
- Eval harness: `TRENDRADAR_EVAL_ENRICH=none|repo|readme` re-runs the 25-repo A/B.

### Added — 仓库发版订阅（releases）

- `internal/subscription` + `trendradar subscribe add|remove|list`: 手动维护订阅列表，
  存于独立 `subscriptions.yaml`（默认相对 config 文件目录；`--file` 可覆盖），
  带 `owner/repo` 校验、去重、原子写（0600）；重复 add 幂等，remove 不存在返回非 0。
- `internal/releasewatch`: 每日增量拉取订阅仓库的新 Release（无 Release 时回退 Tag），
  按 `id/publishedAt/seenTags` 推进游标；首次可只记基线（`baselineOnFirstRun`）。
  分页覆盖到越过游标，避免页码边界漏项；`perRepoLimit` 溢出按最旧优先延后到下次。
- 中文发版摘要：专用 prompt、notes 注入防护（`<RELEASE_NOTES>` 定界）、
  缓存与单条失败降级（`summarySource=original`），支持 `includePrerelease/drafts`。
- 渲染 `<output.dir>/releases/<period>/<bucket>/<name>.{md,json}` 与 `releases/index.md`，
  与 trending 同分支/同一次 PR；同日二次运行按 `repo@id/tag` 并集合并，不覆盖丢条目。
- 幂等：`render.Comparable` + contentHash；只有发布成功后回写游标。
- 可观测：`run --json` 增 `releases` 报告块；`trendradar-usage.jsonl` 增 `stage="releases"` 行。

## 0.4.1

Improve summary accuracy and record per-run token usage; support local `.env`
for test secrets.

### Added
- Local secret loading: `trendradar` auto-loads nearest `.env.local` / `.env`
  (or `TRENDRADAR_ENV_FILE`); real env wins. `config.yaml` supports `${VAR}`
  expansion, so API keys stay out of the repo. `.env.example` is committed.
- Summary prompt v2: each request now includes language, stars, stars-in-period
  and rank, with explicit style rules and few-shot examples; default temperature
  is 0.2 when unset. `PromptVersion` is bumped to `v2`.
- Summary output validation/sanitization: Markdown links/images, HTML tags, bare
  URLs and control characters are stripped; only missing/invalid repos are
  re-requested instead of re-billing the whole batch.
- `summarize.batchSize` / `summarize.maxRetries` config knobs.
- Per-run token accounting: summaries record prompt/completion/total (and cached)
  tokens by reading the model response's usage. `run --json` now carries a
  `usage` block (contract `Usage`), and non-dry runs append one line to
  `<repo>/.git/trendradar-usage.jsonl` for later aggregation.
- `usage` also reports `calls` / `retries` / `batches` / `cacheHits`, so retries
  and cache hits are visible rather than silently folded into a single number.
- Contract: `RunResult` gains the optional `usage` block (`Usage` def) with
  matching Go and TypeScript types.

## 0.4.0

Per-period output layout: daily / weekly / monthly trending digests can now
coexist on the same date without overwriting each other.

### Changed (Breaking: daily path moved)
- Artifacts now live at `<out>/<period>/<bucket>/<name>.{md,json}`:
  - daily → `daily/<YYYY-MM>/<YYYY-MM-DD>.{md,json}`
  - weekly → `weekly/<ISO-year>/<YYYY-Wnn>.{md,json}` (ISO week/year, e.g.
    `2026-11-01` → `weekly/2026/2026-W44`)
  - monthly → `monthly/<YYYY>/<YYYY-MM>.{md,json}`
  The old `trending/<date>/<date>.*` layout is gone; the data repo must be
  migrated into `daily/` (one-off `git mv`).
- Branches are now `trending/<period>/<name>` (e.g. `trending/weekly/2026-W44`),
  so the same date's three periods get distinct branches/PRs.
- `index.md` is grouped into daily/weekly/monthly sections and accumulates
  history (daily 90 / weekly 12 / monthly 12) instead of being rewritten to a
  single row each run.

### Added
- `run --period daily|weekly|monthly` overrides `config.trending.since` for both
  the fetch and the stored period.
- `serve`: `GET /api/v2/digests/{period}/{date}` (validated period; invalid → 400)
  and `GET /api/v2/manifest?period=` filtering. Manifest entries gain a `period`
  field. `/feed.xml` stays daily-only.

### Unchanged (compatibility)
- Digests are still v2 (`schema:2`, `contentHashAlgo`) and the contract wire is
  unchanged (`ipcSchema=2` / `digestSchema=2` / `contractVersion=0.2.0`).
- `GET /api/v2/digests/{date}` is preserved and maps to daily; its response is
  byte-identical to before.

## 0.3.2

### Removed (v1 contract retirement)
- Removed the legacy v1 contract snapshot (`contract/legacy-v1/`), the
  `tools/contracthash` gate, the CI `contract` job, and the goreleaser
  `CONTRACT.sha256` extra file.
- `serve`: `/api/v1/digests/{date}` and `/api/v1/manifest` now return **410 Gone**;
  use `/api/v2/*` (and `/feed.xml`).

## 0.3.1

### Changed
- Per-date branches are **append-only**: a rerun reuses the branch and adds a
  commit instead of resetting it from the base. Every push is therefore a plain
  fast-forward; `--force-with-lease` is gone.
- A rejected push is fetched, rebased onto the remote branch and retried once.
- Idempotency is decided from the repository, not a local state file: a rerun
  skips when the branch already holds the same artifacts (`generatedAt` aside).
- Summaries are cached in `.git/trendradar-cache.json` across runs, scoped by
  provider/model/prompt version.
- Git push failures now carry `error.details =
  {stage, reason, branch, remote}`.
- `run --json` reports `commit` and an additive `skipReason`
  (`merged` | `pr-closed` | `unchanged`).

### Added
- `run --trigger=schedule`: a scheduled run respects a human-closed PR (skips
  with `skipReason=pr-closed`); a manual run still rebuilds and reopens it.

### Removed
- `internal/state` (`.git/trendradar-state.json`). The repository is the source
  of truth; the plugin keeps UI history.

## 0.3.0

Adopt the shared contract v2 (`@morehao/trendradar-contract@0.2.0`; wire
`ipcSchema=2` / `digestSchema=2` / `contractVersion=0.2.0`).

### Changed
- `internal/ipc` types now alias the shared contract package (single source).
- `run --json` emits the contract `RunResult` (adds `branch`/`commit`/`prUrl`/
  `contentHash`, `startedAt`/`finishedAt`); the ad-hoc `runOutput` is gone.
- `contentHash` is now RFC 8785 JCS + sha256 over a fixed object (Go/TS agree);
  rendered digests are written as v2 (`schema:2`, `contentHashAlgo`).
- `serve` exposes `/api/v2/digests/{date}` and `/api/v2/manifest`; `/api/v1/*`
  redirects (302) to v2 for one release; `/feed.xml` item links point at v2.
  Historical v1 digests are normalized to v2 on read with
  `contentHashAlgo=legacy-struct-marshal-v1`.
- `state.json` carries `stateSchema:2`; an incompatible v1 state triggers one
  re-run (first run after upgrade re-renders; `contentHash` values change).

### Upgrade
- First run after upgrading re-renders and rewrites digests as v2; expected.
- Pair with plugin `0.3.0` (which consumes this contract) in the same window.
