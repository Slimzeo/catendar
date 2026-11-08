import { onMounted, onUnmounted } from 'vue'
import { isPlainEscape, type ShortcutAction } from '@/shortcuts'
import { useShortcutsStore } from '@/stores/shortcuts'

/** Tab is a regular key in the DOM. Remember where it was pressed while leaving native Tab traversal available. */
export function useShortcutKeys(
  handle: (action: ShortcutAction, event: KeyboardEvent, tabOrigin: EventTarget | null) => boolean,
  onTabFocus: (target: Element | null) => void
) {
  const shortcuts = useShortcutsStore()
  let tabOrigin: EventTarget | null = null
  let usedChord = false

  function resetTab() {
    tabOrigin = null
    usedChord = false
  }

  function keydown(event: KeyboardEvent) {
    if (event.defaultPrevented || event.isComposing || event.keyCode === 229) return
    if (event.key === 'Tab') {
      if (!event.repeat) {
        tabOrigin = event.target
        usedChord = false
      }
      return
    }
    if (tabOrigin) usedChord = true
    const action = isPlainEscape(event, !!tabOrigin) ? 'dismiss' : shortcuts.match(event, !!tabOrigin)
    if (action && handle(action, event, tabOrigin)) {
      event.preventDefault()
      event.stopImmediatePropagation()
    }
  }

  function keyup(event: KeyboardEvent) {
    if (event.key !== 'Tab') return
    if (tabOrigin && !usedChord) onTabFocus(document.activeElement)
    resetTab()
  }

  onMounted(() => {
    window.addEventListener('keydown', keydown, true)
    window.addEventListener('keyup', keyup, true)
    window.addEventListener('blur', resetTab)
    document.addEventListener('visibilitychange', resetTab)
  })
  onUnmounted(() => {
    window.removeEventListener('keydown', keydown, true)
    window.removeEventListener('keyup', keyup, true)
    window.removeEventListener('blur', resetTab)
    document.removeEventListener('visibilitychange', resetTab)
  })
}
