// Public admin API facade. All prompt writes share the atomic config endpoint.
export { rulesAPI as systemPromptsAPI, rulesAPI as default } from './systemPromptRules'
export type { SystemPromptCompositionMode, PromptConfig, PromptConfigWrite, PromptHistoryVersion } from './systemPromptRules'
