// Rebuild the inventory, preserving only review decisions for identical bytes.
// Classification is an audit aid. Only the derived inventory itself receives an
// automatic structural verdict; source and documentation require real evidence.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const destination = path.join(root, '.downstream/coverage-v0.2.7.json')
const previous = JSON.parse(fs.readFileSync(destination, 'utf8'))
const upstream = previous.upstream_base
if (!/^[a-f0-9]{40}$/.test(upstream)) throw new Error('review requires an exact upstream commit')
const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', maxBuffer: 32 << 20 })
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex')
const checkOnly = process.argv.includes('--check')
const requireComplete = process.argv.includes('--require-complete')
const reviewFiles = []
for (let index = 2; index < process.argv.length; index++) {
  const argument = process.argv[index]
  if (argument === '--import-review') {
    const file = process.argv[++index]
    if (!file || file.startsWith('--')) throw new Error('--import-review requires a review JSON file')
    reviewFiles.push(file)
  } else if (!['--check', '--require-complete'].includes(argument)) throw new Error(`unknown argument: ${argument}`)
}
if (checkOnly && reviewFiles.length) throw new Error('--check cannot import review decisions')
const decisions = new Set(['plugin', 'core-correctness', 'core-mechanism', 'adapter', 'upstream-covered', 'generated-source', 'reference'])
const validReview = entry => entry?.review_status === 'reviewed' && decisions.has(entry.decision) &&
  typeof entry.owner === 'string' && entry.owner.trim() && typeof entry.reason === 'string' && entry.reason.trim() &&
  Array.isArray(entry.evidence) && entry.evidence.length > 0 && entry.evidence.every(item => typeof item === 'string' && item.trim())
const priorOwners = new Map([...Object.entries(previous.candidate_owners || {}).flatMap(([owner, paths]) => paths.map(file => [file, owner])), ...(previous.entries || []).map(entry => [entry.path, entry.candidate_owner])])
const priorEntries = new Map((previous.entries || []).map(entry => [entry.path, entry]))
const paths = [...new Set([...git('diff', '--name-only', '-z', upstream).split('\0'), ...git('ls-files', '--others', '--exclude-standard', '-z').split('\0')].filter(Boolean))].sort()
function present(file) {
  try { fs.lstatSync(path.join(root, file)); return true }
  catch (error) { if (error.code === 'ENOENT') return false; throw error }
}
const missing = paths.filter(file => !present(file))
const deletedBlobs = new Map()
if (missing.length) {
  for (const record of git('ls-tree', '-r', '-z', upstream, '--', ...missing).split('\0').filter(Boolean)) {
    const split = record.indexOf('\t')
    deletedBlobs.set(record.slice(split + 1), record.slice(0, split).split(' ')[2])
  }
}

function ownerOf(file) {
  if (file.startsWith('plugins/')) return file.split('/')[1] === 'bundle.source.json' ? 'core-extension-and-job-host' : file.split('/')[1]
  if (/\/plugin[^/]*\.(?:go|ts|vue)$|\/plugins\/|backend\/pkg\/(?:extensionapi|pluginapi)\/|internal\/testextensions\//.test(file)) return 'core-extension-and-job-host'
  if (/^\.github\/|^Dockerfile|^deploy\/|^\.dockerignore$|^\.gitignore$|refresh-coverage\.mjs$/.test(file)) return 'operations'
  if (/remote_skill|business_system_prompt|systemPrompt|SystemPrompt/.test(file)) return 'prompt-skills'
  if (/^\.downstream\/|^\.superpowers\/|\.md$/.test(file)) return 'documentation-and-history'
  if (/cindy/i.test(file)) return 'cindy-provider'
  if (/image_studio|ImageStudio|codex_image_generation_bridge/.test(file)) return 'image-tools'
  if (/account_traffic_observe|styles\/theme\.css|tailwind\.config/.test(file)) return 'admin-observability'
  if (/account_quota|quota_estimate|quota_activity/.test(file)) return 'core-quota-and-protocol'
  return priorOwners.get(file) || 'mixed-requires-review'
}

function roleOf(file, bytes) {
  if (/\.(?:png|jpg|jpeg|webp|svg|ico|woff2|pdf)$/.test(file)) return 'asset'
  if (/(?:^|\/)(?:go\.sum|pnpm-lock\.yaml|package-lock\.json)$/.test(file)) return 'dependency-lock'
  if (/\.pb\.go$/.test(file) || /^backend\/ent\//.test(file) && !/^backend\/ent\/schema\//.test(file) || /Code generated.*DO NOT EDIT/.test(bytes.subarray(0, 400).toString('utf8'))) return 'generated'
  if (/remote_skill_seed\/tree\/|plugins\/prompt-skills\/registry\/seed\/(?:tree|pinned)\//.test(file)) return 'bundled-source'
  if (/\.md$|^\.downstream\/.+\.json$|^\.superpowers\//.test(file)) return 'documentation'
  if (/(?:_test\.go|\.spec\.ts|\.test\.[jt]s)$|\/testdata\//.test(file)) return 'test-or-fixture'
  return 'source'
}

const entries = paths.map(file => {
  const target = path.resolve(root, file)
  if (!target.startsWith(root + path.sep)) throw new Error('inventory target escaped repository')
  let bytes = Buffer.alloc(0)
  const exists = present(file)
  if (exists) {
    const stat = fs.lstatSync(target)
    bytes = stat.isSymbolicLink() ? Buffer.from(fs.readlinkSync(target)) : fs.readFileSync(target)
  }
  const metadata = file === '.downstream/coverage-v0.2.7.json'
  const digest = exists && !metadata ? sha256(bytes) : null
  const baseBlob = !exists ? deletedBlobs.get(file) || null : null
  const fingerprint = metadata ? null : digest || sha256(`deleted\0${upstream}\0${file}\0${baseBlob || 'absent'}`)
  const prior = priorEntries.get(file)
  const reusable = prior?.review_fingerprint === fingerprint && fingerprint != null && validReview(prior)
  return { path: file, candidate_owner: ownerOf(file), role: roleOf(file, bytes), present: exists, content_sha256: digest,
    ...(!exists ? { deleted_upstream_blob: baseBlob } : {}), review_fingerprint: fingerprint,
    review_status: reusable ? 'reviewed' : 'pending',
    ...(reusable ? { decision: prior.decision, owner: prior.owner, reason: prior.reason, evidence: prior.evidence } : {}) }
})
// Import explicit human/agent review conclusions only after checking every
// record against the current inventory. No file is written on a failed import.
const imported = new Set()
const byPath = new Map(entries.map(entry => [entry.path, entry]))
for (const file of reviewFiles) {
  const review = JSON.parse(fs.readFileSync(file, 'utf8'))
  if (review.upstream_base !== upstream || !Array.isArray(review.entries) || !review.entries.length) {
    throw new Error(`review does not identify this upstream baseline: ${file}`)
  }
  for (const record of review.entries) {
    if (record.review_status === 'pending') continue
    const entry = byPath.get(record.path)
    if (!entry || imported.has(record.path) || record.path === '.downstream/coverage-v0.2.7.json' || !validReview(record)) {
      throw new Error(`invalid, duplicate or unknown review record: ${record.path}`)
    }
    if (entry.role !== record.role || entry.review_fingerprint !== record.review_fingerprint ||
        entry.content_sha256 !== record.content_sha256 ||
        (!entry.present && entry.deleted_upstream_blob !== record.deleted_upstream_blob)) {
      throw new Error(`review source changed: ${record.path}`)
    }
    Object.assign(entry, { review_status: 'reviewed', decision: record.decision, owner: record.owner,
      reason: record.reason, evidence: record.evidence })
    imported.add(record.path)
  }
}
const sourceDigest = sha256(entries.filter(entry => entry.review_fingerprint != null).map(entry => `${entry.path}\0${entry.review_fingerprint}`).join('\n'))
const metadata = entries.find(entry => entry.path === '.downstream/coverage-v0.2.7.json')
if (metadata) Object.assign(metadata, {
  review_fingerprint: sha256(`derived-inventory\0${sourceDigest}`), review_status: 'reviewed',
  decision: 'generated-source', owner: 'operations',
  reason: 'Derived path, digest and verdict inventory; this structural check does not review the listed source files.',
  evidence: ['node .downstream/refresh-coverage.mjs --check']
})
const candidateOwners = {}
for (const entry of entries) (candidateOwners[entry.candidate_owner] ||= []).push(entry.path)
const generated = entries.filter(entry => ['generated', 'asset', 'dependency-lock'].includes(entry.role)).length
const inventory = {
  schema_version: 3, upstream_base: upstream, basis_commit: git('rev-parse', 'HEAD').trim(),
  scope: 'Current Sub2API downstream source and working tree. Candidate ownership does not establish completed review. Root maintenance, independent services and Worker require a separate audit.',
  source_digest: sourceDigest,
  digest_excludes: ['the inventory itself uses a derived structural fingerprint; deletions bind the upstream blob'],
  total_paths: entries.length, generated_assets_and_locks: generated,
  review_complete: entries.length > 0 && entries.every(validReview),
  pending_paths: entries.filter(entry => !validReview(entry)).map(entry => entry.path),
  role_counts: Object.fromEntries([...new Set(entries.map(entry=>entry.role))].sort().map(role=>[role, entries.filter(entry=>entry.role===role).length])),
  owner_counts: Object.fromEntries(Object.entries(candidateOwners).map(([owner, files]) => [owner, files.length])), entries,
}
if (checkOnly) {
  if (previous.schema_version !== inventory.schema_version || previous.upstream_base !== inventory.upstream_base || previous.basis_commit !== inventory.basis_commit || previous.source_digest !== sourceDigest ||
      JSON.stringify(previous.entries) !== JSON.stringify(entries) || previous.review_complete !== inventory.review_complete) {
    throw new Error('coverage inventory is stale or contains unsupported review claims; refresh it before using the results')
  }
} else fs.writeFileSync(destination, JSON.stringify(inventory, null, 2) + '\n')
console.log(JSON.stringify({ total_paths: inventory.total_paths, generated_assets_and_locks: generated, source_digest: inventory.source_digest,
  review_complete: inventory.review_complete, pending_paths: inventory.pending_paths.length, imported_reviews: imported.size,
  candidate_owners: Object.fromEntries(Object.entries(candidateOwners).map(([owner, files]) => [owner, files.length])) }))
if (requireComplete && !inventory.review_complete) throw new Error(`${inventory.pending_paths.length} paths still require review`)
