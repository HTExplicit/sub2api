import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import type { AdminUsageLog } from '@/types'
import UsageTable from '../UsageTable.vue'

vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }) }))
vi.mock('@/utils/ipGeoLookup', () => ({ getEntry: vi.fn(), fetchBatch: vi.fn() }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

let wrapper: VueWrapper | undefined
afterEach(() => { wrapper?.unmount(); wrapper = undefined })

function renderLatency(firstToken: number | null, duration: number) {
  wrapper = mount(UsageTable, {
    props: { data: [{ id: 71, first_token_ms: firstToken, duration_ms: duration } as AdminUsageLog], columns: [{ key: 'latency', label: 'Latency' }] },
    global: { stubs: {
      DataTable: { props: ['data'], template: '<div data-test="latency-cell"><slot name="cell-latency" :row="data[0]" /></div>' },
      EmptyState: true, IpGeoCell: true, Icon: true
    } }
  })
  return wrapper.get('[data-test="latency-cell"] > div > span[aria-hidden="true"]')
}

describe('usage latency health bar', () => {
  it('uses a valid official two-ended gradient for first-token and total-duration severity', () => {
    const bar = renderLatency(3_000, 200_000)
    expect(bar.classes()).toEqual(expect.arrayContaining(['bg-gradient-to-b', 'from-40%', 'to-60%', 'from-emerald-500', 'to-orange-500']))
    expect(bar.classes()).not.toContain('bg-40%')
    expect(bar.classes()).not.toContain('%')
    // This encodes two metrics, so decorative flat-theme tokens must not
    // erase the total-duration end of the official health indication.
    expect((bar.element as HTMLElement).style.getPropertyValue('--theme-background-gradient-to-b'))
      .toBe('linear-gradient(to bottom, var(--tw-gradient-stops))')
  })

  it('keeps the solid total-duration bar when first-token timing is unknown', () => {
    const bar = renderLatency(null, 400_000)
    expect(bar.classes()).toContain('bg-red-500')
    expect(bar.classes()).not.toContain('bg-gradient-to-b')
  })
})
