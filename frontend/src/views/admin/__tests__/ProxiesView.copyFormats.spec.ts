import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ProxiesView from '../ProxiesView.vue'

const { list, getAllWithCount } = vi.hoisted(() => ({ list: vi.fn(), getAllWithCount: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { proxies: { list, getAllWithCount } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

let wrapper: ReturnType<typeof shallowMount>
afterEach(() => wrapper?.unmount())

describe('proxy copy formats', () => {
  it('offers the standard URL first and the other formats with the real credentials', async () => {
    list.mockResolvedValue({ items: [{ id: 7, name: 'jp', protocol: 'socks5', host: '203.0.113.8', port: 6023, username: 'user-a', password: 'p@ss:w', status: 'active' }], total: 1, pages: 1 })
    getAllWithCount.mockResolvedValue([])
    wrapper = shallowMount(ProxiesView, {
      global: { stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="table" /></div>' },
        DataTable: { props: ['data'], template: '<div v-for="row in data" :key="row.id"><slot name="cell-address" :row="row" /></div>' },
      } },
    })
    await flushPromises()

    await wrapper.get('[data-test="proxy-copy-formats"]').trigger('click')
    const values = wrapper.findAll('[role="menuitem"] .font-mono').map(item => item.text())
    expect(values).toEqual([
      'socks5://user-a:p%40ss%3Aw@203.0.113.8:6023',
      '203.0.113.8:6023:user-a:p@ss:w',
      'user-a:p%40ss%3Aw@203.0.113.8:6023',
      '203.0.113.8:6023',
    ])
  })
})
