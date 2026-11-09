import { defineStore } from 'pinia'
import { computed, reactive, ref } from 'vue'
import { format } from 'date-fns'
import type { CalendarEvent, EventInput, EventPhase } from '@/types'
import { randomEventColor } from '@/constants/eventColors'
import { eventApi } from '@/services/api'
import { useCalendarStore } from '@/stores/calendar'

export const useEventsStore = defineStore('events', () => {
  const calendar = useCalendarStore()
  const events = ref<CalendarEvent[]>([])
  const nextCreateColor = ref(randomEventColor())
  const loading = ref(false)
  const error = ref<string | null>(null)
  const scope = computed(() => calendar.viewMode === 'board'
    ? `board/${calendar.focusedDate}` : `month/${calendar.currentMonth}`)
  const loadedScope = ref('')
  const moves = reactive(new Map<number, { scope: string; phase: EventPhase }>())
  let latestFetch = 0

  function boardEventsForDay(day: string): CalendarEvent[] {
    return loadedScope.value === `board/${day}` ? events.value : []
  }

  const eventsByDate = computed(() => {
    const map: Record<string, CalendarEvent[]> = {}
    if (calendar.viewMode !== 'month' || loadedScope.value !== scope.value) return map
    for (const event of events.value) {
      const day = event.start.slice(0, 10)
      if (!map[day]) map[day] = []
      map[day].push(event)
    }
    return map
  })

  function detail(cause: unknown, fallback: string) {
    return cause instanceof Error ? cause.message : typeof cause === 'string' && cause ? cause : fallback
  }

  async function fetchEvents() {
    const key = scope.value
    const request = ++latestFetch
    if (loadedScope.value !== key) {
      loadedScope.value = key
      events.value = []
    }
    loading.value = true
    error.value = null
    try {
      const rows = calendar.viewMode === 'board'
        ? await eventApi.getBoardEvents(calendar.focusedDate)
        : await eventApi.getEvents(format(calendar.visibleStart, 'yyyy-MM-dd'), format(calendar.visibleEnd, 'yyyy-MM-dd'))
      if (request === latestFetch && key === scope.value) {
        events.value = rows.map(row => {
          const move = moves.get(row.id)
          return move?.scope === key ? { ...row, phase: move.phase } : row
        })
      }
    } catch (cause) {
      if (request === latestFetch) error.value = detail(cause, 'Could not load the tasks')
    } finally {
      if (request === latestFetch) loading.value = false
    }
  }

  function replaceInView(event: CalendarEvent) {
    const index = events.value.findIndex(item => item.id === event.id)
    if (index >= 0) events.value[index] = event
  }

  async function createEvent(input: EventInput) {
    const key = scope.value
    try {
      const created = await eventApi.createEvent(input)
      nextCreateColor.value = randomEventColor(created.color)
      if (scope.value === key && loadedScope.value === key &&
          (!input.boardDay || (key === `board/${input.boardDay}` && input.start.slice(0, 10) === input.boardDay)) &&
          !events.value.some(item => item.id === created.id)) {
        events.value.push(created)
      }
      void fetchEvents()
      return created
    } catch (cause) {
      error.value = detail(cause, 'Could not create the task')
      throw cause
    }
  }

  async function updateEvent(id: number, input: EventInput) {
    const key = scope.value
    try {
      const updated = await eventApi.updateEvent(id, input)
      if (scope.value === key && loadedScope.value === key &&
          (!input.boardDay || key === `board/${input.boardDay}`)) replaceInView(updated)
      // A reschedule or a change made across midnight can alter membership.
      void fetchEvents()
      return updated
    } catch (cause) {
      error.value = detail(cause, 'Could not update the task')
      throw cause
    }
  }

  async function setEventPhase(id: number, phase: EventPhase, boardDay?: string) {
    const key = scope.value
    if (loadedScope.value !== key || (boardDay && key !== `board/${boardDay}`)) return
    const previous = events.value.find(item => item.id === id)
    if (!previous || previous.phase === phase || moves.has(id)) return previous
    moves.set(id, { scope: key, phase })
    replaceInView({ ...previous, phase })
    try {
      const saved = boardDay
        ? await eventApi.setBoardEventPhase(id, phase, boardDay)
        : await eventApi.setEventPhase(id, phase)
      if (scope.value === key && loadedScope.value === key) replaceInView(saved)
      void fetchEvents()
      return saved
    } catch (cause) {
      if (scope.value === key && loadedScope.value === key) {
        const row = events.value.find(item => item.id === id)
        if (row) replaceInView({ ...row, phase: previous.phase })
      }
      error.value = detail(cause, 'Could not move the task')
      throw cause
    } finally {
      moves.delete(id)
    }
  }

  async function deleteEvent(id: number) {
    try {
      await eventApi.deleteEvent(id)
      events.value = events.value.filter(item => item.id !== id)
      void fetchEvents()
    } catch (cause) {
      error.value = detail(cause, 'Could not delete the task')
      throw cause
    }
  }

  return {
    scope, nextCreateColor, loading, error, eventsByDate, boardEventsForDay,
    fetchEvents, createEvent, updateEvent, setEventPhase, deleteEvent, isMoving: (id: number) => moves.has(id)
  }
})
