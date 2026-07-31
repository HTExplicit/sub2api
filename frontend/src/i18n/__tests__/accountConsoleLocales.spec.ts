import { describe, expect, it } from 'vitest'
import enAccounts from '../locales/en/admin/accounts'
import enCommon from '../locales/en/common'
import zhAccounts from '../locales/zh/admin/accounts'
import zhCommon from '../locales/zh/common'

describe('account console locale keys', () => {
  it('defines every label introduced by the cockpit account console', () => {
    expect(enAccounts.accounts.columns.classificationRoute).toBe('Management Classification / Request Routing')
    expect(enAccounts.accounts.columns.usage).toBe('Usage')
    expect(enAccounts.accounts.columns.capacityUsage).toBe('Capacity / Usage')
    expect(enAccounts.accounts.usageNoData).toBe('No quota data')
    expect(enCommon.common.clear).toBe('Clear')

    expect(zhAccounts.accounts.columns.classificationRoute).toBe('管理分类 / 请求路由')
    expect(zhAccounts.accounts.columns.usage).toBe('用量')
    expect(zhAccounts.accounts.columns.capacityUsage).toBe('容量 / 用量')
    expect(zhAccounts.accounts.usageNoData).toBe('暂无额度数据')
    expect(zhCommon.common.clear).toBe('清除')
  })
})
