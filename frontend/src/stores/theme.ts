import { defineStore } from 'pinia'
import { nextTick, ref } from 'vue'
import { eventColors } from '@/constants/eventColors'
import { eventPhases } from '@/constants/eventPhases'
import type { EventPhase } from '@/types'

const THEME_KEY = 'catendar-theme'
const SHOW_TIME_KEY = 'catendar-show-time'
const BOARD_COLORS_KEY = 'catendar-board-column-colors'
const paletteValues = new Set(eventColors.map(color => color.value))
const defaultBoardColors: Record<EventPhase, string> = {
  todo: '#7663b5',
  progressing: '#3f83b8',
  done: '#3d8a70'
}

function loadBoardColors(): Record<EventPhase, string> {
  const colors = { ...defaultBoardColors }
  try {
    const stored = localStorage.getItem(BOARD_COLORS_KEY)
    if (!stored) return colors
    const saved: unknown = JSON.parse(stored)
    if (!saved || typeof saved !== 'object' || Array.isArray(saved)) return colors
    for (const phase of eventPhases) {
      const value = (saved as Record<string, unknown>)[phase]
      if (typeof value === 'string' && paletteValues.has(value.toLowerCase())) {
        colors[phase] = value.toLowerCase()
      }
    }
  } catch {
    // Ignore damaged or unavailable preferences and keep the defaults.
  }
  return colors
}

function persistBoardColors(colors: Record<EventPhase, string>): boolean {
  try {
    localStorage.setItem(BOARD_COLORS_KEY, JSON.stringify(colors))
    return true
  } catch {
    return false
  }
}

function prefersDarkTheme(): boolean {
  const savedTheme = localStorage.getItem(THEME_KEY)
  if (savedTheme) return savedTheme === 'dark'
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

export const useThemeStore = defineStore('theme', () => {
  const isDark = ref(prefersDarkTheme())
  const showTime = ref(localStorage.getItem(SHOW_TIME_KEY) !== 'false')
  const boardColors = ref<Record<EventPhase, string>>(loadBoardColors())

  function columnColor(phase: EventPhase): string {
    return boardColors.value[phase]
  }

  function setColumnColor(phase: EventPhase, color: string): boolean {
    const normalized = color.toLowerCase()
    if (!paletteValues.has(normalized)) return false
    boardColors.value = { ...boardColors.value, [phase]: normalized }
    return persistBoardColors(boardColors.value)
  }

  function applyTheme() {
    document.documentElement.dataset.theme = isDark.value ? 'dark' : 'light'
  }

  function toggleTheme() {
    const updateTheme = async () => {
      isDark.value = !isDark.value
      localStorage.setItem(THEME_KEY, isDark.value ? 'dark' : 'light')
      applyTheme()
      await nextTick()
    }

    const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    const transitionDocument = document as Document & {
      startViewTransition?: (update: () => Promise<void>) => unknown
    }

    if (reduceMotion || !transitionDocument.startViewTransition) {
      void updateTheme()
      return
    }
    transitionDocument.startViewTransition(updateTheme)
  }

  function toggleShowTime() {
    showTime.value = !showTime.value
    localStorage.setItem(SHOW_TIME_KEY, String(showTime.value))
  }

  applyTheme()

  return {
    isDark,
    showTime,
    boardColors,
    columnColor,
    setColumnColor,
    toggleTheme,
    toggleShowTime
  }
})
