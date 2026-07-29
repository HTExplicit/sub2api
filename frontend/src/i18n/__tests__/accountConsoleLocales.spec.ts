import { describe, expect, it } from 'vitest'
import enAccounts from '../locales/en/admin/accounts'
import enCommon from '../locales/en/common'
import zhAccounts from '../locales/zh/admin/accounts'
import zhCommon from '../locales/zh/common'

describe('account console locale keys', () => {
  it('defines every label introduced by the cockpit account console', () => {
    expect(enAccounts.accounts.columns.classificationRoute).toBe('Classification / Routing')
    expect(enAccounts.accounts.columns.capacityUsage).toBe('Capacity / Usage')
    expect(enCommon.common.clear).toBe('Clear')

    expect(zhAccounts.accounts.columns.classificationRoute).toBe('分类 / 路由')
    expect(zhAccounts.accounts.columns.capacityUsage).toBe('容量 / 用量')
    expect(zhCommon.common.clear).toBe('清除')
  })
})
