<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { format, parseISO } from 'date-fns'
import { useMessage } from 'naive-ui'
import type { CalendarEvent, EventInput, EventPhase } from '@/types'
import { eventPhases, isEventPhase } from '@/constants/eventPhases'
import { useCalendarStore } from '@/stores/calendar'
import { useEventsStore } from '@/stores/events'
import { useThemeStore } from '@/stores/theme'
import { sourceEmailUrl } from '@/utils/description'
import EventDescription from '@/components/event/EventDescription.vue'

const emit = defineEmits<{
  (event: 'edit', calendarEvent: CalendarEvent, focusResult?: boolean): void
  (event: 'create-details', phase: EventPhase, title: string): void
  (event: 'open-email', sourceURL: string): void
  (event: 'quick-state', active: boolean): void
}>()

const calendarStore = useCalendarStore()
const eventsStore = useEventsStore()
const themeStore = useThemeStore()
const message = useMessage()

const selectedDate = computed(() => calendarStore.focusedDate)
const dayEvents = computed(() => eventsStore.eventsByDate[selectedDate.value] || [])

const columns = computed(() => eventPhases.map(phase => ({
  phase,
  events: dayEvents.value.filter(event => event.phase === phase)
})))

const quickPhase = ref<EventPhase | null>(null)
const quickTitle = ref('')
const quickError = ref('')
const quickSaving = ref(false)
const quickInput = ref<HTMLInputElement | null>(null)
const addButtons = new Map<EventPhase, HTMLButtonElement>()

function setQuickInput(element: unknown) {
  quickInput.value = element instanceof HTMLInputElement ? element : null
}

function setAddButton(phase: EventPhase, element: unknown) {
  if (element instanceof HTMLButtonElement) addButtons.set(phase, element)
  else addButtons.delete(phase)
}

const draggingId = ref<number | null>(null)
const dragTarget = ref<EventPhase | null>(null)

watch(quickPhase, async phase => {
  emit('quick-state', Boolean(phase))
  if (phase) {
    await nextTick()
    quickInput.value?.focus()
  }
})

watch(selectedDate, () => {
  if (quickPhase.value) closeQuick()
})

onBeforeUnmount(() => emit('quick-state', false))

function openQuick(phase: EventPhase) {
  if (quickSaving.value) return
  if (quickPhase.value === phase) {
    quickInput.value?.focus()
    return
  }
  quickPhase.value = phase
  quickTitle.value = ''
  quickError.value = ''
}

function closeQuick() {
  if (quickSaving.value) return
  quickPhase.value = null
  quickTitle.value = ''
  quickError.value = ''
}

function handleQuickEscape() {
  if (quickSaving.value) return
  const phase = quickPhase.value
  closeQuick()
  if (phase) nextTick(() => addButtons.get(phase)?.focus())
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

  quickSaving.value = true
  try {
    const input: EventInput = {
      title,
      start: `${selectedDate.value}T00:00:00`,
      end: `${selectedDate.value}T00:00:00`,
      color: eventsStore.nextCreateColor,
      allDay: true,
      description: '',
      bold: false,
      phase,
      deadline: ''
    }
    await eventsStore.createEvent(input)
    quickTitle.value = ''
    quickError.value = ''
  } catch (error) {
    const detail = error instanceof Error ? error.message : 'Could not add the task'
    quickError.value = detail
    message.error(detail)
  } finally {
    quickSaving.value = false
    await nextTick()
    if (document.activeElement === document.body) quickInput.value?.focus()
  }
}

function startDrag(event: DragEvent, calendarEvent: CalendarEvent) {
  if (eventsStore.isMoving(calendarEvent.id)) {
    event.preventDefault()
    return
  }
  const target = event.target as HTMLElement | null
  if (target?.closest('a, button, select, input, label')) {
    event.preventDefault()
    return
  }
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
  if (!calendarEvent || calendarEvent.phase === phase) return
  void moveEvent(calendarEvent, phase)
}

function changePhase(calendarEvent: CalendarEvent, event: Event) {
  const select = event.target as HTMLSelectElement
  const phase = select.value
  if (!isEventPhase(phase)) {
    select.value = calendarEvent.phase
    return
  }
  if (phase === calendarEvent.phase) return
  void moveEvent(calendarEvent, phase)
}

async function moveEvent(calendarEvent: CalendarEvent, phase: EventPhase) {
  try {
    await eventsStore.setEventPhase(calendarEvent.id, phase)
  } catch (error) {
    const detail = error instanceof Error ? error.message : 'Could not move the task'
    message.error(detail)
  }
}

function openEvent(calendarEvent: CalendarEvent) {
  emit('edit', calendarEvent)
}

function openResultEditor(calendarEvent: CalendarEvent) {
  emit('edit', calendarEvent, true)
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
  return `Due ${format(date, 'MMM d')}`
}

function isOverdue(event: CalendarEvent): boolean {
  return Boolean(event.deadline && event.phase !== 'done' && event.deadline < selectedDate.value)
}

function mailUrl(event: CalendarEvent): string | undefined {
  return sourceEmailUrl(event.description)
}

function cardDescription(event: CalendarEvent): string {
  const url = mailUrl(event)
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
</script>

<template>
  <main class="board" :aria-label="`Tasks for ${calendarStore.dayLabel}`">
    <section
      v-for="column in columns"
      :key="column.phase"
      class="column"
      :class="{ dropping: dragTarget === column.phase, done: column.phase === 'done' }"
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
            'no-meta': !hasMeta(calendarEvent)
          }"
          :style="{ '--event-color': calendarEvent.color }"
          :draggable="!eventsStore.isMoving(calendarEvent.id)"
          @click="openEvent(calendarEvent)"
          @dragstart="startDrag($event, calendarEvent)"
          @dragend="endDrag"
        >
          <h3 :class="{ bold: calendarEvent.bold }">
            <button type="button" class="edit-title" @click.stop="openEvent(calendarEvent)">{{ calendarEvent.title }}</button>
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
                {{ deadlineLabel(calendarEvent.deadline) }}
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
            <label class="phase-control" @click.stop>
              <span class="visually-hidden">Move {{ calendarEvent.title }} to another phase</span>
              <select
                :value="calendarEvent.phase"
                :disabled="eventsStore.isMoving(calendarEvent.id)"
                @click.stop
                @pointerdown.stop
                @keydown.stop
                @change="changePhase(calendarEvent, $event)"
              >
                <option v-for="phase in eventPhases" :key="phase" :value="phase">
                  {{ phase }}
                </option>
              </select>
            </label>
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
          @keydown.esc.stop.prevent="handleQuickEscape"
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

.phase-control {
  display: inline-flex;
  flex: 0 0 auto;
  margin-left: auto;
  align-items: center;
  color: var(--text-tertiary);
}

.no-meta .card-footer {
  position: absolute;
  top: 5px;
  right: 3px;
  margin-left: 0;
}

.no-meta .phase-control select {
  max-width: 88px;
  padding: 1px 2px;
  font-size: 10.5px;
}

.phase-control select {
  min-width: 0;
  padding: 2px 4px;
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

.phase-control select:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.phase-control select:focus-visible {
  border-color: var(--primary-color);
  color: var(--text-primary);
  box-shadow: 0 0 0 2px var(--focus-ring);
}

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
  border: 0;
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
  .phase-control select {
    color: var(--text-secondary);
  }
}
</style>
