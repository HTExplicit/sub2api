<template>
  <div ref="root" class="space-y-4" tabindex="-1" :aria-label="steps[step - 1]" data-test="pelican-composer">
    <ol class="flex flex-wrap gap-3 text-sm" :aria-label="text('新建测试步骤', 'New test steps')">
      <li v-for="(label, index) in steps" :key="label" :aria-current="step === index + 1 ? 'step' : undefined" :class="step === index + 1 ? 'font-semibold text-primary-600' : 'text-muted'">{{ index + 1 }}. {{ label }}</li>
    </ol>
    <pre v-if="error" role="alert" class="whitespace-pre-wrap break-words text-sm text-red-700 dark:text-red-300">{{ error }}</pre>
    <template v-if="step === 1">
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <label class="text-sm">{{ t('admin.pelicanTests.searchAccounts') }}<input v-model="search" type="search" class="input mt-1 w-full" data-test="pelican-account-search" /></label>
        <label class="text-sm">{{ t('admin.pelicanTests.platform') }}<select v-model="platform" class="input mt-1 w-full"><option value="">{{ t('admin.pelicanTests.allPlatforms') }}</option><option v-for="option in CONCRETE_PLATFORM_OPTIONS" :key="option.value" :value="option.value">{{ option.label }}</option></select></label>
        <label class="text-sm">{{ text('分组', 'Group') }}<select v-model="group" class="input mt-1 w-full"><option value="">{{ text('全部分组', 'All groups') }}</option><option v-for="item in groups" :key="item.id" :value="String(item.id)">{{ item.name }}</option></select></label>
        <label class="text-sm">{{ text('状态', 'Status') }}<select v-model="status" class="input mt-1 w-full"><option value="">{{ text('所有状态', 'All statuses') }}</option><option v-for="state in ['active', 'inactive', 'error', 'disabled']" :key="state" :value="state">{{ t(`admin.codexGatewayBorrow.states.${state}`) }}</option></select></label>
      </div>
      <div class="flex flex-wrap items-center gap-2 text-sm">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="selectPage">{{ text('选择当前页', 'Select this page') }} ({{ accounts.length }})</button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || selectingAll || total > 5000" @click="selectAll">{{ text('选择全部匹配', 'Select all matching') }} ({{ total }})</button>
        <button type="button" class="btn btn-ghost btn-sm" :disabled="selectingAll" @click="selected = []; rows = []">{{ t('admin.pelicanTests.clearSelection') }}</button>
        <span role="status">{{ text('跨页已选', 'Selected across pages') }} {{ selected.length }}{{ selectingAll ? text(' · 正在读取匹配项…', ' · Reading matching accounts…') : '' }}</span>
      </div>
      <p class="text-xs text-muted">{{ text('每页 50 个账号；错误、限流和停用账号仍可明确选择。查看与选择不会调用模型。', '50 accounts per page. Error, limited and disabled accounts remain selectable. Browsing and selecting never calls a model.') }}</p>
      <fieldset :disabled="loading || selectingAll" class="grid gap-2 sm:grid-cols-2" :aria-busy="loading">
        <legend class="sr-only">{{ t('admin.pelicanTests.chooseAccounts') }}</legend>
        <label v-for="account in accounts" :key="account.id" class="flex min-w-0 cursor-pointer items-start gap-3 rounded-lg border border-line p-3">
          <input type="checkbox" class="checkbox mt-1" :checked="selected.includes(account.id)" :data-test="`pelican-account-${account.id}`" @change="toggleAccount(account.id)" />
          <span class="min-w-0"><span class="block break-words font-medium">{{ account.name }} #{{ account.id }}</span><span class="text-xs text-muted">{{ account.platform }} · {{ account.status }}</span></span>
        </label>
      </fieldset>
      <p v-if="!loading && !accounts.length" class="py-6 text-center text-muted">{{ t('admin.pelicanTests.noAccounts') }}</p>
      <Pagination v-if="total" v-model:page="page" :total="total" :page-size="50" :show-page-size-selector="false" />
    </template>
    <template v-else>
      <div v-if="step === 2" class="space-y-4">
        <div class="flex flex-wrap items-end gap-2 rounded-lg border border-line p-3">
          <label class="min-w-40 flex-1 text-sm">{{ text('批量模型（留空保留各自模型）', 'Bulk model (blank keeps each model)') }}<input v-model="bulkModel" class="input mt-1 w-full" /></label>
          <label class="text-sm">{{ t('admin.codexGatewayBorrow.effort') }}<select v-model="bulkEffort" class="input mt-1 w-full"><option value="">{{ t('admin.pelicanTests.normalDefault') }}</option><option v-for="effort in effortLevels" :key="effort" :value="effort">{{ effort }}</option></select></label>
          <button type="button" class="btn btn-secondary" @click="applyBulk">{{ text('批量设置兼容组合', 'Apply to compatible combinations') }}</button>
        </div>
        <p v-if="bulkMessage" role="status" class="text-sm">{{ bulkMessage }}</p>
        <div v-for="id in selected" :key="id" class="space-y-3 rounded-lg border border-line p-3">
          <div class="flex items-center justify-between gap-2"><h3 class="min-w-0 break-words font-medium">{{ options[id]?.name || names[id] || id }} #{{ id }}</h3><button type="button" class="btn btn-secondary btn-sm" @click="addRow(id)">{{ t('admin.pelicanTests.addModel') }}</button></div>
          <p v-if="options[id]?.capability_reason" class="text-xs text-muted">{{ options[id].capability_reason }}</p>
          <div v-for="row in rows.filter(item => item.account_id === id)" :key="row.key" class="space-y-1">
            <div class="flex flex-wrap items-end gap-2">
              <label class="min-w-36 flex-1 text-sm">{{ t('admin.pelicanTests.model') }}<input v-model="row.model_id" :list="`pelican-models-${id}`" class="input mt-1 w-full" :data-test="`pelican-model-${row.key}`" @input="row.effort = ''" /></label>
              <label class="text-sm">{{ t('admin.codexGatewayBorrow.effort') }}<select v-model="row.effort" class="input mt-1" :data-test="`pelican-effort-${row.key}`"><option value="">{{ t('admin.pelicanTests.normalDefault') }}</option><option v-for="effort in modelOption(row)?.reasoning_efforts || []" :key="effort" :value="effort">{{ effort }}</option></select></label>
              <button type="button" class="btn btn-ghost btn-sm" :aria-label="`${t('admin.pelicanTests.removeModel')} ${row.model_id} #${id}`" @click="removeRow(row.key)">{{ text('移除', 'Remove') }}</button>
            </div>
            <p v-if="issue(row)" role="alert" class="text-xs text-red-700 dark:text-red-300">{{ issue(row) }}</p>
            <p v-else-if="!modelOption(row)" class="text-xs text-amber-800 dark:text-amber-200">{{ text('手动模型：未经过本地目录确认，提交时仍会检查发送能力。', 'Manual model: not confirmed by the local catalog; sender capability is checked on submission.') }}</p>
            <p v-else class="text-xs text-muted">{{ t('admin.pelicanTests.mappedModel') }}: {{ modelOption(row)?.upstream_model }}</p>
          </div>
          <datalist :id="`pelican-models-${id}`"><option v-for="model in options[id]?.models || []" :key="model.id" :value="model.id">{{ model.text_supported ? model.display_name : model.capability_reason }}</option></datalist>
        </div>
      </div>
      <div v-else class="space-y-4" data-test="pelican-review">
        <p class="font-medium">{{ text('本任务将生成', 'This task will generate') }} {{ rows.length }} {{ text('个账号／模型组合', 'account/model combinations') }}</p>
        <ul class="max-h-64 space-y-2 overflow-auto rounded-lg border border-line p-3 text-sm"><li v-for="row in rows" :key="row.key" class="break-words">{{ options[row.account_id]?.name }} #{{ row.account_id }} · {{ row.model_id }} · {{ row.effort || t('admin.pelicanTests.normalDefault') }}</li></ul>
        <label class="flex flex-wrap items-center gap-2 text-sm">{{ text('每项生成预算', 'Generation budget per item') }}<input v-model.number="budgetMinutes" class="input w-24" type="number" min="1" max="30" step="1" data-test="pelican-budget" />{{ t('admin.pelicanTests.minutes') }}</label>
        <p v-if="!budgetValid" role="alert" class="text-red-700 dark:text-red-300">{{ t('admin.pelicanTests.budgetBoundary') }}</p>
        <p class="text-sm text-muted">{{ text('共享 10 个执行位，同账号串行。排队、准备和生成分别计时。关闭此页面后任务继续；停止需要明确点击任务的停止按钮。', 'Ten shared execution slots; one request at a time per account. Queue, preparation and generation are timed separately. Tasks continue after this page closes; use the task Stop button to cancel.') }}</p>
      </div>
      <details class="rounded-lg border border-line p-3"><summary class="cursor-pointer text-sm">{{ t('admin.pelicanTests.viewPrompt') }}</summary><p class="mt-2 whitespace-pre-wrap text-sm">{{ PELICAN_PROMPT }}</p></details>
    </template>
    <div class="flex justify-between gap-3 border-t border-line pt-4">
      <button type="button" class="btn btn-secondary" :disabled="step === 1 || submitting" @click="step--">{{ text('上一步', 'Back') }}</button>
      <button v-if="step < 3" type="button" class="btn btn-primary" :disabled="!selected.length || optionsLoading || selectingAll || (step === 2 && !valid)" data-test="pelican-next" @click="step++">{{ text('下一步', 'Next') }}</button>
      <button v-else type="button" class="btn btn-primary" :disabled="!valid || !budgetValid || submitting" data-test="pelican-test-start" @click="submit">{{ submitting ? text('正在提交…', 'Submitting…') : text('启动后台任务', 'Start background task') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Pagination from '@/components/common/Pagination.vue'
import { accountsAPI, type AccountListFilters } from '@/api/admin/accounts'
import { groupsAPI } from '@/api/admin/groups'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import { pelicanTestsAPI, PELICAN_PROMPT, type PelicanAccountOption, type PelicanTestRequest } from '@/api/admin/pelicanTests'
import { applyPelicanBulk, pelicanDefaultModel, pelicanDuplicateKeys, pelicanCombinationIssue, type PelicanCombination } from '@/utils/pelicanTaskSelection'
import type { AccountListItem, AdminGroup } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ initial?: Omit<PelicanTestRequest, 'client_task_id'>; submitting?: boolean }>()
const emit = defineEmits<{ submit: [Omit<PelicanTestRequest, 'client_task_id'>] }>()
const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const steps = computed(() => [t('admin.pelicanTests.chooseAccounts'), text('配置模型与档位', 'Models and reasoning'), text('确认组合并启动', 'Review and start')])
const root = ref<HTMLElement | null>(null)
const lifecycle = new AbortController()
const accounts = shallowRef<AccountListItem[]>([]), groups = shallowRef<AdminGroup[]>([])
const selected = ref<number[]>([]), rows = ref<PelicanCombination[]>([])
const options = ref<Record<number, PelicanAccountOption>>({}), names = ref<Record<number, string>>({})
const search = ref(''), platform = ref(''), group = ref(''), status = ref(''), page = ref(1), total = ref(0)
const step = ref(props.initial ? 2 : 1), budgetMinutes = ref((props.initial?.generation_timeout_seconds || 600) / 60)
const optionRequests = ref(0)
const optionsLoading = computed(() => optionRequests.value > 0)
const loading = ref(false), selectingAll = ref(false), error = ref(''), bulkMessage = ref('')
const bulkModel = ref(''), bulkEffort = ref(''), effortLevels = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']
let key = 0, pageRequest: AbortController | undefined, debounce: ReturnType<typeof setTimeout> | undefined
const duplicates = computed(() => pelicanDuplicateKeys(rows.value))
const valid = computed(() => rows.value.length > 0 && rows.value.length <= 5000 && !optionsLoading.value && !rows.value.some(row => issue(row)))
const budgetValid = computed(() => Number.isInteger(budgetMinutes.value) && budgetMinutes.value >= 1 && budgetMinutes.value <= 30)
function modelOption(row: PelicanCombination) { return options.value[row.account_id]?.models.find(model => model.id === row.model_id.trim()) }
function issue(row: PelicanCombination): string {
  if (duplicates.value.has(row.key)) return text('同一账号与模型只能有一个档位，请修改已有行或移除重复行。', 'Use one reasoning level per account/model; edit the existing row or remove duplicates.')
  const value = pelicanCombinationIssue(row, options.value[row.account_id])
  if (value === 'options_pending') return t('common.loading')
  if (value === 'model_required') return text('没有有效的默认模型，请选择或输入模型。', 'No valid default model; select or enter one.')
  if (value === 'effort_unsupported') return text('该模型不支持所选档位，请修改。', 'This model does not support the selected reasoning level.')
  return value
}
function filters(): AccountListFilters { return { search: search.value.trim(), platform: platform.value, group_id: group.value, status: status.value, lite: 'true' } }
async function loadPage() {
  pageRequest?.abort()
  const request = new AbortController(); pageRequest = request
  loading.value = true
  try {
    const result = await accountsAPI.list(page.value, 50, filters(), { signal: request.signal })
    if (request.signal.aborted || lifecycle.signal.aborted) return
    accounts.value = result.items; total.value = result.total
    for (const account of result.items) names.value[account.id] = account.name
  } catch (value) { if (!request.signal.aborted) error.value = extractApiErrorMessage(value, text('读取账号失败', 'Could not load accounts')) }
  finally { if (pageRequest === request) loading.value = false }
}
function toggleAccount(id: number) { selected.value = selected.value.includes(id) ? selected.value.filter(value => value !== id) : [...selected.value, id] }
function selectPage() { selected.value = [...new Set([...selected.value, ...accounts.value.map(account => account.id)])] }
async function selectAll() {
  selectingAll.value = true; const matching = filters(); const ids = new Set(selected.value)
  try {
    for (let next = 1; !lifecycle.signal.aborted; next++) {
      const result = await accountsAPI.list(next, 50, matching, { signal: lifecycle.signal })
      if (result.total > 5000) throw new Error(text('最多选择 5000 个账号，请缩小筛选范围。', 'Select up to 5000 accounts; narrow the filters.'))
      for (const account of result.items) { ids.add(account.id); names.value[account.id] = account.name }
      if (ids.size > 5000) throw new Error(text('跨页最多选择 5000 个账号，请先缩小选择。', 'Select at most 5000 accounts across pages; narrow the selection.'))
      if (next * 50 >= result.total || !result.items.length) break
    }
    if (!lifecycle.signal.aborted) selected.value = [...ids]
  } catch (value) { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, text('读取匹配项失败', 'Could not load matching accounts')) }
  finally { selectingAll.value = false }
}
async function loadOptions(ids: number[]) {
  optionRequests.value++
  try {
    for (let index = 0; index < ids.length; index += 100) {
      const result = await pelicanTestsAPI.getAccountOptions(ids.slice(index, index + 100), lifecycle.signal)
      if (lifecycle.signal.aborted) return
      for (const account of result.accounts) {
        options.value[account.id] = account
        if (selected.value.includes(account.id) && !rows.value.some(row => row.account_id === account.id)) rows.value.push({ key: ++key, account_id: account.id, model_id: pelicanDefaultModel(account), effort: '' })
      }
    }
  } catch (value) { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, text('读取模型选项失败', 'Could not load model options')) }
  finally { optionRequests.value-- }
}
function addRow(id: number) {
  const available = options.value[id]?.models.find(model => model.text_supported && !rows.value.some(row => row.account_id === id && row.model_id === model.id))
  if (rows.value.some(row => row.account_id === id && !row.model_id.trim())) { error.value = text('请先填写该账号已有的空白模型行。', 'Fill the existing blank model row for this account first.'); return }
  rows.value.push({ key: ++key, account_id: id, model_id: available?.id || '', effort: '' })
}
function removeRow(rowKey: number) { const id = rows.value.find(row => row.key === rowKey)?.account_id; rows.value = rows.value.filter(row => row.key !== rowKey); if (id && !rows.value.some(row => row.account_id === id)) selected.value = selected.value.filter(value => value !== id) }
function applyBulk() { const result = applyPelicanBulk(rows.value, options.value, bulkModel.value, bulkEffort.value); rows.value = result.rows; bulkMessage.value = result.duplicate ? text('会产生重复组合，未应用；请修改已有行。', 'Would create duplicates; edit the existing rows.') : `${text('已保留不兼容账号的原参数', 'Incompatible accounts kept their parameters')}: ${[...new Set(result.skipped)].join(', ') || '0'}` }
function submit() { if (valid.value && budgetValid.value) emit('submit', { generation_timeout_seconds: budgetMinutes.value * 60, targets: rows.value.map(row => ({ account_id: row.account_id, model_id: row.model_id.trim(), effort: row.effort })) }) }
watch(selected, ids => {
  rows.value = rows.value.filter(row => ids.includes(row.account_id))
  for (const id of ids) if (options.value[id] && !rows.value.some(row => row.account_id === id)) rows.value.push({ key: ++key, account_id: id, model_id: pelicanDefaultModel(options.value[id]), effort: '' })
  const missing = ids.filter(id => !options.value[id]); if (missing.length) void loadOptions(missing)
})
watch([search, platform, group, status], () => { if (debounce) clearTimeout(debounce); pageRequest?.abort(); debounce = setTimeout(() => { if (page.value !== 1) page.value = 1; else void loadPage() }, 250) })
watch(step, async () => { await nextTick(); root.value?.focus() })
watch(page, () => { void loadPage() })
onMounted(() => {
  if (props.initial) { rows.value = props.initial.targets.map(row => ({ ...row, key: ++key })); selected.value = [...new Set(rows.value.map(row => row.account_id))] }
  void loadPage()
  void groupsAPI.getAllIncludingInactive().then(value => { if (!lifecycle.signal.aborted) groups.value = value }).catch(value => { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, text('读取分组失败', 'Could not load groups')) })
})
onBeforeUnmount(() => { lifecycle.abort(); pageRequest?.abort(); if (debounce) clearTimeout(debounce) })
</script>
