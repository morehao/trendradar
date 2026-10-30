# Changelog

All notable changes to `@morehao/trendradar-contract` are documented here.
This project adheres to a `contract/vX.Y.Z` tag per release (npm + Go submodule).

## 2.0.0

Initial v2 contract (breaking re-baseline; no v1 wire compatibility).

- `ipcSchema = 2`, `digestSchema = 2`; new `contractVersion` handshake.
- `RunResult` gains optional `branch` / `commit` / `prUrl` / `contentHash`; errors
  flow through `ErrorInfo` instead of stderr text.
- `Digest` gains `contentHashAlgo` (`jcs-sha256-v1` | `legacy-struct-marshal-v1`).
- `contentHash` computed with RFC 8785 JCS + sha256 (Go/TS identical).
- `RepoDigest` composes `RepoSnapshot` via `allOf` (Go: embedded struct).
- New `errors.v2.json` (17 codes with retriable/description) and `ErrorCatalog`.
- `VersionInfo` moves into the contract with `contractVersion` + `supportedProviders`.
- `serve` HTTP moves to `/api/v2/digests/{date}` and `/api/v2/manifest`.

### Consumers

- core: pair with core `0.3.0` (first run re-renders; `state.json` re-baselined).
- plugin: pair with plugin `0.2.0` (`lastDigest`/`pendingPr` reset on first run).
