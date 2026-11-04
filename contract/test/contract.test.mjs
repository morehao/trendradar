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

test('contractVersion is 0.6.0', () => assert.equal(contractVersion, '0.6.0'))

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

test('rejects a v2 RunResult (ipcSchema const mismatch)', () => {
  const rr = { ipcSchema: 2, ok: true, command: 'run', startedAt: '2026-10-30T00:00:00Z', finishedAt: '2026-10-30T00:05:00Z', skipped: false, files: [] }
  assert.equal(validators.runResult(rr), false)
  assert.ok(validators.runResult({ ...rr, ipcSchema: 3 }))
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
  for (const name of ['releaseNormal', 'releaseDegraded', 'releaseEmpty', 'releaseWeekly']) {
    assert.equal(releaseHash(fixtures[name]), fixtures[name].contentHash, `${name} jcs hash mismatch`)
  }
})

test('RunRequest is strict about extra fields', () => {
  const req = { ipcSchema: 3, command: 'run', date: '2026-10-30' }
  assert.ok(validators.runRequest(req))
  assert.equal(validators.runRequest({ ...req, unexpected: true }), false)
})

test('RunRequest accepts subscribe with action', () => {
  assert.ok(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'list' }))
  assert.ok(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'add', repo: 'o/r' }))
  assert.ok(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'remove', repo: 'o/r' }))
})

test('RunRequest rejects malformed subscribe requests', () => {
  // missing action
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'subscribe' }), false)
  // add/remove require repo
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'add' }), false)
  // list must not carry repo
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'list', repo: 'o/r' }), false)
  // unknown action
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'bogus' }), false)
  // malformed repo
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'add', repo: 'not-a-repo' }), false)
})

test('non-subscribe commands must not carry subscribe fields', () => {
  // run: repo is allowed only together with stages including 'releases' (see below);
  // a bare run+repo with no stages is still rejected.
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'run', repo: 'o/r' }), false)
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'run', action: 'list' }), false)
})

test('RunRequest.run may restrict releases to a single repo', () => {
  // allowed: run + stages releases + repo
  assert.ok(validators.runRequest({ ipcSchema: 3, command: 'run', stages: ['releases'], repo: 'o/r' }))
  // backward compatible: run + stages releases without repo (all repos)
  assert.ok(validators.runRequest({ ipcSchema: 3, command: 'run', stages: ['releases'] }))
  // repo requires stages to include 'releases'
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'run', stages: ['trending'], repo: 'o/r' }), false)
  // repo without stages is rejected (needs explicit releases stage)
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'run', repo: 'o/r' }), false)
  // run must still not carry action/volume
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'run', action: 'list' }), false)
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'run', volume: 1 }), false)
  // malformed repo
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'run', stages: ['releases'], repo: 'not-a-repo' }), false)
  // serve/config/version still must not carry repo
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'serve', repo: 'o/r' }), false)
})

test('RunResult carries optional branch/commit/prUrl/contentHash', () => {
  const rr = {
    ipcSchema: 3, ok: true, command: 'run',
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
    ['releaseWeekly', fixtures.releaseWeekly],
  ]) {
    assert.ok(validators.releases(doc), `${name} should be valid`)
  }
  // schema=2 is the only accepted document version; v1 files still render
  // through serve (which does not validate) but do not satisfy the schema.
  assert.equal(validators.releases({ ...fixtures.releaseNormal, schema: 1 }), false)
})

test('ReleasesReport requires usage so zero cost is distinguishable', () => {
  const base = { enabled: true, repos: 0, checked: 0, changed: 0, newReleases: 0, durationMs: 0 }
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
    ipcSchema: 3, ok: true, command: 'run',
    startedAt: '2026-10-30T00:00:00Z', finishedAt: '2026-10-30T00:05:00Z',
    skipped: false, files: [],
    releases: {
      enabled: true, repos: 2, checked: 2, changed: 2, newReleases: 4, summarized: 4, degraded: 0,
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
    ipcSchema: 3, ok: true, command: 'run',
    startedAt: '2026-10-30T00:00:00Z', finishedAt: '2026-10-30T00:05:00Z',
    skipped: false, files: [], usage,
  }
  assert.ok(validators.runResult(rr))
  assert.equal(validators.runResult({ ...rr, usage: { ...usage, promptTokens: 'x' } }), false)
})

test('SubscriptionList keeps repos for old consumers and adds items', () => {
  const list = fixtures.subscriptionsNormal
  assert.ok(validators.subscriptionList(list))
  assert.ok(validators.subscriptionList({ repos: [] }), 'items is optional')
  assert.ok(validators.subscriptionList(list), 'items accepted')
  assert.equal(
    validators.subscriptionList({ ...list, items: [{ fullName: 'o/r', status: 'bogus' }] }),
    false,
    'status is a closed enum',
  )
  assert.equal(
    validators.subscriptionList({ ...list, repos: ['not-a-repo'] }),
    false,
    'repos keeps the owner/repo pattern',
  )
  const statuses = new Set(list.items.map((i) => i.status))
  for (const s of ['ok', 'no_change', 'baseline', 'rate_limited', 'failed']) {
    assert.ok(statuses.has(s), `fixture covers status ${s}`)
  }
})

test('the 40-repo subscription fixture stays inside the first-paint budget', () => {
  const list = fixtures.subscriptions40
  assert.equal(list.items.length, 40)
  const bytes = Buffer.byteLength(JSON.stringify(list), 'utf8')
  assert.ok(bytes <= 10 * 1024, `payload ${bytes} B exceeds 10 KB`)
})

test('SubscriptionDoc is the markdown file plus its volume index', () => {
  const doc = fixtures.subscriptionDocNormal
  assert.ok(validators.subscriptionDoc(doc))
  assert.equal(validators.subscriptionDoc({ ...doc, volume: 0 }), false, 'volume is 1-based')
  assert.equal(validators.subscriptionDoc({ ...doc, fullName: 'not a repo' }), false)
  const { markdown, ...rest } = doc
  assert.equal(validators.subscriptionDoc(rest), false, 'markdown is required')
})

test('RunRequest carries optional stages for a release-only run', () => {
  const base = { ipcSchema: 3, command: 'run' }
  assert.ok(validators.runRequest(base), 'absent stages means every stage')
  assert.ok(validators.runRequest({ ...base, stages: ['releases'] }))
  assert.ok(validators.runRequest({ ...base, stages: ['trending', 'digest'] }))
  assert.equal(validators.runRequest({ ...base, stages: ['bogus'] }), false)
  assert.equal(validators.runRequest({ ...base, stages: 'releases' }), false)
  // stages is a run-only knob.
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'version', stages: ['releases'] }), false)
  assert.equal(
    validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'list', stages: ['releases'] }),
    false,
  )
})

test('RunRequest accepts subscribe releases with an optional volume', () => {
  assert.ok(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'releases', repo: 'o/r' }))
  assert.ok(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'releases', repo: 'o/r', volume: 2 }))
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'releases' }), false)
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'list', volume: 2 }), false)
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'remove', repo: 'o/r', volume: 1 }), false)
  assert.equal(validators.runRequest({ ipcSchema: 3, command: 'subscribe', action: 'releases', repo: 'o/r', volume: 0 }), false)
})

test('RunResult can carry a subscription list or document envelope', () => {
  const base = {
    ipcSchema: 3, ok: true, command: 'subscribe',
    startedAt: '2026-11-01T00:00:00Z', finishedAt: '2026-11-01T00:00:01Z',
    skipped: false, files: [],
  }
  assert.ok(validators.runResult({ ...base, subscriptions: fixtures.subscriptionsNormal }))
  assert.ok(validators.runResult({ ...base, subscriptionDoc: fixtures.subscriptionDocNormal }))
})
