import { afterEach, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PelicanWorkDetail from '../PelicanWorkDetail.vue'
import type { PelicanTestResult } from '@/api/admin/pelicanTests'
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ locale: { value: 'zh' } }) }))
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })
it('downloads original stage bytes and retains the generation response download', async () => {
  const create = vi.fn((_blob: Blob) => 'blob:fixture')
  vi.stubGlobal('URL', { createObjectURL: create, revokeObjectURL: vi.fn() })
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  const result: PelicanTestResult = { id: 'r1', account_id: 1, account_name: 'account', model_id: 'model', effort: '', status: 'failed', raw_answer: '', raw_response: 'original response', raw_html: '', html: '', error: '', preview_unavailable: '', evidence: { reviewing: { prompt: 'review', raw_answer: '', raw_response: btoa(String.fromCharCode(255, 0, 1)), error: '', status: 'failed', duration_ms: 12, invocation: {} } } }
  const wrapper = mount(PelicanWorkDetail, { props: { result }, global: { stubs: { PelicanPreview: true } } })
  await wrapper.get('[data-test="pelican-download-response"]').trigger('click')
  expect(create).toHaveBeenCalledOnce()
  await wrapper.get('[data-test="pelican-download-reviewing"]').trigger('click')
  const blob = create.mock.calls[1]![0] as unknown as Blob
  const bytes = await new Promise<ArrayBuffer>(resolve => { const reader = new FileReader(); reader.onload = () => resolve(reader.result as ArrayBuffer); reader.readAsArrayBuffer(blob) })
  expect([...new Uint8Array(bytes)]).toEqual([255, 0, 1])
  wrapper.unmount()
})
