<template>
  <Teleport to="body">
    <Transition name="modal">
      <div
        v-if="show"
        class="modal-overlay"
        :style="zIndexStyle"
        :aria-labelledby="dialogId"
        role="dialog"
        aria-modal="true"
        @click.self="handleClose"
      >
        <!-- Modal panel -->
        <div ref="dialogRef" :class="['modal-content', widthClasses]" tabindex="-1" @click.stop>
          <!-- Header -->
          <div class="modal-header">
            <h3 :id="dialogId" class="modal-title">
              {{ title }}
            </h3>
            <button
              v-if="showCloseButton"
              @click="emit('close')"
              class="-mr-2 rounded-xl p-2 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/30 focus-visible:ring-offset-2 dark:text-dark-500 dark:hover:bg-dark-700 dark:hover:text-dark-300 dark:focus-visible:ring-offset-dark-900"
              aria-label="Close modal"
            >
              <Icon name="x" size="md" />
            </button>
          </div>

          <!-- Body -->
          <div ref="modalBodyRef" class="modal-body">
            <slot></slot>
          </div>

          <!-- Footer -->
          <div v-if="$slots.footer" class="modal-footer">
            <slot name="footer"></slot>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, watch, onMounted, onUnmounted, ref, nextTick } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import {
  DEFAULT_DIALOG_Z_INDEX,
  dialogLayer,
  isTopDialog,
  nextDialogId,
  registerDialog,
  unregisterDialog
} from './dialogStack'

// 生成唯一ID以避免多个对话框时ID冲突(计数器位于 dialogStack 模块作用域,不会随实例重置)
const dialogId = nextDialogId()

// 焦点管理
const dialogRef = ref<HTMLElement | null>(null)
const modalBodyRef = ref<HTMLElement | null>(null)
let previousActiveElement: HTMLElement | null = null

type DialogWidth = 'narrow' | 'normal' | 'wide' | 'extra-wide' | 'full'

interface Props {
  show: boolean
  title: string
  width?: DialogWidth
  closeOnEscape?: boolean
  closeOnClickOutside?: boolean
  showCloseButton?: boolean
  zIndex?: number
}

interface Emits {
  (e: 'close'): void
}

const props = withDefaults(defineProps<Props>(), {
  width: 'normal',
  closeOnEscape: true,
  closeOnClickOutside: false,
  showCloseButton: true,
  zIndex: 50
})

const emit = defineEmits<Emits>()

// Custom z-index style (overrides the default z-50 from CSS). Nested dialogs are layered by the
// shared dialog stack so a child always paints above its parent; an explicit z-index still applies.
const zIndexStyle = computed(() => {
  const layer = dialogLayer(dialogId, props.zIndex)
  return layer !== DEFAULT_DIALOG_Z_INDEX ? { zIndex: layer } : undefined
})

const widthClasses = computed(() => {
  // Width guidance: narrow=confirm/short prompts, normal=standard forms,
  // wide=multi-section forms or rich content, extra-wide=analytics/tables,
  // full=full-screen or very dense layouts.
  const widths: Record<DialogWidth, string> = {
    narrow: 'max-w-md',
    normal: 'max-w-lg',
    wide: 'w-full sm:max-w-2xl md:max-w-3xl lg:max-w-4xl',
    'extra-wide': 'w-full sm:max-w-3xl md:max-w-4xl lg:max-w-5xl xl:max-w-6xl',
    full: 'w-full sm:max-w-4xl md:max-w-5xl lg:max-w-6xl xl:max-w-7xl'
  }
  return widths[props.width]
})

const handleClose = () => {
  if (props.closeOnClickOutside) {
    emit('close')
  }
}

const FOCUSABLE_SELECTOR =
  'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])'

// Tab / Shift+Tab 在栈顶对话框内循环焦点,避免焦点逃逸到被遮罩的页面
const cycleFocus = (event: KeyboardEvent) => {
  const panel = dialogRef.value
  if (!panel) return
  const focusable = Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
    (element) => !element.closest('[hidden], [inert], [style*="display: none"]')
  )
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  const active = document.activeElement
  // 仅当焦点落在页面本身(body/html/面板自身)时才拉回;焦点位于对话框 teleport 到 body 的弹层(下拉、灯箱)时不干预
  const lost =
    !active ||
    active === document.body ||
    active === document.documentElement ||
    active === panel
  if (!first) {
    event.preventDefault()
    panel.focus()
  } else if (event.shiftKey && (active === first || lost)) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && (active === last || lost)) {
    event.preventDefault()
    first.focus()
  }
}

// 只有栈顶对话框响应键盘:嵌套确认框按 Esc 时不再连带关闭父级对话框
const handleKeydown = (event: KeyboardEvent) => {
  if (!props.show || !isTopDialog(dialogId)) return
  if (event.key === 'Escape') {
    if (!props.closeOnEscape) return
    event.preventDefault()
    event.stopImmediatePropagation()
    emit('close')
  } else if (event.key === 'Tab') {
    cycleFocus(event)
  }
}

// Prevent body scroll when modal is open and manage focus
watch(
  () => props.show,
  async (isOpen) => {
    if (isOpen) {
      // 保存当前焦点元素
      previousActiveElement = document.activeElement as HTMLElement
      // 注册到共享对话框栈:body.modal-open 按引用计数管理,关闭嵌套子级时父级仍保持滚动锁定
      registerDialog(dialogId, props.zIndex)

      // 等待DOM更新后设置焦点到对话框(仅当自己仍是栈顶,避免抢走更晚打开的对话框的焦点)
      await nextTick()
      if (modalBodyRef.value) {
        modalBodyRef.value.scrollTop = 0
      }
      if (dialogRef.value && props.show && isTopDialog(dialogId)) {
        const firstFocusable = dialogRef.value.querySelector<HTMLElement>(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
        )
        firstFocusable?.focus()
      }
    } else {
      unregisterDialog(dialogId)
      // 恢复之前的焦点(仅当该元素仍在文档中)
      if (previousActiveElement?.isConnected && typeof previousActiveElement.focus === 'function') {
        previousActiveElement.focus()
      }
      previousActiveElement = null
    }
  },
  { immediate: true }
)

onMounted(() => {
  document.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleKeydown)
  // 确保组件卸载时退出对话框栈(最后一个对话框退出时解除滚动锁定)
  unregisterDialog(dialogId)
})
</script>
