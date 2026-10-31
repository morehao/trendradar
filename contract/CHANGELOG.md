# Changelog

All notable changes to `@morehao/trendradar-contract` are documented here.
This project adheres to a `contract/vX.Y.Z` tag per release (npm + Go submodule).

## Unreleased

_Nothing yet._

## 0.3.0

Repo release subscriptions become a first-class contract capability. The wire
integers stay at `ipcSchema = 2` / `digestSchema = 2`: every addition here is
backward compatible, so existing plugins keep working without a handshake bump.

### Added
- `schema/releases.v2.json`: `ReleaseDigest` (the on-disk document, `schema: 1`),
  `RepoReleases`, `ReleaseItem`, plus `ReleasesReport`,
  `ReleaseManifestEntry` and `SubscriptionList`.
- `RunRequest.command` accepts `subscribe`, with `action`
  (`list|add|remove`) and `repo`. The envelope stays strict
  (`additionalProperties: false`); conditional rules enforce that `list` takes
  no `repo`, `add`/`remove` require one, and non-subscribe commands carry
  neither.
- `RunResult.releases`: the releases-stage diagnostic, now schema-checked
  instead of riding on `additionalProperties`.
- `api.v2.yaml`: `GET /api/v2/releases`, `GET /api/v2/releases/{date}` and
  `GET /api/v2/subscriptions` (read-only; empty arrays rather than 404 when no
  data exists).

### Notes
- **Token cost**: `ReleasesReport.usage` is required and always serialized so a
  consumer can tell "nothing spent" (`calls = 0`) from "spent but the provider
  reported nothing" (`calls > 0` with `usageKnown = false`). Cost is
  deliberately absent from `ReleaseDigest` and from
  `ReleaseManifestEntry`: a stored document describes content, not one run.
- `ReleaseItem.id` is `int64` in Go (GitHub ids exceed int32 on 32-bit
  platforms) and required (`0` for tag fallbacks).
- Generated Go/TS names `id` as `ID`; the generator gained an `x-go-type:
  int64` escape hatch and a conditional-`allOf` branch.

## 0.2.0

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
