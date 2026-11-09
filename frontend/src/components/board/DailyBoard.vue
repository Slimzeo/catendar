<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { format, parseISO } from 'date-fns'
import { useMessage } from 'naive-ui'
import type { ShortcutAction } from '@/shortcuts'
import type { CalendarEvent, EventInput, EventPhase } from '@/types'
import { eventPhases, isEventPhase } from '@/constants/eventPhases'
import { useCalendarStore } from '@/stores/calendar'
import { useEventsStore } from '@/stores/events'
import { useThemeStore } from '@/stores/theme'
import { useSyncSourcesStore } from '@/stores/syncSources'
import { sourceEmailUrl } from '@/utils/description'
import EventDescription from '@/components/event/EventDescription.vue'
import EventRegistrations from '@/components/event/EventRegistrations.vue'

const emit = defineEmits<{
  (event: 'edit', calendarEvent: CalendarEvent, focus?: 'title' | 'description' | 'result'): void
  (event: 'request-delete', calendarEvent: CalendarEvent): void
  (event: 'create-details', phase: EventPhase, title: string): void
  (event: 'open-email', sourceURL: string): void
  (event: 'quick-state', active: boolean): void
}>()

const calendarStore = useCalendarStore()
const eventsStore = useEventsStore()
const themeStore = useThemeStore()
const syncSources = useSyncSourcesStore()
const message = useMessage()

const selectedDate = computed(() => calendarStore.focusedDate)
const dayEvents = computed(() => eventsStore.boardEventsForDay(selectedDate.value))

const columns = computed(() => eventPhases.map(phase => ({
  phase,
  events: dayEvents.value.filter(event => event.phase === phase)
})))

const boardRef = ref<HTMLElement | null>(null)
const activePhase = ref<EventPhase>('todo')
const selectedId = ref<number | null>(null)
const selectedEvent = computed(() => {
  const id = selectedId.value
  if (id == null) return null
  return dayEvents.value.find(event => event.id === id) ?? null
})
const lastColumnIds: Record<EventPhase, number[]> = {
  todo: [],
  progressing: [],
  done: []
}
const phaseMoveId = ref<number | null>(null)
const openPhaseId = ref<number | null>(null)
let phaseMoveToken = 0

const quickPhase = ref<EventPhase | null>(null)
const quickTitle = ref('')
const quickError = ref('')
const quickSaving = ref(false)
const quickInput = ref<HTMLInputElement | null>(null)
const addButtons = new Map<EventPhase, HTMLButtonElement>()
let quickToken = 0

function setQuickInput(element: unknown) {
  quickInput.value = element instanceof HTMLInputElement ? element : null
}

function setAddButton(phase: EventPhase, element: unknown) {
  if (element instanceof HTMLButtonElement) addButtons.set(phase, element)
  else addButtons.delete(phase)
}

function phasePickerId(id: number): string {
  return `board-phase-picker-${id}`
}

function togglePhasePicker(calendarEvent: CalendarEvent, event: MouseEvent) {
  if (eventsStore.isMoving(calendarEvent.id)) return
  // WebKit can leave focus on the previous input when a button is clicked.
  if (event.currentTarget instanceof HTMLButtonElement) event.currentTarget.focus({ preventScroll: true })
  selectEvent(calendarEvent)
  openPhaseId.value = openPhaseId.value === calendarEvent.id ? null : calendarEvent.id
}

let pickerPointerActive = false

function closePickerWhenOutside(event: Event) {
  if (openPhaseId.value == null) return
  // A pointer press is handled by the following click, after its target is fixed.
  if (event.type === 'focusin' && pickerPointerActive) return
  if (!(event.target instanceof Element)) return
  const control = event.target.closest('.phase-trigger, .phase-picker')
  if (control?.closest('article')?.dataset.taskId === String(openPhaseId.value)) return
  openPhaseId.value = null
}

function notePickerPointerDown() {
  pickerPointerActive = true
}

function resetPickerPointer() {
  pickerPointerActive = false
}

function onPickerFocusOut(event: FocusEvent, id: number) {
  if (openPhaseId.value !== id) return
  if (event.relatedTarget != null || pickerPointerActive) return
  openPhaseId.value = null
}

const draggingId = ref<number | null>(null)
const dragTarget = ref<EventPhase | null>(null)

watch(quickPhase, async phase => {
  emit('quick-state', Boolean(phase))
  if (phase) {
    await nextTick()
    if (quickPhase.value === phase) quickInput.value?.focus()
  }
})

watch(selectedDate, () => {
  openPhaseId.value = null
  activePhase.value = 'todo'
  selectedId.value = null
  for (const phase of eventPhases) lastColumnIds[phase] = []
  phaseMoveToken += 1
  phaseMoveId.value = null
  if (quickPhase.value) closeQuick()
})

watch(
  [dayEvents, () => eventsStore.loading],
  reconcileSelection,
  { flush: 'post', immediate: true }
)

watch(selectedId, id => {
  if (pickerPointerActive) return
  if (openPhaseId.value !== id) openPhaseId.value = null
})

onMounted(() => {
  document.addEventListener('pointerdown', notePickerPointerDown, true)
  document.addEventListener('focusin', closePickerWhenOutside, true)
  document.addEventListener('click', closePickerWhenOutside, true)
  document.addEventListener('pointerup', resetPickerPointer, true)
  document.addEventListener('pointercancel', resetPickerPointer, true)
  window.addEventListener('blur', resetPickerPointer)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', notePickerPointerDown, true)
  document.removeEventListener('focusin', closePickerWhenOutside, true)
  document.removeEventListener('click', closePickerWhenOutside, true)
  document.removeEventListener('pointerup', resetPickerPointer, true)
  document.removeEventListener('pointercancel', resetPickerPointer, true)
  window.removeEventListener('blur', resetPickerPointer)
  emit('quick-state', false)
  phaseMoveToken += 1
})

function openQuick(phase: EventPhase) {
  if (quickSaving.value) return
  if (phase !== activePhase.value) {
    activePhase.value = phase
    selectedId.value = firstEventId(phase)
  }
  if (quickPhase.value === phase) {
    quickInput.value?.focus()
    return
  }
  quickToken += 1
  quickPhase.value = phase
  quickTitle.value = ''
  quickError.value = ''
}

function discardQuick() {
  quickToken += 1
  quickPhase.value = null
  quickTitle.value = ''
  quickError.value = ''
}

function closeQuick() {
  if (quickSaving.value) return
  discardQuick()
}

function openQuickDetails() {
  const phase = quickPhase.value
  if (!phase || quickSaving.value) return
  const title = quickTitle.value
  emit('create-details', phase, title)
  closeQuick()
}

async function submitQuick() {
  const phase = quickPhase.value
  if (quickSaving.value || !phase) return
  const title = quickTitle.value.trim()
  if (!title) {
    quickError.value = 'Add a title first.'
    quickInput.value?.focus()
    return
  }

  const createDate = selectedDate.value
  const token = quickToken
  quickSaving.value = true
  try {
    const input: EventInput = {
      title,
      start: `${createDate}T00:00:00`,
      end: `${createDate}T00:00:00`,
      color: eventsStore.nextCreateColor,
      allDay: true,
      description: '',
      bold: false,
      phase,
      deadline: '',
      boardDay: createDate
    }
    const created = await eventsStore.createEvent(input)
    if (token !== quickToken) return
    if (selectedDate.value === createDate) {
      activePhase.value = created.phase
      selectedId.value = created.id
    }
    quickTitle.value = ''
    quickError.value = ''
  } catch (error) {
    if (token !== quickToken) return
    const detail = error instanceof Error ? error.message : 'Could not add the task'
    quickError.value = detail
    message.error(detail)
  } finally {
    quickSaving.value = false
    if (token === quickToken) {
      await nextTick()
      if (document.activeElement === document.body) quickInput.value?.focus()
    }
  }
}

function startDrag(event: DragEvent, calendarEvent: CalendarEvent) {
  if (eventsStore.isMoving(calendarEvent.id)) {
    event.preventDefault()
    return
  }
  const target = event.target as HTMLElement | null
  if (target?.closest('a, button, input, label')) {
    event.preventDefault()
    return
  }
  openPhaseId.value = null
  draggingId.value = calendarEvent.id
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', String(calendarEvent.id))
  }
}

function endDrag() {
  draggingId.value = null
  dragTarget.value = null
}

function dragOver(phase: EventPhase, event: DragEvent) {
  if (!draggingId.value) return
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
  dragTarget.value = phase
}

function dragLeave(phase: EventPhase, event: DragEvent) {
  const column = event.currentTarget as HTMLElement | null
  const related = event.relatedTarget as Node | null
  if (column && related && column.contains(related)) return
  if (dragTarget.value === phase) dragTarget.value = null
}

function dropOn(phase: EventPhase) {
  const id = draggingId.value
  dragTarget.value = null
  draggingId.value = null
  if (!id) return
  const calendarEvent = dayEvents.value.find(item => item.id === id)
  if (!calendarEvent) return
  selectEvent(calendarEvent)
  if (calendarEvent.phase === phase) return
  void moveEvent(calendarEvent, phase)
}

function choosePhase(calendarEvent: CalendarEvent, phase: EventPhase) {
  if (openPhaseId.value !== calendarEvent.id || eventsStore.isMoving(calendarEvent.id)) return
  openPhaseId.value = null
  selectEvent(calendarEvent)
  if (phase !== calendarEvent.phase) void moveEvent(calendarEvent, phase)
  void focusSelection()
}

async function moveEvent(calendarEvent: CalendarEvent, phase: EventPhase) {
  const day = selectedDate.value
  try {
    await eventsStore.setEventPhase(calendarEvent.id, phase, day)
  } catch (error) {
    const detail = error instanceof Error ? error.message : 'Could not move the task'
    message.error(detail)
  }
}

function openEvent(calendarEvent: CalendarEvent) {
  selectEvent(calendarEvent)
  emit('edit', calendarEvent)
}

function openResultEditor(calendarEvent: CalendarEvent) {
  selectEvent(calendarEvent)
  emit('edit', calendarEvent, 'result')
}

function eventTime(event: CalendarEvent): string {
  if (event.allDay) return ''
  const start = parseISO(event.start)
  if (Number.isNaN(start.getTime())) return ''
  const startLabel = format(start, 'HH:mm')
  if (!event.end) return startLabel
  const end = parseISO(event.end)
  if (Number.isNaN(end.getTime())) return startLabel
  if (format(end, 'yyyy-MM-dd') !== format(start, 'yyyy-MM-dd')) return startLabel
  const endLabel = format(end, 'HH:mm')
  return endLabel === startLabel ? startLabel : `${startLabel}–${endLabel}`
}

function deadlineLabel(deadline: string): string {
  const date = parseISO(deadline)
  if (Number.isNaN(date.getTime())) return deadline
  return format(date, 'MMM d')
}

function isOverdue(event: CalendarEvent): boolean {
  return Boolean(event.deadline && event.phase !== 'done' && event.deadline < selectedDate.value)
}

function mailUrl(event: CalendarEvent): string | undefined {
  return syncSources.active.email ? sourceEmailUrl(event.description) : undefined
}

function cardDescription(event: CalendarEvent): string {
  const url = sourceEmailUrl(event.description)
  if (!url) return event.description
  return event.description
    .split(url)
    .join('')
    .replace(/\s*Source email:\s*$/i, '')
    .trim()
}

function openMail(event: CalendarEvent) {
  const url = mailUrl(event)
  if (url) emit('open-email', url)
}

function hasMeta(event: CalendarEvent): boolean {
  return Boolean((themeStore.showTime && eventTime(event)) || event.deadline || mailUrl(event))
}

function hasRegistrations(event: CalendarEvent): boolean {
  return Boolean(event.registrations?.length)
}

function eventsForPhase(phase: EventPhase): CalendarEvent[] {
  return columns.value.find(column => column.phase === phase)?.events ?? []
}

function firstEventId(phase: EventPhase): number | null {
  const events = eventsForPhase(phase)
  return events.length ? events[0].id : null
}

function captureColumnIds(): Record<EventPhase, number[]> {
  const ids: Record<EventPhase, number[]> = { todo: [], progressing: [], done: [] }
  for (const column of columns.value) ids[column.phase] = column.events.map(event => event.id)
  return ids
}

function reconcileSelection() {
  const ids = captureColumnIds()
  const id = selectedId.value

  if (id != null) {
    const calendarEvent = dayEvents.value.find(event => event.id === id)
    if (calendarEvent) {
      if (calendarEvent.phase !== activePhase.value) activePhase.value = calendarEvent.phase
    } else if (!eventsStore.loading) {
      const previousIds = lastColumnIds[activePhase.value]
      const previousIndex = previousIds.indexOf(id)
      const candidates = ids[activePhase.value]
      selectedId.value = candidates.length
        ? candidates[previousIndex >= 0 ? Math.min(previousIndex, candidates.length - 1) : 0]
        : null
    } else {
      return
    }
  } else {
    const candidates = ids[activePhase.value]
    if (candidates.length) selectedId.value = candidates[0]
  }

  for (const phase of eventPhases) lastColumnIds[phase] = ids[phase]
}

function selectEvent(calendarEvent: CalendarEvent) {
  activePhase.value = calendarEvent.phase
  selectedId.value = calendarEvent.id
}

async function focusSelection() {
  reconcileSelection()
  await nextTick()

  const id = selectedId.value
  if (id != null) {
    const title = boardRef.value?.querySelector<HTMLButtonElement>(
      `article[data-task-id="${id}"] .edit-title`
    )
    if (title) {
      title.focus({ preventScroll: true })
      title.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
      return
    }
  }

  const addButton = addButtons.get(activePhase.value)
  if (addButton) {
    addButton.focus({ preventScroll: true })
    addButton.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
  }
}

function syncSelectionFromFocus(target: EventTarget | null) {
  if (!(target instanceof Element) || !boardRef.value?.contains(target)) return
  const column = target.closest<HTMLElement>('[data-phase]')
  if (!column) return
  const phase = column.dataset.phase
  if (!isEventPhase(phase)) return

  const card = target.closest<HTMLElement>('[data-task-id]')
  if (card) {
    const calendarEvent = dayEvents.value.find(item => item.id === Number(card.dataset.taskId))
    if (!calendarEvent) return
    selectEvent(calendarEvent)
    return
  }

  if (phase !== activePhase.value) {
    activePhase.value = phase
    selectedId.value = firstEventId(phase)
  }
}

function cancelQuickAdd() {
  if (quickSaving.value || !quickPhase.value) return
  closeQuick()
  void focusSelection()
}

function moveColumn(direction: -1 | 1) {
  const nextIndex = eventPhases.indexOf(activePhase.value) + direction
  if (nextIndex >= 0 && nextIndex < eventPhases.length) {
    activePhase.value = eventPhases[nextIndex]
    selectedId.value = firstEventId(activePhase.value)
  }
  void focusSelection()
}

function moveTask(direction: -1 | 1) {
  const events = eventsForPhase(activePhase.value)
  if (!events.length) return
  const index = events.findIndex(event => event.id === selectedId.value)
  const nextIndex = Math.max(0, Math.min(index + direction, events.length - 1))
  selectedId.value = events[nextIndex].id
  void focusSelection()
}

function selectTaskPosition(position: number) {
  const target = eventsForPhase(activePhase.value)[position - 1]
  if (!target) return
  selectedId.value = target.id
  void focusSelection()
}

function quickAddFromShortcut() {
  if (quickSaving.value) return
  if (quickPhase.value === activePhase.value) {
    quickInput.value?.focus()
    return
  }
  openQuick(activePhase.value)
}

function requestDeleteSelected() {
  const calendarEvent = selectedEvent.value
  if (!calendarEvent) return
  emit('request-delete', calendarEvent)
}

function editSelectedDescription() {
  const calendarEvent = selectedEvent.value
  if (!calendarEvent) return
  emit('edit', calendarEvent, 'description')
}

function focusStillOnTask(id: number): boolean {
  if (selectedId.value !== id) return false
  const active = document.activeElement
  if (!active || active === document.body) return true
  if (!(active instanceof HTMLElement)) return false
  const card = active.closest<HTMLElement>('[data-task-id]')
  return card?.dataset.taskId === String(id)
}

async function moveSelectedPhase(direction: -1 | 1) {
  if (phaseMoveId.value != null) return
  const calendarEvent = selectedEvent.value
  if (!calendarEvent || eventsStore.isMoving(calendarEvent.id)) return

  const currentIndex = eventPhases.indexOf(calendarEvent.phase)
  const nextIndex = currentIndex + direction
  if (currentIndex < 0 || nextIndex < 0 || nextIndex >= eventPhases.length) return

  const previousPhase = calendarEvent.phase
  const phase = eventPhases[nextIndex]
  const moveId = calendarEvent.id
  const moveDate = selectedDate.value
  const token = ++phaseMoveToken

  phaseMoveId.value = moveId
  activePhase.value = phase
  selectedId.value = moveId
  const move = eventsStore.setEventPhase(moveId, phase, moveDate)
  void focusSelection()

  try {
    const updated = await move
    if (token !== phaseMoveToken || selectedDate.value !== moveDate) return
    if (selectedId.value === moveId) {
      if (updated) activePhase.value = updated.phase
      else {
        activePhase.value = previousPhase
        reconcileSelection()
      }
    }
    await nextTick()
    if (focusStillOnTask(moveId)) await focusSelection()
  } catch (error) {
    if (token !== phaseMoveToken || selectedDate.value !== moveDate) return
    if (selectedId.value === moveId) activePhase.value = previousPhase
    const detail = error instanceof Error ? error.message : 'Could not move the task'
    message.error(detail)
    await nextTick()
    if (focusStillOnTask(moveId)) await focusSelection()
  } finally {
    if (phaseMoveToken === token) phaseMoveId.value = null
  }
}

function runShortcut(action: ShortcutAction): boolean {
  if (action === 'dismiss') {
    if (openPhaseId.value == null) return false
    openPhaseId.value = null
    void focusSelection()
    return true
  }
  openPhaseId.value = null
  switch (action) {
    case 'columnLeft':
      moveColumn(-1)
      return true
    case 'columnRight':
      moveColumn(1)
      return true
    case 'taskUp':
      moveTask(-1)
      return true
    case 'taskDown':
      moveTask(1)
      return true
    case 'quickAdd':
      quickAddFromShortcut()
      return true
    case 'deleteTask':
      requestDeleteSelected()
      return true
    case 'editDescription':
      editSelectedDescription()
      return true
    case 'phaseLeft':
      void moveSelectedPhase(-1)
      return true
    case 'phaseRight':
      void moveSelectedPhase(1)
      return true
    default: {
      const match = /^task(10|[1-9])$/.exec(action)
      if (!match) return false
      selectTaskPosition(Number(match[1]))
      return true
    }
  }
}

defineExpose({
  runShortcut,
  focusSelection,
  syncSelectionFromFocus,
  cancelQuickAdd
})
</script>

<template>
  <main
    ref="boardRef"
    class="board"
    :aria-label="`Tasks for ${calendarStore.dayLabel}`"
    @pointerdown.capture="syncSelectionFromFocus($event.target)"
  >
    <section
      v-for="column in columns"
      :key="column.phase"
      class="column"
      :class="{ dropping: dragTarget === column.phase, done: column.phase === 'done' }"
      :data-phase="column.phase"
      :data-active="activePhase === column.phase ? 'true' : 'false'"
      :aria-labelledby="`board-column-${column.phase}`"
      @dragenter.prevent
      @dragover.prevent="dragOver(column.phase, $event)"
      @dragleave="dragLeave(column.phase, $event)"
      @drop.prevent="dropOn(column.phase)"
    >
      <header class="column-header">
        <h2 :id="`board-column-${column.phase}`">
          {{ column.phase }}
          <span class="column-count">{{ column.events.length }}</span>
        </h2>
        <button
          :ref="element => setAddButton(column.phase, element)"
          type="button"
          class="add-button"
          tabindex="0"
          :aria-label="`Add task to ${column.phase}`"
          :title="`Add task to ${column.phase}`"
          @click="openQuick(column.phase)"
        >
          <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M10 4v12M4 10h12" /></svg>
        </button>
      </header>

      <div class="card-list">
        <article
          v-for="calendarEvent in column.events"
          :key="calendarEvent.id"
          class="card"
          :class="{
            done: calendarEvent.phase === 'done',
            dragging: draggingId === calendarEvent.id,
            'no-meta': !hasMeta(calendarEvent) && !hasRegistrations(calendarEvent)
          }"
          :style="{ '--event-color': calendarEvent.color }"
          :data-task-id="calendarEvent.id"
          :data-selected="selectedId === calendarEvent.id ? 'true' : 'false'"
          :aria-current="selectedId === calendarEvent.id ? 'true' : undefined"
          :draggable="!eventsStore.isMoving(calendarEvent.id)"
          @click="openEvent(calendarEvent)"
          @dragstart="startDrag($event, calendarEvent)"
          @dragend="endDrag"
        >
          <h3 :class="{ bold: calendarEvent.bold }">
            <button type="button" class="edit-title" tabindex="0" @click.stop="openEvent(calendarEvent)">{{ calendarEvent.title }}</button>
          </h3>
          <p v-if="calendarEvent.phase === 'done' && calendarEvent.result" class="card-result">
            {{ calendarEvent.result }}
          </p>
          <event-description
            v-if="cardDescription(calendarEvent)"
            class="card-description"
            :text="cardDescription(calendarEvent)"
            :clamp="2"
            @open-email="sourceURL => emit('open-email', sourceURL)"
          />
          <event-registrations
            v-if="hasRegistrations(calendarEvent)"
            class="card-registrations"
            :registrations="calendarEvent.registrations"
            :context="calendarEvent.title"
            compact
          />
          <button
            v-if="calendarEvent.phase === 'progressing'"
            type="button"
            class="result-action"
            :aria-label="`${calendarEvent.result ? 'Edit' : 'Add'} result for ${calendarEvent.title}`"
            @click.stop="openResultEditor(calendarEvent)"
          >
            {{ calendarEvent.result ? 'Edit result' : 'Add result' }}
          </button>
          <div class="card-footer">
            <div v-if="hasMeta(calendarEvent)" class="card-meta">
              <span v-if="themeStore.showTime && eventTime(calendarEvent)" class="meta-item">
                <svg viewBox="0 0 20 20" aria-hidden="true">
                  <circle cx="10" cy="10" r="7" /><path d="M10 6v4l2.5 1.5" />
                </svg>
                {{ eventTime(calendarEvent) }}
              </span>
              <span
                v-if="calendarEvent.deadline"
                class="meta-item"
                :class="{ late: isOverdue(calendarEvent) }"
              >
                <svg viewBox="0 0 20 20" aria-hidden="true">
                  <path d="M5 17V3.5h9l-1.8 3.2L14 10H5" />
                </svg>
                {{ hasRegistrations(calendarEvent) ? 'Task due' : 'Due' }} {{ deadlineLabel(calendarEvent.deadline) }}
              </span>
              <button
                v-if="mailUrl(calendarEvent)"
                type="button"
                class="meta-item mail-link"
                :aria-label="`Open source email for ${calendarEvent.title}`"
                @click.stop="openMail(calendarEvent)"
              >
                <svg viewBox="0 0 20 20" aria-hidden="true">
                  <rect x="3" y="5" width="14" height="10" rx="2" /><path d="m3.5 6 6.5 5 6.5-5" />
                </svg>
                Email
              </button>
            </div>
            <button
              type="button"
              class="phase-trigger"
              tabindex="0"
              :aria-label="`Move ${calendarEvent.title} to another phase`"
              :aria-expanded="openPhaseId === calendarEvent.id"
              :aria-controls="openPhaseId === calendarEvent.id ? phasePickerId(calendarEvent.id) : undefined"
              :disabled="eventsStore.isMoving(calendarEvent.id)"
              @click.stop="togglePhasePicker(calendarEvent, $event)"
              @focusout="onPickerFocusOut($event, calendarEvent.id)"
            >
              <span class="phase-name">{{ calendarEvent.phase }}</span>
              <svg class="phase-chevron" viewBox="0 0 20 20" aria-hidden="true">
                <path d="m6 8 4 4 4-4" />
              </svg>
            </button>
          </div>
          <div
            v-if="openPhaseId === calendarEvent.id"
            :id="phasePickerId(calendarEvent.id)"
            class="phase-picker"
            role="group"
            :aria-label="`Phases for ${calendarEvent.title}`"
            @click.stop
            @focusout="onPickerFocusOut($event, calendarEvent.id)"
          >
            <button
              v-for="phase in eventPhases"
              :key="phase"
              type="button"
              class="phase-option"
              tabindex="0"
              :aria-pressed="phase === calendarEvent.phase"
              :disabled="eventsStore.isMoving(calendarEvent.id)"
              @click.stop="choosePhase(calendarEvent, phase)"
            >
              <span>{{ phase }}</span>
              <svg v-if="phase === calendarEvent.phase" class="phase-check" viewBox="0 0 20 20" aria-hidden="true">
                <path d="m4.5 10.5 3.5 3.5 7.5-8" />
              </svg>
            </button>
          </div>
        </article>

        <p
          v-if="column.events.length === 0 && quickPhase !== column.phase"
          class="empty"
        >No tasks</p>

        <form
          v-if="quickPhase === column.phase"
          class="quick-add"
          @submit.prevent="submitQuick"
        >
          <input
            :ref="setQuickInput"
            v-model="quickTitle"
            class="quick-input"
            type="text"
            required
            autocomplete="off"
            placeholder="Task title, then Enter"
            :aria-label="`New task in ${column.phase}`"
            :disabled="quickSaving"
            @input="quickError = ''"
          />
          <div class="quick-actions">
            <button
              type="button"
              class="quick-details"
              title="Open the full task editor"
              :disabled="quickSaving"
              @click="openQuickDetails"
            >
              Details…
            </button>
            <button
              type="submit"
              class="quick-button"
              aria-label="Save task"
              title="Save task"
              :disabled="quickSaving"
            >
              <svg viewBox="0 0 20 20" aria-hidden="true"><path d="m4.5 10.5 3.5 3.5 7.5-8" /></svg>
            </button>
            <button
              type="button"
              class="quick-button"
              aria-label="Close quick add"
              title="Close"
              :disabled="quickSaving"
              @click="closeQuick"
            >
              <svg viewBox="0 0 20 20" aria-hidden="true"><path d="m6 6 8 8M14 6l-8 8" /></svg>
            </button>
          </div>
          <p v-if="quickError" class="quick-error" role="alert">{{ quickError }}</p>
        </form>
      </div>
    </section>
  </main>
</template>

<style scoped>
.board {
  display: grid;
  flex: 1;
  min-height: 0;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  padding: 12px 14px 14px;
  overflow: auto;
  background: var(--bg-primary);
}

.column {
  display: flex;
  min-height: 0;
  flex-direction: column;
  border: 1px solid var(--border-soft);
  border-radius: 12px;
  background: var(--bg-secondary);
  transition: border-color 140ms ease, box-shadow 140ms ease;
}

.column[data-active='true'] {
  border-color: color-mix(in srgb, var(--primary-color) 36%, var(--border-soft));
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--primary-color) 8%, transparent);
}

.column.dropping {
  border-color: var(--primary-color);
  box-shadow: 0 0 0 3px var(--focus-ring);
}

.column-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 8px 6px 12px;
}

.column-header h2 {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 7px;
  margin: 0;
  color: var(--text-primary);
  font-family: var(--font-display);
  font-size: 13px;
  font-weight: 650;
  letter-spacing: -0.01em;
  line-height: 1.3;
}

.column-count {
  color: var(--text-tertiary);
  font-family: var(--font-body);
  font-weight: 500;
}

.add-button {
  display: grid;
  width: 26px;
  height: 26px;
  margin-left: auto;
  padding: 0;
  flex: 0 0 auto;
  place-items: center;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}

.add-button:hover,
.add-button:focus-visible {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.add-button:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.add-button svg {
  width: 16px;
  height: 16px;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.7;
}

.card-list {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  gap: 6px;
  padding: 2px 8px 8px;
  overflow-y: auto;
}

.card {
  position: relative;
  padding: 8px 10px 8px 14px;
  border: 1px solid var(--border-soft);
  border-radius: 9px;
  background: var(--bg-elevated);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
  outline: 0;
  transition: border-color 140ms ease, opacity 140ms ease, transform 140ms ease;
}

.card.no-meta {
  padding-right: 94px;
}

.card::before {
  position: absolute;
  top: 9px;
  bottom: 9px;
  left: 5px;
  width: 3px;
  border-radius: 2px;
  background: var(--event-color);
  content: '';
}

.card:hover {
  border-color: var(--border-strong);
}

.card[data-selected='true'] {
  border-color: color-mix(in srgb, var(--primary-color) 52%, var(--border-soft));
  box-shadow: 0 0 0 1px var(--focus-ring), var(--shadow-sm);
}

.card[data-selected='true']:hover {
  border-color: var(--primary-color);
}

.edit-title {
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  text-decoration: inherit;
  overflow-wrap: anywhere;
  cursor: pointer;
}

.card:focus-within {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.edit-title:focus-visible {
  outline: none; /* The enclosing card carries the focus ring. */
}

.card.dragging {
  opacity: 0.45;
}

.card h3 {
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--text-primary);
  font-size: 13.5px;
  font-weight: 600;
  line-height: 1.35;
}

.card h3.bold {
  font-weight: 750;
}

.card.done h3 {
  color: var(--text-secondary);
  text-decoration: line-through;
  text-decoration-color: var(--text-tertiary);
}

.card-description {
  margin-top: 2px;
  font-size: 12.5px;
  -webkit-line-clamp: 2;
}

.card-registrations {
  --registration-font-size: 11px;
  margin-top: 4px;
}

.card-result {
  margin: 2px 0 0;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.4;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.result-action {
  display: inline-flex;
  margin-top: 3px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: 11px;
  font-weight: 600;
  line-height: 1.3;
  cursor: pointer;
}

.result-action:hover,
.result-action:focus-visible {
  color: var(--primary-color);
}

.result-action:focus-visible {
  border-radius: 4px;
  outline: 2px solid var(--focus-ring);
  outline-offset: 2px;
}

.card-footer {
  display: flex;
  min-width: 0;
  align-items: flex-end;
  justify-content: space-between;
  gap: 8px;
  margin-top: 4px;
}

.card-meta {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 3px 10px;
  color: var(--text-tertiary);
  font-size: 12px;
}

.meta-item {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 4px;
}

.meta-item svg {
  width: 13px;
  height: 13px;
  flex: 0 0 auto;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.65;
}

.meta-item.late {
  color: var(--danger-color);
}

.mail-link {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--text-tertiary);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}

.mail-link:hover,
.mail-link:focus-visible {
  color: var(--primary-color);
}

.mail-link:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.phase-trigger {
  display: inline-flex;
  min-width: 0;
  flex: 0 0 auto;
  align-items: center;
  gap: 3px;
  margin-left: auto;
  padding: 1px 3px 1px 5px;
  border: 1px solid transparent;
  border-radius: 5px;
  outline: 0;
  background: transparent;
  color: var(--text-tertiary);
  font: inherit;
  font-size: 11px;
  line-height: 1.3;
  cursor: pointer;
}

.phase-trigger:hover:not(:disabled),
.phase-trigger:focus-visible,
.phase-trigger[aria-expanded='true'] {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.phase-trigger:focus-visible {
  border-color: var(--primary-color);
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.phase-trigger:disabled {
  cursor: default;
  opacity: 0.45;
}

.phase-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.phase-chevron {
  width: 11px;
  height: 11px;
  flex: 0 0 auto;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.8;
  opacity: 0.75;
  transition: transform 140ms ease;
}

.phase-trigger[aria-expanded='true'] .phase-chevron {
  transform: rotate(180deg);
}

.phase-picker {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 3px;
  margin-top: 6px;
  padding-top: 6px;
  border-top: 1px solid var(--border-soft);
}

.phase-option {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  justify-content: center;
  gap: 2px;
  padding: 3px 4px;
  border: 1px solid var(--border-soft);
  border-radius: 5px;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: 11px;
  line-height: 1.25;
  overflow-wrap: anywhere;
  cursor: pointer;
}

.phase-option:hover:not(:disabled),
.phase-option:focus-visible {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.phase-option:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.phase-option[aria-pressed='true'] {
  border-color: color-mix(in srgb, var(--primary-color) 40%, var(--border-soft));
  color: var(--text-primary);
  font-weight: 600;
}

.phase-option:disabled {
  cursor: default;
  opacity: 0.45;
}

.phase-check {
  width: 10px;
  height: 10px;
  flex: 0 0 auto;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.2;
}

.no-meta .card-footer {
  position: absolute;
  top: 5px;
  right: 3px;
  margin-left: 0;
}

.no-meta .phase-trigger {
  max-width: 88px;
  font-size: 10.5px;
}

.no-meta .phase-picker {
  margin-right: -84px;
}

.empty {
  margin: 0;
  padding: 10px;
  border: 1px dashed var(--border-strong);
  border-radius: 9px;
  color: var(--text-tertiary);
  font-size: 12.5px;
  text-align: center;
}

.quick-add {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 4px;
  padding: 5px 6px 5px 9px;
  border: 1px solid var(--primary-color);
  border-radius: 9px;
  background: var(--bg-elevated);
  box-shadow: 0 0 0 3px var(--focus-ring);
}

.quick-input {
  width: 100%;
  min-width: 0;
  padding: 0;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  font-size: 13.5px;
}

.quick-input::placeholder {
  color: var(--text-tertiary);
}

.quick-input:disabled {
  opacity: 0.6;
}

.quick-actions {
  display: flex;
  align-items: center;
  gap: 2px;
}

.quick-details {
  height: 24px;
  padding: 0 5px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-tertiary);
  font: inherit;
  font-size: 11px;
  white-space: nowrap;
  cursor: pointer;
}

.quick-details:hover,
.quick-details:focus-visible {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.quick-details:focus-visible {
  outline: 2px solid var(--focus-ring);
}

.quick-details:disabled {
  cursor: default;
  opacity: 0.45;
}

.quick-button {
  display: grid;
  width: 24px;
  height: 24px;
  padding: 0;
  place-items: center;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}

.quick-button:hover,
.quick-button:focus-visible {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.quick-button:focus-visible {
  outline: 2px solid var(--focus-ring);
}

.quick-button:disabled {
  cursor: default;
  opacity: 0.45;
}

.quick-button svg {
  width: 15px;
  height: 15px;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.8;
}

.quick-error {
  grid-column: 1 / -1;
  margin: 1px 0 0;
  color: var(--danger-color);
  font-size: 11px;
  line-height: 1.4;
}

@media (max-width: 860px) {
  .board {
    gap: 9px;
    padding: 10px;
  }
}

@media (max-width: 640px) {
  .board {
    grid-template-columns: minmax(0, 1fr);
    gap: 8px;
    padding: 8px;
    overflow-y: auto;
  }

  .column {
    min-height: auto;
  }

  .card-list {
    overflow: visible;
  }

  .column-header {
    padding-top: 7px;
  }
}

@media (hover: none) {
  .phase-trigger {
    color: var(--text-secondary);
  }
}
</style>
