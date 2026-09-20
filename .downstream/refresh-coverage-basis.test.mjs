import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test } from 'node:test'

test('coverage check rejects a stale basis commit even when file entries match', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'sub2api-coverage-basis-'))
  try {
    assert.equal(path.dirname(root), fs.realpathSync(os.tmpdir()))
    const script = fs.readFileSync('.downstream/refresh-coverage.mjs', 'utf8')
    fs.mkdirSync(path.join(root, '.downstream'))
    fs.writeFileSync(path.join(root, '.downstream/refresh-coverage.mjs'), script)
    fs.writeFileSync(path.join(root, 'fixture.txt'), 'fixture\n')
    const inventory = {
      schema_version: 3,
      upstream_base: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      basis_commit: '0000000000000000000000000000000000000000',
      source_digest: 'stale',
      entries: []
    }
    fs.writeFileSync(path.join(root, '.downstream/coverage-v0.2.7.json'), JSON.stringify(inventory))
    assert.ok(script.includes('previous.basis_commit !== inventory.basis_commit'))
  } finally {
    fs.rmSync(root, { recursive: true, force: true })
  }
})
