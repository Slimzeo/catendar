<script setup lang="ts">
import { ref, watch } from 'vue'
import { NButton } from 'naive-ui'
import {
  SHORTCUT_DEFINITIONS,
  SHORTCUT_GROUP_LABELS,
  formatShortcut,
  isModifierKeyName,
  isPlainEscape,
  shortcutFromKeyboardEvent,
  shortcutLabel
} from '@/shortcuts'
import type { ShortcutAction, ShortcutDefinition, ShortcutGroupId } from '@/shortcuts'
import { useShortcutsStore } from '@/stores/shortcuts'

interface ShortcutFeedback {
  tone: 'success' | 'error'
  text: string
}

const emit = defineEmits<{
  (event: 'recording', value: boolean): void
}>()

const GROUP_ORDER: ShortcutGroupId[] = ['navigation', 'actions', 'date', 'dialogs', 'positions']
const groups = GROUP_ORDER
  .map(id => ({
    id,
    label: SHORTCUT_GROUP_LABELS[id],
    definitions: SHORTCUT_DEFINITIONS.filter(definition => definition.group === id)
  }))
  .filter(group => group.definitions.length > 0)

const shortcuts = useShortcutsStore()
const recordingAction = ref<ShortcutAction | null>(null)
const tabHeld = ref(false)
const feedback = ref<ShortcutFeedback | null>(null)

watch(recordingAction, action => emit('recording', action !== null), { immediate: true })

function chordText(action: ShortcutAction): string {
  return formatShortcut(shortcuts.bindings[action]) || 'Unassigned'
}

function controlLabel(definition: ShortcutDefinition): string {
  if (recordingAction.value === definition.action) {
    return `Recording ${definition.label} shortcut. Press a key combination, or Escape to cancel.`
  }
  return `${definition.label} shortcut, ${chordText(definition.action)}. Activate to change.`
}

function beginRecording(action: ShortcutAction, event: MouseEvent) {
  if (recordingAction.value === action) {
    cancelRecording()
    return
  }
  recordingAction.value = action
  tabHeld.value = false
  feedback.value = null
  const target = event.currentTarget
  if (target instanceof HTMLElement) target.focus()
}

function cancelRecording() {
  recordingAction.value = null
  tabHeld.value = false
}

function handleControlBlur(action: ShortcutAction) {
  if (recordingAction.value === action) cancelRecording()
}

function handleRecorderKeyup(event: KeyboardEvent) {
  if (event.key !== 'Tab') return
  const wasHeld = tabHeld.value
  tabHeld.value = false
  if (wasHeld && recordingAction.value && !feedback.value) {
    feedback.value = {
      tone: 'error',
      text: 'Tab must be combined with another key. Press a combination or Escape to cancel.'
    }
  }
}

function handleRecorderKeydown(event: KeyboardEvent) {
  const action = recordingAction.value
  if (!action) return

  if (isPlainEscape(event, tabHeld.value)) {
    event.preventDefault()
    event.stopPropagation()
    cancelRecording()
    return
  }

  if (event.key === 'Tab') {
    event.preventDefault()
    event.stopPropagation()
    tabHeld.value = true
    return
  }

  if (isModifierKeyName(event.key)) {
    event.preventDefault()
    event.stopPropagation()
    return
  }

  if (
    event.isComposing ||
    event.keyCode === 229 ||
    (event as KeyboardEvent & { which?: number }).which === 229 ||
    event.key === 'Unidentified' ||
    event.key === 'Dead' ||
    event.key === 'Process'
  ) {
    event.preventDefault()
    event.stopPropagation()
    return
  }

  event.preventDefault()
  event.stopPropagation()

  const chord = shortcutFromKeyboardEvent(event, tabHeld.value)
  if (!chord) {
    feedback.value = {
      tone: 'error',
      text: 'Press a letter, number, punctuation key, arrow, or function key.'
    }
    return
  }

  const result = shortcuts.setBinding(action, chord)
  if (result.ok) {
    const label = shortcutLabel(action)
    recordingAction.value = null
    tabHeld.value = false
    feedback.value = {
      tone: result.saved ? 'success' : 'error',
      text: result.saved
        ? `${label} is now ${formatShortcut(result.chord)}.`
        : 'Changed for this session. Local preferences could not be saved.'
    }
    return
  }

  if (result.reason === 'conflict' && result.conflictWith) {
    feedback.value = {
      tone: 'error',
      text: `Already used by ${shortcutLabel(result.conflictWith)}. Press another combination or Escape to cancel.`
    }
    return
  }

  feedback.value = {
    tone: 'error',
    text: action === 'dismiss'
      ? 'Use a function key or include Ctrl, Alt, or Meta so you can still type in the editor.'
      : 'Enter, Space and Tab have built-in controls. Choose a different key or add a modifier.'
  }
}

function resetToDefaults() {
  cancelRecording()
  const saved = shortcuts.resetDefaults()
  feedback.value = {
    tone: saved ? 'success' : 'error',
    text: saved ? 'Default shortcuts restored.' : 'Defaults restored for this session. Local preferences could not be saved.'
  }
}
</script>

<template>
  <div class="shortcuts-panel">
    <div class="panel-header">
      <p class="panel-hint">Select a shortcut, then press a key combination. Escape cancels recording.</p>
      <n-button size="small" secondary attr-type="button" @click="resetToDefaults">Reset to defaults</n-button>
    </div>

    <p
      v-if="feedback"
      class="shortcut-feedback"
      :class="feedback?.tone"
      :role="feedback?.tone === 'error' ? 'alert' : 'status'"
    >
      {{ feedback?.text }}
    </p>

    <div class="shortcut-groups">
      <section
        v-for="group in groups"
        :key="group.id"
        class="shortcut-group"
        :class="{ 'position-group': group.id === 'positions' }"
      >
        <h3>{{ group.label }}</h3>
        <div class="shortcut-rows" :class="{ 'position-grid': group.id === 'positions' }">
          <div v-for="definition in group.definitions" :key="definition.action" class="shortcut-row">
            <span class="shortcut-name">{{ definition.label }}</span>
            <button
              type="button"
              class="shortcut-control"
              :class="{ recording: recordingAction === definition.action }"
              :aria-label="controlLabel(definition)"
              @click="beginRecording(definition.action, $event)"
              @keydown="handleRecorderKeydown"
              @keyup="handleRecorderKeyup"
              @blur="handleControlBlur(definition.action)"
            >
              <span v-if="recordingAction === definition.action" class="recording-label">Press keys…</span>
              <kbd v-else class="shortcut-chord">{{ chordText(definition.action) }}</kbd>
            </button>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.shortcuts-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.panel-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.panel-hint {
  max-width: 48ch;
  margin: 0;
  color: var(--text-tertiary);
  font-size: 11px;
  line-height: 1.45;
}

.shortcut-feedback {
  margin: 0;
  padding: 7px 9px;
  border-radius: 8px;
  font-size: 11px;
  line-height: 1.45;
}

.shortcut-feedback.error {
  border: 1px solid color-mix(in srgb, var(--danger-color) 32%, var(--border-soft));
  background: color-mix(in srgb, var(--danger-color) 9%, transparent);
  color: var(--danger-color);
}

.shortcut-feedback.success {
  border: 1px solid color-mix(in srgb, var(--primary-color) 28%, var(--border-soft));
  background: var(--primary-muted);
  color: var(--text-secondary);
}

.shortcut-groups {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(245px, 1fr));
  align-items: start;
  gap: 16px 20px;
}

.shortcut-group h3 {
  margin: 0 0 7px;
  color: var(--text-tertiary);
  font-size: 9px;
  font-weight: 750;
  letter-spacing: 0.1em;
  text-transform: uppercase;
}

.shortcut-rows {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.shortcut-row {
  display: flex;
  min-width: 0;
  min-height: 29px;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.shortcut-name {
  overflow: hidden;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 550;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.shortcut-control {
  display: inline-flex;
  min-width: 66px;
  max-width: 150px;
  justify-content: center;
  padding: 3px 8px;
  border: 1px solid var(--border-soft);
  border-radius: 7px;
  overflow: hidden;
  background: var(--bg-elevated);
  color: var(--text-primary);
  cursor: pointer;
  font: inherit;
  font-size: 10px;
  line-height: 1.4;
}

.shortcut-control:hover {
  border-color: color-mix(in srgb, var(--primary-color) 35%, var(--border-soft));
  background: var(--bg-hover);
}

.shortcut-control:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.shortcut-control.recording {
  border-color: var(--primary-color);
  background: var(--primary-muted);
  color: var(--primary-color);
}

.shortcut-chord {
  min-width: 0;
  overflow: hidden;
  font-family: var(--font-body);
  font-size: 10px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.recording-label {
  font-size: 10px;
  font-weight: 650;
  white-space: nowrap;
}

.position-group {
  grid-column: 1 / -1;
}

.position-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(96px, 1fr));
  gap: 5px 12px;
}

.position-grid .shortcut-row {
  flex-direction: column;
  align-items: stretch;
  gap: 3px;
  padding: 5px 6px;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
}

.position-grid .shortcut-name {
  font-size: 10px;
}

.position-grid .shortcut-control {
  width: 100%;
  max-width: none;
}

@media (max-width: 520px) {
  .panel-header {
    align-items: stretch;
    flex-direction: column;
  }

  .panel-header :deep(.n-button) {
    align-self: flex-start;
  }
}
</style>
