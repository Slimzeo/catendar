<script setup lang="ts">
import { useSyncSourcesStore } from '@/stores/syncSources'
import { computed, ref } from 'vue'
import { NButton, NTooltip } from 'naive-ui'
import type { AISyncState } from '@/types'
import type { CalendarView } from '@/stores/calendar'
import { useCalendarStore } from '@/stores/calendar'
import { useThemeStore } from '@/stores/theme'
import { setAlwaysOnTop } from '@/wails'

const props = withDefaults(defineProps<{
  conversationState?: AISyncState
  conversationPending?: number
}>(), {
  conversationState: 'idle',
  conversationPending: 0
})

const sources = useSyncSourcesStore()

const emit = defineEmits<{
  (event: 'create'): void
  (event: 'ai-sync'): void
  (event: 'settings'): void
}>()

const calendarStore = useCalendarStore()
const themeStore = useThemeStore()
const isPinned = ref(false)

const periodLabel = computed(() => (
  calendarStore.viewMode === 'board' ? calendarStore.dayLabel : calendarStore.monthLabel
))
const navigationLabel = computed(() => (
  calendarStore.viewMode === 'board' ? 'Day navigation' : 'Month navigation'
))
const previousLabel = computed(() => (
  calendarStore.viewMode === 'board' ? 'Previous day' : 'Previous month'
))
const nextLabel = computed(() => (
  calendarStore.viewMode === 'board' ? 'Next day' : 'Next month'
))
const isAtToday = computed(() => (
  calendarStore.viewMode === 'board' ? calendarStore.isToday : calendarStore.isCurrentMonth
))
const pendingReviews = computed(() => sources.active.codex ? props.conversationPending : 0)
const aiSyncBusy = computed(() => sources.active.codex && props.conversationState === 'running')
const aiSyncLabel = computed(() => {
  if (!sources.active.codex) return 'AI Sync — open email sync'
  const pending = pendingReviews.value
  if (pending) return `AI Sync — ${pending} conversation ${pending === 1 ? 'change needs' : 'changes need'} review`
  if (aiSyncBusy.value) return 'AI Sync — syncing'
  return 'AI Sync — sync Codex conversations'
})

function switchView(view: CalendarView) {
  calendarStore.setView(view)
}

function togglePin() {
  const nextState = !isPinned.value
  if (setAlwaysOnTop(nextState)) {
    isPinned.value = nextState
  }
}
</script>

<template>
  <header class="toolbar">
    <button class="brand" type="button" title="Go to today" aria-label="Go to today" @click="calendarStore.goToToday">
      <img src="/catendar.png" alt="" />
      <span class="brand-name"><strong>cat</strong>endar</span>
    </button>

    <nav class="calendar-nav" :aria-label="navigationLabel">
      <div class="view-switch" role="group" aria-label="View">
        <button
          type="button"
          :aria-pressed="calendarStore.viewMode === 'board'"
          @click="switchView('board')"
        >
          Board
        </button>
        <button
          type="button"
          :aria-pressed="calendarStore.viewMode === 'month'"
          @click="switchView('month')"
        >
          Month
        </button>
      </div>
      <n-button quaternary circle size="small" :aria-label="previousLabel" @click="calendarStore.prevPeriod">
        <svg viewBox="0 0 20 20" aria-hidden="true"><path d="m12.5 4.5-5 5.5 5 5.5" /></svg>
      </n-button>
      <h1 class="calendar-label" aria-live="polite">{{ periodLabel }}</h1>
      <n-button quaternary circle size="small" :aria-label="nextLabel" @click="calendarStore.nextPeriod">
        <svg viewBox="0 0 20 20" aria-hidden="true"><path d="m7.5 4.5 5 5.5-5 5.5" /></svg>
      </n-button>
      <n-button
        quaternary
        size="small"
        class="today-button"
        :disabled="isAtToday"
        @click="calendarStore.goToToday"
      >
        Today
      </n-button>
    </nav>

    <div class="toolbar-actions">
      <n-button
        v-if="sources.active.codex || sources.active.email"
        secondary
        type="primary"
        size="small"
        class="ai-sync-button"
        :class="{ busy: aiSyncBusy, review: pendingReviews > 0 }"
        :aria-label="aiSyncLabel"
        :title="aiSyncLabel"
        aria-haspopup="dialog"
        @click="emit('ai-sync')"
      >
        <template #icon>
          <svg viewBox="0 0 20 20" aria-hidden="true">
            <path d="m10 2 1.1 4.2L15 8l-3.9 1.8L10 14l-1.1-4.2L5 8l3.9-1.8L10 2ZM15.5 13l.6 2.1L18 16l-1.9.9-.6 2.1-.6-2.1L13 16l1.9-.9.6-2.1Z" />
          </svg>
        </template>
        <span class="ai-sync-label">
          {{ aiSyncBusy ? 'Syncing' : 'AI Sync' }}
        </span>
        <span
          v-if="pendingReviews"
          class="ai-sync-badge"
          aria-hidden="true"
        >{{ pendingReviews > 9 ? '9+' : pendingReviews }}</span>
        <span v-else-if="aiSyncBusy" class="ai-sync-dot" aria-hidden="true" />
      </n-button>

      <n-button
        v-if="calendarStore.viewMode === 'month'"
        type="primary"
        size="small"
        class="new-event-button"
        aria-label="New event"
        @click="emit('create')"
      >
        <template #icon>
          <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M10 4v12M4 10h12" /></svg>
        </template>
        <span class="new-event-label">New event</span>
      </n-button>

      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button
            quaternary
            circle
            size="small"
            :class="{ active: !themeStore.showTime }"
            :aria-label="themeStore.showTime ? 'Hide event times' : 'Show event times'"
            :aria-pressed="!themeStore.showTime"
            @click="themeStore.toggleShowTime"
          >
            <svg v-if="themeStore.showTime" viewBox="0 0 20 20" aria-hidden="true">
              <path d="M2.5 10s2.8-5 7.5-5 7.5 5 7.5 5-2.8 5-7.5 5-7.5-5-7.5-5Z" />
              <circle cx="10" cy="10" r="2.25" />
            </svg>
            <svg v-else viewBox="0 0 20 20" aria-hidden="true">
              <path d="M7 5.7A7.2 7.2 0 0 1 10 5c4.7 0 7.5 5 7.5 5a11.6 11.6 0 0 1-2.1 2.7M12.2 14.7A7.4 7.4 0 0 1 10 15c-4.7 0-7.5-5-7.5-5a11.9 11.9 0 0 1 2-2.6M3 3l14 14" />
            </svg>
          </n-button>
        </template>
        {{ themeStore.showTime ? 'Hide event times' : 'Show event times' }}
      </n-tooltip>

      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button
            quaternary
            circle
            size="small"
            :class="{ active: isPinned }"
            aria-label="Keep window on top"
            :aria-pressed="isPinned"
            @click="togglePin"
          >
            <svg viewBox="0 0 20 20" aria-hidden="true">
              <path d="M7 3.5h6l-.8 4 2.3 2.4v1.4h-9V9.9l2.3-2.4-.8-4ZM10 11.3v5.2" />
            </svg>
          </n-button>
        </template>
        {{ isPinned ? 'Stop keeping on top' : 'Keep window on top' }}
      </n-tooltip>

      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button
            quaternary
            circle
            size="small"
            :aria-label="themeStore.isDark ? 'Use light theme' : 'Use dark theme'"
            @click="themeStore.toggleTheme"
          >
            <svg v-if="themeStore.isDark" viewBox="0 0 20 20" aria-hidden="true">
              <circle cx="10" cy="10" r="3.5" />
              <path d="M10 2v2M10 16v2M2 10h2M16 10h2M4.3 4.3l1.4 1.4M14.3 14.3l1.4 1.4M15.7 4.3l-1.4 1.4M5.7 14.3l-1.4 1.4" />
            </svg>
            <svg v-else viewBox="0 0 20 20" aria-hidden="true">
              <path d="M16.8 12.2A7 7 0 0 1 7.8 3.2 7 7 0 1 0 16.8 12.2Z" />
            </svg>
          </n-button>
        </template>
        {{ themeStore.isDark ? 'Use light theme' : 'Use dark theme' }}
      </n-tooltip>

      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button
            quaternary
            circle
            size="small"
            aria-label="Settings"
            aria-haspopup="dialog"
            @click="emit('settings')"
          >
            <svg viewBox="0 0 20 20" aria-hidden="true">
              <circle cx="10" cy="10" r="2.4" />
              <path d="M16.4 11.2v-2.4l-2-.5a5 5 0 0 0-.5-1.1l1.1-1.8-1.7-1.7-1.8 1.1a5 5 0 0 0-1.1-.5L9.9 2H7.5L7 4.1a5 5 0 0 0-1.1.5L4.1 3.5 2.4 5.2 3.5 7a5 5 0 0 0-.5 1.1l-2 .5V11l2 .5a5 5 0 0 0 .5 1.1l-1.1 1.8 1.7 1.7L6 15a5 5 0 0 0 1.1.5l.5 2.1H10l.5-2.1a5 5 0 0 0 1.1-.5l1.8 1.1 1.7-1.7-1.1-1.8a5 5 0 0 0 .5-1.1l1.9-.3Z" />
            </svg>
          </n-button>
        </template>
        Settings
      </n-tooltip>
    </div>
  </header>
</template>

<style scoped>
.toolbar {
  position: relative;
  z-index: 5;
  display: grid;
  grid-template-columns: minmax(max-content, 1fr) auto minmax(max-content, 1fr);
  min-height: 58px;
  align-items: center;
  gap: 16px;
  padding: 8px 14px;
  border-bottom: 1px solid var(--border-soft);
  background: var(--bg-toolbar);
  box-shadow: 0 1px 0 color-mix(in srgb, var(--bg-elevated) 65%, transparent);
  -webkit-app-region: drag;
}

.brand,
.calendar-nav,
.toolbar-actions {
  -webkit-app-region: no-drag;
}

.brand {
  display: inline-flex;
  width: fit-content;
  align-items: center;
  gap: 9px;
  padding: 3px;
  border: 0;
  border-radius: 10px;
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
}

.brand:hover {
  color: var(--primary-color);
}

.brand:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 2px;
}

.brand img {
  width: 30px;
  height: 30px;
  border-radius: 8px;
  box-shadow: var(--shadow-sm);
}

.brand-name {
  font-family: var(--font-display);
  font-size: 14px;
  font-weight: 500;
  letter-spacing: -0.015em;
}

.brand-name strong {
  color: var(--primary-color);
  font-weight: 750;
}

.calendar-nav {
  display: flex;
  align-items: center;
  gap: 4px;
}

.view-switch {
  display: flex;
  margin-right: 6px;
  padding: 2px;
  border-radius: 9px;
  background: var(--bg-secondary);
}

.view-switch button {
  padding: 3px 11px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}

.view-switch button[aria-pressed='true'] {
  background: var(--bg-elevated);
  box-shadow: var(--shadow-sm);
  color: var(--text-primary);
  font-weight: 600;
}

.view-switch button:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.calendar-label {
  min-width: 156px;
  margin: 0 3px;
  color: var(--text-primary);
  font-family: var(--font-display);
  font-size: 17px;
  font-weight: 650;
  letter-spacing: -0.025em;
  line-height: 1.2;
  text-align: center;
  white-space: nowrap;
}

.toolbar-actions {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
}

.toolbar :deep(.n-button) {
  min-width: 34px;
}

.toolbar :deep(.n-button.active) {
  background: var(--primary-muted);
  color: var(--primary-color);
}

.toolbar svg {
  width: 17px;
  height: 17px;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.65;
}

.new-event-button {
  margin-right: 3px;
}

.ai-sync-button {
  margin-right: 2px;
}

.ai-sync-button :deep(.n-button__content) {
  gap: 5px;
}

.ai-sync-badge {
  display: inline-grid;
  min-width: 15px;
  height: 15px;
  padding: 0 4px;
  place-items: center;
  border-radius: 8px;
  background: var(--danger-color);
  color: #fff;
  font-size: 9px;
  font-weight: 750;
  line-height: 1;
}

.ai-sync-dot {
  width: 6px;
  height: 6px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: currentColor;
  animation: ai-sync-pulse 1.4s ease-in-out infinite;
}

@keyframes ai-sync-pulse {
  50% { opacity: 0.35; }
}

@media (prefers-reduced-motion: reduce) {
  .ai-sync-dot {
    animation: none;
  }
}

@media (max-width: 860px) {
  .toolbar {
    grid-template-columns: auto 1fr auto;
    gap: 8px;
    padding-inline: 10px;
  }

  .brand-name,
  .new-event-label,
  .ai-sync-label {
    display: none;
  }

  .calendar-nav {
    justify-self: center;
  }

  .calendar-label {
    min-width: 132px;
    font-size: 15px;
  }
}

@media (max-width: 720px) {
  .toolbar {
    grid-template-columns: auto 1fr;
    min-height: auto;
    row-gap: 5px;
    padding: 6px;
  }

  .brand img {
    width: 27px;
    height: 27px;
  }

  .toolbar-actions {
    justify-self: end;
  }

  .calendar-nav {
    grid-row: 2;
    grid-column: 1 / -1;
    width: 100%;
    justify-content: center;
    justify-self: stretch;
  }
}

@media (max-width: 420px) {
  .toolbar {
    gap: 3px;
  }

  .toolbar-actions {
    gap: 0;
  }

  .toolbar :deep(.n-button) {
    min-width: 28px;
  }

  .view-switch {
    margin-right: 3px;
    padding: 1px;
  }

  .view-switch button {
    padding: 3px 7px;
    font-size: 12px;
  }

  .calendar-label {
    min-width: 82px;
    margin: 0 2px;
    font-size: 13px;
  }

  .today-button {
    font-size: 12px;
  }
}
</style>
