import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { useCalendarStore, type CalendarView } from './calendar'

export type SyncSource = 'codex' | 'email'
type Sources = Record<CalendarView, Record<SyncSource, boolean>>
const storageKey = 'catendar-sync-sources-v1'
export const sourceViews = ['board', 'month'] as const
export const sourceNames = { codex: 'Codex conversations', email: 'Email' } as const

function readSources(): Sources {
  const sources: Sources = { board: { codex: true, email: false }, month: { codex: true, email: true } }
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) || '{}')
    for (const view of sourceViews) {
      for (const key of ['codex', 'email'] as const) {
        if (typeof saved?.[view]?.[key] === 'boolean') sources[view][key] = saved[view][key]
      }
    }
  } catch { /* Keep the defaults when upgrading an unreadable preference. */ }
  return sources
}

export const useSyncSourcesStore = defineStore('syncSources', () => {
  const calendar = useCalendarStore()
  const sources = ref(readSources())
  const active = computed(() => sources.value[calendar.viewMode])

  function setEnabled(view: CalendarView, key: SyncSource, enabled: boolean) {
    const next = { ...sources.value, [view]: { ...sources.value[view], [key]: enabled } }
    localStorage.setItem(storageKey, JSON.stringify(next))
    sources.value = next
  }

  function allowsRun(key: SyncSource, origin: CalendarView | null) {
    return origin ? sources.value[origin][key] : sourceViews.some(view => sources.value[view][key])
  }

  return { sources, active, setEnabled, allowsRun }
})
