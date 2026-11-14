import { computed } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'

// Chart.js renders on canvas, so CSS motion preferences do not affect it.
export function useChartMotion() {
  const reducedMotion = usePreferredReducedMotion()
  return computed(() => ({
    animation: reducedMotion.value === 'reduce' ? false as const : { duration: 180 },
    transitions: {
      active: { animation: { duration: 0 } },
      resize: { animation: { duration: 0 } }
    }
  }))
}

// Tooltips have their own animation defaults, separate from chart transitions.
export const chartTooltipMotion = {
  animation: { duration: 0 },
  animations: { opacity: { duration: 0 } }
}
