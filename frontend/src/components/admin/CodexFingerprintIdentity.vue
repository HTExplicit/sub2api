<template>
  <div class="space-y-5" data-test="fingerprint-identity">
    <section class="space-y-3">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h3 class="font-semibold text-ink">{{ text('固定设备身份', 'Fixed device identity') }}</h3>
        <span class="text-sm text-muted" data-test="fingerprint-persistence">{{ persistence }}</span>
      </div>
      <p class="text-sm text-muted">{{ text('身份来源账号', 'Identity source account') }} #{{ view.identity.identity_account_id }}<span v-if="view.identity.identity_account_id !== accountId">{{ text(' · 使用父账号身份、种子和设备标识', ' · Uses the parent account identity, seed and device identifier') }}</span></p>
      <dl class="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2 xl:grid-cols-4">
        <div v-for="field in deviceFields" :key="field.label"><dt class="text-muted">{{ field.label }}</dt><dd class="break-words font-mono text-ink">{{ field.value }}</dd></div>
      </dl>
      <p class="text-xs text-muted">{{ text('平台沙箱标签用于指纹兼容映射；实际权限与沙箱状态由请求决定。', 'The platform sandbox label is used for fingerprint compatibility mapping; actual permissions and sandbox state come from the request.') }}</p>
    </section>

    <section class="space-y-3 border-t border-line pt-4">
      <h3 class="font-semibold text-ink">{{ text('当前策略，适用于新请求', 'Current policy for new requests') }}</h3>
      <p v-if="!view.simulation_enabled" class="text-sm text-ink" data-test="fingerprint-client-identity">{{ text('模拟总开关已关闭：保留客户端身份与标识。下面的 UA、Originator 和版本仅用于缺失身份头的协议兜底，不代表客户端实际设备。', 'Simulation is off: retain client identity and identifiers. The UA, Originator and version below are protocol fallbacks for missing identity headers, not the actual client device.') }}</p>
      <p v-else-if="view.identity.identity_source === 'override_ua'" class="text-sm text-ink" data-test="fingerprint-override">{{ text('账号自定义 UA 已覆盖固定设备身份；客户端版本仍采用全局生效版本。', 'The account UA override replaces the fixed device identity; the client version still follows the globally effective version.') }}</p>
      <dl class="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2 xl:grid-cols-4">
        <div><dt class="text-muted">{{ text('身份来源', 'Identity source') }}</dt><dd class="text-ink">{{ identitySource }}</dd></div>
        <div><dt class="text-muted">{{ text('Originator', 'Originator') }}</dt><dd class="font-mono text-ink">{{ view.identity.originator }}</dd></div>
        <div><dt class="text-muted">{{ view.simulation_enabled ? text('客户端版本', 'Client version') : text('协议兜底版本', 'Protocol fallback version') }}</dt><dd class="font-mono text-ink">{{ view.identity.version }}</dd></div>
        <div><dt class="text-muted">{{ text('保存的收敛模式', 'Saved convergence mode') }}</dt><dd class="text-ink">{{ modeLabel(view.mode) }}{{ !view.identity.fingerprint_mode_configured ? text('（后端默认）', ' (backend default)') : '' }}</dd></div>
        <div><dt class="text-muted">{{ text('生效收敛模式', 'Effective convergence mode') }}</dt><dd class="text-ink">{{ modeLabel(view.identity.fingerprint_mode_effective) }}</dd></div>
        <div class="sm:col-span-2"><dt class="text-muted">{{ text('生效状态', 'Policy status') }}</dt><dd class="text-ink">{{ reason }}</dd></div>
        <template v-if="view.simulation_enabled">
          <div v-for="field in effectiveFields" :key="field.label"><dt class="text-muted">{{ field.label }}</dt><dd class="break-words font-mono text-ink">{{ field.value }}</dd></div>
        </template>
        <div class="sm:col-span-2 xl:col-span-4"><dt class="text-muted">{{ view.simulation_enabled ? text('当前策略 UA', 'Current policy UA') : text('缺失身份头的协议兜底 UA', 'Protocol fallback UA for missing identity headers') }}</dt><dd class="break-words font-mono text-ink" data-test="fingerprint-policy-ua">{{ view.identity.user_agent }}</dd></div>
      </dl>
    </section>

    <section class="space-y-3 border-t border-line pt-4">
      <h3 class="font-semibold text-ink">{{ text('标识处理规则', 'Identifier handling rules') }}</h3>
      <dl class="space-y-3 text-sm">
        <div v-for="field in identifierFields" :key="field.key" class="grid gap-1 sm:grid-cols-[10rem_minmax(0,1fr)]">
          <dt class="text-muted">{{ field.label }}<span class="block font-mono text-xs">{{ field.key }}</span></dt>
          <dd class="min-w-0 text-ink"><span class="block">{{ ruleLabel(field.policy.rule) }}</span><code v-if="field.policy.behavior === 'fixed' && field.policy.value" class="mt-1 block break-all" :data-test="'fingerprint-' + field.key">{{ field.policy.value }}</code></dd>
        </div>
      </dl>
      <p class="text-xs text-muted">{{ text('请求相关的值只展示处理规则。真实回合 ID、父／根回合引用、窗口序号、开始时间及显式自定义缓存键继续保留；此页展示策略，不是已发送请求的抓包记录。', 'Request-dependent values show handling rules only. Real turn IDs, parent/root turn references, window indexes, start times and explicit custom cache keys are preserved. This page describes policy, not a capture of a sent request.') }}</p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CodexFingerprintAccountView, CodexFingerprintMode } from '@/api/admin/codexFingerprint'

const props = defineProps<{ view: CodexFingerprintAccountView; accountId: number }>()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const persistence = computed(() => !props.view.device_identity ? text('身份缺失', 'Identity missing') : props.view.identity.identity_persisted ? text('已持久保存', 'Persisted') : text('按种子派生，尚未持久保存', 'Derived from the seed; not yet persisted'))
const generatedAt = computed(() => {
  const value = props.view.device_identity?.generated_at
  if (!value) return text('未记录', 'Not recorded')
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale.value.startsWith('en') ? 'en-US' : 'zh-CN', { hour12: false })
})
const deviceFields = computed(() => {
  const device = props.view.device_identity
  return [
    { label: text('操作系统', 'Operating system'), value: device?.os_type ?? '—' },
    { label: text('系统版本', 'OS version'), value: device?.os_version ?? '—' },
    { label: text('架构', 'Architecture'), value: device?.arch ?? '—' },
    { label: text('终端', 'Terminal'), value: device?.terminal ?? '—' },
    { label: text('平台沙箱标签', 'Platform sandbox label'), value: device?.sandbox ?? '—' },
    { label: text('身份结构版本', 'Identity schema version'), value: device?.v ?? '—' },
    { label: text('生成时间（本地时间）', 'Generated at (local time)'), value: generatedAt.value }
  ]
})
const effectiveFields = computed(() => {
  const device = props.view.effective_device
  const unknown = text('无法从 UA 确定', 'Cannot determine from UA')
  return [
    { label: text('UA 声明的操作系统', 'OS declared by UA'), value: device?.os_type ?? unknown },
    { label: text('UA 声明的系统版本', 'OS version declared by UA'), value: device?.os_version ?? unknown },
    { label: text('UA 声明的架构', 'Architecture declared by UA'), value: device?.arch ?? unknown },
    { label: text('UA 声明的终端', 'Terminal declared by UA'), value: device?.terminal ?? unknown },
    { label: text('沙箱兼容映射目标', 'Sandbox compatibility mapping target'), value: device?.platform_sandbox ?? unknown }
  ]
})
const identitySource = computed(() => ({ account: text('账号固定 TUI', 'Fixed account TUI'), override_ua: text('账号自定义 UA', 'Account UA override'), canonical: text('全局默认', 'Global default'), protocol_fallback: text('协议兜底', 'Protocol fallback') })[props.view.identity.identity_source] || props.view.identity.identity_source)
const reason = computed(() => ({ ok: text('按当前策略生效', 'Active under the current policy'), seed_missing: text('身份种子缺失，未收敛', 'Missing identity seed; no convergence'), explicit_off: text('保留设备／会话标识；身份模拟仍由总开关控制', 'Preserve device/session identifiers; identity simulation follows the master switch'), simulation_disabled: text('总开关关闭，保留客户端身份与标识', 'Master switch off; retain client identity and identifiers'), non_oauth: text('该账号类型不适用', 'Not applicable to this account type') })[props.view.identity.fingerprint_reason] || props.view.identity.fingerprint_reason)
function modeLabel(mode: CodexFingerprintMode): string {
  return { off: text('保留设备／会话标识', 'Preserve device/session identifiers'), device: text('仅设备', 'Device only'), session: text('设备＋会话', 'Device + session'), full: text('设备＋会话＋线程', 'Device + session + thread') }[mode] || mode
}
const identifierFields = computed(() => [
  { key: 'installation_id', label: text('设备标识', 'Device identifier') },
  { key: 'session_id', label: text('会话标识', 'Session identifier') },
  { key: 'thread_id', label: text('线程标识', 'Thread identifier') },
  { key: 'parent_thread_id', label: text('父线程引用', 'Parent thread reference') },
  { key: 'window_id', label: text('窗口标识', 'Window identifier') }
].map(field => ({ ...field, policy: props.view.identifier_policy?.[field.key as keyof CodexFingerprintAccountView['identifier_policy']] ?? { behavior: 'passthrough', rule: 'unknown', value: null } })))
function ruleLabel(rule: string): string {
  const labels: Record<string, string> = {
    preserve_client: text('保留客户端原值', 'Preserve the client value'),
    account_device: text('使用账号固定设备标识；写入请求元数据', 'Use the fixed account device identifier in request metadata'),
    account_session: text('使用账号固定会话标识', 'Use the fixed account session identifier'),
    mapped_thread: text('按真实线程确定性映射；缺失线程时采用请求会话来源', 'Map each real thread deterministically; use the request session source when no thread is present'),
    account_thread: text('合并为账号固定线程标识', 'Merge into the fixed account thread identifier'),
    mapped_parent_thread: text('与线程使用同一映射，保留父线程关系', 'Use the same thread mapping to preserve parent relationships'),
    remove_parent_self_reference: text('合并线程后移除会形成自引用的父线程字段', 'Remove parent thread fields that would become self-references after merging'),
    mapped_window: text('映射线程前缀，保留请求中的窗口序号', 'Map the thread prefix and preserve the request window index'),
    merged_window: text('使用固定线程前缀，保留请求中的窗口序号', 'Use the fixed thread prefix and preserve the request window index'),
    unknown: text('处理规则未返回，请刷新身份', 'Handling rule not returned; refresh the identity')
  }
  return labels[rule] || rule
}
</script>
