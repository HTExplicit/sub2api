import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import PluginsView from '../PluginsView.vue'

const {
  listPlugins,
  uploadPlugin,
  updatePlugin,
  followBundled,
  enablePlugin,
  savePluginConfig,
  createUISession,
  stepUpRun,
} = vi.hoisted(() => ({
  listPlugins: vi.fn(),
  uploadPlugin: vi.fn(),
  updatePlugin: vi.fn(),
  followBundled: vi.fn(),
  enablePlugin: vi.fn(),
  savePluginConfig: vi.fn(),
  createUISession: vi.fn(),
  stepUpRun: vi.fn((action: () => Promise<unknown>) => action()),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    plugins: {
      list: listPlugins,
      upload: uploadPlugin,
      update: updatePlugin,
      followBundled,
      enable: enablePlugin,
      disable: vi.fn(),
      remove: vi.fn(),
      getConfig: vi.fn().mockResolvedValue({}),
      saveConfig: savePluginConfig,
      test: vi.fn().mockResolvedValue({ success: true, message: 'ok', latency_ms: 1 }),
      createUISession,
    },
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
  }),
}))

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: stepUpRun }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => '',
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const plugin = {
  revision: 4,
  package_sha256: 'b'.repeat(64),
  update_policy: 'pinned' as const,
  id: 7,
  plugin_key: 'local.test.transport',
  name: 'Test Transport',
  version: '1.0.0',
  description: '',
  author: 'test',
  manifest: {
    schema_version: 1,
    id: 'local.test.transport',
    name: 'Test Transport',
    version: '1.0.0',
    requires: {
      sub2api: '>=0.1.0',
      plugin_protocol: 1,
      transport_api: 1,
      ui_bridge: 1,
    },
    capabilities: [],
    ui: { entrypoint: 'ui/index.html' },
  },
  binary_sha256: 'a'.repeat(64),
  signature_status: 'trusted' as const,
  state: 'disabled' as const,
  last_error: '',
  installed_at: '2026-08-22T00:00:00Z',
  updated_at: '2026-08-22T00:00:00Z',
  bindings: [
    {
      id: 1,
      plugin_id: 7,
      capability: 'openai.oauth.outbound_transport.v1',
      platform: 'openai',
      account_type: 'oauth',
      enabled: false,
      rollout_percent: 100,
    },
  ],
  compatibility: {
    compatible: true,
    tested: true,
    status: 'compatible' as const,
    message: '',
    current_sub2api_version: '0.1.0',
    required_sub2api_version: '>=0.1.0',
    recommended_sub2api_version: '0.1.0',
    plugin_protocol: 1,
    transport_api: 1,
    ui_bridge: 1,
  },
  runtime_healthy: false,
  runtime_message: '',
}

function mountView() {
  return mount(PluginsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        BaseDialog: { template: '<div><slot /></div>' },
        Icon: true,
        TotpStepUpDialog: true,
      },
    },
  })
}

describe('管理员插件页二次验证', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    stepUpRun.mockImplementation((action: () => Promise<unknown>) => action())
    listPlugins.mockResolvedValue([plugin])
    uploadPlugin.mockResolvedValue(plugin)
    updatePlugin.mockResolvedValue(plugin)
    followBundled.mockResolvedValue(plugin)
    enablePlugin.mockResolvedValue(plugin)
    savePluginConfig.mockResolvedValue({ enabled: true })
    createUISession.mockResolvedValue({
      url: '/api/v1/plugin-ui/token/index.html#bridge_token=bridge',
      bridge_token: 'bridge',
      ui_bridge_version: 1,
      expires_at: '2026-08-22T01:00:00Z',
    })
  })

  it('启用插件通过 step-up 控制器执行', async () => {
    const wrapper = mountView()
    await flushPromises()

    const button = wrapper.findAll('button').find((item) => item.text().includes('admin.plugins.enable'))
    expect(button).toBeDefined()
    await button!.trigger('click')
    await flushPromises()

    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(enablePlugin).toHaveBeenCalledWith(plugin, 100, false)
  })

  it('上传插件通过 step-up 控制器执行', async () => {
    const wrapper = mountView()
    await flushPromises()
    const input = wrapper.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', {
      configurable: true,
      value: [new File(['plugin'], 'transport.s2plugin', { type: 'application/zip' })],
    })

    await input.trigger('change')
    await flushPromises()

    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(uploadPlugin).toHaveBeenCalledTimes(1)
  })

  it('step-up期间不把已读取的操作快照换成新revision', async () => {
    const displayed = { ...plugin }
    listPlugins.mockResolvedValue([displayed])
    let delayed: (() => Promise<unknown>) | undefined
    let finish: (() => void) | undefined
    stepUpRun.mockImplementation((action: () => Promise<unknown>) => {
      delayed = action
      return new Promise<void>(resolve => { finish = resolve })
    })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(item => item.text().includes('admin.plugins.enable'))!.trigger('click')
    displayed.revision = 99
    await delayed!()
    expect(enablePlugin).toHaveBeenCalledWith(expect.objectContaining({ revision: 4, package_sha256: plugin.package_sha256 }), 100, false)
    finish!()
    await flushPromises()
    wrapper.unmount()
  })

  it('更新携带当前版本快照且不先停用插件', async () => {
    const enabled = { ...plugin, state: 'enabled', bindings: [{ ...plugin.bindings[0], enabled: true }] }
    listPlugins.mockResolvedValue([enabled])
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('admin.plugins.updatePackage'))!.trigger('click')
    const input = wrapper.findAll('input[type="file"]')[1]!
    const file = new File(['new signed package'], 'updated.s2plugin')
    Object.defineProperty(input.element, 'files', { configurable: true, value: [file] })
    await input.trigger('change')
    await flushPromises()
    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(updatePlugin).toHaveBeenCalledWith(enabled, file)
    expect(uploadPlugin).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('固定版本仅在明确选择后恢复跟随内置版', async () => {
    const pinned = { ...plugin, plugin_key: 'codexrip.account-tools' }
    listPlugins.mockResolvedValue([pinned])
    const wrapper = mountView()
    await flushPromises()
    expect(followBundled).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text().includes('admin.plugins.followBundle'))!.trigger('click')
    await flushPromises()
    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(followBundled).toHaveBeenCalledWith(pinned)
    wrapper.unmount()
  })
})
