import { webcrypto } from 'node:crypto'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { accountKeyDigestTerm, isAccountKeyDigestTerm } from '../accountKeySearch'

afterEach(() => { vi.unstubAllGlobals() })

describe('account key search terms', () => {
  it('builds the digest term of the trimmed text', async () => {
    vi.stubGlobal('crypto', webcrypto)
    // SHA-256("abc"), the FIPS 180 example
    await expect(accountKeyDigestTerm(' \tabc\r\n')).resolves.toBe(
      'sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'
    )
  })

  it('has no digest term for an empty text or without crypto.subtle', async () => {
    vi.stubGlobal('crypto', webcrypto)
    await expect(accountKeyDigestTerm('   ')).resolves.toBeNull()
    vi.stubGlobal('crypto', {})
    await expect(accountKeyDigestTerm('abc')).resolves.toBeNull()
  })

  it('recognises only a complete lowercase digest term', () => {
    const term = `sha256:${'0f'.repeat(32)}`
    expect(isAccountKeyDigestTerm(term)).toBe(true)
    for (const value of [term.toUpperCase(), `sha256:${'0F'.repeat(32)}`, term.slice(0, -1), `${term}0`, ` ${term}`, 'abc', '']) {
      expect(isAccountKeyDigestTerm(value)).toBe(false)
    }
  })
})
