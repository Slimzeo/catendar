import { defineStore } from 'pinia'
import { reactive } from 'vue'
import type { ShortcutAction } from '@/shortcuts'
import {
  DEFAULT_SHORTCUTS, SHORTCUT_ACTIONS, defaultPhaseShortcuts, isBindableShortcut,
  normalizeShortcut, shortcutFromKeyboardEvent
} from '@/shortcuts'

const storageKey = 'catendar-shortcuts-v2'
type Bindings = Record<ShortcutAction, string>
type BindingResult =
  | { ok: true; chord: string; saved: boolean }
  | { ok: false; reason: 'invalid' }
  | { ok: false; reason: 'conflict'; conflictWith: ShortcutAction }

function decodeBindings(raw: string, defaults: Bindings): Bindings {
  const bindings = { ...defaults }
  const saved: unknown = JSON.parse(raw)
  if (!saved || typeof saved !== 'object' || Array.isArray(saved)) return bindings
  for (const action of SHORTCUT_ACTIONS) {
    const chord = normalizeShortcut((saved as Record<string, unknown>)[action])
    if (chord && isBindableShortcut(chord, action)) bindings[action] = chord
  }
  // Stored overrides may refer to a default that another action now uses.
  return new Set(Object.values(bindings)).size === SHORTCUT_ACTIONS.length
    ? bindings
    : { ...defaults }
}

function loadBindings(): Bindings {
  try {
    const current = localStorage.getItem(storageKey)
    if (current !== null) return decodeBindings(current, DEFAULT_SHORTCUTS)
    const legacy = localStorage.getItem('catendar-shortcuts-v1')
    if (legacy !== null) {
      const oldDefaults = { ...DEFAULT_SHORTCUTS, ...defaultPhaseShortcuts(false) }
      const bindings = decodeBindings(legacy, oldDefaults)
      for (const action of ['phaseLeft', 'phaseRight'] as const) {
        const next = DEFAULT_SHORTCUTS[action]
        if (bindings[action] === oldDefaults[action] && !Object.values(bindings).includes(next)) {
          bindings[action] = next
        }
      }
      persist(bindings)
      return bindings
    }
  } catch {
    // Ignore damaged or unavailable preferences.
  }
  return { ...DEFAULT_SHORTCUTS }
}

function persist(bindings: Bindings): boolean {
  try {
    localStorage.setItem(storageKey, JSON.stringify(bindings))
    return true
  } catch {
    return false
  }
}

export const useShortcutsStore = defineStore('shortcuts', () => {
  const bindings = reactive(loadBindings())

  function match(event: KeyboardEvent, tabHeld = false): ShortcutAction | undefined {
    const chord = shortcutFromKeyboardEvent(event, tabHeld)
    return chord ? SHORTCUT_ACTIONS.find(action => bindings[action] === chord) : undefined
  }

  function setBinding(action: ShortcutAction, value: string): BindingResult {
    const chord = normalizeShortcut(value)
    if (!chord || !isBindableShortcut(chord, action)) return { ok: false, reason: 'invalid' }
    const conflict = SHORTCUT_ACTIONS.find(other => other !== action && bindings[other] === chord)
    if (conflict) return { ok: false, reason: 'conflict', conflictWith: conflict }
    bindings[action] = chord
    return { ok: true, chord, saved: persist(bindings) }
  }

  function resetDefaults(): boolean {
    Object.assign(bindings, DEFAULT_SHORTCUTS)
    return persist(bindings)
  }

  return { bindings, match, setBinding, resetDefaults }
})
