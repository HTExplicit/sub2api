import { afterEach, expect, it } from 'vitest'
import { apiClient } from '../client'

const originalAdapter = apiClient.defaults.adapter
afterEach(() => { apiClient.defaults.adapter = originalAdapter })

it('only explicitly marked plugin config bypasses ordinary success-envelope unwrapping', async () => {
  apiClient.defaults.adapter = async config => ({ config, status: 200, statusText: 'OK', data: { code: 0, message: 'user config value', data: { nested: true } }, headers: { 'x-sub2api-plugin-revision': '4' } })
  const config = await apiClient.get('/admin/plugins/7/config', { rawPluginConfig: true })
  expect(config.data).toEqual({ code: 0, message: 'user config value', data: { nested: true } })
  expect(config.headers['x-sub2api-plugin-revision']).toBe('4')
  const normal = await apiClient.get('/ordinary-resource')
  expect(normal.data).toEqual({ nested: true })
})
