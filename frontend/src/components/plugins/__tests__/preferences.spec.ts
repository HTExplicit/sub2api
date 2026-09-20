import { beforeEach, describe, expect, it } from 'vitest'
import { pluginPreferenceKey, readPluginPreference } from '../preferences'

describe('plugin browser preferences', () => {
  beforeEach(() => localStorage.clear())
  it('shares the existing user draft and isolates other users and plugins', () => {
    localStorage.setItem('account-test-text:https://fixture.test:7', 'saved test prompt')
    expect(readPluginPreference('https://fixture.test', 7, 'codexrip.account-tools', 'test-prompt')).toBe('saved test prompt')
    expect(readPluginPreference('https://fixture.test', 8, 'codexrip.account-tools', 'test-prompt')).toBeNull()
    expect(readPluginPreference('https://fixture.test', 7, 'codexrip.prompt-skills', 'test-prompt')).toBeNull()
    localStorage.setItem(pluginPreferenceKey('https://fixture.test', 7, 'codexrip.account-tools', 'test-prompt'), 'new draft')
    expect(readPluginPreference('https://fixture.test', 7, 'codexrip.account-tools', 'test-prompt')).toBe('new draft')
  })
})
