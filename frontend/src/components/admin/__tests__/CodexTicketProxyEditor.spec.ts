import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, vi } from 'vitest'
import CodexTicketProxyEditor from '../CodexTicketProxyEditor.vue'
const mocks = vi.hoisted(() => ({ testProxy: vi.fn() }))
vi.mock('@/api/admin/codexTickets', () => ({ codexTicketsAPI: mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
describe('draft ticket proxy editor', () => {
  it('only parses on blur and invalidates old connection evidence on edit', async () => {
    const wrapper = mount(CodexTicketProxyEditor, { props: { modelValue: 'Password: fixture\nPort: 2721\nHost: proxy.example.com\nUsername: demo' } })
    await wrapper.get('textarea').trigger('blur')
    expect(mocks.testProxy).not.toHaveBeenCalled()
    const normalized = wrapper.emitted('update:modelValue')![0][0] as string
    expect(normalized).toBe('http://demo:fixture@proxy.example.com:2721')
    await wrapper.setProps({ modelValue: normalized })
    mocks.testProxy.mockResolvedValue({ success: false, network_reachable: true, code: 'target_http_status', message: 'HTTP 403; not verified', stages: [{ name: 'http', success: false, duration_ms: 12 }] })
    await wrapper.findAll('button').find(b => b.text().endsWith('.test'))!.trigger('click'); await flushPromises()
    expect(mocks.testProxy).toHaveBeenCalledWith(normalized)
    expect(wrapper.get('[role="status"]').text()).toContain('HTTP 403')
    await wrapper.setProps({ modelValue: 'http://other.example.com:8080' })
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('keeps ambiguous source input and reports a parsing error', async () => {
    const wrapper = mount(CodexTicketProxyEditor, { props: { modelValue: 'Host: one\nHost: two\nPort: 8080' } })
    await wrapper.get('textarea').trigger('blur')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('duplicate_field')
    wrapper.unmount()
  })
  it('shows the safe transport cause supplied by the server', async () => {
    const wrapper = mount(CodexTicketProxyEditor, { props: { modelValue: 'http://proxy.example.com:8080' } })
    mocks.testProxy.mockResolvedValue({ success: false, network_reachable: false, code: 'ticket_proxy_eof', message: 'Connection closed', failure_detail: 'Head [redacted]: EOF', stages: [] })
    await wrapper.findAll('button').find(b => b.text().endsWith('.test'))!.trigger('click'); await flushPromises()
    expect(wrapper.get('[role="status"]').text()).toContain('Head [redacted]: EOF')
    wrapper.unmount()
  })
})
