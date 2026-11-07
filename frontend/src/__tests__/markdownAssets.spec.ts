import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import zhDocument from '../../../docs/legal/admin-compliance.zh.md?raw'
import enDocument from '../../../docs/legal/admin-compliance.en.md?raw'

const sourceDirectory = dirname(fileURLToPath(import.meta.url))

describe('legal document asset imports', () => {
  it('preserves both source documents as raw text during related dependency tracing', () => {
    expect(zhDocument).toBe(readFileSync(resolve(sourceDirectory, '../../../docs/legal/admin-compliance.zh.md'), 'utf8'))
    expect(enDocument).toBe(readFileSync(resolve(sourceDirectory, '../../../docs/legal/admin-compliance.en.md'), 'utf8'))
  })
})
