# Changelog

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
