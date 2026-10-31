import test from 'node:test'
import assert from 'node:assert/strict'
import crypto from 'node:crypto'
import canonicalize from 'canonicalize'
import { validators, fixtures, errorCatalog, contractVersion } from '../gen/ts/index.js'

const REPO_OPTIONAL = ['description', 'forks']
const jcsHash = (d) => {
  const repos = d.repos.map((r) => {
    const o = { ...r }
    for (const k of REPO_OPTIONAL) if (o[k] === undefined || o[k] === null) delete o[k]
    return o
  })
  const basis = { schema: d.schema, date: d.date, period: d.period, languages: d.languages, count: d.count, degraded: d.degraded, repos }
  return 'sha256:' + crypto.createHash('sha256').update(canonicalize(basis), 'utf8').digest('hex')
}

test('contractVersion is 0.3.0', () => assert.equal(contractVersion, '0.3.0'))

test('error catalog has 17 retriable entries', () => {
  assert.equal(Object.keys(errorCatalog).length, 17)
  assert.equal(errorCatalog.FETCH_RATE_LIMITED.retriable, true)
  assert.equal(errorCatalog.REPO_DIRTY.retriable, false)
})

// Trending digest fixtures only; release fixtures are validated against the
// releases schema separately below.
const DIGEST_FIXTURES = ['normal', 'degraded', 'emptyLang', 'missingOptional']

test('all fixtures validate against digest v2 schema', () => {
  for (const name of DIGEST_FIXTURES) {
    assert.ok(validators.digest(fixtures[name]), `${name} should be valid`)
  }
})

test('rejects a v1-shaped digest (schema const mismatch)', () => {
  assert.equal(validators.digest({ ...fixtures.normal, schema: 1 }), false)
})

// Release digests use a different JCS basis (no languages; repos are release
// groups, not repo snapshots) — see internal/releasedigest.ContentHash.
const releaseHash = (d) => {
  const basis = { schema: d.schema, date: d.date, period: d.period, count: d.count, degraded: d.degraded, repos: d.repos }
  return 'sha256:' + crypto.createHash('sha256').update(canonicalize(basis), 'utf8').digest('hex')
}

test('jcs contentHash matches every trending fixture', () => {
  for (const name of DIGEST_FIXTURES) {
    assert.equal(jcsHash(fixtures[name]), fixtures[name].contentHash, `${name} jcs hash mismatch`)
  }
})

test('jcs contentHash matches every release fixture', () => {
  for (const name of ['releaseNormal', 'releaseDegraded', 'releaseEmpty']) {
    assert.equal(releaseHash(fixtures[name]), fixtures[name].contentHash, `${name} jcs hash mismatch`)
  }
})

test('RunRequest is strict about extra fields', () => {
  const req = { ipcSchema: 2, command: 'run', date: '2026-10-30' }
  assert.ok(validators.runRequest(req))
  assert.equal(validators.runRequest({ ...req, unexpected: true }), false)
})

test('RunRequest accepts subscribe with action', () => {
  assert.ok(validators.runRequest({ ipcSchema: 2, command: 'subscribe', action: 'list' }))
  assert.ok(validators.runRequest({ ipcSchema: 2, command: 'subscribe', action: 'add', repo: 'o/r' }))
  assert.ok(validators.runRequest({ ipcSchema: 2, command: 'subscribe', action: 'remove', repo: 'o/r' }))
})

test('RunRequest rejects malformed subscribe requests', () => {
  // missing action
  assert.equal(validators.runRequest({ ipcSchema: 2, command: 'subscribe' }), false)
  // add/remove require repo
  assert.equal(validators.runRequest({ ipcSchema: 2, command: 'subscribe', action: 'add' }), false)
  // list must not carry repo
  assert.equal(validators.runRequest({ ipcSchema: 2, command: 'subscribe', action: 'list', repo: 'o/r' }), false)
  // unknown action
  assert.equal(validators.runRequest({ ipcSchema: 2, command: 'subscribe', action: 'bogus' }), false)
  // malformed repo
  assert.equal(validators.runRequest({ ipcSchema: 2, command: 'subscribe', action: 'add', repo: 'not-a-repo' }), false)
})

test('non-subscribe commands must not carry subscribe fields', () => {
  assert.equal(validators.runRequest({ ipcSchema: 2, command: 'run', repo: 'o/r' }), false)
  assert.equal(validators.runRequest({ ipcSchema: 2, command: 'run', action: 'list' }), false)
})

test('RunResult carries optional branch/commit/prUrl/contentHash', () => {
  const rr = {
    ipcSchema: 2, ok: true, command: 'run',
    startedAt: '2026-10-30T00:00:00Z', finishedAt: '2026-10-30T00:05:00Z',
    skipped: false, files: [],
    branch: 'feat/x', commit: 'abc123', prUrl: 'https://github.com/o/r/pull/1',
    contentHash: fixtures.normal.contentHash,
  }
  assert.ok(validators.runResult(rr))
})

test('release fixtures validate against the releases schema', () => {
  for (const [name, doc] of [
    ['releaseNormal', fixtures.releaseNormal],
    ['releaseDegraded', fixtures.releaseDegraded],
    ['releaseEmpty', fixtures.releaseEmpty],
  ]) {
    assert.ok(validators.releases(doc), `${name} should be valid`)
  }
  assert.equal(validators.releases({ ...fixtures.releaseNormal, schema: 2 }), false)
})

test('ReleasesReport requires usage so zero cost is distinguishable', () => {
  const base = { enabled: true, repos: 0, newReleases: 0, durationMs: 0 }
  assert.equal(validators.releasesReport(base), false, 'usage is required')
  const zeroUsage = {
    promptTokens: 0, completionTokens: 0, totalTokens: 0, cachedTokens: 0,
    usageKnown: false, calls: 0, retries: 0, batches: 0, cacheHits: 0,
  }
  assert.ok(validators.releasesReport({ ...base, usage: zeroUsage }))
  assert.equal(
    validators.releasesReport({ ...base, usage: { ...zeroUsage, totalTokens: 'x' } }),
    false,
    'usage fields are typed',
  )
})

test('RunResult carries an optional releases report', () => {
  const rr = {
    ipcSchema: 2, ok: true, command: 'run',
    startedAt: '2026-10-30T00:00:00Z', finishedAt: '2026-10-30T00:05:00Z',
    skipped: false, files: [],
    releases: {
      enabled: true, repos: 2, newReleases: 4, summarized: 4, degraded: 0,
      failed: 0, rateLimited: 0, skipped: 0, truncated: 0, historyTruncated: 0,
      durationMs: 16851,
      usage: {
        promptTokens: 2918, completionTokens: 946, totalTokens: 3864, cachedTokens: 0,
        usageKnown: true, calls: 2, retries: 0, batches: 0, cacheHits: 0,
      },
    },
  }
  assert.ok(validators.runResult(rr))
  assert.equal(validators.runResult({ ...rr, releases: { ...rr.releases, newReleases: 'x' } }), false)
})

test('RunResult accepts an optional usage block', () => {
  const usage = {
    promptTokens: 10, completionTokens: 5, totalTokens: 15, cachedTokens: 0,
    usageKnown: true, calls: 1, retries: 0, batches: 1, cacheHits: 0,
  }
  const rr = {
    ipcSchema: 2, ok: true, command: 'run',
    startedAt: '2026-10-30T00:00:00Z', finishedAt: '2026-10-30T00:05:00Z',
    skipped: false, files: [], usage,
  }
  assert.ok(validators.runResult(rr))
  assert.equal(validators.runResult({ ...rr, usage: { ...usage, promptTokens: 'x' } }), false)
})
