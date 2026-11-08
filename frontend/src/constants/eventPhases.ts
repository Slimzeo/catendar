import type { EventPhase } from '@/types'

export const eventPhases: readonly EventPhase[] = ['todo', 'progressing', 'done']

export function isEventPhase(value: unknown): value is EventPhase {
  return eventPhases.some(phase => phase === value)
}
