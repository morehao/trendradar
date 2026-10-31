# Changelog

## Unreleased

### Changed
- Per-date branches are **append-only**: a rerun reuses the branch and adds a
  commit instead of resetting it from the base. Every push is therefore a plain
  fast-forward; `--force-with-lease` is gone. This removes the `(stale info)`
  dead end that occurred after a closed PR whose remote branch had been deleted.
- A rejected push is fetched, rebased onto the remote branch and retried once,
  so "someone appended first" is recoverable instead of terminal.
- Idempotency is decided from the repository, not a local state file: a rerun
  skips when the branch already holds the same artifacts (`generatedAt` aside).
  `.git/trendradar-state.json` is removed.
- Summaries are cached in `.git/trendradar-cache.json` across runs, scoped by
  provider/model/prompt version, so identical input renders an identical digest
  without another model call.
- Git push failures now carry `error.details =
  {stage, reason, branch, remote}` with `reason` in
  `{stale-lease, non-fast-forward, auth, protected, hook, pathspec, unknown}`.
- `run --json` reports `commit` and an additive `skipReason`
  (`merged` | `pr-closed` | `unchanged`).

### Added
- `run --trigger=schedule`: a scheduled run respects a human-closed PR (skips
  with `skipReason=pr-closed` and does not recreate the branch); a manual run
  still rebuilds and reopens it.

### Removed
- `internal/state` (`.git/trendradar-state.json`). The repository is the source
  of truth; the plugin keeps UI history.
- Removed the legacy v1 contract snapshot (`contract/legacy-v1/`), the
  `tools/contracthash` gate, the CI `contract` job, and the goreleaser
  `CONTRACT.sha256` extra file.
- `serve`: `/api/v1/digests/{date}` and `/api/v1/manifest` now return **410 Gone**;
  use `/api/v2/*` (and `/feed.xml`).

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
