#!/usr/bin/env node
// Convert the legacy v1 digest fixtures into v2 fixtures and (re)compute the
// RFC 8785 JCS contentHash for each, so fixtures carry a real, reproducible hash.
// Legacy v1 fixtures live at ../../fixtures (contract/fixtures); outputs go to
// ./fixtures (contract/v2/fixtures).
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import { fileURLToPath } from 'node:url'
import canonicalize from 'canonicalize'

const here = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.dirname(here)
const V1 = path.join(ROOT, '..', 'fixtures')
const OUT = path.join(ROOT, 'fixtures')

// optional RepoDigest fields that must be omitted when absent (nil in Go)
const REPO_OPTIONAL = ['description', 'forks']

function normalizeRepo(r) {
  const o = { ...r }
  for (const k of REPO_OPTIONAL) if (o[k] === undefined || o[k] === null) delete o[k]
  return o
}

function jcsHash(digest) {
  const basis = {
    schema: digest.schema,
    date: digest.date,
    period: digest.period,
    languages: digest.languages,
    count: digest.count,
    degraded: digest.degraded,
    repos: digest.repos.map(normalizeRepo),
  }
  const canon = canonicalize(basis)
  if (canon === undefined) throw new Error('canonicalize returned undefined')
  return 'sha256:' + crypto.createHash('sha256').update(canon, 'utf8').digest('hex')
}

function toV2(v1) {
  const d = {
    schema: 2,
    date: v1.date,
    period: v1.period,
    languages: v1.languages,
    count: v1.count,
    degraded: v1.degraded,
    contentHash: '',
    contentHashAlgo: 'jcs-sha256-v1',
    coreVersion: v1.coreVersion,
    generatedAt: v1.generatedAt,
    repos: v1.repos.map(normalizeRepo),
  }
  d.contentHash = jcsHash(d)
  return d
}

const map = {
  'digest-normal.json': 'digest-normal.json',
  'digest-degraded.json': 'digest-degraded.json',
  'digest-empty-lang.json': 'digest-empty-lang.json',
}

fs.mkdirSync(OUT, { recursive: true })
for (const [src, dst] of Object.entries(map)) {
  const v1 = JSON.parse(fs.readFileSync(path.join(V1, src), 'utf8'))
  const v2 = toV2(v1)
  fs.writeFileSync(path.join(OUT, dst), JSON.stringify(v2, null, 2) + '\n')
  console.log('wrote', dst, v2.contentHash)
}

// missing-optional fixture: derived from normal, first two repos, drop description+forks
const normal = JSON.parse(fs.readFileSync(path.join(OUT, 'digest-normal.json'), 'utf8'))
const mo = JSON.parse(JSON.stringify(normal))
mo.repos = mo.repos.slice(0, 2).map((r) => {
  const c = { ...r }
  delete c.description
  delete c.forks
  return c
})
mo.count = mo.repos.length
mo.contentHash = jcsHash(mo)
fs.writeFileSync(path.join(OUT, 'digest-missing-optional.json'), JSON.stringify(mo, null, 2) + '\n')
console.log('wrote digest-missing-optional.json', mo.contentHash)
