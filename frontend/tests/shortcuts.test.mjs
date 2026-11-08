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

before(async () => {
  server = await createServer({
    server: { middlewareMode: true, hmr: false, watch: null },
    optimizeDeps: { noDiscovery: true },
    logLevel: 'silent'
  })
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
})

test('plain arrows, held Tab and Control dispatch three distinct commands', () => {
  const shortcuts = useShortcutsStore()
  const right = { key: 'ArrowRight', code: 'ArrowRight' }
  assert.equal(shortcuts.match(right), 'nextDate')
  assert.equal(shortcuts.match(right, true), 'columnRight')
  assert.equal(shortcuts.match({ ...right, ctrlKey: true }), 'phaseRight')
  assert.equal(shortcuts.match({ ...right, ctrlKey: true }, true), undefined)
  assert.equal(shortcuts.match({ key: 'd' }), 'deleteTask')
  assert.equal(shortcuts.match({ key: 'd', ctrlKey: true }), 'confirmDelete')
  assert.equal(shortcuts.match({ key: 'd', isComposing: true }), undefined)
  assert.equal(shortcuts.match({ key: '0' }), 'task10')
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
    saved.set('catendar-shortcuts-v1', bad)
    setActivePinia(createPinia())
    assert.deepEqual({ ...useShortcutsStore().bindings }, keys.DEFAULT_SHORTCUTS)
  }
})
