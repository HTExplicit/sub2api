import { shallowReactive } from 'vue'

/**
 * Shared registry of every shown BaseDialog.
 *
 * - Keyboard handling (Escape, Tab cycling) and auto-focus only act on the top-most dialog,
 *   so a confirm nested inside an editor no longer closes the editor as well.
 * - The `body.modal-open` scroll lock is reference counted: added on the first registration,
 *   removed on the last unregistration, instead of every dialog toggling it on its own.
 * - `dialogLayer` stacks nested dialogs ten z-index steps above the previous one so a child
 *   always paints above its parent even when both use the default z-index.
 */

interface DialogEntry {
  id: string
  zIndex: number
}

/** Matches `.modal-overlay` (`z-50`) in style.css; BaseDialog emits no inline style at this layer. */
export const DEFAULT_DIALOG_Z_INDEX = 50

/**
 * A dialog opened with an explicit z-index at or above this value is a gate
 * (AdminComplianceDialog uses 80): it always paints at exactly that z-index and
 * dialogs opened later are capped beneath it instead of being raised above it.
 */
export const GATE_DIALOG_Z_INDEX = 80

const LAYER_STEP = 10
const BODY_LOCK_CLASS = 'modal-open'

const dialogs = shallowReactive<DialogEntry[]>([])
let sequence = 0

/** Module-scope counter so nested dialogs never share a `modal-title-N` id. */
export function nextDialogId(): string {
  return `modal-title-${++sequence}`
}

export function registerDialog(id: string, zIndex = DEFAULT_DIALOG_Z_INDEX): void {
  const index = dialogs.findIndex((entry) => entry.id === id)
  if (index >= 0) {
    // Replace rather than mutate: the array is shallowReactive, so only splice/push notify.
    dialogs.splice(index, 1, { id, zIndex })
  } else {
    dialogs.push({ id, zIndex })
  }
  document.body.classList.add(BODY_LOCK_CLASS)
}

export function unregisterDialog(id: string): void {
  const index = dialogs.findIndex((entry) => entry.id === id)
  if (index >= 0) {
    dialogs.splice(index, 1)
  }
  if (dialogs.length === 0) {
    document.body.classList.remove(BODY_LOCK_CLASS)
  }
}

const isGate = (entry: DialogEntry): boolean => entry.zIndex >= GATE_DIALOG_Z_INDEX

/** Resolved z-index of every registered dialog, in registration order. */
function resolveLayers(): Map<string, number> {
  const gates = dialogs.filter(isGate).map((entry) => entry.zIndex)
  const ceiling = gates.length > 0 ? Math.min(...gates) - 1 : Number.POSITIVE_INFINITY
  const layers = new Map<string, number>()
  let previous = DEFAULT_DIALOG_Z_INDEX - LAYER_STEP
  for (const entry of dialogs) {
    if (isGate(entry)) {
      layers.set(entry.id, entry.zIndex)
      continue
    }
    previous = Math.min(Math.max(previous + LAYER_STEP, entry.zIndex), ceiling)
    layers.set(entry.id, previous)
  }
  return layers
}

/**
 * z-index for a shown dialog: max(previous dialog + 10, explicit), never above a gate.
 * Falls back to `explicit` while the dialog is not registered (hidden).
 */
export function dialogLayer(id: string, explicit = DEFAULT_DIALOG_Z_INDEX): number {
  return resolveLayers().get(id) ?? explicit
}

/** Whether `id` is the dialog painted on top: highest layer, later registration winning ties. */
export function isTopDialog(id: string): boolean {
  let topId: string | undefined
  let topLayer = Number.NEGATIVE_INFINITY
  for (const [entryId, layer] of resolveLayers()) {
    if (layer >= topLayer) {
      topId = entryId
      topLayer = layer
    }
  }
  return topId === id
}
