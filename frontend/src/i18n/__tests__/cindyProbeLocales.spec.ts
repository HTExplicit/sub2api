import { describe, expect, it } from 'vitest'
import en from '@/i18n/locales/en/admin/accounts'
import zh from '@/i18n/locales/zh/admin/accounts'

describe('Cindy balance probe locale semantics', () => {
  it('describes healthy results as Luna availability for this run, not a numeric balance claim', () => {
    expect(en.accounts.cindyProbe.counts.healthy).toBe('Luna available this run')
    expect(en.accounts.cindyProbe.itemState.healthy).toBe('Luna available this run')
    expect(zh.accounts.cindyProbe.counts.healthy).toBe('本次 Luna 可用')
    expect(zh.accounts.cindyProbe.itemState.healthy).toBe('本次 Luna 可用')
    expect(en.accounts.cindyProbe.checkedAt).toBe('Checked at')
    expect(zh.accounts.cindyProbe.checkedAt).toBe('检查时间')
    expect(en.accounts.cindyProbe.recent).toBe('Recent audit')
    expect(zh.accounts.cindyProbe.recent).toBe('最近巡检')
    expect(en.accounts.columns.recentCindyProbe).toBe('Recent Audit')
    expect(zh.accounts.columns.recentCindyProbe).toBe('最近巡检')
  })
})
