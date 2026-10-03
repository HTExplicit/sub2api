import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import type { Account, AccountTestPlanView } from '@/types'
import Select from '@/components/common/Select.vue'
import { CANDY_QUALITY_PROMPT } from '@/utils/accountQualityCheck'
import AccountTestModal from '../AccountTestModal.vue'

const { getAccountTestPlan } = vi.hoisted(() => ({ getAccountTestPlan: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getAccountTestPlan } } }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params ? `${key} ${JSON.stringify(params)}` : key })
}))

function plan(accountID: number): AccountTestPlanView {
  const models = [
    { id: 'gpt-6-astra', type: 'model', created_at: '', display_name: 'GPT-6 Astra', reasoning_efforts: ['low', 'medium', 'high', 'xhigh'] },
    { id: 'gpt-6-lite', type: 'model', created_at: '', display_name: 'GPT-6 Lite', reasoning_efforts: ['low', 'medium'] },
    { id: 'gpt-image-2', type: 'model', created_at: '', display_name: 'GPT Image 2' }
  ]
  return { schema_version: 1, account_id: accountID, wire_platform: 'openai', default_mode: 'default', models,
    mode_views: { default: { model_ids: models.map(model => model.id), default_model_id: 'gpt-6-astra' } } }
}

function account(type = 'oauth'): Account {
  return { id: 42, name: 'OpenAI account', platform: 'openai', type, status: 'active', credentials: {} } as Account
}

function stream(events: Array<Record<string, unknown>>) {
  const encoder = new TextEncoder()
  const chunks = events.map(event => encoder.encode(`data: ${JSON.stringify(event)}\n`))
  let index = 0
  return {
    ok: true,
    body: {
      getReader: () => ({
        read: vi.fn(async () => index < chunks.length ? { done: false, value: chunks[index++] } : { done: true, value: undefined }),
        cancel: vi.fn(async () => undefined),
        releaseLock: vi.fn()
      })
    }
  } as unknown as Response
}

let wrapper: VueWrapper | undefined

async function open(target = account()) {
  wrapper = mount(AccountTestModal, {
    attachTo: document.body,
    props: { show: false, account: target },
    global: { plugins: [createPinia()], stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }, Icon: true } }
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

const modeSelect = () => wrapper!.findAllComponents(Select)
  .find(select => (select.props('options') as Array<{ value: unknown }>).some(option => option.value === 'compact'))!
const modeValues = () => (modeSelect().props('options') as Array<{ value: unknown }>).map(option => option.value)
async function chooseMode(mode: string) {
  modeSelect().vm.$emit('update:modelValue', mode)
  await flushPromises()
}
// The reasoning effort control is the dialog's only native select.
const effort = () => wrapper!.get('select').element as HTMLSelectElement
const start = () => wrapper!.findAll('button').find(button => button.text().includes('admin.accounts.startTest'))!

async function runQualityCheck(events: Array<Record<string, unknown>>) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(stream(events)))
  await open()
  await chooseMode('quality')
  await start().trigger('click')
  await flushPromises()
  return wrapper!
}

beforeEach(() => {
  getAccountTestPlan.mockReset().mockImplementation(async (accountID: number) => plan(accountID))
  localStorage.setItem('auth_token', 'test-token')
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(stream([])))
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
  localStorage.clear()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('AccountTestModal quality check', () => {
  it('is offered only to OpenAI OAuth accounts', async () => {
    await open(account('apikey'))
    expect(modeValues()).toEqual(['default', 'compact'])
    wrapper!.unmount()

    await open(account('oauth'))
    expect(modeValues()).toEqual(['default', 'compact', 'quality'])
  })

  it('replaces the prompt with the collapsed read-only question and sends it as a normal text test', async () => {
    await open()
    expect(wrapper!.find('textarea').exists()).toBe(true)

    await chooseMode('quality')
    expect(wrapper!.find('textarea').exists()).toBe(false)
    const question = wrapper!.get('details[data-test="quality-question"]')
    expect((question.element as HTMLDetailsElement).open).toBe(false)
    expect(question.get('summary').text()).toBe('admin.accounts.qualityCheck.questionSummary {"answer":21}')
    expect(question.get('[data-test="quality-question-text"]').element.textContent).toBe(CANDY_QUALITY_PROMPT)
    expect(question.findAll('input, textarea, select, [contenteditable]')).toHaveLength(0)

    await start().trigger('click')
    await flushPromises()
    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [url, request] = vi.mocked(global.fetch).mock.calls[0]
    expect(String(url)).toContain('/admin/accounts/42/test')
    expect(JSON.parse(String(request?.body))).toEqual({
      model_id: 'gpt-6-astra', prompt: CANDY_QUALITY_PROMPT, mode: 'default', reasoning_effort: 'high'
    })
  })

  it('needs a text model and never sends the question to an image model', async () => {
    await open()
    await chooseMode('quality')
    wrapper!.findAllComponents(Select)[0].vm.$emit('update:modelValue', 'gpt-image-2')
    await flushPromises()
    expect(wrapper!.get('[data-test="quality-text-model-only"]').text()).toBe('admin.accounts.qualityCheck.textModelOnly')
    expect(start().attributes('disabled')).toBeDefined()
    await start().trigger('click')
    await flushPromises()
    expect(global.fetch).not.toHaveBeenCalled()

    wrapper!.findAllComponents(Select)[0].vm.$emit('update:modelValue', 'gpt-6-astra')
    await flushPromises()
    expect(wrapper!.find('[data-test="quality-text-model-only"]').exists()).toBe(false)
    expect(start().attributes('disabled')).toBeUndefined()
  })

  it('switches the effort to high on entering and restores the previous choice on leaving', async () => {
    await open()
    effort().value = 'low'
    await wrapper!.get('select').trigger('change')

    await chooseMode('quality')
    expect(effort().value).toBe('high')
    // The administrator can still change it while in the quality check.
    effort().value = 'xhigh'
    await wrapper!.get('select').trigger('change')
    expect(effort().value).toBe('xhigh')

    await chooseMode('default')
    expect(effort().value).toBe('low')

    // A model without high keeps the chosen effort.
    wrapper!.findAllComponents(Select)[0].vm.$emit('update:modelValue', 'gpt-6-lite')
    await flushPromises()
    effort().value = 'medium'
    await wrapper!.get('select').trigger('change')
    await chooseMode('quality')
    expect(effort().value).toBe('medium')
  })

  it('shows no anomaly for a final answer of 21 and keeps the full answer in the output', async () => {
    const answer = '最少需要取出 **21 颗**。\n\n所以 20 颗无法保证成功，答案为 \\(\\boxed{21}\\)。'
    const modal = await runQualityCheck([
      { type: 'test_start', model: 'gpt-6-astra' },
      { type: 'content', text: answer },
      { type: 'upstream_model', upstream_model: 'gpt-6-astra' },
      { type: 'test_complete', success: true, reasoning_tokens: 761 }
    ])
    const card = modal.get('[data-test="quality-verdict"]')
    expect(card.get('[data-test="quality-verdict-kind"]').text()).toBe('admin.accounts.qualityCheck.verdictNormal')
    expect(card.get('[data-test="quality-final-answer"]').text()).toBe('21')
    expect(card.get('[data-test="quality-reasoning-tokens"]').text()).toBe('761')
    expect(card.get('[data-test="quality-declared-model"]').text()).toContain('gpt-6-astra')
    expect(card.get('[data-test="quality-declared-model"]').text()).toContain('admin.accounts.qualityCheck.modelMatches')
    expect(card.find('[data-test="quality-reasoning-hint"]').exists()).toBe(false)
    expect(card.find('[data-test="quality-verdict-reason"]').exists()).toBe(false)
    expect(card.text()).toContain('admin.accounts.qualityCheck.note')
    expect(modal.text()).toContain('最少需要取出 **21 颗**。')
  })

  it('suspects degradation for another number and hints at exactly 516 reasoning tokens', async () => {
    const modal = await runQualityCheck([
      { type: 'test_start', model: 'gpt-6-astra' },
      { type: 'content', text: '最少需要取出 **29 个糖果**。\n\n答案是 **29 个**。' },
      { type: 'upstream_model', upstream_model: 'gpt-6-mini' },
      { type: 'test_complete', success: true, reasoning_tokens: 516 }
    ])
    const card = modal.get('[data-test="quality-verdict"]')
    expect(card.get('[data-test="quality-verdict-kind"]').text()).toBe('admin.accounts.qualityCheck.verdictSuspect')
    expect(card.get('[data-test="quality-final-answer"]').text()).toBe('29')
    expect(card.get('[data-test="quality-reasoning-tokens"]').text()).toBe('516')
    expect(card.get('[data-test="quality-reasoning-hint"]').text()).toBe('admin.accounts.qualityCheck.reasoningTokensHint {"tokens":516}')
    expect(card.get('[data-test="quality-declared-model"]').text())
      .toContain('admin.accounts.qualityCheck.modelDiffers {"model":"gpt-6-astra"}')
  })

  it('suspects degradation when another model answered, even with the right answer', async () => {
    const modal = await runQualityCheck([
      { type: 'test_start', model: 'gpt-6-astra' },
      { type: 'content', text: '答案是 **21 个**。' },
      { type: 'upstream_model', upstream_model: 'gpt-6-mini' },
      { type: 'test_complete', success: true, reasoning_tokens: 3712 }
    ])
    const card = modal.get('[data-test="quality-verdict"]')
    expect(card.get('[data-test="quality-verdict-kind"]').text()).toBe('admin.accounts.qualityCheck.verdictSuspect')
    expect(card.get('[data-test="quality-verdict-reason"]').text()).toBe('admin.accounts.qualityCheck.reasonModelDiffers')
    expect(card.get('[data-test="quality-final-answer"]').text()).toBe('21')
  })

  // A failed test never reaches the upstream_model event, so it cannot say the
  // upstream declared no model.
  it.each([
    ['the test failed', [{ type: 'content', text: '最少需要取出 **21 颗**' }, { type: 'error', error: 'stream ended before terminal' }], 'reasonFailed', 'declaredModelUnavailable'],
    ['the stream ended without a terminal event', [{ type: 'content', text: '最少需要取出 **21 颗**' }], 'reasonIncomplete', 'declaredModelUnavailable'],
    ['the answer did not finish', [{ type: 'content', text: '最少需要取出 **21 颗**' }, { type: 'test_complete', success: true, output_limited: true }], 'reasonIncomplete', 'notDeclared'],
    ['no final number is found', [{ type: 'content', text: '这道题需要考虑最坏情况。' }, { type: 'test_complete', success: true }], 'reasonNoNumber', 'notDeclared']
  ])('cannot judge when %s', async (_name, events, reason, declared) => {
    const modal = await runQualityCheck([{ type: 'test_start', model: 'gpt-6-astra' }, ...events])
    const card = modal.get('[data-test="quality-verdict"]')
    expect(card.get('[data-test="quality-verdict-kind"]').text()).toBe('admin.accounts.qualityCheck.verdictUndetermined')
    expect(card.get('[data-test="quality-verdict-reason"]').text()).toBe(`admin.accounts.qualityCheck.${reason}`)
    expect(card.find('[data-test="quality-final-answer"]').exists()).toBe(false)
    expect(card.get('[data-test="quality-reasoning-tokens"]').text()).toBe('admin.accounts.qualityCheck.notReported')
    expect(card.get('[data-test="quality-declared-model"]').text()).toBe(`admin.accounts.qualityCheck.${declared}`)
  })

  it('reports a stream that ends without a terminal event as an error and keeps the partial answer', async () => {
    const modal = await runQualityCheck([{ type: 'test_start', model: 'gpt-6-astra' }, { type: 'content', text: '最少需要取出' }])
    expect(modal.get('[data-test="test-error"]').text()).toBe('admin.accounts.testStreamEnded')
    expect(modal.text()).toContain('最少需要取出')
    expect(modal.findAll('button').some(button => button.text().includes('admin.accounts.retry'))).toBe(true)
  })

  it('shows no verdict for an ordinary text test', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(stream([
      { type: 'test_start', model: 'gpt-6-astra' },
      { type: 'content', text: '21' },
      { type: 'test_complete', success: true, reasoning_tokens: 516 }
    ])))
    await open()
    await start().trigger('click')
    await flushPromises()
    expect(wrapper!.text()).toContain('admin.accounts.testCompleted')
    expect(wrapper!.find('[data-test="quality-verdict"]').exists()).toBe(false)
  })

  it.each([
    ['gpt-6-luna', true],
    ['gpt-6-astra', false]
  ])('says so only when an ordinary test is answered by another model (%s)', async (declared, mismatch) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(stream([
      { type: 'test_start', model: 'gpt-6-astra' },
      { type: 'content', text: 'OK' },
      { type: 'upstream_model', upstream_model: declared },
      { type: 'test_complete', success: true }
    ])))
    await open()
    await start().trigger('click')
    await flushPromises()
    expect(wrapper!.text()).toContain('admin.accounts.testCompleted')
    expect(wrapper!.text().includes(`admin.accounts.batchTest.upstreamResponse: ${declared}（admin.accounts.batchTest.modelMismatch）`)).toBe(mismatch)
  })
})
