import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import VersionBadge from '../VersionBadge.vue'

const mocks = vi.hoisted(() => ({
  appStore: {
    versionLoading: false,
    currentVersion: '0.1.166-codexrip.1',
    latestVersion: '0.1.166',
    hasUpdate: false,
    updateStrategy: 'downstream' as 'upstream' | 'downstream',
    upstreamBaseVersion: '0.1.166',
    upstreamUpdateAvailable: false,
    releaseInfo: null,
    buildType: 'release',
    fetchVersion: vi.fn(),
    clearVersionCache: vi.fn()
  },
  performUpdate: vi.fn(),
  getRollbackVersions: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

vi.mock('@/stores', () => ({
  useAuthStore: () => ({ isAdmin: true }),
  useAppStore: () => mocks.appStore
}))

vi.mock('@/api/admin/system', () => ({
  performUpdate: mocks.performUpdate,
  restartService: vi.fn(),
  getRollbackVersions: mocks.getRollbackVersions,
  rollback: vi.fn()
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() })
}))

function mountBadge() {
  return mount(VersionBadge, {
    global: {
      stubs: {
        Icon: { template: '<span data-test="icon" />' }
      }
    }
  })
}

describe('VersionBadge downstream releases', () => {
  beforeEach(() => {
    mocks.appStore.currentVersion = '0.1.166-codexrip.1'
    mocks.appStore.latestVersion = '0.1.166'
    mocks.appStore.hasUpdate = false
    mocks.appStore.updateStrategy = 'downstream'
    mocks.appStore.upstreamBaseVersion = '0.1.166'
    mocks.appStore.upstreamUpdateAvailable = false
    mocks.appStore.buildType = 'release'
    mocks.appStore.fetchVersion.mockReset()
    mocks.performUpdate.mockReset()
    mocks.getRollbackVersions.mockReset()
  })

  it('shows a managed downstream build without offering official update or rollback', async () => {
    const wrapper = mountBadge()

    expect(wrapper.get('button').attributes('title')).toBe('version.downstreamManaged')
    await wrapper.get('button').trigger('click')

    expect(wrapper.text()).toContain('version.downstreamManaged')
    expect(wrapper.text()).toContain('version.upstreamBase')
    expect(wrapper.text()).not.toContain('version.updateNow')
    expect(wrapper.text()).not.toContain('version.rollback')
    expect(mocks.performUpdate).not.toHaveBeenCalled()
    expect(mocks.getRollbackVersions).not.toHaveBeenCalled()
  })

  it('marks a newer official baseline as waiting for downstream synchronization', async () => {
    mocks.appStore.latestVersion = '0.1.167'
    mocks.appStore.upstreamUpdateAvailable = true
    const wrapper = mountBadge()

    expect(wrapper.get('button').attributes('title')).toBe('version.waitingDownstreamSync')
    await wrapper.get('button').trigger('click')

    expect(wrapper.text()).toContain('version.waitingDownstreamSync')
    expect(wrapper.text()).not.toContain('version.updateNow')
    expect(wrapper.text()).not.toContain('version.rollback')
    expect(mocks.performUpdate).not.toHaveBeenCalled()
    expect(mocks.getRollbackVersions).not.toHaveBeenCalled()
  })
})
