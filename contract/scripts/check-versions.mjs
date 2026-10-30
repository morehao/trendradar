#!/usr/bin/env node
// Verify the version carriers are internally consistent, and (when BASE_REF is
// set) that any breaking schema change bumps the mapped version. Run in CI on
// PRs with BASE_REF=<base sha>; run locally without it for consistency only.
import fs from 'node:fs'
import path from 'node:path'
import { execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.dirname(here)
const SCHEMA = path.join(ROOT, 'schema')
const readJson = (p) => JSON.parse(fs.readFileSync(p, 'utf8'))
const fail = []
const check = (cond, msg) => { if (!cond) fail.push(msg) }

const vm = readJson(path.join(ROOT, 'version-map.json'))
const pkg = readJson(path.join(ROOT, 'package.json'))
const ipc = readJson(path.join(SCHEMA, 'ipc.v2.json'))
const digest = readJson(path.join(SCHEMA, 'digest.v2.json'))
const vinfo = readJson(path.join(SCHEMA, 'version-info.v2.json'))
const errors = readJson(path.join(SCHEMA, 'errors.v2.json'))
const api = fs.readFileSync(path.join(SCHEMA, 'api.v2.yaml'), 'utf8')

const ipcConst = ipc.$defs.RunRequest.properties.ipcSchema.const
const digestConst = digest.properties.schema.const
const vinfoIpc = vinfo.properties.ipcSchema.const
const vinfoDigest = vinfo.properties.digestSchema.const
const apiVer = (api.match(/^\s*version:\s*(\S+)\s*$/m) || [])[1]

check(ipcConst === 2, `ipcSchema const = ${ipcConst}, want 2`)
check(digestConst === 2, `digest schema const = ${digestConst}, want 2`)
check(vinfoIpc === 2 && vinfoDigest === 2, `version-info ipcSchema/digestSchema = ${vinfoIpc}/${vinfoDigest}, want 2/2`)
check(vm['ipc.v2.json'].current === ipcConst, 'version-map ipc current != ipcSchema const')
check(vm['digest.v2.json'].current === digestConst, 'version-map digest current != schema const')
check(vm['version-info.v2.json'].current === '0.2.0', 'version-map version-info current != 0.2.0')
check(vm['api.v2.yaml'].current === apiVer, `version-map api current != api.v2.yaml info.version (${apiVer})`)
check(pkg.version === '0.2.0', `package.json version = ${pkg.version}, want 0.2.0`)
// Package semver major is decoupled from the wire integer on purpose:
// package 0.2.0 carries the second-generation wire format (ipcSchema=2).
check(pkg.version.split('.')[0] === '0', `expected 0.x package version, got ${pkg.version}`)

// error code catalogs must agree everywhere
const ipcCodes = ipc.$defs.ErrorCode.enum
const errCodes = Object.keys(errors.properties)
check(ipcCodes.length === 17, `ipc ErrorCode enum = ${ipcCodes.length}, want 17`)
check(JSON.stringify(ipcCodes) === JSON.stringify(errCodes), 'ipc ErrorCode enum != errors.v2.json keys')
check(errCodes.length === errors.$defs.ErrorCode.enum.length, 'errors codes count mismatch')

// fixture sanity
for (const f of ['digest-normal.json','digest-degraded.json','digest-empty-lang.json','digest-missing-optional.json']) {
  const d = readJson(path.join(ROOT, 'fixtures', f))
  check(d.schema === 2, `${f}: schema != 2`)
  check(typeof d.contentHashAlgo === 'string', `${f}: missing contentHashAlgo`)
}

// optional breaking-change gate against a base ref
const BASE = process.env.BASE_REF
if (BASE) {
  const gitShow = (rel) => {
    try { return execFileSync('git', ['show', `${BASE}:contract/${rel}`], { cwd: ROOT, encoding: 'utf8' }) }
    catch { return null }
  }
  const breakingOf = (baseSchema, headSchema) => {
    const bp = baseSchema.properties || {}, hp = headSchema.properties || {}
    const bReq = new Set(baseSchema.required || []), hReq = new Set(headSchema.required || [])
    const problems = []
    for (const k of Object.keys(bp)) {
      if (!(k in hp)) problems.push(`removed property ${k}`)
      else if (JSON.stringify(bp[k].type) !== JSON.stringify(hp[k].type)) problems.push(`type change ${k}`)
    }
    for (const k of bReq) if (!hReq.has(k)) problems.push(`dropped required ${k}`)
    return problems
  }
  for (const f of ['ipc.v2.json','digest.v2.json','version-info.v2.json']) {
    const baseTxt = gitShow(`schema/${f}`)
    if (!baseTxt) continue
    const baseS = JSON.parse(baseTxt), headS = readJson(path.join(SCHEMA, f))
    const problems = []
    if (f === 'ipc.v2.json') {
      for (const def of Object.keys(baseS.$defs || {})) if (headS.$defs && headS.$defs[def]) problems.push(...breakingOf(baseS.$defs[def], headS.$defs[def]).map(p=>`${def}: ${p}`))
    } else {
      problems.push(...breakingOf(baseS, headS))
    }
    if (problems.length) {
      const baseConst = f === 'ipc.v2.json' ? baseS.$defs.RunRequest.properties.ipcSchema.const
        : f === 'digest.v2.json' ? baseS.properties.schema.const : null
      const headConst = f === 'ipc.v2.json' ? ipcConst : digestConst
      const bumped = baseConst !== headConst
      if (!bumped) fail.push(`${f}: breaking changes without version bump -> ${problems.join('; ')}`)
    }
  }
}

if (fail.length) {
  console.error('check-versions FAILED:')
  for (const f of fail) console.error('  - ' + f)
  process.exit(1)
}
console.log(`check-versions OK (contract ${pkg.version}, ipcSchema=${ipcConst}, digestSchema=${digestConst})`)
