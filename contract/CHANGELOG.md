# Changelog

All notable changes to `@morehao/trendradar-contract` are documented here.
This project adheres to a `contract/vX.Y.Z` tag per release (npm + Go submodule).

## 0.6.0

Digest inlined in `RunResult`. **Breaking wire bump**: `ipcSchema` 2 → 3
(`digestSchema` stays 2). A core/plugin pair must both move to this contract
line; the plugin's handshake hard-rejects the mismatch.

### Added
- `RunResult.digest` (optional): the digest document this run rendered —
  `{ path, bytes, sha256, text }`. `bytes`/`sha256` describe `text`'s UTF-8 bytes
  exactly as written to `path`; `text` is capped at 1 MiB.
  Present whenever the digest stage rendered a document (including
  `skipped=unchanged` and the `merged` / `pr-closed` early exits, which render
  before the PR pre-check); absent for dry runs, release-only runs, and failures
  before rendering. Consumers no longer need to read the output repository to
  display the digest.

## 0.5.0

Single-repo release analysis. Wire integers unchanged (`ipcSchema = 2` /
`digestSchema = 2`); additive at the envelope level, so a plugin/core built
against 0.4.0 keeps working.

### Added
- `RunRequest.command=run` may now carry `repo`: with `stages` including
  `releases`, the run analyzes only that one subscribed repository's releases.
  `repo` requires `stages` to contain `releases`; `run` still must not carry
  `action`/`volume`. `serve`/`config`/`version` still must not carry `repo`.

## 0.4.0

Weekly release records. The wire integers stay at `ipcSchema = 2` /
`digestSchema = 2`; `ReleaseDigest.schema` moves 1 → 2. Every field change below
is additive at the envelope level, so a plugin built against 0.3.0 keeps working.

### Added
- `SubscriptionList.items[]` (`SubscriptionItem`: `fullName`, `url`,
  `latestTag`, `latestSummaryAt`, `lastCheckedAt`, `status`). `repos` stays the
  only required field, so an older core still satisfies the schema.
- `SubscriptionDoc` + `SubscriptionVolume`: the exact markdown of one volume of a
  repo's release document plus its volume index (`index`, `file`, `from`, `to`,
  `lines`). Shared by the IPC `subscribe releases` command and
  `GET /api/v2/repos/{owner}/{repo}/releases`.
- `RunRequest.stages` (`trending|digest|releases`) and `RunRequest.volume`;
  `action` gains `releases` (requires `repo`, optional `volume`).
- `RunResult.subscriptions` / `RunResult.subscriptionDoc`: the subscribe
  command's payload now rides inside the result envelope instead of stdout.
  `RunResult.command` accepts `subscribe`.
- `ReleasesReport.checked` / `changed` (both required).
- Fixtures: `release-weekly.json` (schema-2 weekly shape without per-item
  summaries), `subscriptions-normal.json`, `subscriptions-40.json` (the 10 KB
  first-paint budget), `subscription-doc-normal.json`.
- `api.v2.yaml`: `GET /api/v2/repos/{owner}/{repo}/releases`.

### Changed
- `ReleaseDigest.schema` = 2: `RepoReleases` gains `summaryZh`,
  `periodStart`, `periodEnd`; `ReleaseItem.summaryZh` becomes optional (still
  readable for documents written before this version, no longer written).
- Release fixtures regenerated with `schema: 2` (their `contentHash` changes
  accordingly; the JCS basis is unchanged).
- The contract test now asserts the 40-repo subscription fixture stays ≤ 10 KB.

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
