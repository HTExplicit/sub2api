import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CodexFingerprintPanel from '../CodexFingerprintPanel.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'en' } }) }))

const fixture = (agent = 'configured-agent') => ({
  configured: { identity_source: 'account', profile_source: 'legacy_generated', profile_schema: 1, user_agent: agent, originator: 'codex-tui', version: '0.156.0', fingerprint_mode_effective: 'device', account_revision: '2026-09-23T00:00:00Z' },
  cookie_max_age_seconds: 120,
  models: [],
  captured_reference: { client_version: '0.156.0', user_agent: 'captured-reference' }
})

describe('CodexFingerprintPanel', () => {
  beforeEach(() => { vi.clearAllMocks() })
  it('reads only on open and keeps unknown observations distinct from configured values', async () => {
    mocks.get.mockResolvedValue({ data: fixture() })
    const wrapper = mount(CodexFingerprintPanel, { props: { accountId: 7 } })
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledTimes(1)
    expect(mocks.get).toHaveBeenCalledWith('/admin/accounts/7/codex-fingerprint')
    expect(mocks.put).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('No final request observation')
    expect(wrapper.text()).toContain('configured-agent')
    expect(wrapper.text()).toContain('legacy_generated')
    wrapper.unmount()
  })
  it('ignores a late response for the previous account', async () => {
    let finish: (result: unknown) => void = () => {}
    mocks.get.mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValueOnce({ data: fixture('second-account') })
    const wrapper = mount(CodexFingerprintPanel, { props: { accountId: 7 } })
    await wrapper.setProps({ accountId: 8 }); await flushPromises()
    finish({ data: fixture('old-account') }); await flushPromises()
    expect(wrapper.text()).toContain('second-account'); expect(wrapper.text()).not.toContain('old-account')
    wrapper.unmount()
  })
  it('shows final transport and digest differences without claiming a TLS match', async () => {
    mocks.get.mockResolvedValue({ data: { ...fixture(),
      captured_reference: { client_version: '0.156.0', user_agent: 'captured-reference', binary_sha256: 'c'.repeat(64), captured_at: '2026-09-23T00:00:00Z', transport_scope: 'loopback', responses: [{ method: 'GET', transport: { client_hello: { alpn: ['http/1.1'] } } }] },
      models: [{ model: 'gpt-6-sol', phase: 'ready', enrolled: false, failed_cycles: 0, qualified: true, verified_at: '2026-09-23T00:00:42Z' }],
      observed: { observed_at: '2026-09-23T00:01:00Z', ingress: 'ws', transport: 'http', http_protocol: 'HTTP/1.1', user_agent: 'wire-agent', originator: 'codex_exec', version: '0.156.0', tls_implementation: 'go-crypto-tls', request_encoding: 'zstd', cookie_names: ['__cflb'], cookie_versions: { __cflb: 'd'.repeat(16) }, connection_evidence: 'short-hash', identity_fields: { session: { header_digest: 'aaaaaaaaaaaaaaaa', body_digest: 'bbbbbbbbbbbbbbbb', consistency: 'mismatch' } } } } })
    const wrapper = mount(CodexFingerprintPanel, { props: { accountId: 7 } }); await flushPromises()
    expect(wrapper.text()).toContain('ws → http'); expect(wrapper.text()).toContain('Header/body mismatch'); expect(wrapper.text()).toContain('aaaaaaaaaaaaaaaa'); expect(wrapper.text()).toContain('Not captured')
    expect(wrapper.text()).toContain('c'.repeat(64)); expect(wrapper.text()).toContain('d'.repeat(16)); expect(wrapper.text()).toContain('2026-09-23T00:00:42Z')
    expect(wrapper.text()).toContain('codex_exec'); expect(wrapper.text()).toContain('client_hello')
    expect(mocks.put).not.toHaveBeenCalled(); wrapper.unmount()
  })
})
