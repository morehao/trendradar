#!/usr/bin/env node
// Prove every property of the legacy v1 schemas is covered by the v2 schemas.
// Unmapped v1 fields (deletions) are failures; new v2 fields are expected and
// listed as informational. Also asserts the error-code set is unchanged.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.dirname(here)
const V2 = path.join(ROOT, 'schema')
const V1 = path.join(ROOT, 'legacy-v1', 'schema') // legacy contract/legacy-v1/schema
const readJson = (p) => JSON.parse(fs.readFileSync(p, 'utf8'))

function propsOf(schema, defName) {
  const s = defName === '@root' ? schema : schema.$defs[defName]
  if (!s) return null
  if (s.allOf) {
    const keys = {}
    for (const part of s.allOf) {
      if (part.$ref) {
        const name = part.$ref.split('/').pop()
        Object.assign(keys, propsOf(schema, name) || {})
      } else Object.assign(keys, part.properties || {})
    }
    return keys
  }
  return s.properties || {}
}

const maps = [
  { v1: 'model.v1.json', v2: 'model.v2.json', pairs: [['RepoSnapshot','RepoSnapshot'],['RepoDigest','RepoDigest']] },
  { v1: 'ipc.v1.json', v2: 'ipc.v2.json', pairs: [['RunRequest','RunRequest'],['RunResult','RunResult'],['ErrorInfo','ErrorInfo'],['FileArtifact','FileArtifact'],['StateRun','StateRun'],['State','State']] },
  { v1: 'digest.v1.json', v2: 'digest.v2.json', pairs: [['@root','@root']] },
]

const failures = []
for (const m of maps) {
  const v1 = readJson(path.join(V1, m.v1))
  const v2 = readJson(path.join(V2, m.v2))
  for (const [d1, d2] of m.pairs) {
    const a = propsOf(v1, d1) || {}
    const b = propsOf(v2, d2) || {}
    const missing = Object.keys(a).filter((k) => !(k in b))
    const added = Object.keys(b).filter((k) => !(k in a))
    if (missing.length) failures.push(`${m.v2} ${d2}: v1 fields not covered in v2 -> ${missing.join(', ')}`)
    if (added.length) console.log(`info: ${m.v2} ${d2}: new v2 fields -> ${added.join(', ')}`)
  }
}

// error-code set unchanged
const v1Err = readJson(path.join(V1, 'ipc.v1.json')).$defs.ErrorCode.enum
const v2Err = readJson(path.join(V2, 'errors.v2.json'))
const v2Codes = Object.keys(v2Err.properties)
const removed = v1Err.filter((c) => !v2Codes.includes(c))
if (removed.length) failures.push(`error codes removed vs v1 -> ${removed.join(', ')}`)

if (failures.length) {
  console.error('check-field-map FAILED:')
  for (const f of failures) console.error('  - ' + f)
  process.exit(1)
}
console.log('check-field-map OK: all v1 fields mapped into v2')
