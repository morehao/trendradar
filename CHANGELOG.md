# Changelog

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
