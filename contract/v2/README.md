# @morehao/trendradar-contract (v2)

The single source of truth for the data shapes shared by the **trendradar Go
core** (`github.com/morehao/trendradar`) and the **DSH plugin**
(`@morehao/dsh-github-trending`).

This package lives inside the core repository at `contract/v2/` and ships two
artifacts from one tag:

- an **npm package** `@morehao/trendradar-contract` (types + ajv validators + fixtures), and
- a **nested Go module** `github.com/morehao/trendradar/contract/v2` (structs + error catalog + embedded schemas).

Both are released together by the tag `contract/v2.Y.Z` (see `CHANGELOG.md`).

## Install

```bash
npm i @morehao/trendradar-contract@2.0.0
go get github.com/morehao/trendradar/contract/v2@v2.0.0   # resolves tag contract/v2.0.0
```

## Layout

| Path | Purpose |
|---|---|
| `schema/model.v2.json` | `RepoSnapshot`; `RepoDigest` composes it via `allOf` |
| `schema/ipc.v2.json` | `RunRequest` (strict), `RunResult`, `ErrorInfo`, `State`, `FileArtifact` |
| `schema/digest.v2.json` | rendered digest document, incl. `contentHashAlgo` |
| `schema/errors.v2.json` | 17 error codes with `retriable`/`description` (single source of truth) |
| `schema/version-info.v2.json` | `trendradar version --json` handshake |
| `schema/api.v2.yaml` | `trendradar serve` OpenAPI (`/api/v2`) |
| `fixtures/*.json` | golden v2 digests (incl. `digest-missing-optional.json`) |
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
import contract "github.com/morehao/trendradar/contract/v2"

var _ contract.RunResult
contract.ErrorCatalog[contract.ErrorCodeREPO_DIRTY].Retriable // => false
contract.SchemaFS // embedded schema/* files
```

## Versioning & compatibility

- `ipcSchema` (const 2) and `digestSchema` (const 2) are the wire integers;
  `contractVersion` (`"2.0.0"`) is the package handshake.
- Adding an **optional** field is a minor change. Removing/renaming a field,
  changing a type, or tightening validation is **breaking**: bump the wire
  integer in `version-map.json` and the package major. `make verify-versions`
  enforces this against the PR base.
- `RunRequest` is the only strict object (`additionalProperties: false`).

## contentHash

`contentHash` = `sha256(JCS({schema,date,period,languages,count,degraded,repos}))`
(RFC 8785 JCS; `generatedAt`/`coreVersion`/`contentHash`/`contentHashAlgo`
excluded; optional repo fields omitted when absent). New files use
`contentHashAlgo: "jcs-sha256-v1"`; historical v1 digests normalized on read use
`"legacy-struct-marshal-v1"` with their original `contentHash` preserved.

## Development

```bash
npm ci
npm run generate        # regenerate contract.gen.go + gen/ts (deterministic)
npm run verify-versions # carriers consistent (+ breaking gate when BASE_REF set)
npm run verify-field-map# every v1 field covered by v2
npm test                # ajv fixture validation + JCS cross-checks
go test ./...           # jsonschema fixture validation + JCS hash + error catalog
```

Generated files (`contract.gen.go`, `gen/ts/*`) are committed; CI fails if they
drift from `schema/`.
