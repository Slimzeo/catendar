import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { format } from 'date-fns'
import type { CalendarEvent, EventInput, EventPhase } from '@/types'
import { eventApi } from '@/services/api'
import { useCalendarStore } from './calendar'

export const useEventsStore = defineStore('events', () => {
  const events = ref<CalendarEvent[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const loadedStart = ref('')
  const loadedEnd = ref('')
  const movingIds = ref<number[]>([])
  let latestFetch = 0

  const eventsByDate = computed(() => {
    const map: Record<string, CalendarEvent[]> = {}
    const eventList = events.value || []
    for (const event of eventList) {
      const key = event.start.split('T')[0].split(' ')[0]
      if (!map[key]) map[key] = []
      map[key].push(event)
    }
    return map
  })

  async function fetchEvents() {
    const calendarStore = useCalendarStore()
    const start = format(calendarStore.visibleStart, 'yyyy-MM-dd')
    const end = format(calendarStore.visibleEnd, 'yyyy-MM-dd')
    const fetchId = ++latestFetch

    if (start !== loadedStart.value || end !== loadedEnd.value) {
      loadedStart.value = start
      loadedEnd.value = end
      events.value = []
    }

    loading.value = true
    error.value = null
    try {
      const result = await eventApi.getEvents(start, end)
      if (fetchId === latestFetch) events.value = result
    } catch (e) {
      if (fetchId === latestFetch) {
        error.value = e instanceof Error ? e.message : 'Failed to fetch events'
      }
    } finally {
      if (fetchId === latestFetch) loading.value = false
    }
  }

  function refreshAfterMutation() {
    if (loading.value) void fetchEvents()
  }

  async function createEvent(input: EventInput): Promise<CalendarEvent> {
    try {
      const newEvent = await eventApi.createEvent(input)
      if (!events.value.some(event => event.id === newEvent.id)) {
        events.value.push(newEvent)
      }
      error.value = null
      refreshAfterMutation()
      return newEvent
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to create event'
      throw e
    }
  }

  async function updateEvent(id: number, input: EventInput): Promise<CalendarEvent> {
    try {
      const updated = await eventApi.updateEvent(id, input)
      const index = events.value.findIndex(e => e.id === id)
      if (index !== -1) {
        events.value[index] = updated
      }
      error.value = null
      refreshAfterMutation()
      return updated
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to update event'
      throw e
    }
  }

  async function setEventPhase(id: number, phase: EventPhase): Promise<CalendarEvent | undefined> {
    const event = events.value.find(item => item.id === id)
    if (!event || event.phase === phase || movingIds.value.includes(id)) return event

    const index = events.value.findIndex(item => item.id === id)
    events.value[index] = { ...event, phase }
    movingIds.value.push(id)

    try {
      const updated = await eventApi.setEventPhase(id, phase)
      const currentIndex = events.value.findIndex(item => item.id === id)
      if (currentIndex !== -1) events.value[currentIndex] = updated
      error.value = null
      refreshAfterMutation()
      return updated
    } catch (e) {
      const currentIndex = events.value.findIndex(item => item.id === id)
      if (currentIndex !== -1) {
        events.value[currentIndex] = { ...events.value[currentIndex], phase: event.phase }
      }
      error.value = e instanceof Error ? e.message : 'Failed to move event'
      throw e
    } finally {
      movingIds.value = movingIds.value.filter(item => item !== id)
    }
  }

  async function deleteEvent(id: number): Promise<void> {
    try {
      await eventApi.deleteEvent(id)
      events.value = events.value.filter(e => e.id !== id)
      error.value = null
      refreshAfterMutation()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to delete event'
      throw e
    }
  }

  function isMoving(id: number): boolean {
    return movingIds.value.includes(id)
  }

  return {
    loading,
    error,
    eventsByDate,
    fetchEvents,
    createEvent,
    updateEvent,
    setEventPhase,
    deleteEvent,
    isMoving
  }
})
