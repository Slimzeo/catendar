import assert from 'node:assert/strict'
import { after, before, beforeEach, test } from 'node:test'
import { createPinia, setActivePinia } from 'pinia'
import { createServer } from 'vite'

let server, keys, useShortcutsStore
const saved = new Map()
const storage = {
  getItem: key => saved.get(key) ?? null,
  setItem: (key, value) => saved.set(key, value)
}
const storageDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
const navigatorDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'navigator')

before(async () => {
  server = await createServer({
    server: { middlewareMode: true, hmr: false, watch: null },
    optimizeDeps: { noDiscovery: true },
    logLevel: 'silent'
  })
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { platform: 'MacIntel' } })
  keys = await server.ssrLoadModule('/src/shortcuts.ts')
  const stores = await server.ssrLoadModule('/src/stores/shortcuts.ts')
  useShortcutsStore = stores.useShortcutsStore
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
})
beforeEach(() => {
  saved.clear()
  setActivePinia(createPinia())
})
after(async () => {
  await server?.close()
  if (storageDescriptor) Object.defineProperty(globalThis, 'localStorage', storageDescriptor)
  else delete globalThis.localStorage
  if (navigatorDescriptor) Object.defineProperty(globalThis, 'navigator', navigatorDescriptor)
  else delete globalThis.navigator
})

test('Mac uses Option for phases, leaving Control arrows available to Spaces', () => {
  const shortcuts = useShortcutsStore()
  const right = { key: 'ArrowRight', code: 'ArrowRight' }
  assert.equal(shortcuts.match(right), 'nextDate')
  assert.equal(shortcuts.match(right, true), 'columnRight')
  assert.equal(shortcuts.match({ ...right, altKey: true }), 'phaseRight')
  assert.equal(shortcuts.match({ key: 'ArrowLeft', altKey: true }), 'phaseLeft')
  assert.equal(shortcuts.match({ ...right, ctrlKey: true }), undefined)
  assert.equal(shortcuts.match({ ...right, altKey: true }, true), undefined)
  assert.equal(keys.formatShortcut(shortcuts.bindings.phaseRight), 'Option + →')
  assert.deepEqual(keys.defaultPhaseShortcuts(false), {phaseLeft: 'Ctrl+ArrowLeft', phaseRight: 'Ctrl+ArrowRight'})
  assert.equal(shortcuts.match({ key: 'd' }), 'deleteTask')
  assert.equal(shortcuts.match({ key: 'd', ctrlKey: true }), 'confirmDelete')
  assert.equal(shortcuts.match({ key: 'd', isComposing: true }), undefined)
  assert.equal(shortcuts.match({ key: '0' }), 'task10')
})

test('upgrade changes only the old phase defaults and respects occupied keys', () => {
  saved.set('catendar-shortcuts-v1', JSON.stringify({
    phaseRight: 'Ctrl+ArrowRight', phaseLeft: 'Ctrl+ArrowLeft', columnLeft: 'Alt+ArrowLeft', quickAdd: 'Q'
  }))
  const shortcuts = useShortcutsStore()
  assert.equal(shortcuts.bindings.phaseRight, 'Alt+ArrowRight')
  assert.equal(shortcuts.bindings.phaseLeft, 'Ctrl+ArrowLeft')
  assert.equal(shortcuts.bindings.columnLeft, 'Alt+ArrowLeft')
  assert.equal(shortcuts.bindings.quickAdd, 'Q')
  assert.equal(shortcuts.setBinding('phaseRight', 'Ctrl+ArrowRight').ok, true)
  setActivePinia(createPinia())
  assert.equal(useShortcutsStore().bindings.phaseRight, 'Ctrl+ArrowRight')
  saved.clear()
  saved.set('catendar-shortcuts-v1', JSON.stringify({ phaseRight: 'F6' }))
  setActivePinia(createPinia())
  assert.equal(useShortcutsStore().bindings.phaseRight, 'F6')
})

test('recording and dispatch agree for punctuation and Mac Option keys', () => {
  const shortcuts = useShortcutsStore()
  for (const event of [
    { key: '?', code: 'Slash', shiftKey: true },
    { key: '∆', code: 'KeyJ', altKey: true },
    { key: '%', code: 'Digit5', ctrlKey: true, shiftKey: true }
  ]) {
    const chord = keys.shortcutFromKeyboardEvent(event)
    assert.ok(chord)
    assert.equal(shortcuts.setBinding('quickAdd', chord).ok, true)
    assert.equal(shortcuts.match(event), 'quickAdd')
  }
  assert.equal(keys.shortcutFromKeyboardEvent({ key: 'Tab' }), null)
  assert.equal(shortcuts.setBinding('quickAdd', 'Tab').ok, false)
  assert.equal(shortcuts.setBinding('quickAdd', 'Enter').ok, false)
})

test('conflicts and typing keys cannot accidentally confirm or dismiss a dialog', () => {
  const shortcuts = useShortcutsStore()
  assert.equal(shortcuts.setBinding('confirmDelete', 'D').conflictWith, 'deleteTask')
  assert.equal(shortcuts.bindings.confirmDelete, 'Ctrl+D')
  assert.equal(shortcuts.setBinding('dismiss', 'X').ok, false)
  assert.equal(shortcuts.setBinding('dismiss', 'Ctrl+K').ok, true)
  assert.equal(shortcuts.setBinding('quickAdd', 'Escape').ok, false)
  assert.equal(keys.isPlainEscape({ key: 'Escape' }), true)
})

test('remaps survive a fresh store, disable the old key and reset together', () => {
  const shortcuts = useShortcutsStore()
  assert.equal(shortcuts.setBinding('quickAdd', 'Q').ok, true)
  assert.equal(shortcuts.setBinding('task5', 'G').ok, true)
  setActivePinia(createPinia())
  const reopened = useShortcutsStore()
  assert.equal(reopened.match({ key: 'q' }), 'quickAdd')
  assert.equal(reopened.match({ key: 'g' }), 'task5')
  assert.equal(reopened.match({ key: 'a' }), undefined)
  reopened.resetDefaults()
  assert.deepEqual({ ...reopened.bindings }, keys.DEFAULT_SHORTCUTS)
})

test('invalid saved data leaves a usable, unambiguous keymap', () => {
  for (const bad of ['{', 'null', '{"quickAdd":"D"}']) {
    saved.clear()
    saved.set('catendar-shortcuts-v1', bad)
    setActivePinia(createPinia())
    assert.deepEqual({ ...useShortcutsStore().bindings }, keys.DEFAULT_SHORTCUTS)
  }
})

test('existing custom keys survive the addition of priority commands', () => {
  saved.set('catendar-shortcuts-v2', JSON.stringify({ taskDown: 'Alt+ArrowDown', quickAdd: 'Q' }))
  const shortcuts = useShortcutsStore()
  assert.equal(shortcuts.bindings.taskDown, 'Alt+ArrowDown')
  assert.equal(shortcuts.bindings.quickAdd, 'Q')
  assert.equal(shortcuts.bindings.taskLower, '')
  assert.equal(shortcuts.bindings.taskRaise, 'Alt+ArrowUp')
  assert.equal(shortcuts.setBinding('taskLower', 'F8').ok, true)
  setActivePinia(createPinia())
  assert.equal(useShortcutsStore().bindings.taskLower, 'F8')
})
