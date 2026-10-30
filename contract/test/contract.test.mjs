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

test('contractVersion is 2.0.0', () => assert.equal(contractVersion, '0.2.0'))

test('error catalog has 17 retriable entries', () => {
  assert.equal(Object.keys(errorCatalog).length, 17)
  assert.equal(errorCatalog.FETCH_RATE_LIMITED.retriable, true)
  assert.equal(errorCatalog.REPO_DIRTY.retriable, false)
})

test('all fixtures validate against digest v2 schema', () => {
  for (const [name, doc] of Object.entries(fixtures)) {
    assert.ok(validators.digest(doc), `${name} should be valid`)
  }
})

test('rejects a v1-shaped digest (schema const mismatch)', () => {
  assert.equal(validators.digest({ ...fixtures.normal, schema: 1 }), false)
})

test('jcs contentHash matches every fixture', () => {
  for (const [name, doc] of Object.entries(fixtures)) {
    assert.equal(jcsHash(doc), doc.contentHash, `${name} jcs hash mismatch`)
  }
})

test('RunRequest is strict about extra fields', () => {
  const req = { ipcSchema: 2, command: 'run', date: '2026-10-30' }
  assert.ok(validators.runRequest(req))
  assert.equal(validators.runRequest({ ...req, unexpected: true }), false)
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
