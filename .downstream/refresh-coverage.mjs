// Rebuild the inventory, preserving only review decisions for identical bytes.
// Classification is an audit aid; it never marks a path reviewed automatically.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const destination = path.join(root, '.downstream/coverage-v0.2.7.json')
const previous = JSON.parse(fs.readFileSync(destination, 'utf8'))
const upstream = previous.upstream_base
const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', maxBuffer: 32 << 20 })
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex')
const priorOwners = new Map([...Object.entries(previous.candidate_owners || {}).flatMap(([owner, paths]) => paths.map(file => [file, owner])), ...(previous.entries || []).map(entry => [entry.path, entry.candidate_owner])])
const priorEntries = new Map((previous.entries || []).map(entry => [entry.path, entry]))
const paths = [...new Set([...git('diff', '--name-only', '-z', upstream).split('\0'), ...git('ls-files', '--others', '--exclude-standard', '-z').split('\0')].filter(Boolean))].sort()

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
  if (/remote_skill_seed\/tree\//.test(file)) return 'bundled-source'
  if (/\.md$|^\.downstream\/.+\.json$|^\.superpowers\//.test(file)) return 'documentation'
  if (/(?:_test\.go|\.spec\.ts|\.test\.[jt]s)$|\/testdata\//.test(file)) return 'test-or-fixture'
  return 'source'
}

const entries = paths.map(file => {
  const target = path.resolve(root, file)
  if (!target.startsWith(root + path.sep)) throw new Error('inventory target escaped repository')
  let bytes = Buffer.alloc(0), present = false
  if (fs.existsSync(target)) {
    const stat = fs.lstatSync(target)
    bytes = stat.isSymbolicLink() ? Buffer.from(fs.readlinkSync(target)) : fs.readFileSync(target)
    present = true
  }
  const metadata = /^\.downstream\/(?:coverage-v0\.2\.7\.(?:json|md)|implementation-v0\.2\.7\.md)$/.test(file)
  const digest = present && !metadata ? sha256(bytes) : null
  const prior = priorEntries.get(file)
  return { path: file, candidate_owner: ownerOf(file), role: roleOf(file, bytes), present, content_sha256: digest,
    review_status: prior?.content_sha256 === digest && digest != null ? prior.review_status : 'pending',
    ...(prior?.content_sha256 === digest && digest != null && prior.evidence ? { evidence: prior.evidence } : {}) }
})
const candidateOwners = {}
for (const entry of entries) (candidateOwners[entry.candidate_owner] ||= []).push(entry.path)
const generated = entries.filter(entry => ['generated', 'asset', 'dependency-lock'].includes(entry.role)).length
const inventory = {
  schema_version: 2, upstream_base: upstream, basis_commit: git('rev-parse', 'HEAD').trim(),
  scope: 'Current Sub2API downstream source and working tree. Candidate ownership does not establish completed review. Root maintenance, independent services and Worker require a separate audit.',
  source_digest: sha256(entries.filter(entry => entry.content_sha256 != null).map(entry => `${entry.path}\0${entry.content_sha256}`).join('\n')),
  digest_excludes: ['inventory and implementation-state documents; missing paths have present=false'],
  total_paths: entries.length, generated_assets_and_locks: generated,
  review_complete: false,
  role_counts: Object.fromEntries([...new Set(entries.map(entry=>entry.role))].sort().map(role=>[role, entries.filter(entry=>entry.role===role).length])),
  owner_counts: Object.fromEntries(Object.entries(candidateOwners).map(([owner, files]) => [owner, files.length])), entries,
}
fs.writeFileSync(destination, JSON.stringify(inventory, null, 2) + '\n')
console.log(JSON.stringify({ total_paths: inventory.total_paths, generated_assets_and_locks: generated, source_digest: inventory.source_digest,
  candidate_owners: Object.fromEntries(Object.entries(candidateOwners).map(([owner, files]) => [owner, files.length])) }))
