<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { format, parseISO } from 'date-fns'
import { NButton, NCard, NDatePicker, NInput, NModal, NSelect, NSwitch } from 'naive-ui'
import type { CalendarEvent, EventInput, EventPhase } from '@/types'
import { eventPhases } from '@/constants/eventPhases'
import ColorPalettePicker from './ColorPalettePicker.vue'
import EventRegistrations from './EventRegistrations.vue'

const props = withDefaults(defineProps<{
  show: boolean
  event?: CalendarEvent | null
  defaultDate?: string
  defaultTitle?: string
  defaultColor: string
  defaultPhase?: EventPhase
  initialFocus?: 'title' | 'description' | 'result'
  variant?: 'board' | 'calendar'
  saving?: boolean
}>(), {
  event: null,
  defaultDate: '',
  defaultTitle: '',
  defaultPhase: 'todo',
  initialFocus: 'title',
  variant: 'calendar',
  saving: false
})

const emit = defineEmits<{
  (event: 'update:show', value: boolean): void
  (event: 'save', input: EventInput): void
  (event: 'request-delete', item: CalendarEvent): void
}>()

const titleInputRef = ref<{ focus: () => void } | null>(null)
const descriptionInputRef = ref<{ focus: () => void } | null>(null)
const resultInputRef = ref<{ focus: () => void } | null>(null)
const title = ref('')
const description = ref('')
const result = ref('')
const startDate = ref<number | null>(null)
const startTime = ref('09:00')
const endTime = ref('10:00')
const endDate = ref('')
const allDay = ref(true)
const selectedColor = ref(props.defaultColor)
const bold = ref(false)
const phase = ref<EventPhase>('todo')
const deadline = ref('')
const formError = ref('')
const titleError = ref('')
const showCalendarDetails = ref(false)
let initializing = false

const phaseOptions = eventPhases.map(value => ({ label: value, value }))

const isEditing = computed(() => Boolean(props.event?.id))
const isTask = computed(() => props.variant === 'board')
const registrations = computed(() => props.event?.registrations ?? [])
const modalTitle = computed(() => {
  if (isTask.value) return isEditing.value ? 'Edit task' : 'New task'
  return isEditing.value ? 'Edit event' : 'New event'
})
const titlePlaceholder = computed(() => (isTask.value ? 'Task title' : 'Event title'))
const submitLabel = computed(() => {
  if (isTask.value) return isEditing.value ? 'Save task' : 'Create task'
  return isEditing.value ? 'Save changes' : 'Create event'
})

watch(startDate, (value, previousValue) => {
  if (initializing || !value) return
  const previousDate = previousValue ? format(new Date(previousValue), 'yyyy-MM-dd') : ''
  if (!endDate.value || endDate.value === previousDate) {
    endDate.value = format(new Date(value), 'yyyy-MM-dd')
  }
}, { flush: 'sync' })

watch(
  () => props.show,
  show => {
    if (!show) return
    initializeForm()
    nextTick(() => {
      if (props.initialFocus === 'description') {
        descriptionInputRef.value?.focus()
      } else if (props.initialFocus === 'result' && phase.value === 'progressing') {
        resultInputRef.value?.focus()
      } else {
        titleInputRef.value?.focus()
      }
    })
  },
  { immediate: true }
)

function initializeForm() {
  formError.value = ''
  titleError.value = ''
  showCalendarDetails.value = false
  initializing = true

  try {
    if (props.event) {
      const start = parseISO(props.event.start)
      const end = props.event.end ? parseISO(props.event.end) : null
      title.value = props.event.title
      description.value = props.event.description || ''
      result.value = props.event.result || ''
      startDate.value = start.getTime()
      startTime.value = format(start, 'HH:mm')
      endTime.value = end ? format(end, 'HH:mm') : ''
      endDate.value = end ? format(end, 'yyyy-MM-dd') : format(start, 'yyyy-MM-dd')
      allDay.value = props.event.allDay
      selectedColor.value = props.event.color
      bold.value = props.event.bold
      phase.value = props.event.phase || props.defaultPhase
      deadline.value = props.event.deadline || ''
      return
    }

    const date = props.defaultDate ? parseISO(props.defaultDate) : new Date()
    title.value = props.defaultTitle
    description.value = ''
    result.value = ''
    startDate.value = date.getTime()
    startTime.value = '09:00'
    endTime.value = '10:00'
    endDate.value = format(date, 'yyyy-MM-dd')
    allDay.value = true
    selectedColor.value = props.defaultColor
    bold.value = false
    phase.value = props.defaultPhase
    deadline.value = ''
  } finally {
    initializing = false
  }
}

function handleSave() {
  const trimmedTitle = title.value.trim()
  formError.value = ''
  titleError.value = ''
  if (!trimmedTitle) {
    titleError.value = isTask.value
      ? 'Add a task title before saving.'
      : 'Add a title before saving.'
    titleInputRef.value?.focus()
    return
  }
  if (!startDate.value) {
    formError.value = 'Choose a date before saving.'
    showCalendarDetails.value = true
    return
  }
  if (!allDay.value && !startTime.value) {
    formError.value = 'Choose a start time.'
    showCalendarDetails.value = true
    return
  }

  const trimmedDeadline = deadline.value.trim()
  if (trimmedDeadline && !/^\d{4}-\d{2}-\d{2}$/.test(trimmedDeadline)) {
    formError.value = 'Use YYYY-MM-DD for the deadline.'
    return
  }

  const date = format(new Date(startDate.value), 'yyyy-MM-dd')
  const start = allDay.value ? `${date}T00:00:00` : `${date}T${startTime.value}:00`
  let end = start

  if (allDay.value) {
    const endDateValue = endDate.value && endDate.value >= date ? endDate.value : date
    end = `${endDateValue}T00:00:00`
  } else {
    const endDateValue = endDate.value && endDate.value > date ? endDate.value : date
    const endTimeValue = endTime.value || startTime.value
    end = `${endDateValue}T${endTimeValue}:00`
    if (end < start) {
      formError.value = 'End must not be earlier than start.'
      showCalendarDetails.value = true
      return
    }
  }

  const resultValue = result.value.trim()
  const input: EventInput = {
    title: trimmedTitle,
    description: description.value.trim(),
    start,
    end,
    allDay: allDay.value,
    color: selectedColor.value,
    bold: bold.value
  }
  if (phase.value !== props.event?.phase) input.phase = phase.value
  if (trimmedDeadline !== (props.event?.deadline || '')) input.deadline = trimmedDeadline
  if (resultValue !== (props.event?.result || '').trim()) input.result = resultValue
  emit('save', input)
}

</script>

<template>
  <n-modal :show="show" :mask-closable="!saving" :close-on-esc="!saving" @update:show="value => emit('update:show', value)">
    <n-card
      class="event-card"
      :class="{ 'task-panel': isTask }"
      :title="modalTitle"
      :bordered="false"
      closable
      :closable-disabled="saving"
      @close="emit('update:show', false)"
    >
      <form class="event-form" @submit.prevent="handleSave">
        <div class="form-group">
          <div class="label-row">
            <label for="event-title">Title</label>
            <button
              type="button"
              class="bold-button"
              :class="{ active: bold }"
              :aria-pressed="bold"
              title="Emphasize title"
              @click="bold = !bold"
            >
              B
            </button>
          </div>
          <n-input
            ref="titleInputRef"
            v-model:value="title"
            :input-props="{
              id: 'event-title',
              'aria-invalid': titleError ? 'true' : undefined,
              'aria-describedby': titleError ? 'event-title-error' : undefined
            }"
            :placeholder="titlePlaceholder"
            size="large"
            :disabled="saving"
            @input="titleError = ''; formError = ''"
          />
          <p v-if="titleError" id="event-title-error" class="field-error" role="alert">
            {{ titleError }}
          </p>
        </div>

        <div class="form-group description-field">
          <label for="event-description">
            Description
            <span class="optional">{{ isTask ? 'Optional' : 'Links are clickable' }}</span>
          </label>
          <n-input
            ref="descriptionInputRef"
            v-model:value="description"
            type="textarea"
            :input-props="{ id: 'event-description' }"
            placeholder="Notes, location, or meeting link"
            :rows="3"
            :disabled="saving"
          />
        </div>

        <div v-if="phase === 'progressing'" class="form-group result-field">
          <label for="event-result">
            Result
            <span class="optional">Optional</span>
          </label>
          <n-input
            ref="resultInputRef"
            v-model:value="result"
            type="textarea"
            :input-props="{ id: 'event-result' }"
            placeholder="Outcome, progress, or next step"
            :rows="3"
            :disabled="saving"
            @input="formError = ''"
          />
        </div>

        <div v-else-if="phase === 'done' && result.trim()" class="form-group result-field">
          <label>Result</label>
          <div class="result-readonly">{{ result }}</div>
        </div>

        <div v-if="isEditing && registrations.length" class="form-group registrations-field">
          <label>Registrations <span class="optional">{{ registrations.length }}</span></label>
          <div class="registrations-box">
            <event-registrations :registrations="registrations" :context="title" />
          </div>
        </div>

        <div class="form-row phase-row">
          <div class="form-group deadline-field">
            <label for="event-deadline">{{ registrations.length ? 'Task deadline' : 'Deadline' }} <span class="optional">Optional</span></label>
            <div class="deadline-control">
              <input
                id="event-deadline"
                v-model="deadline"
                type="date"
                class="time-input date-input"
                aria-label="Deadline"
                :disabled="saving"
              />
              <button
                v-if="deadline"
                type="button"
                class="clear-deadline"
                :disabled="saving"
                aria-label="Clear deadline"
                @click="deadline = ''"
              >
                Clear
              </button>
            </div>
          </div>
          <div class="form-group phase-field">
            <label>Phase</label>
            <n-select
              v-model:value="phase"
              :options="phaseOptions"
              :disabled="saving"
              aria-label="Phase"
            />
          </div>
        </div>

        <div class="calendar-details">
          <button
            v-if="isTask"
            type="button"
            class="details-toggle"
            :aria-expanded="showCalendarDetails"
            aria-controls="event-calendar-details"
            @click="showCalendarDetails = !showCalendarDetails"
          >
            <svg viewBox="0 0 20 20" aria-hidden="true"><path d="m7.5 4.5 5 5.5-5 5.5" /></svg>
            Calendar details
          </button>

          <div
            v-show="!isTask || showCalendarDetails"
            id="event-calendar-details"
            class="calendar-details-body"
          >
            <div class="form-row date-row">
              <div class="form-group date-field">
                <label>Date</label>
                <n-date-picker
                  v-model:value="startDate"
                  type="date"
                  :disabled="saving"
                  style="width: 100%"
                />
              </div>
              <label class="all-day-control">
                <span>
                  <strong>All day</strong>
                  <small>Hide start and end times</small>
                </span>
                <n-switch v-model:value="allDay" :disabled="saving" />
              </label>
            </div>

            <div v-if="!allDay" class="form-row time-row">
              <div class="form-group">
                <label for="event-start-time">Start time</label>
                <input id="event-start-time" v-model="startTime" type="time" class="time-input" :disabled="saving" />
              </div>
              <div class="form-group">
                <label for="event-end-time">End time <span class="optional">Optional</span></label>
                <input id="event-end-time" v-model="endTime" type="time" class="time-input" :disabled="saving" />
              </div>
              <div class="form-group">
                <label for="event-end-date">End date</label>
                <input id="event-end-date" v-model="endDate" type="date" class="time-input date-input" :disabled="saving" />
              </div>
            </div>

            <div class="form-group color-field">
              <label>Color</label>
              <color-palette-picker v-model="selectedColor" />
            </div>
          </div>
        </div>

        <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>

        <footer class="form-actions">
          <n-button v-if="event" type="error" ghost :disabled="saving" @click="emit('request-delete', event)">Delete</n-button>
          <span v-else />

          <div class="primary-actions">
            <n-button :disabled="saving" @click="emit('update:show', false)">Cancel</n-button>
            <n-button type="primary" attr-type="submit" :loading="saving">
              {{ submitLabel }}
            </n-button>
          </div>
        </footer>
      </form>
    </n-card>
  </n-modal>
</template>

<style scoped>
.event-card {
  width: min(520px, calc(100vw - 28px));
  max-height: calc(100vh - 24px);
  overflow-y: auto;
}

.task-panel {
  position: fixed;
  top: 12px;
  right: 12px;
  bottom: 12px;
  display: flex;
  width: min(440px, calc(100vw - 24px));
  max-height: none;
  flex-direction: column;
  overflow: hidden;
}

.event-card :deep(.n-card-header) {
  flex: 0 0 auto;
  padding: 14px 18px 10px;
}

.event-card :deep(.n-card__content) {
  min-height: 0;
  padding: 0 18px 18px;
  flex: 1 1 auto;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.event-form {
  display: flex;
  flex-direction: column;
}

.form-group {
  min-width: 0;
  margin-bottom: 17px;
}

.form-group > label,
.label-row label {
  display: block;
  margin-bottom: 7px;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 650;
}

.optional {
  margin-left: 4px;
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 500;
}

.label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.label-row label {
  margin-bottom: 0;
}

.bold-button {
  display: grid;
  width: 28px;
  height: 28px;
  padding: 0;
  place-items: center;
  border: 1px solid var(--border-strong);
  border-radius: 7px;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: 12px;
  font-weight: 800;
  cursor: pointer;
}

.bold-button:hover,
.bold-button.active {
  border-color: var(--primary-color);
  background: var(--primary-muted);
  color: var(--primary-color);
}

.bold-button:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 2px;
}

.form-row {
  display: flex;
  gap: 14px;
}

.form-row .form-group {
  flex: 1;
}

.phase-row {
  align-items: flex-end;
}

.phase-field {
  flex: 0 0 150px !important;
}

.deadline-control {
  display: flex;
  align-items: center;
  gap: 7px;
}

.deadline-control .date-input {
  flex: 1;
}

.clear-deadline {
  flex: 0 0 auto;
  padding: 5px 8px;
  border: 1px solid var(--border-soft);
  border-radius: 7px;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}

.clear-deadline:hover,
.clear-deadline:focus-visible {
  border-color: var(--primary-color);
  background: var(--primary-muted);
  color: var(--primary-color);
}

.clear-deadline:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.date-row {
  align-items: center;
}

.date-field {
  flex: 1.2 !important;
}

.all-day-control {
  display: flex;
  flex: 1;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 4px 0 17px;
  padding: 8px 10px;
  border: 1px solid var(--border-soft);
  border-radius: 9px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  cursor: pointer;
}

.all-day-control span {
  display: flex;
  min-width: 0;
  flex-direction: column;
}

.all-day-control strong {
  font-size: 12px;
  font-weight: 650;
}

.all-day-control small {
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.time-input {
  width: 100%;
  height: 34px;
  padding: 0 10px;
  border: 1px solid var(--border-strong);
  border-radius: 8px;
  outline: none;
  background: var(--bg-elevated);
  color: var(--text-primary);
  color-scheme: light;
  font: inherit;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}

[data-theme='dark'] .time-input {
  color-scheme: dark;
}

.time-input:hover {
  border-color: var(--primary-color);
}

.time-input:focus-visible {
  border-color: var(--primary-color);
  box-shadow: 0 0 0 3px var(--focus-ring);
}

.description-field {
  margin-bottom: 17px;
}

.registrations-field {
  margin-bottom: 17px;
}

.registrations-box {
  --registration-font-size: 12px;
  padding: 7px 9px;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
  background: var(--bg-secondary);
}

.result-readonly {
  padding: 8px 10px;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
  background: var(--bg-secondary);
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.field-error {
  margin: 6px 0 0;
  color: var(--danger-color);
  font-size: 12px;
  line-height: 1.45;
}

.calendar-details {
  margin-bottom: 14px;
  padding-top: 13px;
  border-top: 1px solid var(--border-soft);
}

.details-toggle {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 6px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: 12px;
  font-weight: 650;
  text-align: left;
  cursor: pointer;
}

.details-toggle:hover,
.details-toggle:focus-visible {
  color: var(--text-primary);
}

.details-toggle:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 2px;
}

.details-toggle svg {
  width: 14px;
  height: 14px;
  flex: 0 0 auto;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.8;
  transition: transform 140ms ease;
}

.details-toggle[aria-expanded='true'] svg {
  transform: rotate(90deg);
}

.calendar-details-body {
  padding-top: 12px;
}

.color-field {
  margin-bottom: 0;
}

.form-error {
  margin: 0 0 10px;
  color: var(--danger-color);
  font-size: 12px;
  line-height: 1.45;
}

.form-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-top: 8px;
  padding-top: 16px;
  border-top: 1px solid var(--border-soft);
}

.primary-actions {
  display: flex;
  gap: 8px;
}

@media (max-width: 640px) {
  .task-panel {
    inset: 0;
    width: 100%;
    max-width: none;
    height: 100%;
    margin: 0;
    border-radius: 0;
    box-shadow: none;
  }

  .event-card :deep(.n-card-header) {
    padding: 12px 16px 8px;
  }

  .event-card :deep(.n-card__content) {
    padding: 0 16px 16px;
  }
}

@media (max-width: 520px) {
  .form-row {
    flex-direction: column;
    gap: 0;
  }

  .phase-field {
    flex: 1 !important;
  }

  .all-day-control {
    margin-top: 0;
  }

  .form-actions {
    align-items: stretch;
    flex-direction: column-reverse;
  }

  .primary-actions {
    display: grid;
    grid-template-columns: 1fr 1fr;
  }
}
</style>
