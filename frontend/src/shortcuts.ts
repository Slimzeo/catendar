export const SHORTCUT_GROUP_LABELS = {
  navigation: 'Navigation',
  actions: 'Task actions',
  date: 'Date & phases',
  dialogs: 'Dialogs',
  positions: 'Task positions'
} as const

export type ShortcutGroupId = keyof typeof SHORTCUT_GROUP_LABELS

const macKeyboard = /^Mac/i.test(globalThis.navigator?.platform ?? '')

export function defaultPhaseShortcuts(mac = macKeyboard) {
  const modifier = mac ? 'Alt' : 'Ctrl'
  return { phaseLeft: `${modifier}+ArrowLeft`, phaseRight: `${modifier}+ArrowRight` }
}

const phaseDefaults = defaultPhaseShortcuts()

export const SHORTCUT_DEFINITIONS = [
  { action: 'columnLeft', label: 'Previous column', group: 'navigation', defaultChord: 'Tab+ArrowLeft' },
  { action: 'columnRight', label: 'Next column', group: 'navigation', defaultChord: 'Tab+ArrowRight' },
  { action: 'taskUp', label: 'Previous task', group: 'navigation', defaultChord: 'ArrowUp' },
  { action: 'taskDown', label: 'Next task', group: 'navigation', defaultChord: 'ArrowDown' },
  { action: 'task1', label: 'Task 1', group: 'positions', defaultChord: '1' },
  { action: 'task2', label: 'Task 2', group: 'positions', defaultChord: '2' },
  { action: 'task3', label: 'Task 3', group: 'positions', defaultChord: '3' },
  { action: 'task4', label: 'Task 4', group: 'positions', defaultChord: '4' },
  { action: 'task5', label: 'Task 5', group: 'positions', defaultChord: '5' },
  { action: 'task6', label: 'Task 6', group: 'positions', defaultChord: '6' },
  { action: 'task7', label: 'Task 7', group: 'positions', defaultChord: '7' },
  { action: 'task8', label: 'Task 8', group: 'positions', defaultChord: '8' },
  { action: 'task9', label: 'Task 9', group: 'positions', defaultChord: '9' },
  { action: 'task10', label: 'Task 10', group: 'positions', defaultChord: '0' },
  { action: 'quickAdd', label: 'Quick add', group: 'actions', defaultChord: 'A' },
  { action: 'deleteTask', label: 'Delete task', group: 'actions', defaultChord: 'D' },
  { action: 'confirmDelete', label: 'Confirm delete', group: 'dialogs', defaultChord: 'Ctrl+D' },
  { action: 'editDescription', label: 'Edit description', group: 'actions', defaultChord: 'U' },
  { action: 'phaseLeft', label: 'Previous phase', group: 'date', defaultChord: phaseDefaults.phaseLeft },
  { action: 'phaseRight', label: 'Next phase', group: 'date', defaultChord: phaseDefaults.phaseRight },
  { action: 'previousDate', label: 'Previous date', group: 'date', defaultChord: 'ArrowLeft' },
  { action: 'nextDate', label: 'Next date', group: 'date', defaultChord: 'ArrowRight' },
  { action: 'today', label: 'Today', group: 'date', defaultChord: 'T' },
  { action: 'newEvent', label: 'New event', group: 'actions', defaultChord: 'N' },
  { action: 'dismiss', label: 'Dismiss', group: 'dialogs', defaultChord: 'Escape' }
] as const

export type ShortcutDefinition = typeof SHORTCUT_DEFINITIONS[number]
export type ShortcutAction = ShortcutDefinition['action']
export const SHORTCUT_ACTIONS = SHORTCUT_DEFINITIONS.map(item => item.action)
export const DEFAULT_SHORTCUTS = Object.fromEntries(
  SHORTCUT_DEFINITIONS.map(({ action, defaultChord }) => [action, defaultChord])
) as Record<ShortcutAction, string>

const modifiers = ['Ctrl', 'Alt', 'Shift', 'Meta', 'Tab'] as const
const modifierKeys = new Set(['Control', 'Alt', 'Shift', 'Meta', 'OS', 'AltGraph', 'Tab'])
const keyLabels: Record<string, string> = {
  ArrowLeft: '←', ArrowRight: '→', ArrowUp: '↑', ArrowDown: '↓',
  Escape: 'Esc', PageUp: 'PgUp', PageDown: 'PgDn', Delete: 'Del',
  Home: 'Home', End: 'End', Backspace: 'Backspace', Space: 'Space',
  Enter: 'Enter', Insert: 'Ins', Minus: '-', Equal: '=', Comma: ',',
  Period: '.', Slash: '/', Backslash: '\\', Semicolon: ';', Quote: "'",
  BracketLeft: '[', BracketRight: ']', Backquote: '`'
}
const namedKeys = new Set(Object.keys(keyLabels))

function primaryKey(key: string): string | undefined {
  if (/^[a-z0-9]$/i.test(key) || /^F([1-9]|1\d|2[0-4])$/i.test(key)) return key.toUpperCase()
  if (key === ' ') return 'Space'
  return namedKeys.has(key) ? key : undefined
}

export function isModifierKeyName(key: string): boolean {
  return modifierKeys.has(key)
}

export function isPlainEscape(event: KeyboardEvent, tabHeld = false): boolean {
  return event.key === 'Escape' && !tabHeld && !event.ctrlKey && !event.altKey && !event.metaKey && !event.shiftKey
}

export function normalizeShortcut(value: unknown): string | null {
  if (typeof value !== 'string') return null
  const parts = value.trim().split('+')
  const key = primaryKey(parts.pop() || '')
  if (!key) return null
  const chordModifiers = modifiers.filter(modifier => parts.includes(modifier))
  if (chordModifiers.length !== parts.length) return null
  return [...chordModifiers, key].join('+')
}

export function isBindableShortcut(chord: string, action?: ShortcutAction): boolean {
  if (!chord || chord === 'Enter' || chord === 'Space') return false
  if (action && chord === 'Escape') return action === 'dismiss'
  if (action === 'dismiss') {
    // A normal character or editing key must still work in the description field.
    return chord.split('+').some(part => ['Ctrl', 'Alt', 'Meta'].includes(part)) || /(^|\+)F\d+$/.test(chord)
  }
  return true
}

export function shortcutFromKeyboardEvent(event: KeyboardEvent, tabHeld = false): string | null {
  if (event.isComposing || event.keyCode === 229 || ['Dead', 'Process', 'Unidentified'].includes(event.key)) return null
  let key = primaryKey(event.key)
  // Shifted digits, punctuation and Option-letter combinations can yield a different character.
  if (!key && /^Digit[0-9]$/.test(event.code)) key = event.code.slice(-1)
  if (!key && event.altKey && /^Key[A-Z]$/.test(event.code)) key = event.code.slice(3)
  if (!key && namedKeys.has(event.code)) key = event.code
  if (!key) return null
  const parts: string[] = []
  if (event.ctrlKey) parts.push('Ctrl')
  if (event.altKey) parts.push('Alt')
  if (event.shiftKey) parts.push('Shift')
  if (event.metaKey) parts.push('Meta')
  if (tabHeld) parts.push('Tab')
  parts.push(key)
  return parts.join('+')
}

export function formatShortcut(chord: string): string {
  return chord.split('+').map(part => part === 'Alt' && macKeyboard ? 'Option' : keyLabels[part] ?? part).join(' + ')
}

export function shortcutLabel(action: ShortcutAction): string {
  return SHORTCUT_DEFINITIONS.find(definition => definition.action === action)!.label
}
