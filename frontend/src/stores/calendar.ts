import { defineStore } from 'pinia'
import { ref, computed, onScopeDispose } from 'vue'
import {
  startOfMonth,
  startOfDay,
  endOfMonth,
  startOfWeek,
  endOfWeek,
  addDays,
  addMonths,
  subDays,
  subMonths,
  isWeekend,
  isSameMonth,
  isSameDay,
  format,
  parseISO
} from 'date-fns'
import type { DayCell } from '@/types'

export type CalendarView = 'board' | 'month'

export const useCalendarStore = defineStore('calendar', () => {
  const currentDate = ref(new Date())
  const today = ref(currentDate.value)
  const viewMode = ref<CalendarView>('board')

  const currentMonth = computed(() => format(currentDate.value, 'yyyy-MM'))
  const monthLabel = computed(() => format(currentDate.value, 'MMMM yyyy'))
  const dayLabel = computed(() => format(currentDate.value, 'EEE, MMM d'))
  const focusedDate = computed(() => format(currentDate.value, 'yyyy-MM-dd'))
  const isToday = computed(() => isSameDay(currentDate.value, today.value))
  const isCurrentMonth = computed(() => isSameMonth(currentDate.value, today.value))

  let midnightTimer: ReturnType<typeof setTimeout>
  function syncLocalDay() {
    const now = new Date()
    if (!isSameDay(today.value, now)) {
      if (isToday.value) currentDate.value = now
      today.value = now
    }
    clearTimeout(midnightTimer)
    const nextMidnight = startOfDay(addDays(now, 1))
    midnightTimer = setTimeout(syncLocalDay, nextMidnight.getTime() - now.getTime())
  }
  syncLocalDay()
  window.addEventListener('focus', syncLocalDay)
  onScopeDispose(() => {
    clearTimeout(midnightTimer)
    window.removeEventListener('focus', syncLocalDay)
  })

  const monthStart = computed(() => startOfMonth(currentDate.value))
  const monthEnd = computed(() => endOfMonth(currentDate.value))
  const visibleStart = computed(() => startOfWeek(monthStart.value))
  const visibleEnd = computed(() => endOfWeek(monthEnd.value))

  const calendarDays = computed((): DayCell[] => {
    const days: DayCell[] = []
    let day = visibleStart.value

    while (day <= visibleEnd.value) {
      days.push({
        date: new Date(day),
        isCurrentMonth: isSameMonth(day, currentDate.value),
        isToday: isSameDay(day, today.value),
        isWeekend: isWeekend(day)
      })
      day = addDays(day, 1)
    }

    return days
  })

  const weeks = computed(() => {
    const result: DayCell[][] = []
    const days = calendarDays.value
    for (let i = 0; i < days.length; i += 7) {
      result.push(days.slice(i, i + 7))
    }
    return result
  })

  function nextMonth() {
    currentDate.value = addMonths(currentDate.value, 1)
  }

  function prevMonth() {
    currentDate.value = subMonths(currentDate.value, 1)
  }

  function nextDay() {
    currentDate.value = addDays(currentDate.value, 1)
  }

  function prevDay() {
    currentDate.value = subDays(currentDate.value, 1)
  }

  function nextPeriod() {
    if (viewMode.value === 'board') nextDay()
    else nextMonth()
  }

  function prevPeriod() {
    if (viewMode.value === 'board') prevDay()
    else prevMonth()
  }

  function setView(view: CalendarView) {
    viewMode.value = view
  }

  function goToDate(value: string) {
    const date = parseISO(value)
    if (!Number.isNaN(date.getTime())) currentDate.value = date
  }

  function goToToday() {
    currentDate.value = new Date()
  }

  return {
    currentDate,
    today,
    viewMode,
    currentMonth,
    monthLabel,
    dayLabel,
    focusedDate,
    isToday,
    isCurrentMonth,
    visibleStart,
    visibleEnd,
    weeks,
    nextMonth,
    prevMonth,
    nextDay,
    prevDay,
    nextPeriod,
    prevPeriod,
    setView,
    goToDate,
    goToToday
  }
})
