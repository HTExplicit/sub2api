import { computed, ref, watch } from 'vue'
import type { PromptConfig, PromptConfigWrite, PromptContent, PromptRule, PromptHistoryVersion } from '@/api/admin/systemPromptRules'

const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value))
const equal = (left: unknown, right: unknown) => JSON.stringify(left) === JSON.stringify(right)

function mergeFields<T extends object>(base: T | undefined, local: T, remote: T): T {
  const result = clone(remote)
  for (const key of Object.keys(local) as (keyof T)[]) {
    if (!equal(local[key], base?.[key])) result[key] = clone(local[key])
  }
  return result
}

// Apply only local edits to the latest snapshot. Rules are matched by their stable
// ID so an external addition is not lost when an administrator keeps their draft.
export function mergePromptDraft(base: PromptConfig, local: PromptConfig, remote: PromptConfig): PromptConfig {
  const result = clone(remote)
  for (const key of ['enabled', 'expose_server_prompt', 'compact_enabled'] as const) {
    if (local[key] !== base[key]) result[key] = local[key]
  }
  const baseRules = new Map(base.policy.rules.map(rule => [rule.id, rule]))
  const localRules = new Map(local.policy.rules.map(rule => [rule.id, rule]))
  const remoteRules = new Map(remote.policy.rules.map(rule => [rule.id, rule]))
  const orderChanged = !equal(base.policy.rules.map(rule => rule.id), local.policy.rules.map(rule => rule.id))
  const orderedIDs = orderChanged
    ? [...localRules.keys(), ...[...remoteRules.keys()].filter(id => !baseRules.has(id) && !localRules.has(id))]
    : [...remoteRules.keys(), ...[...localRules.keys()].filter(id => !remoteRules.has(id))]
  result.policy.rules = orderedIDs.flatMap(id => {
    const original = baseRules.get(id), ours = localRules.get(id), theirs = remoteRules.get(id)
    if (!ours) return !original && theirs ? [theirs] : []
    if (!theirs) {
      const unchanged = equal(ours, original) && equal(local.contents[id], base.contents[id]) && local.policy.default_rule_ids.includes(id) === base.policy.default_rule_ids.includes(id)
      return unchanged ? [] : [clone(ours)]
    }
    const merged = mergeFields(original, ours, theirs)
    // The one-time content migration replaces template/version references,
    // while the stable rule and any locally edited body keep their identity.
    if (original && theirs.template_id !== original.template_id) {
      merged.template_id = theirs.template_id
      merged.version_id = theirs.version_id
    }
    return [merged]
  })
  if (orderChanged) result.policy.rules.forEach((rule, index) => { rule.order = (index + 1) * 100 })
  const defaults = new Set(remote.policy.default_rule_ids)
  for (const id of new Set([...base.policy.default_rule_ids, ...local.policy.default_rule_ids])) {
    if (local.policy.default_rule_ids.includes(id) !== base.policy.default_rule_ids.includes(id)) {
      if (local.policy.default_rule_ids.includes(id)) defaults.add(id)
      else defaults.delete(id)
    }
  }
  result.policy.default_rule_ids = [...defaults].filter(id => result.policy.rules.some(rule => rule.id === id))
  result.contents = Object.fromEntries(result.policy.rules.flatMap(rule => {
    const ours = local.contents[rule.id], theirs = remote.contents[rule.id]
    if (!ours && !theirs) return []
    const content = ours && theirs ? mergeFields(base.contents[rule.id], ours, theirs) : clone((ours || theirs)!)
    content.template_id = rule.template_id
    content.version_id = rule.version_id
    return [[rule.id, content]]
  }))
  return result
}

export function useSystemPromptConfigDraft(storageKey: string) {
  let activeStorageKey = storageKey
  const baseline = ref<PromptConfig | null>(null)
  const draft = ref<PromptConfig | null>(null)
  const pendingRemote = ref<PromptConfig | null>(null)
  const bodyBases = ref<Record<string, string>>({})
  const selectedID = ref('')
  try {
    const stored = JSON.parse(sessionStorage.getItem(storageKey) || 'null')
    if (stored?.baseline?.policy?.version === 2 && stored?.draft?.policy?.version === 2) {
      baseline.value = stored.baseline
      draft.value = stored.draft
      bodyBases.value = stored.bodyBases || {}
      selectedID.value = stored.selectedID || ''
    }
  } catch { /* An unavailable browser session store must not block editing. */ }

  const dirty = computed(() => !!baseline.value && !!draft.value && !equal(
    { policy: draft.value.policy, enabled: draft.value.enabled, expose: draft.value.expose_server_prompt, compact: draft.value.compact_enabled, contents: draft.value.contents },
    { policy: baseline.value.policy, enabled: baseline.value.enabled, expose: baseline.value.expose_server_prompt, compact: baseline.value.compact_enabled, contents: baseline.value.contents },
  ))
  const selectedRule = computed(() => draft.value?.policy.rules.find(rule => rule.id === selectedID.value) || null)
  const selectedContent = computed(() => draft.value?.contents[selectedID.value] || null)
  const contentEdits = computed(() => Object.fromEntries((draft.value?.policy.rules || []).flatMap(rule => {
    const content = draft.value?.contents[rule.id]
    if (!content || content.managed || (rule.template_id !== 0 && !content.restore_version_id && content.body === bodyBases.value[rule.id])) return []
    return [[rule.id, { body: content.body, ...(content.restore_version_id ? { restore_version_id: content.restore_version_id } : {}) }]]
  })))
  const ruleDirty = (id: string) => !equal(draft.value?.policy.rules.find(rule => rule.id === id), baseline.value?.policy.rules.find(rule => rule.id === id)) || !equal(draft.value?.contents[id], baseline.value?.contents[id]) || draft.value?.policy.default_rule_ids.includes(id) !== baseline.value?.policy.default_rule_ids.includes(id)

  function selectAvailable() {
    if (!draft.value?.policy.rules.some(rule => rule.id === selectedID.value)) selectedID.value = draft.value?.policy.rules[0]?.id || ''
  }
  function accept(next: PromptConfig) {
    baseline.value = clone(next)
    draft.value = clone(next)
    bodyBases.value = Object.fromEntries(Object.entries(next.contents).map(([id, content]) => [id, content.body]))
    pendingRemote.value = null
    selectAvailable()
  }
  function receive(next: PromptConfig) {
    if (!dirty.value) { accept(next); return }
    if (baseline.value?.revision !== next.revision) pendingRemote.value = clone(next)
    if (draft.value) draft.value.capabilities = clone(next.capabilities)
  }
  function keepDraft() {
    if (!baseline.value || !draft.value || !pendingRemote.value) return
    const next = pendingRemote.value
    draft.value = mergePromptDraft(baseline.value, draft.value, next)
    bodyBases.value = Object.fromEntries(Object.entries(next.contents).map(([id, content]) => [id, content.body]))
    baseline.value = clone(next)
    pendingRemote.value = null
    selectAvailable()
  }
  function saved(sent: PromptConfig, next: PromptConfig) {
    const current = draft.value
    draft.value = current ? mergePromptDraft(sent, current, next) : clone(next)
    baseline.value = clone(next)
    bodyBases.value = Object.fromEntries(Object.entries(next.contents).map(([id, content]) => [id, content.body]))
    pendingRemote.value = null
    selectAvailable()
  }
  function request(): PromptConfigWrite | null {
    if (!draft.value || !baseline.value || pendingRemote.value) return null
    return {
      expected_revision: baseline.value.revision,
      enabled: draft.value.enabled,
      policy: clone(draft.value.policy),
      contents: clone(contentEdits.value),
    }
  }
  function add(name = '', body = '') {
    if (!draft.value) return
    const id = `rule-${crypto.randomUUID().slice(0, 12)}`
    draft.value.policy.rules.push({ id, name, enabled: true, template_id: 0, version_id: 0, order: (draft.value.policy.rules.length + 1) * 100, role: 'auto', position: 'control_append', platforms: [], model_match: 'upstream', models: [] })
    draft.value.contents[id] = { body, composition_mode: 'inline', managed: false, template_id: 0, version_id: 0 }
    bodyBases.value[id] = ''
    selectedID.value = id
  }
  function remove(id: string) {
    if (!draft.value) return
    draft.value.policy.rules = draft.value.policy.rules.filter(rule => rule.id !== id)
    draft.value.policy.default_rule_ids = draft.value.policy.default_rule_ids.filter(ruleID => ruleID !== id)
    delete draft.value.contents[id]
    selectAvailable()
  }
  function useVersion(rule: PromptRule, content: PromptContent) {
    if (!draft.value) return
    rule.template_id = content.template_id
    rule.version_id = content.version_id
    draft.value.contents[rule.id] = clone(content)
    bodyBases.value[rule.id] = content.body
  }
  function restore(version: PromptHistoryVersion) {
    const content = selectedContent.value
    if (!content || !version.restorable || contentEdits.value[selectedID.value]) return
    content.body = version.body
    content.restore_version_id = version.id
  }
  function switchSession(nextKey: string) {
    activeStorageKey = nextKey
    baseline.value = null; draft.value = null; pendingRemote.value = null; bodyBases.value = {}; selectedID.value = ''
    try {
      const stored = JSON.parse(sessionStorage.getItem(nextKey) || 'null')
      if (stored?.baseline?.policy?.version === 2 && stored?.draft?.policy?.version === 2) {
        baseline.value = stored.baseline; draft.value = stored.draft; bodyBases.value = stored.bodyBases || {}; selectedID.value = stored.selectedID || ''
      }
    } catch { /* Only this session's valid draft can be restored. */ }
  }
  watch([draft, baseline, selectedID, bodyBases], () => {
    try {
      if (dirty.value) sessionStorage.setItem(activeStorageKey, JSON.stringify({ baseline: baseline.value, draft: draft.value, bodyBases: bodyBases.value, selectedID: selectedID.value }))
      else sessionStorage.removeItem(activeStorageKey)
    } catch { /* Keep the in-memory draft if storage is full or disabled. */ }
  }, { deep: true, flush: 'post' })
  return { baseline, draft, dirty, pendingRemote, selectedID, selectedRule, selectedContent, contentEdits, ruleDirty, receive, accept, keepDraft, saved, request, add, remove, useVersion, restore, switchSession }
}
