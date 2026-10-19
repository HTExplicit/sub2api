<template>
  <div class="space-y-5" data-test="pelican-composer">
    <pre v-if="error" role="alert" class="whitespace-pre-wrap break-words rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200">{{ error }}</pre>
    <fieldset :disabled="submitting" class="min-w-0 disabled:opacity-70">
      <legend class="sr-only">{{ text('配置鹈鹕测试', 'Configure Pelican test') }}</legend>
      <div class="grid min-w-0 gap-5 xl:grid-cols-[minmax(300px,0.85fr)_minmax(0,1.5fr)]">
        <section class="min-w-0 self-start rounded-xl border border-line bg-white dark:bg-dark-900">
          <header class="flex items-center justify-between border-b border-line p-4"><h2 class="font-semibold">{{ text('选择账号', 'Choose accounts') }}</h2><span class="rounded-full bg-primary-50 px-3 py-1 text-xs font-medium text-primary-700 dark:bg-primary-950 dark:text-primary-300">{{ text('已选', 'Selected') }} {{ selected.length }}</span></header>
          <div class="space-y-3 p-4">
            <label class="block text-sm">{{ t('admin.pelicanTests.searchAccounts') }}<input v-model="search" type="search" class="input mt-1 w-full" data-test="pelican-account-search" /></label>
            <div class="grid grid-cols-2 gap-2">
              <label class="text-xs text-muted">{{ t('admin.pelicanTests.platform') }}<select v-model="platform" class="input mt-1 w-full"><option value="">{{ t('admin.pelicanTests.allPlatforms') }}</option><option v-for="option in CONCRETE_PLATFORM_OPTIONS" :key="option.value" :value="option.value">{{ option.label }}</option></select></label>
              <label class="text-xs text-muted">{{ text('分组', 'Group') }}<select v-model="group" class="input mt-1 w-full"><option value="">{{ text('全部分组', 'All groups') }}</option><option v-for="item in groups" :key="item.id" :value="String(item.id)">{{ item.name }}</option></select></label>
            </div>
            <label class="block text-xs text-muted">{{ text('账号状态', 'Account status') }}<select v-model="status" class="input mt-1 w-full"><option value="">{{ text('所有状态', 'All statuses') }}</option><option v-for="state in ['active', 'inactive', 'error', 'disabled']" :key="state" :value="state">{{ t(`admin.pelicanTests.states.${state}`) }}</option></select></label>
            <div class="flex flex-wrap items-center gap-2 border-t border-line pt-3 text-xs">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || selectingAll" @click="selectPage">{{ text('选择本页', 'Select page') }} · {{ accounts.length }}</button>
              <button type="button" class="btn btn-ghost btn-sm" :disabled="loading || selectingAll || total > 5000" @click="selectAll">{{ text('全部匹配', 'All matching') }} · {{ total }}</button>
            </div>
            <p v-if="selectingAll" role="status" class="text-xs text-muted">{{ text('正在读取全部匹配账号…', 'Reading all matching accounts…') }}</p>
          </div>
          <fieldset :disabled="loading || selectingAll" class="max-h-[30rem] divide-y divide-gray-100 overflow-y-auto border-y border-line dark:divide-dark-800" :aria-busy="loading">
            <legend class="sr-only">{{ t('admin.pelicanTests.chooseAccounts') }}</legend>
            <label v-for="account in accounts" :key="account.id" class="flex min-w-0 cursor-pointer items-center gap-3 px-4 py-3 transition-colors hover:bg-gray-50 dark:hover:bg-dark-800" :class="selected.includes(account.id) ? 'bg-primary-50 dark:bg-primary-950' : ''">
              <input type="checkbox" class="checkbox shrink-0" :checked="selected.includes(account.id)" :data-test="`pelican-account-${account.id}`" @change="toggleAccount(account.id)" />
              <span class="min-w-0 flex-1"><span class="block break-words text-sm font-medium">{{ account.name }}</span><span class="text-xs text-muted">#{{ account.id }} · {{ account.platform }}</span></span>
              <span class="shrink-0 text-xs text-muted">{{ t(`admin.pelicanTests.states.${account.status}`) }}</span>
            </label>
            <p v-if="loading" role="status" class="p-5 text-center text-sm text-muted">{{ t('common.loading') }}</p>
            <p v-else-if="!accounts.length" class="p-5 text-center text-sm text-muted">{{ t('admin.pelicanTests.noAccounts') }}</p>
          </fieldset>
          <div v-if="total" class="p-3"><Pagination v-model:page="page" :total="total" :page-size="50" :show-page-size-selector="false" /></div>
        </section>
        <section class="min-w-0 space-y-4" :aria-label="text('模型与检测配置', 'Models and test configuration')">
          <div class="rounded-xl border border-line bg-white p-4 dark:bg-dark-900">
            <h2 class="font-semibold">{{ text('检测设置', 'Test settings') }}</h2>
            <fieldset class="mt-3 flex flex-wrap gap-4"><legend class="sr-only">{{ text('检测项目', 'Tests') }}</legend><label class="flex items-center gap-2 text-sm"><input v-model="testItems" type="checkbox" value="pelican" />{{ text('鹈鹕动画与视觉审核', 'Animation and visual review') }}</label><label class="flex items-center gap-2 text-sm"><input v-model="testItems" type="checkbox" value="candy" />{{ text('糖果推理', 'Candy reasoning') }}</label></fieldset>
            <p v-if="!testItems.length" role="alert" class="mt-2 text-sm text-red-700 dark:text-red-300">{{ text('至少选择一个检测项目。', 'Select at least one test.') }}</p>
            <div class="mt-4 grid gap-3 sm:grid-cols-2">
              <label for="pelican-repeat-count" class="text-sm">{{ text('每个模型测试次数', 'Runs per model') }}<input id="pelican-repeat-count" v-model.number="repeatCount" type="number" inputmode="numeric" min="1" :max="repeatMaximum" step="1" class="input mt-1 w-full" :aria-invalid="!repeatValid" aria-describedby="pelican-repeat-help" data-test="pelican-repeat-count" /></label>
              <label class="text-sm">{{ text('每项生成时限（分钟）', 'Generation limit (minutes)') }}<input v-model.number="budgetMinutes" class="input mt-1 w-full" type="number" min="1" max="30" step="1" :aria-invalid="!budgetValid" data-test="pelican-budget" /></label>
            </div>
            <p id="pelican-repeat-help" class="mt-2 text-xs" :class="repeatValid ? 'text-muted' : 'text-red-700 dark:text-red-300'" :role="repeatValid ? undefined : 'alert'">{{ text('次数须为正整数，当前每个模型最多', 'Enter a positive integer, up to') }} {{ repeatMaximum }} {{ text('次；整批最多 5000 条测试。', 'runs per model; at most 5000 tests per task.') }}</p>
            <p v-if="!budgetValid" role="alert" class="mt-2 text-sm text-red-700 dark:text-red-300">{{ t('admin.pelicanTests.budgetBoundary') }}</p>
          </div>
          <div class="flex items-center justify-between gap-3"><h2 class="font-semibold">{{ text('已选模型组合', 'Selected combinations') }} <span class="ml-1 text-sm font-normal text-muted">{{ rows.length }}</span></h2><button type="button" class="btn btn-ghost btn-sm" :disabled="selectingAll" @click="clear">{{ text('清空草稿', 'Clear draft') }}</button></div>
          <details v-if="selected.length" class="rounded-lg border border-line bg-white p-3 dark:bg-dark-900">
            <summary class="cursor-pointer text-sm font-medium">{{ text('批量设置模型与思考强度', 'Set models and reasoning in bulk') }}</summary>
            <div class="mt-3 flex flex-wrap items-end gap-2">
              <div class="min-w-40 flex-1 text-sm"><label for="pelican-bulk-model">{{ t('admin.pelicanTests.bulkModel') }}</label><Select id="pelican-bulk-model" v-model="bulkModel" class="mt-1" :aria-label="t('admin.pelicanTests.bulkModel')" :options="bulkModelOptions" searchable :disabled="optionsLoading || submitting" data-test="pelican-bulk-model" /></div>
              <label class="text-sm">{{ t('admin.pelicanTests.effort') }}<select v-model="bulkEffort" class="input mt-1 w-full"><option value="">{{ t('admin.pelicanTests.normalDefault') }}</option><option v-for="effort in effortLevels" :key="effort" :value="effort">{{ effort }}</option></select></label>
              <button type="button" class="btn btn-secondary" :disabled="optionsLoading" @click="applyBulk">{{ text('应用到兼容组合', 'Apply to compatible rows') }}</button>
            </div><p v-if="bulkMessage" role="status" class="mt-2 text-sm">{{ bulkMessage }}</p>
          </details>
          <div v-if="!selected.length" class="rounded-xl border border-dashed border-line px-6 py-14 text-center"><p class="font-medium">{{ text('从账号列表开始', 'Start with an account') }}</p><p class="mt-2 text-sm text-muted">{{ text('选择账号后，在这里配置模型；同一账号可以添加多个模型。', 'Choose accounts to configure their models here. Each account can use multiple models.') }}</p></div>
          <div v-for="entry in rowGroups" :key="entry.id" class="min-w-0 space-y-3 rounded-xl border border-line bg-white p-4 dark:bg-dark-900">
            <div class="flex items-center justify-between gap-3"><h3 class="min-w-0 break-words font-medium">{{ options[entry.id]?.name || names[entry.id] || entry.id }} <span class="text-xs font-normal text-muted">#{{ entry.id }}</span></h3><button type="button" class="btn btn-secondary btn-sm shrink-0" :disabled="optionsLoading" @click="addRow(entry.id)">{{ t('admin.pelicanTests.addModel') }}</button></div>
            <p v-if="options[entry.id]?.capability_reason" class="text-xs text-muted">{{ options[entry.id].capability_reason }}</p>
            <p v-if="!entry.rows.length" role="status" class="text-sm text-muted">{{ text('正在读取可用模型…', 'Loading available models…') }}</p>
            <div v-for="row in entry.rows" :key="row.key" class="space-y-1">
              <div class="flex flex-wrap items-end gap-2">
                <div class="min-w-36 flex-1 text-sm"><label :for="`pelican-model-${row.key}`">{{ t('admin.pelicanTests.model') }}</label><Select :id="`pelican-model-${row.key}`" v-model="row.model_id" class="mt-1" :aria-label="`${t('admin.pelicanTests.model')} #${entry.id}`" :options="rowModelOptions(row)" searchable :disabled="optionsLoading || submitting" :error="!!issue(row)" :data-test="`pelican-model-${row.key}`" @change="row.effort = ''" /></div>
                <label class="text-sm">{{ t('admin.pelicanTests.effort') }}<select v-model="row.effort" class="input mt-1 w-full" :data-test="`pelican-effort-${row.key}`"><option value="">{{ t('admin.pelicanTests.normalDefault') }}</option><option v-for="effort in modelOption(row)?.reasoning_efforts || []" :key="effort" :value="effort">{{ effort }}</option></select></label>
                <button type="button" class="btn btn-ghost btn-sm" :aria-label="`${t('admin.pelicanTests.removeModel')} ${row.model_id} #${entry.id}`" @click="removeRow(row.key)">{{ text('移除', 'Remove') }}</button>
              </div>
              <p v-if="issue(row)" role="alert" class="text-xs text-red-700 dark:text-red-300">{{ issue(row) }}</p><p v-else class="break-words text-xs text-muted">{{ t('admin.pelicanTests.mappedModel') }}: {{ modelOption(row)?.upstream_model }}</p>
            </div>
          </div>
          <Pagination v-if="selected.length > 20" v-model:page="selectedPage" :total="selected.length" :page-size="20" :show-page-size-selector="false" />
          <details class="rounded-lg border border-line p-3 text-sm"><summary class="cursor-pointer text-muted">{{ text('提示词与执行说明', 'Prompt and execution details') }}</summary><p class="mt-3 whitespace-pre-wrap">{{ PELICAN_PROMPT }}</p><p class="mt-3 text-xs leading-relaxed text-muted">{{ text('共享 10 个模型请求执行位，同账号不同模型可并行，同模型重复依次执行，并遵守账号自身并发额度。视觉审核使用同一账号和模型；不支持图片时显示未审核。关闭页面后任务继续。', 'Ten shared request slots; different models on one account can run in parallel, while repeats run in order. Account limits apply. Review uses the same account and model; models without image input remain unreviewed. Tasks continue after this page closes.') }}</p></details>
        </section>
      </div>
    </fieldset>
    <footer class="sticky bottom-0 z-20 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-primary-200 bg-white p-4 shadow-lg dark:border-primary-800 dark:bg-dark-900" data-test="pelican-review">
      <div class="min-w-0"><p class="text-sm font-semibold" data-test="pelican-total-tests">{{ rows.length }} {{ text('个账号／模型组合', 'account/model combinations') }} × {{ repeatCount }} {{ text('次', 'runs') }} = {{ totalTests }} {{ text('条测试', 'tests') }}</p><p class="mt-1 text-xs text-muted">{{ testItems.includes('pelican') ? text('鹈鹕与视觉审核', 'Animation and review') : '' }}{{ testItems.length === 2 ? ' + ' : '' }}{{ testItems.includes('candy') ? text('糖果推理', 'Candy reasoning') : '' }} · {{ budgetMinutes }} {{ text('分钟生成时限', 'minute generation limit') }}</p></div>
      <button type="button" class="btn btn-primary min-w-36" :disabled="!valid || !budgetValid || submitting" data-test="pelican-test-start" @click="submit">{{ submitting ? text('正在提交…', 'Submitting…') : text('启动后台测试', 'Start background test') }}</button>
    </footer>
  </div>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import { accountsAPI, type AccountListFilters } from '@/api/admin/accounts'
import { groupsAPI } from '@/api/admin/groups'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import { pelicanTestsAPI, PELICAN_PROMPT, type PelicanAccountOption, type PelicanModelOption, type PelicanTestRequest } from '@/api/admin/pelicanTests'
import { applyPelicanBulk, pelicanDefaultModel, pelicanDuplicateKeys, pelicanCombinationIssue, type PelicanCombination } from '@/utils/pelicanTaskSelection'
import type { AccountListItem, AdminGroup } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ initial?: Omit<PelicanTestRequest, 'client_task_id'>; submitting?: boolean }>()
const emit = defineEmits<{ submit: [Omit<PelicanTestRequest, 'client_task_id'>]; draft: [Omit<PelicanTestRequest, 'client_task_id'>]; clear: [] }>()
const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const lifecycle = new AbortController()
const accounts = shallowRef<AccountListItem[]>([]), groups = shallowRef<AdminGroup[]>([])
const selected = ref<number[]>([]), rows = ref<PelicanCombination[]>([])
const options = ref<Record<number, PelicanAccountOption>>({}), names = ref<Record<number, string>>({})
const search = ref(''), platform = ref(''), group = ref(''), status = ref(''), page = ref(1), total = ref(0)
const selectedPage = ref(1)
const budgetMinutes = ref((props.initial?.generation_timeout_seconds ?? 600) / 60)
const optionRequests = ref(0)
const optionsLoading = computed(() => optionRequests.value > 0)
const loading = ref(false), selectingAll = ref(false), error = ref(''), bulkMessage = ref('')
const bulkModel = ref(''), bulkEffort = ref(''), effortLevels = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']
let key = 0, pageRequest: AbortController | undefined, debounce: ReturnType<typeof setTimeout> | undefined
const duplicates = computed(() => pelicanDuplicateKeys(rows.value))
const testItems = ref(props.initial?.test_items || ['pelican', 'candy'])
const repeatCount = ref(props.initial?.repeat_count ?? 1)
const repeatMaximum = computed(() => Math.floor(5000 / Math.max(1, rows.value.length)))
const repeatValid = computed(() => Number.isInteger(repeatCount.value) && repeatCount.value >= 1 && repeatCount.value <= repeatMaximum.value)
const totalTests = computed(() => rows.value.length * repeatCount.value)
const valid = computed(() => !selectingAll.value && selected.value.length === new Set(rows.value.map(row => row.account_id)).size && repeatValid.value && testItems.value.length > 0 && rows.value.length > 0 && rows.value.length <= 5000 && !optionsLoading.value && !rows.value.some(row => issue(row)))
const budgetValid = computed(() => Number.isInteger(budgetMinutes.value) && budgetMinutes.value >= 1 && budgetMinutes.value <= 30)
function modelOption(row: PelicanCombination) { return options.value[row.account_id]?.models.find(model => model.id === row.model_id.trim()) }
function modelLabel(model: PelicanModelOption) { return model.display_name && model.display_name !== model.id ? `${model.display_name} (${model.id})` : model.id }
function rowModelOptions(row: PelicanCombination) {
  const models = (options.value[row.account_id]?.models || []).map(model => ({ value: model.id, label: `${modelLabel(model)}${model.text_supported ? '' : ` · ${model.capability_reason}`}`, disabled: !model.text_supported }))
  if (row.model_id && !models.some(model => model.value === row.model_id)) models.unshift({ value: row.model_id, label: `${row.model_id} · ${t('admin.pelicanTests.modelUnavailable')}`, disabled: true })
  return models
}
const bulkModelOptions = computed(() => {
  const models = new Map<string, string>()
  for (const id of selected.value) for (const model of options.value[id]?.models || []) if (model.text_supported && !models.has(model.id)) models.set(model.id, modelLabel(model))
  return [{ value: '', label: t('admin.pelicanTests.keepModels') }, ...[...models].sort(([a], [b]) => a.localeCompare(b)).map(([value, label]) => ({ value, label }))]
})
function issue(row: PelicanCombination): string {
  if (duplicates.value.has(row.key)) return text('同一账号与模型只能有一个档位，请修改已有行或移除重复行。', 'Use one reasoning level per account/model; edit the existing row or remove duplicates.')
  const value = pelicanCombinationIssue(row, options.value[row.account_id])
  if (value === 'options_pending') return t('common.loading')
  if (value === 'model_required') return t('admin.pelicanTests.noAvailableModel')
  if (value === 'model_unavailable') return t('admin.pelicanTests.modelUnavailable')
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
function parameters(): Omit<PelicanTestRequest, 'client_task_id'> {
  return { repeat_count: repeatCount.value, generation_timeout_seconds: budgetMinutes.value * 60, test_items: [...testItems.value], targets: selected.value.flatMap(id => {
    const selectedRows = rows.value.filter(row => row.account_id === id)
    return selectedRows.length ? selectedRows.map(row => ({ account_id: id, model_id: row.model_id.trim(), effort: row.effort })) : [{ account_id: id, model_id: '', effort: '' }]
  }) }
}
function submit() { if (valid.value && budgetValid.value && !props.submitting) emit('submit', parameters()) }
function clear() { selected.value = []; rows.value = []; testItems.value = ['pelican', 'candy']; repeatCount.value = 1; budgetMinutes.value = 10; error.value = ''; bulkMessage.value = ''; emit('clear') }
const rowGroups = computed(() => {
  const grouped = new Map<number, PelicanCombination[]>()
  for (const row of rows.value) { const group = grouped.get(row.account_id) || []; group.push(row); grouped.set(row.account_id, group) }
  return selected.value.slice((selectedPage.value - 1) * 20, selectedPage.value * 20).map(id => ({ id, rows: grouped.get(id) || [] }))
})
watch([rows, selected, testItems, repeatCount, budgetMinutes], () => { emit('draft', parameters()); selectedPage.value = Math.min(selectedPage.value, Math.max(1, Math.ceil(selected.value.length / 20))) }, { deep: true })
watch(selected, ids => {
  rows.value = rows.value.filter(row => ids.includes(row.account_id))
  for (const id of ids) if (options.value[id] && !rows.value.some(row => row.account_id === id)) rows.value.push({ key: ++key, account_id: id, model_id: pelicanDefaultModel(options.value[id]), effort: '' })
  const missing = ids.filter(id => !options.value[id]); if (missing.length) void loadOptions(missing)
})
watch([search, platform, group, status], () => { if (debounce) clearTimeout(debounce); pageRequest?.abort(); debounce = setTimeout(() => { if (page.value !== 1) page.value = 1; else void loadPage() }, 250) })
watch(page, () => { void loadPage() })
onMounted(() => {
  if (props.initial) { rows.value = props.initial.targets.map(row => ({ account_id: row.account_id, model_id: row.model_id, effort: row.effort, key: ++key })); selected.value = [...new Set(rows.value.map(row => row.account_id))] }
  void loadPage()
  void groupsAPI.getAllIncludingInactive().then(value => { if (!lifecycle.signal.aborted) groups.value = value }).catch(value => { if (!lifecycle.signal.aborted) error.value = extractApiErrorMessage(value, text('读取分组失败', 'Could not load groups')) })
})
onBeforeUnmount(() => { lifecycle.abort(); pageRequest?.abort(); if (debounce) clearTimeout(debounce) })
</script>
