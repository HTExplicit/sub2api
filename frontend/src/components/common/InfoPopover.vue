<template>
  <span class="inline-flex max-w-full" @pointerenter="onPointerEnter" @pointerleave="onPointerLeave">
    <button
      ref="triggerRef"
      type="button"
      class="inline-flex max-w-full items-center rounded text-left"
      :aria-expanded="open ? 'true' : 'false'"
      :aria-controls="open ? panelId : undefined"
      aria-haspopup="dialog"
      :aria-label="label || undefined"
      v-bind="triggerAttrs"
      @click.stop="onClick"
      @focus="onFocus"
      @blur="onBlur"
      @keydown.esc.prevent="close(true)"
    >
      <slot name="trigger" />
    </button>
    <Teleport to="body">
      <div
        v-if="open"
        :id="panelId"
        ref="panelRef"
        role="dialog"
        :aria-label="label || undefined"
        tabindex="-1"
        :class="[
          'theme-dark-surface fixed z-[99999] overflow-y-auto overscroll-contain rounded-lg bg-gray-900 p-3 text-xs leading-relaxed text-white shadow-xl ring-1 ring-white/10 focus:outline-none dark:bg-gray-800',
          widthClass
        ]"
        :style="panelStyle"
        :data-placement="placement"
        @pointerenter="cancelClose"
        @pointerleave="onPointerLeave"
        @focusout="onPanelFocusOut"
        @keydown.esc.prevent="close(true)"
      >
        <slot />
      </div>
    </Teleport>
  </span>
</template>

<script setup lang="ts">
// Detail popover for admin tables: a real button opens it (hover with a mouse, click or tap, keyboard
// focus / Enter), the content is rendered only while open (v-if), teleported so tables and dialogs do
// not clip it, placed above the trigger or below when there is more room there, clamped to the
// viewport, and scrollable when longer than the space available. Escape or a click outside closes it.
import { nextTick, onBeforeUnmount, ref, useId } from 'vue'

withDefaults(defineProps<{
  label?: string
  widthClass?: string
  triggerAttrs?: Record<string, string>
}>(), {
  label: '',
  widthClass: 'w-max max-w-[min(26rem,calc(100vw-1rem))]',
  triggerAttrs: () => ({})
})

const panelId = `info-popover-${useId()}`
const open = ref(false)
const pinned = ref(false)
const placement = ref<'top' | 'bottom'>('top')
const panelStyle = ref<Record<string, string>>({ left: '0px', top: '0px', visibility: 'hidden' })
const triggerRef = ref<HTMLButtonElement | null>(null)
const panelRef = ref<HTMLElement | null>(null)
let closeTimer: ReturnType<typeof setTimeout> | null = null
// focus returned to the trigger by Escape must not reopen the popover
let suppressNextFocus = false

const MARGIN = 8
const GAP = 6
const MIN_HEIGHT = 96

function place(): void {
  const trigger = triggerRef.value
  const panel = panelRef.value
  if (!trigger || !panel) return
  const rect = trigger.getBoundingClientRect()
  const viewportWidth = document.documentElement.clientWidth || window.innerWidth
  const viewportHeight = document.documentElement.clientHeight || window.innerHeight
  const above = rect.top - GAP - MARGIN
  const below = viewportHeight - rect.bottom - GAP - MARGIN
  const natural = panel.scrollHeight
  const useBelow = natural > above && below > above
  const maxHeight = Math.max(MIN_HEIGHT, useBelow ? below : above)
  const height = Math.min(natural, maxHeight)
  const width = panel.offsetWidth
  const left = Math.min(Math.max(rect.left + rect.width / 2 - width / 2, MARGIN), Math.max(MARGIN, viewportWidth - width - MARGIN))
  const top = useBelow ? rect.bottom + GAP : Math.max(MARGIN, rect.top - GAP - height)
  placement.value = useBelow ? 'bottom' : 'top'
  panelStyle.value = { left: `${Math.round(left)}px`, top: `${Math.round(top)}px`, maxHeight: `${Math.round(maxHeight)}px` }
}

function onViewportChange(): void {
  if (open.value) place()
}

function listen(on: boolean): void {
  if (on) {
    window.addEventListener('resize', onViewportChange)
    window.addEventListener('scroll', onViewportChange, true)
    document.addEventListener('pointerdown', onDocumentPointerDown, true)
  } else {
    window.removeEventListener('resize', onViewportChange)
    window.removeEventListener('scroll', onViewportChange, true)
    document.removeEventListener('pointerdown', onDocumentPointerDown, true)
  }
}

async function show(focusPanel = false): Promise<void> {
  cancelClose()
  if (!open.value) {
    panelStyle.value = { left: '0px', top: '0px', visibility: 'hidden' }
    open.value = true
    listen(true)
    await nextTick()
  }
  place()
  if (focusPanel) panelRef.value?.focus({ preventScroll: true })
}

function close(returnFocus = false): void {
  cancelClose()
  if (!open.value) return
  const panelHadFocus = !!panelRef.value?.contains(document.activeElement)
  open.value = false
  pinned.value = false
  listen(false)
  if (returnFocus || panelHadFocus) {
    suppressNextFocus = true
    triggerRef.value?.focus({ preventScroll: true })
  }
}

function cancelClose(): void {
  if (closeTimer) {
    clearTimeout(closeTimer)
    closeTimer = null
  }
}

function scheduleClose(): void {
  cancelClose()
  closeTimer = setTimeout(() => {
    closeTimer = null
    if (pinned.value) return
    const active = document.activeElement
    if (active && (triggerRef.value === active || panelRef.value?.contains(active))) return
    close()
  }, 150)
}

function onPointerEnter(event: PointerEvent): void {
  if (event.pointerType === 'mouse') void show()
}

function onPointerLeave(event: PointerEvent): void {
  if (event.pointerType !== 'mouse' || pinned.value) return
  const next = event.relatedTarget as Node | null
  if (next && (triggerRef.value?.contains(next) || panelRef.value?.contains(next))) return
  scheduleClose()
}

// Click or tap pins the popover open (a second click closes it); a keyboard click (Enter / Space,
// event.detail === 0) also moves focus into it so long content can be scrolled with the keys.
function onClick(event: MouseEvent): void {
  if (open.value && pinned.value) {
    close()
    return
  }
  pinned.value = true
  void show(event.detail === 0)
}

function onFocus(): void {
  if (suppressNextFocus) {
    suppressNextFocus = false
    return
  }
  void show()
}

function onBlur(event: FocusEvent): void {
  const next = event.relatedTarget as Node | null
  if (next && panelRef.value?.contains(next)) return
  if (!pinned.value) scheduleClose()
}

function onPanelFocusOut(event: FocusEvent): void {
  const next = event.relatedTarget as Node | null
  if (next && (panelRef.value?.contains(next) || triggerRef.value === next)) return
  if (!pinned.value) scheduleClose()
}

function onDocumentPointerDown(event: PointerEvent): void {
  const target = event.target as Node | null
  if (target && (triggerRef.value?.contains(target) || panelRef.value?.contains(target))) return
  close()
}

onBeforeUnmount(() => {
  cancelClose()
  listen(false)
})
</script>
