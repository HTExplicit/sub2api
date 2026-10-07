<template>
  <div ref="container" class="relative h-full min-h-0 w-full min-w-0 overflow-hidden bg-white" data-testid="borrow-pelican-preview">
    <iframe
      v-if="safeUrl"
      :src="safeUrl"
      :title="title"
      class="absolute block border-0"
      :style="frameStyle"
      sandbox="allow-scripts"
      referrerpolicy="no-referrer"
      loading="lazy"
      :tabindex="interactive ? undefined : -1"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { buildApiUrl } from '@/api/client'

const props = withDefaults(defineProps<{ previewUrl: string; title: string; interactive?: boolean }>(), { interactive: false })
const container = ref<HTMLElement | null>(null)
const available = ref({ width: 0, height: 0 })
let observer: ResizeObserver | undefined

// A server-issued capability is the only executable document accepted here.
// Generated source is never placed into the admin document, srcdoc, or a blob iframe.
const safeUrl = computed(() => {
  try {
    const url = new URL(props.previewUrl, window.location.origin)
    const api = new URL(buildApiUrl('/codex-gateway-borrow/preview/'), window.location.origin)
    const prefix = api.pathname.replace(/\/+$/, '')
    if (url.origin !== api.origin || url.search || url.hash || url.username || url.password) return ''
    if (!url.pathname.startsWith(`${prefix}/`) || !/^[a-zA-Z0-9_-]{43}\/index\.html$/.test(url.pathname.slice(prefix.length + 1))) return ''
    return url.href
  } catch {
    return ''
  }
})

// Keep the authored viewport stable in thumbnails and enlarged previews. The
// containing 4:3 surface scales the whole document without changing its layout.
const frameStyle = computed(() => {
  const scale = Math.max(0, Math.min(1, available.value.width / 1024, available.value.height / 768))
  return {
    width: '1024px', height: '768px',
    left: `${Math.max(0, (available.value.width - 1024 * scale) / 2)}px`,
    top: `${Math.max(0, (available.value.height - 768 * scale) / 2)}px`,
    transform: `scale(${scale})`, transformOrigin: 'top left',
    pointerEvents: props.interactive ? 'auto' as const : 'none' as const
  }
})

function resize() {
  if (container.value) available.value = { width: container.value.clientWidth, height: container.value.clientHeight }
}

onMounted(() => {
  resize()
  if (typeof ResizeObserver !== 'undefined' && container.value) {
    observer = new ResizeObserver(resize)
    observer.observe(container.value)
  }
  window.addEventListener('resize', resize)
})
onBeforeUnmount(() => {
  observer?.disconnect()
  window.removeEventListener('resize', resize)
})
</script>
