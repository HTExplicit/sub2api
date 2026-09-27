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
  it('shows raw cookies, identifiers, proxies, the stored qualification and the original failure', async () => {
    const scope = { account_id: 7, identity: 'owner-hash', profile_hash: 'profile-hash', route_hash: 'route-hash', route_evidence: 'connection', connection_lease_id: 'lease-raw-id', transport: 'http', account_proxy_id: 34 }
    mocks.get.mockResolvedValue({ data: { ...fixture(),
      routing_enabled: true, fail_closed: true, harvest_proxy_url: 'http://harvest-user:harvest-pass@harvest.example:8080',
      account_proxy: { id: 34, name: 'hk-egress', protocol: 'http', host: 'hk.example', port: 3128, username: 'egress-user', status: 'active' },
      current_scope: { ...scope, connection_lease_id: undefined },
      observed: { observed_at: '2026-09-26T00:00:00Z', ingress: 'http', transport: 'http', http_protocol: 'HTTP/1.1', user_agent: 'wire-agent', request_encoding: '', tls_implementation: 'go-crypto-tls', state: 'raw-request-state', state_present: true, state_length: 17, cookie_names: ['__cflb'], cookies: [{ name: '__cflb', value: 'route-cookie-value', routing: true }, { name: '__cf_bm', value: 'infra-cookie-value', routing: false }], connection_evidence: 'short-hash', connection_lease_id: 'lease-raw-id', account_proxy_id: 34, identity_fields: { session: { header_value: 'session-raw', body_value: 'session-raw', header_digest: 'h'.repeat(16), consistency: 'match' } } },
      models: [{ model: 'gpt-6-astra', phase: 'retry', enrolled: true, failed_cycles: 1, qualified: false, last_attempt_at: '2026-09-26T00:00:05Z', operation_id: 'job-9', last_code: 'routing_upstream', identity_matches: true, route_matches_current: true, revoked_at: '2026-09-26T00:01:00Z', revocation_reason: 'routing_connection_expired: connection lease expired',
        qualification: { scope, bundle: { key: 'bundle.live.fixture', revision: 3, expires_at: '2026-09-26T00:02:00Z' }, model: 'gpt-6-astra', verified_at: '2026-09-26T00:00:00Z', expires_at: '2026-09-26T00:02:00Z' },
        last_observation: { stage: 'acquire', code: 'routing_upstream', http_status: 429, completed: false, model_matched: false, cookie_sent: false, observed_at: '2026-09-26T00:00:05Z', upstream_error_code: 'rate_limit_exceeded', upstream_error_message: 'Rate limit reached', request_id: 'req_fixture', cf_ray: 'ray-fixture', upstream_body: '{"error":{"code":"rate_limit_exceeded"}}',
          state: 'raw-response-state', response_headers: [{ name: 'Set-Cookie', value: '__cflb=response-cookie; Path=/' }, { name: 'X-Codex-Turn-State', value: 'raw-response-state' }], response_headers_omitted: ['X-Large'] } }] } })
    const wrapper = mount(CodexFingerprintPanel, { props: { accountId: 7 } }); await flushPromises()
    const text = wrapper.text()
    for (const value of ['__cflb=route-cookie-value', '__cf_bm=infra-cookie-value', 'lease-raw-id', 'session-raw', '#34 hk-egress', 'egress-user', 'harvest-pass', 'route-hash', 'bundle.live.fixture', 'rev 3', 'job-9', 'rate_limit_exceeded', 'Rate limit reached', 'req_fixture', 'ray-fixture', 'raw-request-state', 'raw-response-state']) expect(text).toContain(value)
    expect(wrapper.get('[data-test="codex-fingerprint-revocation-gpt-6-astra"]').text()).toBe('Route qualification withdrawn 2026-09-26T00:01:00Z: routing_connection_expired: connection lease expired')
    const headers = wrapper.get('[data-test="codex-fingerprint-headers-gpt-6-astra"]').text()
    expect(headers).toContain('Set-Cookie: __cflb=response-cookie; Path=/')
    expect(headers).toContain('X-Large')
    expect(mocks.put).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('shows the server reason when the fingerprint cannot be read', async () => {
    mocks.get.mockRejectedValue({ status: 503, message: 'Codex fingerprint unavailable: account 7 is not an OpenAI OAuth/setup-token Codex account' })
    const wrapper = mount(CodexFingerprintPanel, { props: { accountId: 7 } }); await flushPromises()
    expect(wrapper.get('[data-test="codex-fingerprint-error"]').text()).toContain('account 7 is not an OpenAI OAuth/setup-token Codex account')
    wrapper.unmount()
  })
})
