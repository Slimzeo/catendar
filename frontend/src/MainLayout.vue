<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { format } from 'date-fns'
import { NButton, useMessage } from 'naive-ui'
import MonthlyCalendar from './components/calendar/MonthlyCalendar.vue'
import AppToolbar from './components/common/AppToolbar.vue'
import DailyBoard from './components/board/DailyBoard.vue'
import DayEventsDrawer from './components/event/DayEventsDrawer.vue'
import DeleteEventDialog from './components/event/DeleteEventDialog.vue'
import { isPlainEscape, type ShortcutAction } from './shortcuts'
import { useShortcutKeys } from './composables/useShortcutKeys'
import { useCalendarStore, type CalendarView } from './stores/calendar'
import { useConversationSyncStore } from './stores/conversationSync'
import { useEventsStore } from './stores/events'
import { useSyncSourcesStore } from './stores/syncSources'
import { aiSyncApi } from './services/api'
import type { AISyncStatus, CalendarEvent, EmailMessage, EventInput, EventPhase } from './types'

const loadEventModal = () => import('./components/event/EventModal.vue')
const loadSettingsModal = () => import('./components/settings/SettingsModal.vue')
const loadEmailSourceModal = () => import('./components/ai-sync/EmailSourceModal.vue')
const EventModal = defineAsyncComponent(loadEventModal)
const SettingsModal = defineAsyncComponent(loadSettingsModal)
const EmailSourceModal = defineAsyncComponent(loadEmailSourceModal)

const calendarStore = useCalendarStore()
const sources = useSyncSourcesStore()
const eventsStore = useEventsStore()
const conversationStore = useConversationSyncStore()
const message = useMessage()

const showEventModal = ref(false)
const editingEvent = ref<CalendarEvent | null>(null)
const defaultDate = ref('')
const defaultTitle = ref('')
const defaultPhase = ref<EventPhase>('todo')
const modalVariant = ref<'board' | 'calendar'>('calendar')
const modalBoardDay = ref('')
const savingEvent = ref(false)
const initialField = ref<'title' | 'description' | 'result'>('title')
const board = ref<InstanceType<typeof DailyBoard> | null>(null)
const boardQuickActive = ref(false)
const pendingDelete = ref<CalendarEvent | null>(null)

const showSettings = ref(false)
const settingsModal = ref<InstanceType<typeof SettingsModal> | null>(null)
const settingsTab = ref<'shortcuts' | 'ai-sync'>('shortcuts')
const showAISyncStatus = ref(false)
const aiSyncStatus = ref<AISyncStatus>({
  state: 'idle',
  emailCount: 0,
  readEmails: 0,
  unreadableEmails: 0,
  created: 0,
  skipped: 0,
  rejected: 0,
  conflicts: 0
})
let aiSyncPoller: ReturnType<typeof setInterval> | null = null
let modalPreloadTimer: ReturnType<typeof setTimeout> | null = null
const emailStarting = ref(false)
const emailRunOrigin = ref<CalendarView | null>(null)
let emailStatusRequest = 0
let emailPreviewRequest = 0
const isAISyncing = computed(() => aiSyncStatus.value.state === 'running')
const showEmailSource = ref(false)
const emailSourceLoading = ref(false)
const sourceEmail = ref<EmailMessage | null>(null)
const aiSyncCounts = computed(() => {
  const status = aiSyncStatus.value
  const parts = [`${status.created} created`, `${status.skipped} skipped`]
  if (status.rejected) parts.push(`${status.rejected} rejected`)
  if (status.conflicts) parts.push(`${status.conflicts} conflicts`)
  if (status.unreadableEmails) parts.push(`${status.unreadableEmails} unreadable`)
  return parts.join(' · ')
})

const showDayDrawer = ref(false)
const selectedDayDate = ref('')
const drawerSide = ref<'left' | 'right'>('left')

const selectedDayEvents = computed(() => {
  if (!selectedDayDate.value) return []
  return eventsStore.eventsByDate[selectedDayDate.value] || []
})

function openDayDrawer(date: string) {
  selectedDayDate.value = date
  showDayDrawer.value = true
}

function openBoardFromDrawer(date: string) {
  showDayDrawer.value = false
  calendarStore.goToDate(date)
  calendarStore.setView('board')
}

function openSettings(tab: 'shortcuts' | 'ai-sync') {
  settingsTab.value = tab
  showSettings.value = true
  if (tab === 'ai-sync' && sources.active.codex) void conversationStore.refresh({ activities: true })
}

async function openAISync() {
  if (!sources.active.codex && !sources.active.email) return
  openSettings('ai-sync')
  // The toolbar is the explicit manual trigger. A click during a run only
  // reveals the current status; it never queues a second run.
  if (!sources.active.codex || conversationStore.running || conversationStore.starting) return
  await conversationStore.start()
}

function defaultCreateDate() {
  return calendarStore.viewMode === 'board'
    ? calendarStore.focusedDate
    : format(new Date(), 'yyyy-MM-dd')
}

function openCreateModal(date = defaultCreateDate(), phase: EventPhase = 'todo', title = '') {
  showDayDrawer.value = false
  editingEvent.value = null
  defaultDate.value = date
  defaultTitle.value = title
  defaultPhase.value = phase
  initialField.value = 'title'
  modalVariant.value = calendarStore.viewMode === 'board' ? 'board' : 'calendar'
  modalBoardDay.value = modalVariant.value === 'board' ? date : ''
  showEventModal.value = true
}

function openCreateDetails(phase: EventPhase, title: string) {
  openCreateModal(calendarStore.focusedDate, phase, title)
}

function openEditModal(event: CalendarEvent, focus: 'title' | 'description' | 'result' = 'title') {
  showDayDrawer.value = false
  editingEvent.value = event
  defaultDate.value = ''
  defaultTitle.value = ''
  initialField.value = focus
  modalVariant.value = calendarStore.viewMode === 'board' ? 'board' : 'calendar'
  modalBoardDay.value = modalVariant.value === 'board' ? calendarStore.focusedDate : ''
  showEventModal.value = true
}

async function handleSave(input: EventInput) {
  if (savingEvent.value) return
  savingEvent.value = true
  const task = modalVariant.value === 'board'
  const saved: EventInput = task && modalBoardDay.value
    ? { ...input, boardDay: modalBoardDay.value }
    : input
  try {
    if (editingEvent.value?.id) {
      await eventsStore.updateEvent(editingEvent.value.id, saved)
      message.success(task ? 'Task saved' : 'Changes saved')
    } else {
      await eventsStore.createEvent(saved)
      message.success(task ? 'Task created' : 'Event created')
    }
    showEventModal.value = false
  } catch {
    message.error(eventsStore.error || (task ? 'Could not save the task' : 'Could not save the event'))
  } finally {
    savingEvent.value = false
  }
}

function requestDelete(item: CalendarEvent) {
  if (!savingEvent.value && !pendingDelete.value) pendingDelete.value = item
}

function cancelDelete() {
  if (!savingEvent.value) pendingDelete.value = null
}

async function confirmDelete() {
  if (savingEvent.value || !pendingDelete.value) return
  const id = pendingDelete.value.id
  savingEvent.value = true
  const task = calendarStore.viewMode === 'board'
  try {
    await eventsStore.deleteEvent(id)
    message.success(task ? 'Task deleted' : 'Event deleted')
    pendingDelete.value = null
    showEventModal.value = false
  } catch {
    message.error(eventsStore.error || (task ? 'Could not delete the task' : 'Could not delete the event'))
  } finally {
    savingEvent.value = false
  }
}

async function startAISync() {
  if (!sources.active.email || emailStarting.value || isAISyncing.value) return
  emailStarting.value = true
  emailRunOrigin.value = calendarStore.viewMode
  emailStatusRequest++
  showAISyncStatus.value = true
  try {
    aiSyncStatus.value = await aiSyncApi.start()
    startAISyncPolling()
  } catch (error) {
    const detail = errorMessage(error, 'Could not start email sync')
    message.error(detail)
    showAISyncStatus.value = false
    if (sources.active.email && /not configured|not available|not installed/i.test(detail)) {
      openSettings('ai-sync')
    }
  } finally {
    emailStarting.value = false
  }
}

async function cancelAISync() {
  try {
    await aiSyncApi.cancel()
  } catch (error) {
    message.error(errorMessage(error, 'Could not cancel email sync'))
  }
}

async function openSourceEmail(sourceURL: string) {
  if (!sources.active.email) return
  const request = ++emailPreviewRequest
  showEmailSource.value = true
  emailSourceLoading.value = true
  sourceEmail.value = null
  try {
    const email = await aiSyncApi.getSourceEmail(sourceURL)
    if (request === emailPreviewRequest) sourceEmail.value = email
  } catch (error) {
    if (request !== emailPreviewRequest) return
    showEmailSource.value = false
    message.error(errorMessage(error, 'Could not read source email'))
  } finally {
    if (request === emailPreviewRequest) emailSourceLoading.value = false
  }
}

function startAISyncPolling() {
  stopAISyncPolling()
  aiSyncPoller = setInterval(() => void pollAISyncStatus(), 650)
}

function stopAISyncPolling() {
  if (aiSyncPoller) clearInterval(aiSyncPoller)
  aiSyncPoller = null
}

async function pollAISyncStatus() {
  if (emailStarting.value) return
  const request = ++emailStatusRequest
  try {
    const previousState = aiSyncStatus.value.state
    const status = await aiSyncApi.status()
    if (request !== emailStatusRequest) return
    aiSyncStatus.value = status
    if (status.state === 'running') {
      showAISyncStatus.value = true
      if (!aiSyncPoller) startAISyncPolling()
      return
    }
    stopAISyncPolling()
    if (previousState !== 'running') return
    if (status.state === 'succeeded') {
      await eventsStore.fetchEvents()
      if (sources.active.email) message.success(`Email sync complete · ${aiSyncCounts.value}`)
    } else if (status.state === 'cancelled') {
      if (sources.active.email) message.info('Email sync cancelled')
    } else if (status.state === 'failed') {
      if (sources.active.email) message.error(status.message || 'Email sync failed')
    }
  } catch (error) {
    stopAISyncPolling()
    if (sources.active.email) message.error(errorMessage(error, 'Could not read email sync status'))
  }
}

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error) return error.message
  if (typeof error === 'string' && error) return error
  return fallback
}

const editingOrDeleting = computed(() => showEventModal.value || !!pendingDelete.value)
const otherDialogOpen = computed(() => showSettings.value || showEmailSource.value || showDayDrawer.value)
const repeatable = new Set<ShortcutAction>(['taskUp', 'taskDown', 'columnLeft', 'columnRight', 'previousDate', 'nextDate'])

function wrapBoardTab(event: KeyboardEvent) {
  if (event.shiftKey || event.altKey || event.ctrlKey || event.metaKey || !(event.target instanceof HTMLElement)) return
  const controls = event.target.closest('.board')?.querySelectorAll('button:enabled, input:enabled, a[href]')
  if (!controls || controls[controls.length - 1] !== event.target) return
  // Leaving the webview would lose the rest of a held Tab chord on the last card or empty column.
  const toolbar = (event.currentTarget as HTMLElement).querySelector<HTMLButtonElement>('.toolbar button')
  if (toolbar) {
    event.preventDefault()
    toolbar.focus()
  }
}

function handleShortcut(action: ShortcutAction, event: KeyboardEvent, tabOrigin: EventTarget | null): boolean {
  if (otherDialogOpen.value) {
    if (action !== 'dismiss' || isPlainEscape(event, !!tabOrigin)) return false
    if (showSettings.value) return settingsModal.value?.dismiss() ?? false
    if (showEmailSource.value) showEmailSource.value = false
    else showDayDrawer.value = false
    return true
  }
  if (pendingDelete.value) {
    if (action === 'dismiss') cancelDelete()
    else if (action === 'confirmDelete') {
      if (!event.repeat) void confirmDelete()
    } else return false
    return true
  }
  if (showEventModal.value) {
    // Let the editor's focus trap handle native Escape, including open pickers.
    if (action !== 'dismiss' || isPlainEscape(event, !!tabOrigin)) return false
    if (!savingEvent.value) showEventModal.value = false
    return true
  }
  const target = tabOrigin ?? event.target
  if (boardQuickActive.value && target instanceof Element && target.closest('.quick-add')) {
    if (action !== 'dismiss') return false
    board.value?.cancelQuickAdd()
    return true
  }

  if (target instanceof Element && target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"], [role="combobox"], [role="listbox"]')) {
    return false
  }
  if (event.repeat && !repeatable.has(action)) return true
  switch (action) {
    case 'previousDate':
      calendarStore.prevPeriod()
      break
    case 'nextDate':
      calendarStore.nextPeriod()
      break
    case 'today':
      calendarStore.goToToday()
      break
    case 'newEvent':
      openCreateModal()
      break
    case 'confirmDelete':
      return !!board.value
    default:
      if (!tabOrigin && target instanceof Element && target.closest('article[data-task-id]')) {
        board.value?.syncSelectionFromFocus(target)
      }
      return board.value?.runShortcut(action) ?? false
  }
  return true
}

useShortcutKeys(handleShortcut, target => {
  if (!editingOrDeleting.value && !otherDialogOpen.value && !target?.closest('.quick-add')) {
    board.value?.syncSelectionFromFocus(target)
  }
})

watch(editingOrDeleting, async open => {
  if (open) return
  await nextTick()
  if (!otherDialogOpen.value && !boardQuickActive.value) board.value?.focusSelection()
}, { flush: 'post' })

watch([() => eventsStore.scope, () => calendarStore.today.getTime()], () => void eventsStore.fetchEvents())

// Page changes may refresh local status. A sync only starts from an explicit action.
watch(() => sources.active.codex, enabled => {
  if (enabled) void conversationStore.refresh({ silent: true })
}, { immediate: true })

watch(() => sources.active.email, enabled => {
  if (enabled) void pollAISyncStatus()
  else {
    emailPreviewRequest++
    emailSourceLoading.value = false
    showEmailSource.value = false
    sourceEmail.value = null
  }
}, { immediate: true })

watch([isAISyncing, () => sources.allowsRun('email', emailRunOrigin.value)], ([active, allowed]) => {
  if (active && !allowed) void cancelAISync()
})

onMounted(() => {
  eventsStore.fetchEvents()
  requestAnimationFrame(() => {
    void loadEventModal()
  })
  modalPreloadTimer = setTimeout(() => {
    void loadSettingsModal()
    void loadEmailSourceModal()
  }, 300)
})

onUnmounted(() => {
  stopAISyncPolling()
  conversationStore.stopPolling()
  if (modalPreloadTimer) clearTimeout(modalPreloadTimer)
})
</script>

<template>
  <div class="app-container" @keydown.tab="wrapBoardTab">
    <app-toolbar
      :conversation-state="conversationStore.status.state"
      :conversation-pending="conversationStore.pendingCount"
      @create="openCreateModal()"
      @ai-sync="openAISync"
      @settings="openSettings('shortcuts')"
    />

    <transition name="sync-status">
      <div
        v-if="sources.active.email && showAISyncStatus && aiSyncStatus.state !== 'idle' && !showSettings"
        class="ai-sync-status"
        :class="aiSyncStatus.state"
        role="status"
      >
        <span class="sync-indicator" aria-hidden="true" />
        <div class="sync-copy">
          <strong>{{ aiSyncStatus.message || 'Email sync' }}</strong>
          <span v-if="aiSyncStatus.state === 'running' && aiSyncStatus.emailCount">
            {{ aiSyncStatus.emailCount }} recent emails
            <span v-if="aiSyncStatus.readEmails"> · {{ aiSyncStatus.readEmails }} bodies read</span>
            <span v-if="aiSyncStatus.unreadableEmails"> · {{ aiSyncStatus.unreadableEmails }} unreadable</span>
          </span>
          <span v-else-if="aiSyncStatus.state === 'succeeded'">{{ aiSyncCounts }}</span>
        </div>
        <n-button v-if="isAISyncing" text size="small" @click="cancelAISync">Cancel</n-button>
        <n-button v-else-if="aiSyncStatus.state === 'failed'" text size="small" @click="openSettings('ai-sync')">
          Settings
        </n-button>
        <n-button v-if="!isAISyncing" text size="small" aria-label="Dismiss" @click="showAISyncStatus = false">×</n-button>
      </div>
    </transition>

    <div class="calendar-area">
      <div
        v-if="eventsStore.loading"
        class="loading-line"
        role="progressbar"
        aria-label="Loading events"
      />

      <transition name="status">
        <div v-if="eventsStore.error" class="error-banner" role="alert">
          <span>{{ eventsStore.error }}</span>
          <n-button text size="small" @click="eventsStore.fetchEvents">Retry</n-button>
        </div>
      </transition>

      <daily-board
        v-if="calendarStore.viewMode === 'board'"
        ref="board"
        @edit="openEditModal"
        @request-delete="requestDelete"
        @create-details="openCreateDetails"
        @open-email="openSourceEmail"
        @quick-state="boardQuickActive = $event"
      />
      <monthly-calendar
        v-else
        @show-day="openDayDrawer"
        @create="openCreateModal"
        @edit="openEditModal"
      />
    </div>

    <day-events-drawer
      v-model:show="showDayDrawer"
      :date="selectedDayDate"
      :events="selectedDayEvents"
      :drawer-side="drawerSide"
      @create="openCreateModal"
      @edit="openEditModal"
      @toggle-side="drawerSide = drawerSide === 'left' ? 'right' : 'left'"
      @open-email="openSourceEmail"
      @open-board="openBoardFromDrawer"
    />

    <event-modal
      v-if="showEventModal"
      v-model:show="showEventModal"
      :event="editingEvent"
      :default-date="defaultDate"
      :default-title="defaultTitle"
      :default-color="eventsStore.nextCreateColor"
      :default-phase="defaultPhase"
      :initial-focus="initialField"
      :variant="modalVariant"
      :saving="savingEvent"
      @save="handleSave"
      @request-delete="requestDelete"
    />

    <delete-event-dialog
      v-if="pendingDelete"
      :title="pendingDelete.title"
      :kind="calendarStore.viewMode === 'board' ? 'task' : 'event'"
      :busy="savingEvent"
      @cancel="cancelDelete"
      @confirm="confirmDelete"
    />

    <SettingsModal
      ref="settingsModal"
      v-model:show="showSettings"
      :initial-tab="settingsTab"
      :syncing="isAISyncing"
      :email-status="aiSyncStatus"
      @saved="showAISyncStatus = false"
      @email-sync="startAISync"
      @cancel-email-sync="cancelAISync"
    />

    <EmailSourceModal
      v-model:show="showEmailSource"
      :loading="emailSourceLoading"
      :message="sourceEmail"
    />
  </div>
</template>

<style scoped>
.app-container {
  display: flex;
  width: 100%;
  height: 100vh;
  min-width: 0;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.calendar-area {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 0;
  flex-direction: column;
}

.ai-sync-status {
  display: grid;
  z-index: 4;
  grid-template-columns: 9px minmax(0, 1fr) auto auto;
  min-height: 40px;
  align-items: center;
  gap: 10px;
  padding: 6px 13px;
  border-bottom: 1px solid var(--border-soft);
  background: color-mix(in srgb, var(--primary-muted) 66%, var(--bg-toolbar));
  color: var(--text-secondary);
}

.sync-indicator {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--primary-color);
  box-shadow: 0 0 0 4px var(--primary-muted);
}

.ai-sync-status.running .sync-indicator {
  animation: sync-pulse 1.2s ease-in-out infinite;
}

.ai-sync-status.succeeded .sync-indicator {
  background: #4a9a6b;
}

.ai-sync-status.failed .sync-indicator,
.ai-sync-status.cancelled .sync-indicator {
  background: var(--danger-color);
}

.sync-copy {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: 9px;
}

.sync-copy strong {
  overflow: hidden;
  color: var(--text-primary);
  font-size: 11px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sync-copy span {
  color: var(--text-tertiary);
  font-size: 10px;
  white-space: nowrap;
}

.sync-status-enter-active,
.sync-status-leave-active {
  transition: opacity 160ms ease, transform 180ms cubic-bezier(0.22, 1, 0.36, 1);
}

.sync-status-enter-from,
.sync-status-leave-to {
  opacity: 0;
  transform: translateY(-7px);
}

.loading-line {
  position: absolute;
  inset: 0 0 auto;
  z-index: 6;
  height: 2px;
  overflow: hidden;
  background: var(--primary-muted);
}

.loading-line::after {
  position: absolute;
  width: 32%;
  height: 100%;
  background: var(--primary-color);
  content: '';
  animation: loading 1s ease-in-out infinite;
}

.error-banner {
  position: absolute;
  top: 10px;
  left: 50%;
  z-index: 6;
  display: flex;
  max-width: min(520px, calc(100% - 24px));
  align-items: center;
  gap: 14px;
  padding: 8px 10px 8px 13px;
  border: 1px solid color-mix(in srgb, var(--danger-color) 32%, var(--border-soft));
  border-radius: 9px;
  background: var(--bg-elevated);
  box-shadow: var(--shadow-md);
  color: var(--danger-color);
  font-size: 12px;
  transform: translateX(-50%);
}

.error-banner span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.status-enter-active,
.status-leave-active {
  transition: opacity 160ms ease, transform 160ms ease;
}

.status-enter-from,
.status-leave-to {
  opacity: 0;
  transform: translate(-50%, -6px);
}

@keyframes loading {
  from { transform: translateX(-110%); }
  to { transform: translateX(330%); }
}

@keyframes sync-pulse {
  50% {
    box-shadow: 0 0 0 7px color-mix(in srgb, var(--primary-color) 3%, transparent);
    opacity: 0.72;
  }
}

@media (max-width: 560px) {
  .sync-copy span {
    display: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .loading-line::after {
    animation: none;
    transform: none;
  }
}
</style>
