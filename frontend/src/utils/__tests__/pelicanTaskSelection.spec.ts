import { describe, expect, it } from 'vitest'
import { applyPelicanBulk, pelicanDefaultModel, pelicanDuplicateKeys, pelicanCombinationIssue } from '../pelicanTaskSelection'
import type { PelicanAccountOption } from '@/api/admin/pelicanTests'

const account = { id: 1, default_model_id: 'sol', manual_model_allowed: true, models: [
  { id: 'codex-auto-review', text_supported: false, capability_reason: 'not text', reasoning_efforts: [] },
  { id: 'sol', text_supported: true, reasoning_efforts: ['low', 'high'] }
] } as PelicanAccountOption
describe('Pelican combination policy', () => {
  it('honors only a valid backend default and leaves invalid defaults empty', () => {
    expect(pelicanDefaultModel(account)).toBe('sol')
    expect(pelicanDefaultModel({ ...account, default_model_id: 'codex-auto-review' })).toBe('')
    expect(pelicanCombinationIssue({ key: 1, account_id: 1, model_id: 'codex-auto-review', effort: '' }, account)).toBe('not text')
  })
  it('detects conflicting reasoning rows without discarding either selection', () => {
    const rows = [{ key: 1, account_id: 1, model_id: 'sol', effort: 'low' }, { key: 2, account_id: 1, model_id: ' sol ', effort: 'high' }]
    expect([...pelicanDuplicateKeys(rows)]).toEqual([1, 2])
    expect(rows.map(row => row.effort)).toEqual(['low', 'high'])
  })
  it('keeps incompatible bulk rows unchanged and permits explicit unverified manual input', () => {
    const rows = [{ key: 1, account_id: 1, model_id: 'sol', effort: '' }, { key: 2, account_id: 2, model_id: 'native', effort: 'low' }]
    const other = { ...account, id: 2, models: [{ id: 'native', text_supported: true, reasoning_efforts: ['low'] }] } as PelicanAccountOption
    const result = applyPelicanBulk(rows, { 1: account, 2: other }, 'sol', 'high')
    expect(result.rows[0].effort).toBe('high'); expect(result.rows[1]).toEqual(rows[1]); expect(result.skipped).toEqual([2])
    expect(pelicanCombinationIssue({ key: 3, account_id: 1, model_id: 'manual-model', effort: '' }, account)).toBe('')
  })
})
