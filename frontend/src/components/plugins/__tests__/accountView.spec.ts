import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'
import { accountMatchesPredicate, accountViewAllowsAction, accountViewIdentityHeader, CINDY_ACCOUNT_VIEW_ALIAS, ownedViewReference,
  publicPluginContext, resolveAccountView, viewIdentity } from '../accountView'
import { captureAccountView, narrowAccountViewSelection, readWithAccountView } from '@/composables/useAccountViewContext'
import { accountViewClient, accountViewRequestConfig } from '@/api/admin/accountViewClient'
import { accountAPIForView } from '@/api/admin/accounts'
import { cindyAccount, cindyView, viewContributions } from './accountView.fixtures'

const http = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: http }))
const deferred = <T>() => { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done }); return { promise, resolve } }

function scopeFixture() {
  let actor = 41
  const items = viewContributions()
  const view = items.find(item => item.slot === 'account.view.v1')!
  const query = { search: 'private search', account_ids: [1, 2] }
  const scope = captureAccountView({ contribution: view, presetID: 'insufficient', query, actorID: actor,
    currentActor: () => actor, currentItems: () => items })
  return { scope, items, query, actor: (value: number) => { actor = value } }
}

describe('account.view.v1 bounded contract', () => {
  beforeEach(() => http.request.mockReset())
  it('pins the legacy alias and rejects ambiguous or cross-owner references', () => {
    const items = viewContributions(), view = cindyView()
    const impostor = { ...view, plugin_id: 9, plugin_key: 'example.other' }
    expect(resolveAccountView([impostor], CINDY_ACCOUNT_VIEW_ALIAS.plugin_key, CINDY_ACCOUNT_VIEW_ALIAS.view_id)).toBeUndefined()
    expect(resolveAccountView([...items, { ...view }], view.plugin_key!, view.id)).toBeUndefined()
    expect(ownedViewReference([{ ...items[0], id: 'foreign', slot: 'surface', plugin_id: 9, plugin_key: 'example.other' }], view, 'foreign', 'surface')).toBeUndefined()
  })
  it('renders only declared same-package view actions while preserving other core slot owners', () => {
    const owner = cindyView(), action = viewContributions().find(item => item.id === 'cindy-balance-recover')!
    expect(accountViewAllowsAction(action, owner)).toBe(true)
    expect(accountViewAllowsAction({ ...action, id: 'undeclared' }, owner)).toBe(false)
    expect(accountViewAllowsAction({ ...action, package_sha256: 'c'.repeat(64) }, owner)).toBe(false)
    expect(accountViewAllowsAction({ ...action, plugin_id: 99, plugin_key: 'codexrip.account-tools' }, owner)).toBe(true)
    expect(accountViewAllowsAction(action)).toBe(true)
  })
  it('uses versioned server facts for all predicates, including strict canonical identity and false', () => {
    const row = cindyAccount()
    expect(accountMatchesPredicate(row, { platforms: ['cindy'], types: ['apikey'], statuses: ['unschedulable'], plans: ['PRO'], privacy_mode: 'private', cindy_only: true, cindy_balance_status: 'insufficient' })).toBe(true)
    expect(accountMatchesPredicate(row, { plans: ['free'] })).toBe(false)
    expect(accountMatchesPredicate(row, { privacy_mode: '__unset__' })).toBe(false)
    expect(accountMatchesPredicate(row, { cindy_only: false })).toBe(false)
    const malformedIdentity = { ...row, credentials: { base_url: 'https://api.laxarouter.ai', api_key: 'secret' },
      account_view_facts: { ...row.account_view_facts!, canonical_cindy: false } }
    expect(accountMatchesPredicate(malformedIdentity, { cindy_only: true })).toBe(false)
    expect(accountMatchesPredicate(malformedIdentity, { cindy_only: false })).toBe(true)
    expect(accountMatchesPredicate({ ...row, account_view_facts: undefined }, { statuses: ['active'] })).toBe(false)
    expect(accountMatchesPredicate(row, { unsupported: true } as never)).toBe(false)
    expect(accountMatchesPredicate({ id: 2, platform: 'openai', type: 'oauth' } as Account)).toBe(true)
  })
  it('projects exact context DTOs, preserving legitimate choices but never aliased or spread Account secrets', () => {
    const account = { id: 1, name: 'public label', credentials: { api_key: 'SENTINEL_CREDENTIAL' }, extra: { raw_key: 'SENTINEL_EXTRA' }, token: 'SENTINEL_TOKEN' }
    const projected = publicPluginContext({ mode: 'widget', account_id: 1, accounts: [account],
      view_props: { ...account, account, alias: account, payload: account,
        groups: [{ ...account, platform: 'cindy', wire_platform: 'openai', provider_profile: 'cindy_laxa_v1' }],
        proxies: [account], target: { mode: 'selected', accountIds: [1], count: 1, alias: account },
        filters: { search: 'public search', credentials: account.credentials, extra: account.extra },
        folders: [{ id: 2, name: 'Folder', sort_order: 0, account_count: 3 }] } })
    const serialized = JSON.stringify(projected)
    expect(serialized).not.toContain('SENTINEL')
    expect(projected).not.toHaveProperty('accounts')
    expect(projected.view_props).toMatchObject({ groups: [{ id: 1, name: 'public label', platform: 'cindy', wire_platform: 'openai', provider_profile: 'cindy_laxa_v1' }], proxies: [{ id: 1, name: 'public label' }], target: { mode: 'selected', accountIds: [1], count: 1 } })
    expect(projected.view_props).not.toHaveProperty('account')
    expect(projected.view_props).not.toHaveProperty('alias')
    expect(() => publicPluginContext({ account_ids: [1, { credentials: 'secret' }] })).toThrow()
    expect(() => publicPluginContext({ view_props: { selectedIds: Array(3201).fill(1) } })).toThrow()
  })
  it('keeps operation owner headers and overrides body attempts to broaden the captured view', () => {
    const { scope, query } = scopeFixture()
    query.search = 'changed'; query.account_ids.push(3)
    const bound = accountViewRequestConfig(scope, { method: 'POST', headers: { 'X-Sub2API-Plugin': '9', 'X-Sub2API-Plugin-Package': 'c'.repeat(64) }, data: { account_ids: [1], view_context: { view_id: 'all', query: {} } } })
    expect(bound.headers).toMatchObject({ 'X-Sub2API-Plugin': '9', 'X-Sub2API-Plugin-Package': 'c'.repeat(64) })
    expect(bound.data.view_context).toMatchObject({ plugin_id: 7, view_id: 'cindy-accounts', preset_id: 'insufficient', query: { search: 'private search', account_ids: [1, 2] } })
    const header = accountViewIdentityHeader(viewIdentity(cindyView(), 'insufficient'))
    const decoded = atob(header.replace(/-/g, '+').replace(/_/g, '/'))
    expect(decoded).not.toContain('private search')
    expect(decoded).not.toContain('account_ids')
    expect(() => accountViewRequestConfig(scope, { method: 'POST', data: new FormData() })).toThrow('ACCOUNT_VIEW_INVALID_REQUEST')
    expect(narrowAccountViewSelection(scope, [2])?.context.query.account_ids).toEqual([2])
    expect(() => narrowAccountViewSelection(scope, [3])).toThrow()
  })
  it('preserves the Cindy surface, core probe, diagnostics and configuration producer DTOs', () => {
    const view = cindyView()
    const state = { identity: viewIdentity(view, 'insufficient'), base_query: view.account_view!.base_query,
      preset: view.account_view!.presets.find(item => item.id === 'insufficient')!,
      query: { platforms: ['cindy'], types: ['apikey'], statuses: ['unschedulable'], plans: ['pro'],
        proxies: ['direct', '3'], folders: ['uncategorized', '2'], tags: [4], account_ids: [1], group_id: -1,
        privacy_mode: '__unset__', search: 'bounded filter', sort_by: 'name', sort_order: 'asc' },
      selected_ids: [1], preset_counts: { cindy: 2, insufficient: 1 }, available: true }
    expect(publicPluginContext({ account_view_state: state }).account_view_state).toEqual(state)
    const probe = { selectedIds: [1], initiallyExpanded: true, filters: { platforms: ['cindy'],
      proxy_ids: [3], folder_ids: [2], tag_ids: [4], account_ids: [1], include_direct: true,
      include_uncategorized: true, group_id: -1, cindy_balance_status: 'insufficient' } }
    expect(publicPluginContext({ view_props: probe }).view_props).toEqual(probe)
    expect(publicPluginContext({ mode: 'configuration' })).toEqual({ mode: 'configuration' })
    expect(publicPluginContext({ error_id: 123, mode: 'widget', contribution_id: 'ops-error-diagnostics' }))
      .toEqual({ error_id: 123, mode: 'widget', contribution_id: 'ops-error-diagnostics' })
  })
  it('does not issue new IO after disable and does not publish a late actor response', async () => {
    const fixture = scopeFixture(), response = deferred<any>()
    http.request.mockReturnValue(response.promise)
    const request = accountViewClient(fixture.scope).get('/admin/accounts')
    fixture.actor(42)
    response.resolve({ data: { secret: 'late' } })
    await expect(request).rejects.toThrow('context changed')
    http.request.mockClear(); fixture.actor(41)
    fixture.items.find(item => item.slot === 'account.view.v1')!.available = false
    await expect(accountViewClient(fixture.scope).get('/admin/accounts')).rejects.toThrow('unavailable')
    expect(http.request).not.toHaveBeenCalled()
  })
  it('fences late query responses and leaves ordinary core operations independent', async () => {
    const { scope } = scopeFixture(), result = deferred<string>()
    let revision = 0
    const request = readWithAccountView({ capture: () => scope, available: () => true, revision: () => String(revision) }, () => result.promise)
    revision++; revision++; result.resolve('old query')
    await expect(request).rejects.toThrow('query changed')
    const core = { list: vi.fn() } as never
    expect(accountAPIForView(undefined, core)).toBe(core)
  })
})
