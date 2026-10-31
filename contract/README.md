# @morehao/trendradar-contract (0.4.0)

The single source of truth for the data shapes shared by the **trendradar Go
core** (`github.com/morehao/trendradar`) and the **DSH plugin**
(`@morehao/dsh-github-trending`).

This package lives inside the core repository at `contract/` and ships two
artifacts from one tag:

- an **npm package** `@morehao/trendradar-contract` (types + ajv validators + fixtures), and
- a **nested Go module** `github.com/morehao/trendradar/contract` (structs + error catalog + embedded schemas).

Both are released together by the tag `contract/v0.Y.Z` (see `CHANGELOG.md`).

> Version model: the **package** is `0.4.0`, while the
> **wire format** it carries is the second generation — `ipcSchema=2`,
> `digestSchema=2`. The integers 2 distinguish this contract from the legacy
> in-repo v1; they are intentionally decoupled from the package semver.

## Install

```bash
npm i @morehao/trendradar-contract@0.4.0
go get github.com/morehao/trendradar/contract@v0.4.0   # resolves tag contract/v0.4.0
```

## Layout

| Path | Purpose |
|---|---|
| `schema/model.v2.json` | `RepoSnapshot`; `RepoDigest` composes it via `allOf` |
| `schema/ipc.v2.json` | `RunRequest` (strict), `RunResult`, `ErrorInfo`, `State`, `FileArtifact` |
| `schema/digest.v2.json` | rendered digest document, incl. `contentHashAlgo` |
| `schema/releases.v2.json` | release-subscription document + `ReleasesReport` (run cost) |
| `schema/errors.v2.json` | 17 error codes with `retriable`/`description` (single source of truth) |
| `schema/version-info.v2.json` | `trendradar version --json` handshake |
| `schema/api.v2.yaml` | `trendradar serve` OpenAPI (`/api/v2`, incl. releases) |
| `fixtures/*.json` | golden v2 digests + release documents |
| `version-map.json` | schema file → version carrier → propagation |
| `contract.gen.go` | generated Go structs + `ErrorCode` + `ErrorCatalog` + `SchemaFS` |
| `gen/ts/index.{js,d.ts}` | generated TS types + runtime (`validators`, `errorCatalog`, `fixtures`, `rawSchema`) |

## TypeScript usage

```ts
import { validators, fixtures, errorCatalog, type Digest, type RunResult } from '@morehao/trendradar-contract'

validators.digest(fixtures.normal)        // => true (ajv, draft 2020-12 + formats)
errorCatalog.FETCH_RATE_LIMITED.retriable // => true
```

## Go usage

```go
import contract "github.com/morehao/trendradar/contract"

var _ contract.RunResult
contract.ErrorCatalog[contract.ErrorCodeREPO_DIRTY].Retriable // => false
contract.SchemaFS // embedded schema/* files
```

## Versioning & compatibility

- `ipcSchema` (const 2) and `digestSchema` (const 2) are the wire integers;
  `contractVersion` (`"0.4.0"`) is the package handshake.
- Adding an **optional** field is a minor change. Removing/renaming a field,
  changing a type, or tightening validation is **breaking**: bump the affected
  wire integer and/or the package version, declared in `version-map.json`.
  `make verify-versions` enforces this against the PR base.
- `RunRequest` is the only strict object (`additionalProperties: false`). It
  carries conditional rules for `command: "subscribe"`: `action` is required,
  `list` takes no `repo`/`volume`, `add`/`remove` require a `repo` and no
  `volume`, `releases` requires a `repo` and may name a `volume`; other commands
  must not carry any of them. `stages` is run-only.
- `ReleaseDigest.schema` is `2`: the summary moved from per-tag
  (`ReleaseItem.summaryZh`, now optional and no longer written) to one per repo
  per week (`RepoReleases.summaryZh` + `periodStart`/`periodEnd`). Old documents
  still parse; new writers leave the per-item field empty.
- `ReleasesReport.checked` / `changed` are **required** so `0` ("checked nothing")
  is distinguishable from "not reported".
- Token cost for releases lives in `ReleasesReport.usage` (always present) and
  **not** in the stored `ReleaseDigest`, because cost describes one run and
  cannot be reproduced from a document.

### IPC ↔ HTTP mirrors

Every read-only IPC capability has an HTTP mirror that returns the *same*
contract type (only the transport envelope differs). Writes are never exposed
over HTTP; the surface is read-only, `no-store`, and unauthenticated by design
(bind to `127.0.0.1`).

| IPC | HTTP | Status |
|---|---|---|
| `subscribe list` (`SubscriptionList`, incl. `items[]`) | `GET /api/v2/subscriptions` | added in 0.4.0 |
| `subscribe releases --repo` (`SubscriptionDoc`) | `GET /api/v2/repos/{owner}/{repo}/releases?volume=N` | added in 0.4.0 |
| `run` weekly digest (`ReleaseDigest`) | `GET /api/v2/releases`, `GET /api/v2/releases/{date}` | unchanged since 0.3.0 |
| `subscribe add/remove` | — | writes stay IPC-only |

When adding a read-only IPC command, add its route in the same PR (and a row
here); when adding a write command, state in the PR why it is not mirrored.

## contentHash

`contentHash` = `sha256(JCS({schema,date,period,languages,count,degraded,repos}))`
(RFC 8785 JCS; `generatedAt`/`coreVersion`/`contentHash`/`contentHashAlgo`
excluded; optional repo fields omitted when absent). New files use
`contentHashAlgo: "jcs-sha256-v1"`; historical v1 digests normalized on read use
`"legacy-struct-marshal-v1"` with their original `contentHash` preserved.

## Development

```bash
npm ci
npm run generate        # regenerate contract.gen.go + gen/ts (deterministic, gofmt'd)
npm run verify-versions # carriers consistent (+ breaking gate when BASE_REF set)
npm test                # ajv fixture validation + JCS cross-checks
go test ./...           # jsonschema fixture validation + JCS hash + error catalog
```

Generated files (`contract.gen.go`, `gen/ts/*`) are committed; CI fails if they
drift from `schema/`.
