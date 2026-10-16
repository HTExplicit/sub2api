export default {
  deepseekReasoning: {
    title: 'DeepSeek Default Reasoning Effort',
    description: 'Use the configured effort when a downstream request omits its reasoning effort.',
    enabled: 'Enable DeepSeek default reasoning effort',
    effort: 'Default reasoning effort',
    unsaved: 'Unsaved changes',
    rules: 'Only fills omitted effort. Group mappings, ceilings and deny rules still apply. Explicit client effort and disabled thinking follow existing rules. Max may increase reasoning time and usage.',
    scope: 'Covers Responses, Chat Completions and Messages, including relay providers, using the final mapped DeepSeek model. Older models with unknown or unsupported effort capabilities are left unchanged. Turning this off restores existing behavior.',
    saved: 'DeepSeek default reasoning effort saved',
    loadFailed: 'Unable to load DeepSeek reasoning settings',
    saveFailed: 'Unable to save DeepSeek reasoning settings'
  }
}
