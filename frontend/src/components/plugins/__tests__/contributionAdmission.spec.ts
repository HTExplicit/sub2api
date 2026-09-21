import { describe, expect, it } from 'vitest'
import vectors from '../../../../../backend/internal/service/testdata/plugin_contribution_buckets.json'
import type { PluginContribution } from '@/api/admin/plugins'
import { contributionAccountBucket, contributionAdmission } from '../contributionAdmission'

const item: PluginContribution = { id: 'fixture', slot: 'account.actions', permission: 'admin', plugin_id: 1, label: {}, available: true,
  account_scope: { version: 1, bindings: [{ platform: 'openai', account_type: 'oauth', rollout_percent: 50 }, { platform: 'openai', account_type: 'setup-token', rollout_percent: 80 }] } }
const account = (id: number, type = 'oauth') => ({ id, type, platform: 'openai', status: 'active', parent_account_id: null })

describe('contribution account admission', () => {
  it('matches the host uint64 bucket vectors exactly', () => {
    for (const vector of vectors) expect(contributionAccountBucket(vector.id)).toBe(vector.bucket)
    expect(contributionAccountBucket(Number.MAX_SAFE_INTEGER + 1)).toBeNull()
    expect(contributionAccountBucket(0)).toBeNull()
  })
  it('requires the complete selected set and matches intersected scope without globally hiding partial contributions', () => {
    expect(contributionAdmission(item, { account: account(2) }).allowed).toBe(true)
    expect(contributionAdmission(item, { account: account(3) }).allowed).toBe(false)
    expect(contributionAdmission(item, { account: account(3, 'setup-token') }).allowed).toBe(true)
    expect(contributionAdmission(item, { accountIds: [2, 3], accounts: [account(2)] })).toEqual({ allowed: false, reason: 'unknown' })
    expect(contributionAdmission(item, { accountIds: [2, 3], accounts: [account(2), account(3)] }).allowed).toBe(false)
    expect(contributionAdmission({ ...item, account_scope: undefined }, { account: account(3) }).allowed).toBe(true)
    expect(contributionAdmission({ ...item, slot: 'admin.settings' }).allowed).toBe(true)
  })
})
