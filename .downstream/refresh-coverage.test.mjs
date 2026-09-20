import assert from 'node:assert/strict'
import { test } from 'node:test'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync, spawnSync } from 'node:child_process'

test('review imports bind source bytes and reject unsupported claims before writing', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'sub2api-review-import-'))
  t.after(() => {
    assert.equal(path.dirname(root), fs.realpathSync(os.tmpdir()))
    assert.ok(path.basename(root).startsWith('sub2api-review-import-'))
    fs.rmSync(root, { recursive: true })
  })
  const git = (...args) => execFileSync('git', args, { cwd: root, stdio: 'pipe' }).toString().trim()
  git('init', '-q'); git('config', 'core.autocrlf', 'false')
  fs.writeFileSync(path.join(root, 'source.go'), 'package fixture\n')
  git('add', 'source.go'); git('-c', 'user.name=Review fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-qm', 'fixture')
  const upstream = git('rev-parse', 'HEAD')
  fs.mkdirSync(path.join(root, '.downstream'))
  fs.copyFileSync(fileURLToPath(new URL('./refresh-coverage.mjs', import.meta.url)), path.join(root, '.downstream/refresh-coverage.mjs'))
  const inventoryPath = path.join(root, '.downstream/coverage-v0.2.7.json')
  fs.writeFileSync(inventoryPath, JSON.stringify({ upstream_base: upstream, entries: [] }))
  fs.appendFileSync(path.join(root, 'source.go'), '// changed\n')
  const run = (...args) => spawnSync(process.execPath, ['.downstream/refresh-coverage.mjs', ...args], { cwd: root, encoding: 'utf8' })
  assert.equal(run().status, 0)
  const record = JSON.parse(fs.readFileSync(inventoryPath)).entries.find(entry => entry.path === 'source.go')
  Object.assign(record, { review_status: 'reviewed', decision: 'core-mechanism', owner: 'fixture', reason: 'The fixture contains only a package declaration and comment.', evidence: ['source inspection of exact fixture bytes'] })
  const reviewPath = path.join(root, '.git/review.json')
  const save = entries => fs.writeFileSync(reviewPath, JSON.stringify({ upstream_base: upstream, entries }))
  save([record])
  assert.equal(run('--import-review', reviewPath).status, 0)
  assert.equal(JSON.parse(fs.readFileSync(inventoryPath)).entries.find(entry => entry.path === 'source.go').review_status, 'reviewed')
  const before = fs.readFileSync(inventoryPath, 'utf8')
  for (const invalid of [
    { ...record, review_fingerprint: '0'.repeat(64) },
    { ...record, role: 'generated' },
    { ...record, evidence: [] },
    { ...record, path: '.downstream/coverage-v0.2.7.json' }
  ]) {
    save([record, invalid])
    assert.notEqual(run('--import-review', reviewPath).status, 0)
    assert.equal(fs.readFileSync(inventoryPath, 'utf8'), before)
  }
  save([{ ...record, review_fingerprint: '0'.repeat(64) }])
  assert.match(run('--import-review', reviewPath).stderr, /review source changed/)
  assert.notEqual(run('--check', '--import-review', reviewPath).status, 0)
  assert.equal(run('--check').status, 0)
  assert.notEqual(run('--check', '--require-complete').status, 0, 'other unreviewed files must keep the gate closed')
  fs.appendFileSync(path.join(root, 'source.go'), '// changed after review\n')
  assert.equal(run().status, 0)
  assert.equal(JSON.parse(fs.readFileSync(inventoryPath)).entries.find(entry => entry.path === 'source.go').review_status, 'pending')
})
