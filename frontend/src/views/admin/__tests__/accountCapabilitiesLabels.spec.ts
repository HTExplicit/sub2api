import { describe, expect, it, vi } from 'vitest'
import { capabilityCodeLabel } from '../accountCapabilitiesHelpers'
import zh from '@/i18n/locales/zh/admin/accountCapabilities'
import en from '@/i18n/locales/en/admin/accountCapabilities'

describe('account capability structured-code labels', () => {
  const codes = [
    'configuration_changed', 'evidence_expired', 'evidence_superseded', 'no_current_inference_evidence',
    'unsupported_public_protocol', 'configured_hint_not_live_discovery', 'credential_invalid',
    'account_disabled', 'authentication_failed', 'model_unavailable', 'quota_exhausted', 'rate_limited',
    'protocol_unsupported', 'timeout', 'output_budget_exhausted', 'no_semantic_output',
    'tool_contract_mismatch', 'text_completed', 'tool_roundtrip_passed', 'metadata_available',
    'catalog_discovered', 'catalog_empty', 'catalog_partial',
  ]

  it('provides matching nonempty Chinese and English labels for every requested fixed reason code', () => {
    const chinese = zh.accountCapabilities.reasonLabels as Record<string, string>
    const english = en.accountCapabilities.reasonLabels as Record<string, string>
    expect(Object.keys(chinese).sort()).toEqual(Object.keys(english).sort())
    for (const code of codes) {
      expect(chinese[code]).toMatch(/[\u4e00-\u9fff]/)
      expect(english[code].length).toBeGreaterThan(0)
      expect(capabilityCodeLabel(code, 'reasonLabels', (key) => chinese[key.split('.').at(-1)!], (key) => Object.hasOwn(chinese, key.split('.').at(-1)!))).toBe(chinese[code])
    }
  })

  it('localizes catalog and uncertain states without calling token-count availability a liveness pass', () => {
    const states = zh.accountCapabilities.states as Record<string, string>
    const english = en.accountCapabilities.states as Record<string, string>
    for (const state of ['discovered', 'empty', 'partial', 'available', 'uncertain', 'canceled']) {
      expect(states[state]).toMatch(/[\u4e00-\u9fff]/)
      expect(english[state]).toBeTruthy()
    }
    expect(states.available).toContain('非模型判活')
    expect(zh.accountCapabilities.reasonLabels.authentication_failed).toContain('尚未证实')
    expect(zh.accountCapabilities.reasonLabels.catalog_partial).toContain('仅包含')
  })

  it('preserves unknown codes and free-form provider text without translation or normalization', () => {
    const translate = vi.fn(() => 'should not be used')
    expect(capabilityCodeLabel('new_provider_code', 'reasonLabels', translate, () => false)).toBe('new_provider_code')
    expect(capabilityCodeLabel('Unrecognized upstream message: do not alter', 'reasonLabels', translate, () => true)).toBe('Unrecognized upstream message: do not alter')
    expect(capabilityCodeLabel('Credential_Invalid', 'reasonLabels', translate, () => true)).toBe('Credential_Invalid')
    expect(translate).not.toHaveBeenCalled()
  })
})
