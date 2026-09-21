import { describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { captureAccountView } from '@/composables/useAccountViewContext'
import { accountAPIForView } from '@/api/admin/accounts'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { viewContributions } from './accountView.fixtures'

function fixture() {
  const items = viewContributions(), view = items.find(item => item.slot === 'account.view.v1')!
  const query = { account_ids: [1], search: 'original' }
  const scope = captureAccountView({ contribution: view, presetID: 'insufficient', query, actorID: 41, currentActor: () => 41, currentItems: () => items })
  const update = vi.fn().mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' }).mockResolvedValue({ id: 9 })
  const api = accountAPIForView(scope, { bulkUpdate: update } as never)
  return { items, query, update, api, scope }
}

describe('captured view operations through existing server step-up', () => {
  it('keeps the original target and view after a challenge and retries only once', async () => {
    const f = fixture(), controller = useStepUp(), ids = [1]
    const target = [...ids]
    const result = controller.run(() => f.api.bulkUpdate(target, { status: 'inactive' }))
    await flushPromises()
    expect(controller.visible.value).toBe(true)
    ids.splice(0, 1, 2); f.query.account_ids = [2]; f.query.search = 'new'
    controller.onVerified()
    await expect(result).resolves.toEqual({ id: 9 })
    expect(f.update).toHaveBeenCalledTimes(2)
    for (const call of f.update.mock.calls) {
      expect(call[0]).toEqual([1])
      expect(call[2].context).toMatchObject({ preset_id: 'insufficient', query: { account_ids: [1], search: 'original' } })
    }
  })
  it('does not retry after cancellation or send a retry after the view is disabled', async () => {
    const first = fixture(), canceled = useStepUp()
    const result = canceled.run(() => first.api.bulkUpdate([1], {})).catch(error => error)
    await flushPromises(); canceled.onCancel()
    expect(isStepUpCancelled(await result)).toBe(true)
    expect(first.update).toHaveBeenCalledTimes(1)
    const second = fixture(), revoked = useStepUp()
    const retry = revoked.run(() => second.api.bulkUpdate([1], {}))
    await flushPromises()
    second.items.find(item => item.slot === 'account.view.v1')!.available = false
    revoked.onVerified()
    await expect(retry).rejects.toThrow('unavailable')
    expect(second.update).toHaveBeenCalledTimes(1)
  })
})
