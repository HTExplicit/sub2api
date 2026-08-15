<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1500px] space-y-6">
      <header class="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 class="text-2xl font-semibold text-gray-950 dark:text-white">{{ t('imageStudio.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('imageStudio.description') }}</p>
        </div>
        <button
          v-if="history.length > 0"
          type="button"
          class="btn btn-secondary inline-flex items-center gap-2"
          data-testid="clear-history"
          @click="clearHistory"
        >
          <Icon name="trash" size="sm" />
          {{ t('imageStudio.clear') }}
        </button>
      </header>

      <div
        class="grid min-w-0 gap-6 lg:grid-cols-[minmax(320px,390px)_minmax(0,1fr)]"
        data-testid="image-studio-layout"
      >
        <form
          class="h-fit min-w-0 space-y-5 rounded-md border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-900"
          @submit.prevent="submit"
        >
          <div>
            <label for="image-studio-key" class="input-label">{{ t('imageStudio.apiKey') }}</label>
            <select
              id="image-studio-key"
              v-model.number="form.apiKeyId"
              class="input"
              :disabled="loadingKeys || submitting"
              data-testid="api-key-select"
            >
              <option :value="0">{{ t('imageStudio.selectKey') }}</option>
              <option v-for="key in imageApiKeys" :key="key.id" :value="key.id">
                {{ key.name }} · {{ key.group?.name || `#${key.group_id}` }}
              </option>
            </select>
            <p v-if="!loadingKeys && imageApiKeys.length === 0" class="input-hint text-amber-600 dark:text-amber-400">
              {{ t('imageStudio.noKeys') }}
            </p>
          </div>

          <div>
            <label for="image-studio-model" class="input-label">{{ t('imageStudio.model') }}</label>
            <select
              id="image-studio-model"
              v-model="form.model"
              class="input"
              :disabled="loadingCapabilities || imageModels.length === 0 || submitting"
              data-testid="model-select"
            >
              <option value="">
                {{ loadingCapabilities ? t('imageStudio.loadingModels') : t('imageStudio.selectModel') }}
              </option>
              <option v-for="capability in imageModels" :key="capability.id" :value="capability.id">
                {{ capability.id }}
              </option>
            </select>
            <p v-if="capabilityError" class="input-hint text-amber-600 dark:text-amber-400">
              {{ t('imageStudio.capabilitiesUnavailable') }}
            </p>
            <p
              v-else-if="selectedApiKey && !loadingCapabilities && imageModels.length === 0"
              class="input-hint text-amber-600 dark:text-amber-400"
            >
              {{ t('imageStudio.noModels') }}
            </p>
          </div>

          <div class="grid grid-cols-2 rounded-md bg-gray-100 p-1 dark:bg-dark-800" role="group">
            <button
              type="button"
              class="h-9 rounded px-3 text-sm font-medium transition-colors"
              :class="form.mode === 'generate' ? activeModeClass : inactiveModeClass"
              :disabled="!supportsGeneration || submitting"
              data-testid="mode-generate"
              @click="form.mode = 'generate'"
            >
              {{ t('imageStudio.generate') }}
            </button>
            <button
              type="button"
              class="h-9 rounded px-3 text-sm font-medium transition-colors"
              :class="form.mode === 'edit' ? activeModeClass : inactiveModeClass"
              :disabled="!supportsEdit || submitting"
              data-testid="mode-edit"
              @click="form.mode = 'edit'"
            >
              {{ t('imageStudio.edit') }}
            </button>
          </div>

          <div>
            <label for="image-studio-prompt" class="input-label">{{ t('imageStudio.prompt') }}</label>
            <textarea
              id="image-studio-prompt"
              v-model="form.prompt"
              rows="6"
              maxlength="12000"
              class="input min-h-[132px] resize-y"
              :placeholder="t('imageStudio.promptPlaceholder')"
              :disabled="submitting"
              data-testid="prompt-input"
            />
          </div>

          <div
            v-if="form.mode === 'edit' && supportsReferenceImage"
            class="grid gap-4 sm:grid-cols-2 lg:grid-cols-1 xl:grid-cols-2"
          >
            <ImageFileField
              :label="t('imageStudio.reference')"
              :preview-url="sourcePreviewURL"
              :button-label="sourceFile ? t('imageStudio.replaceImage') : t('imageStudio.chooseImage')"
              :remove-label="t('imageStudio.removeImage')"
              :disabled="submitting"
              required
              data-testid="reference-upload"
              @change="selectSourceImage"
              @remove="clearSourceImage"
            />
            <ImageFileField
              v-if="supportsMask"
              :label="t('imageStudio.mask')"
              :preview-url="maskPreviewURL"
              :button-label="maskFile ? t('imageStudio.replaceImage') : t('imageStudio.chooseImage')"
              :remove-label="t('imageStudio.removeImage')"
              :disabled="submitting"
              data-testid="mask-upload"
              @change="selectMaskImage"
              @remove="clearMaskImage"
            />
          </div>

          <div
            v-if="availableSizes.length || availableQualities.length || outputCountEnabled"
            class="grid grid-cols-2 gap-4"
          >
            <div v-if="availableSizes.length">
              <label for="image-studio-size" class="input-label">{{ t('imageStudio.size') }}</label>
              <select id="image-studio-size" v-model="form.size" class="input" :disabled="submitting">
                <option v-for="size in availableSizes" :key="size" :value="size">{{ size }}</option>
              </select>
            </div>
            <div v-if="availableQualities.length">
              <label for="image-studio-quality" class="input-label">{{ t('imageStudio.quality') }}</label>
              <select id="image-studio-quality" v-model="form.quality" class="input" :disabled="submitting">
                <option v-for="quality in availableQualities" :key="quality" :value="quality">{{ quality }}</option>
              </select>
            </div>
            <div v-if="outputCountEnabled" class="col-span-2">
              <label for="image-studio-count" class="input-label">{{ t('imageStudio.count') }}</label>
              <input
                id="image-studio-count"
                v-model.number="form.count"
                type="number"
                min="1"
                :max="maxOutputCount"
                step="1"
                class="input"
                :disabled="submitting"
              />
            </div>
          </div>

          <button
            type="submit"
            class="btn btn-primary flex h-11 w-full items-center justify-center gap-2"
            :disabled="!canSubmit"
            data-testid="submit-image"
          >
            <Icon :name="submitting ? 'refresh' : 'sparkles'" size="sm" :class="submitting ? 'animate-spin' : ''" />
            {{ submitting ? t('imageStudio.running') : t('imageStudio.run') }}
          </button>
        </form>

        <section class="min-w-0" aria-labelledby="image-studio-history-heading">
          <div class="mb-3 flex items-center justify-between gap-3">
            <h2 id="image-studio-history-heading" class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('imageStudio.history') }}
            </h2>
            <span class="text-xs tabular-nums text-gray-500 dark:text-gray-400">{{ history.length }}</span>
          </div>

          <div
            v-if="loadingHistory"
            class="flex min-h-[260px] items-center justify-center border-y border-gray-200 dark:border-dark-700"
          >
            <Icon name="refresh" size="lg" class="animate-spin text-primary-500" />
          </div>
          <div
            v-else-if="history.length === 0"
            class="flex min-h-[260px] items-center justify-center border-y border-gray-200 text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400"
            data-testid="empty-history"
          >
            {{ t('imageStudio.emptyHistory') }}
          </div>
          <div v-else class="grid min-w-0 gap-4 2xl:grid-cols-2" data-testid="history-grid">
            <article
              v-for="record in history"
              :key="record.id"
              class="min-w-0 overflow-hidden rounded-md border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900"
            >
              <div class="grid gap-1 border-b border-gray-100 px-4 py-3 dark:border-dark-800">
                <div class="flex min-w-0 items-start justify-between gap-3">
                  <div class="min-w-0">
                    <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="record.prompt">
                      {{ record.prompt }}
                    </p>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                      {{ record.model }} · {{ record.size }} · {{ record.quality }}
                    </p>
                  </div>
                  <div class="flex shrink-0 items-center gap-1">
                    <button
                      type="button"
                      class="icon-button"
                      :title="t('imageStudio.retry')"
                      @click="retryRecord(record)"
                    >
                      <Icon name="refresh" size="sm" />
                    </button>
                    <button
                      type="button"
                      class="icon-button text-red-500 hover:bg-red-50 dark:hover:bg-red-950/30"
                      :title="t('imageStudio.delete')"
                      :data-testid="`delete-history-${record.id}`"
                      @click="removeRecord(record.id)"
                    >
                      <Icon name="trash" size="sm" />
                    </button>
                  </div>
                </div>
                <time class="text-xs text-gray-400" :datetime="new Date(record.createdAt).toISOString()">
                  {{ formatDate(record.createdAt) }}
                </time>
              </div>

              <div class="grid grid-cols-2 gap-px bg-gray-100 dark:bg-dark-800">
                <div v-for="image in record.images" :key="image.id" class="group relative aspect-square min-w-0 bg-gray-50 dark:bg-dark-950">
                  <button type="button" class="h-full w-full" @click="previewURL = image.url">
                    <img :src="image.url" :alt="record.prompt" class="h-full w-full object-cover" />
                  </button>
                  <button
                    type="button"
                    class="absolute bottom-2 right-2 flex h-9 w-9 items-center justify-center rounded bg-black/70 text-white opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100"
                    :title="t('imageStudio.download')"
                    @click="downloadImage(record, image)"
                  >
                    <Icon name="download" size="sm" />
                  </button>
                </div>
              </div>

              <p
                v-if="record.images[0]?.revisedPrompt"
                class="border-t border-gray-100 px-4 py-3 text-xs leading-5 text-gray-500 dark:border-dark-800 dark:text-gray-400"
              >
                <span class="font-medium text-gray-700 dark:text-gray-300">{{ t('imageStudio.revisedPrompt') }}:</span>
                {{ record.images[0].revisedPrompt }}
              </p>
            </article>
          </div>
        </section>
      </div>
    </div>

    <BaseDialog
      :show="Boolean(previewURL)"
      :title="t('imageStudio.title')"
      width="wide"
      data-testid="image-preview-dialog"
      @close="previewURL = ''"
    >
      <div class="flex max-h-[78vh] items-center justify-center bg-gray-100 p-2 dark:bg-dark-950">
        <img v-if="previewURL" :src="previewURL" alt="" class="max-h-[74vh] max-w-full object-contain" />
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { keysAPI } from '@/api'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import {
  editImages,
  generateImages,
  listModelCapabilities,
  MAX_IMAGE_BYTES,
  validateImageBlob,
  type ModelCapability,
} from '@/api/imageStudio'
import {
  clearImageStudioHistory,
  deleteImageStudioHistory,
  listImageStudioHistory,
  saveImageStudioHistory,
  type ImageStudioHistoryRecord,
  type ImageStudioMode,
} from '@/features/image-studio/history'
import type { ApiKey } from '@/types'

type DisplayHistoryImage = ImageStudioHistoryRecord['images'][number] & { url: string }
type DisplayHistoryRecord = Omit<ImageStudioHistoryRecord, 'images'> & { images: DisplayHistoryImage[] }

const API_KEY_PAGE_SIZE = 100
const ALLOWED_UPLOAD_MIME_TYPES = new Set(['image/png', 'image/jpeg', 'image/webp'])
const activeModeClass = 'bg-white text-gray-950 shadow-sm dark:bg-dark-700 dark:text-white'
const inactiveModeClass = 'text-gray-500 enabled:hover:text-gray-900 disabled:cursor-not-allowed disabled:opacity-40 dark:text-gray-400 dark:enabled:hover:text-white'

const ImageFileField = defineComponent({
  props: {
    label: { type: String, required: true },
    previewUrl: { type: String, default: '' },
    buttonLabel: { type: String, required: true },
    removeLabel: { type: String, required: true },
    disabled: { type: Boolean, default: false },
    required: { type: Boolean, default: false },
  },
  emits: ['change', 'remove'],
  setup(props, { emit, attrs }) {
    return () => h('div', { class: 'min-w-0', ...attrs }, [
      h('label', { class: 'input-label' }, [props.label, props.required ? ' *' : '']),
      h('div', { class: 'relative aspect-square overflow-hidden rounded-md border border-dashed border-gray-300 bg-gray-50 dark:border-dark-600 dark:bg-dark-950' }, [
        props.previewUrl
          ? h('img', { src: props.previewUrl, alt: '', class: 'h-full w-full object-contain' })
          : h(Icon, { name: 'upload', size: 'lg', class: 'absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 text-gray-400' }),
      ]),
      h('div', { class: 'mt-2 flex items-center gap-2' }, [
        h('label', { class: ['btn btn-secondary flex min-w-0 flex-1 cursor-pointer items-center justify-center gap-2 truncate', props.disabled ? 'pointer-events-none opacity-50' : ''] }, [
          h(Icon, { name: 'upload', size: 'sm' }),
          h('span', { class: 'truncate' }, props.buttonLabel),
          h('input', {
            type: 'file',
            accept: 'image/png,image/jpeg,image/webp',
            class: 'sr-only',
            disabled: props.disabled,
            onChange: (event: Event) => emit('change', event),
          }),
        ]),
        props.previewUrl
          ? h('button', {
              type: 'button',
              class: 'icon-button shrink-0 text-red-500',
              title: props.removeLabel,
              disabled: props.disabled,
              onClick: () => emit('remove'),
            }, [h(Icon, { name: 'x', size: 'sm' })])
          : null,
      ]),
    ])
  },
})

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const apiKeys = ref<ApiKey[]>([])
const capabilities = ref<ModelCapability[]>([])
const history = ref<DisplayHistoryRecord[]>([])
const loadingKeys = ref(false)
const loadingCapabilities = ref(false)
const loadingHistory = ref(false)
const capabilityError = ref(false)
const submitting = ref(false)
const sourceFile = ref<File | null>(null)
const maskFile = ref<File | null>(null)
const sourcePreviewURL = ref('')
const maskPreviewURL = ref('')
const previewURL = ref('')
let capabilityController: AbortController | null = null
let submissionController: AbortController | null = null
let keysLoadGeneration = 0

const form = reactive({
  apiKeyId: 0,
  model: '',
  mode: 'generate' as ImageStudioMode,
  prompt: '',
  size: '',
  quality: '',
  count: 1,
})

const imageApiKeys = computed(() => apiKeys.value.filter(key =>
  key.status === 'active' && key.group?.allow_image_generation === true,
))
const selectedApiKey = computed(() => imageApiKeys.value.find(key => key.id === Number(form.apiKeyId)) || null)
const historyOwnerKey = computed(() => {
  const userID = Number(authStore.user?.id)
  return Number.isSafeInteger(userID) && userID > 0 ? `user:${userID}` : ''
})
const imageModels = computed(() => capabilities.value.filter(capability =>
  capability.kind === 'image' &&
  capability.output_modalities?.includes('image') &&
  (capability.endpoints?.includes('images.generations') || capability.endpoints?.includes('images.edits')),
))
const selectedCapability = computed(() => imageModels.value.find(capability => capability.id === form.model) || null)
const supportsGeneration = computed(() => selectedCapability.value?.endpoints?.includes('images.generations') === true)
const supportsEdit = computed(() => selectedCapability.value?.endpoints?.includes('images.edits') === true)
const selectedControls = computed(() => form.mode === 'edit'
  ? selectedCapability.value?.controls?.edit
  : selectedCapability.value?.controls?.generation)
const supportsReferenceImage = computed(() => selectedControls.value?.supports_reference_image === true)
const supportsMask = computed(() => selectedControls.value?.supports_mask === true)
const availableSizes = computed(() => {
  return selectedControls.value?.sizes?.filter(Boolean) || []
})
const availableQualities = computed(() => {
  return selectedControls.value?.qualities?.filter(Boolean) || []
})
const outputCountEnabled = computed(() => (selectedControls.value?.max_output_count || 1) > 1)
const maxOutputCount = computed(() => Math.min(Math.max(selectedControls.value?.max_output_count || 1, 1), 4))
const canSubmit = computed(() => Boolean(
  selectedApiKey.value &&
  selectedCapability.value &&
  form.prompt.trim() &&
  !submitting.value &&
  (form.mode === 'generate'
    ? supportsGeneration.value
    : supportsEdit.value && supportsReferenceImage.value && sourceFile.value),
))

function errorMessage(error: unknown): string {
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') {
    return error.message
  }
  return t('imageStudio.failed')
}

function randomID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function revokeURL(value: string): void {
  if (value) URL.revokeObjectURL(value)
}

function setFilePreview(target: 'source' | 'mask', file: File | null): void {
  if (target === 'source') {
    revokeURL(sourcePreviewURL.value)
    sourcePreviewURL.value = file ? URL.createObjectURL(file) : ''
    sourceFile.value = file
    return
  }
  revokeURL(maskPreviewURL.value)
  maskPreviewURL.value = file ? URL.createObjectURL(file) : ''
  maskFile.value = file
}

async function fileFromEvent(event: Event): Promise<File | null> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0] || null
  input.value = ''
  if (!file) return null
  if (!ALLOWED_UPLOAD_MIME_TYPES.has(file.type.toLowerCase()) || file.size > MAX_IMAGE_BYTES) {
    appStore.showError(t('imageStudio.invalidFile'))
    return null
  }
  try {
    await validateImageBlob(file, file.type)
    return file
  } catch {
    appStore.showError(t('imageStudio.invalidFile'))
    return null
  }
}

async function selectSourceImage(event: Event): Promise<void> {
  const file = await fileFromEvent(event)
  if (file) setFilePreview('source', file)
}

async function selectMaskImage(event: Event): Promise<void> {
  const file = await fileFromEvent(event)
  if (file) setFilePreview('mask', file)
}

function clearSourceImage(): void {
  setFilePreview('source', null)
}

function clearMaskImage(): void {
  setFilePreview('mask', null)
}

function resetOwnerScopedForm(): void {
  clearSourceImage()
  clearMaskImage()
  previewURL.value = ''
  form.mode = 'generate'
  form.prompt = ''
  form.size = ''
  form.quality = ''
  form.count = 1
}

function revokeHistoryURLs(): void {
  for (const record of history.value) {
    for (const image of record.images) revokeURL(image.url)
  }
}

function displayRecords(records: ImageStudioHistoryRecord[]): void {
  revokeHistoryURLs()
  history.value = records.map(record => ({
    ...record,
    images: record.images.map(image => ({ ...image, url: URL.createObjectURL(image.blob) })),
  }))
}

async function loadHistory(): Promise<void> {
  const ownerKey = historyOwnerKey.value
  if (!ownerKey) {
    displayRecords([])
    return
  }
  loadingHistory.value = true
  try {
    const records = await listImageStudioHistory(ownerKey)
    if (ownerKey === historyOwnerKey.value) displayRecords(records)
  } finally {
    loadingHistory.value = false
  }
}

async function loadKeys(): Promise<void> {
  const ownerKey = historyOwnerKey.value
  const generation = ++keysLoadGeneration
  if (!ownerKey) {
    apiKeys.value = []
    loadingKeys.value = false
    return
  }
  loadingKeys.value = true
  try {
    const loadedKeys: ApiKey[] = []
    let page = 1
    while (true) {
      const response = await keysAPI.list(page, API_KEY_PAGE_SIZE, {
        status: 'active',
        sort_by: 'created_at',
        sort_order: 'desc',
      })
      if (generation !== keysLoadGeneration || ownerKey !== historyOwnerKey.value) return
      const pageItems = response.items || []
      loadedKeys.push(...pageItems)

      const declaredPages = Number(response.pages)
      const hasDeclaredPages = Number.isFinite(declaredPages) && declaredPages > 0
      if (pageItems.length === 0 || (hasDeclaredPages && page >= declaredPages) || (!hasDeclaredPages && pageItems.length < API_KEY_PAGE_SIZE)) {
        break
      }
      page += 1
    }
    if (generation === keysLoadGeneration && ownerKey === historyOwnerKey.value) {
      apiKeys.value = loadedKeys
    }
  } catch (error) {
    if (generation !== keysLoadGeneration || ownerKey !== historyOwnerKey.value) return
    appStore.showError(errorMessage(error))
  } finally {
    if (generation === keysLoadGeneration) loadingKeys.value = false
  }
}

async function loadCapabilities(): Promise<void> {
  capabilityController?.abort()
  capabilities.value = []
  form.model = ''
  capabilityError.value = false
  const key = selectedApiKey.value
  if (!key) return

  const controller = new AbortController()
  capabilityController = controller
  loadingCapabilities.value = true
  try {
    const response = await listModelCapabilities(key.key, controller.signal)
    if (controller.signal.aborted) return
    capabilities.value = Array.isArray(response.data) ? response.data : []
    form.model = imageModels.value[0]?.id || ''
  } catch (error) {
    if (controller.signal.aborted) return
    capabilityError.value = true
  } finally {
    if (capabilityController === controller) {
      capabilityController = null
      loadingCapabilities.value = false
    }
  }
}

async function submit(): Promise<void> {
  const key = selectedApiKey.value
  const capability = selectedCapability.value
  const ownerKey = historyOwnerKey.value
  if (!key || !capability) return
  if (!ownerKey) {
    appStore.showError(t('imageStudio.historyUnavailable'))
    return
  }
  const prompt = form.prompt.trim()
  if (!prompt) {
    appStore.showError(t('imageStudio.missingPrompt'))
    return
  }
  if (form.mode === 'generate' && !supportsGeneration.value) {
    appStore.showError(t('imageStudio.modelUnavailable'))
    return
  }
  if (form.mode === 'edit' && (!supportsEdit.value || !supportsReferenceImage.value)) {
    appStore.showError(t('imageStudio.modelUnavailable'))
    return
  }
  if (form.mode === 'edit' && !sourceFile.value) {
    appStore.showError(t('imageStudio.missingImage'))
    return
  }

  submissionController?.abort()
  const controller = new AbortController()
  submissionController = controller
  submitting.value = true
  try {
    const common = {
      model: capability.id,
      prompt,
      n: outputCountEnabled.value
        ? Math.min(Math.max(Math.trunc(form.count) || 1, 1), maxOutputCount.value)
        : undefined,
      size: availableSizes.value.length ? form.size : undefined,
      quality: availableQualities.value.length ? form.quality : undefined,
      signal: controller.signal,
    }
    const editSource = form.mode === 'edit' ? sourceFile.value : null
    const isEdit = editSource !== null
    if (editSource) await validateImageBlob(editSource, editSource.type)
    if (isEdit && supportsMask.value && maskFile.value) {
      await validateImageBlob(maskFile.value, maskFile.value.type)
    }
    const generated = isEdit
      ? await editImages(key.key, {
          ...common,
          image: editSource,
          imageName: editSource.name,
          mask: supportsMask.value ? maskFile.value : null,
          maskName: supportsMask.value ? maskFile.value?.name : undefined,
        })
      : await generateImages(key.key, common)

    if (controller.signal.aborted || ownerKey !== historyOwnerKey.value) return

    const record: ImageStudioHistoryRecord = {
      id: randomID(),
      createdAt: Date.now(),
      mode: form.mode,
      model: capability.id,
      prompt,
      size: common.size || '',
      quality: common.quality || '',
      count: common.n || 1,
      sourceImage: editSource || undefined,
      sourceImageName: editSource?.name,
      maskImage: isEdit && supportsMask.value ? maskFile.value || undefined : undefined,
      maskImageName: isEdit && supportsMask.value ? maskFile.value?.name : undefined,
      images: generated.map(image => ({ id: randomID(), ...image })),
    }
    const saved = await saveImageStudioHistory(ownerKey, record)
    if (ownerKey !== historyOwnerKey.value) return
    if (saved) {
      await loadHistory()
      appStore.showSuccess(t('imageStudio.saved'))
    } else {
      displayRecords([record, ...history.value.map(({ images, ...item }) => ({
        ...item,
        images: images.map(({ url: _url, ...image }) => image),
      }))])
      appStore.showError(t('imageStudio.historyUnavailable'))
    }
  } catch (error) {
    if (controller.signal.aborted || ownerKey !== historyOwnerKey.value) return
    appStore.showError(`${t('imageStudio.failed')}: ${errorMessage(error)}`)
  } finally {
    if (submissionController === controller) {
      submissionController = null
      submitting.value = false
    }
  }
}

async function retryRecord(record: DisplayHistoryRecord): Promise<void> {
  if (!imageModels.value.some(model => model.id === record.model)) {
    appStore.showError(t('imageStudio.modelUnavailable'))
    return
  }
  form.model = record.model
  form.mode = record.mode
  form.prompt = record.prompt
  form.size = record.size
  form.quality = record.quality
  form.count = record.count
  setFilePreview('source', record.sourceImage ? new File([record.sourceImage], record.sourceImageName || 'reference.png', { type: record.sourceImage.type }) : null)
  setFilePreview('mask', record.maskImage ? new File([record.maskImage], record.maskImageName || 'mask.png', { type: record.maskImage.type }) : null)
  await nextTick()
  await submit()
}

async function removeRecord(id: string): Promise<void> {
  try {
    if (!await deleteImageStudioHistory(historyOwnerKey.value, id)) {
      appStore.showError(t('imageStudio.historyUpdateFailed'))
      return
    }
    await loadHistory()
  } catch {
    appStore.showError(t('imageStudio.historyUpdateFailed'))
  }
}

async function clearHistory(): Promise<void> {
  if (!window.confirm(t('imageStudio.clearConfirm'))) return
  const ownerKey = historyOwnerKey.value
  if (!ownerKey) return
  try {
    if (!await clearImageStudioHistory(ownerKey)) {
      if (ownerKey === historyOwnerKey.value) appStore.showError(t('imageStudio.historyUpdateFailed'))
      return
    }
    if (ownerKey !== historyOwnerKey.value) return
    displayRecords([])
  } catch {
    if (ownerKey === historyOwnerKey.value) appStore.showError(t('imageStudio.historyUpdateFailed'))
  }
}

function extensionForMimeType(mimeType: string): string {
  if (mimeType === 'image/jpeg') return 'jpg'
  if (mimeType === 'image/webp') return 'webp'
  return 'png'
}

function downloadImage(record: DisplayHistoryRecord, image: DisplayHistoryImage): void {
  const link = document.createElement('a')
  link.href = image.url
  link.download = `${record.model}-${record.createdAt}.${extensionForMimeType(image.mimeType)}`
  document.body.appendChild(link)
  link.click()
  link.remove()
}

function formatDate(timestamp: number): string {
  return new Date(timestamp).toLocaleString()
}

watch(() => form.apiKeyId, () => void loadCapabilities())
watch(selectedCapability, (capability) => {
  if (!capability) return
  if (!supportsGeneration.value && supportsEdit.value) form.mode = 'edit'
  if (!supportsEdit.value && form.mode === 'edit') form.mode = 'generate'
  if (!availableSizes.value.includes(form.size)) form.size = availableSizes.value[0] || ''
  if (!availableQualities.value.includes(form.quality)) form.quality = availableQualities.value[0] || ''
  form.count = Math.min(Math.max(form.count, 1), maxOutputCount.value)
})
watch(() => form.mode, () => {
  if (!availableSizes.value.includes(form.size)) form.size = availableSizes.value[0] || ''
  if (!availableQualities.value.includes(form.quality)) form.quality = availableQualities.value[0] || ''
  form.count = Math.min(Math.max(form.count, 1), maxOutputCount.value)
  if (!supportsMask.value) clearMaskImage()
})
watch(historyOwnerKey, (ownerKey, previousOwnerKey) => {
  if (ownerKey === previousOwnerKey) return
  capabilityController?.abort()
  submissionController?.abort()
  keysLoadGeneration += 1
  apiKeys.value = []
  capabilities.value = []
  form.apiKeyId = 0
  form.model = ''
  resetOwnerScopedForm()
  displayRecords([])
  if (ownerKey) void Promise.all([loadKeys(), loadHistory()])
})

onMounted(() => {
  void Promise.all([loadKeys(), loadHistory()])
})

onBeforeUnmount(() => {
  capabilityController?.abort()
  submissionController?.abort()
  keysLoadGeneration += 1
  revokeURL(sourcePreviewURL.value)
  revokeURL(maskPreviewURL.value)
  revokeHistoryURLs()
})
</script>
