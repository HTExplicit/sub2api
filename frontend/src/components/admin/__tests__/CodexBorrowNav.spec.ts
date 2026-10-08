import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import CodexBorrowNav from '../CodexBorrowNav.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const paths = [
  '/admin/codex-gateway-borrow',
  '/admin/codex-gateway-borrow/status',
]

async function renderNav(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: paths.map(routePath => ({ path: routePath, component: { template: '<div />' } })),
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(CodexBorrowNav, { global: { plugins: [router] } })
  return { router, wrapper }
}

describe('Codex borrowing page navigation', () => {
  it.each(paths)('renders real links and marks only the current page on %s', async path => {
    const { wrapper } = await renderNav(path)

    expect(wrapper.findAll('a').map(link => link.attributes('href'))).toEqual(paths)
    expect(wrapper.findAll('a[aria-current="page"]')).toHaveLength(1)
    expect(wrapper.get('a[aria-current="page"]').attributes('href')).toBe(path)
    wrapper.unmount()
  })

  it('navigates between pages and supports browser Back', async () => {
    const { router, wrapper } = await renderNav(paths[0])

    await wrapper.get(`a[href="${paths[1]}"]`).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe(paths[1])
    router.back()
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe(paths[0]))
    expect(wrapper.get('a[aria-current="page"]').attributes('href')).toBe(paths[0])
    wrapper.unmount()
  })
})
