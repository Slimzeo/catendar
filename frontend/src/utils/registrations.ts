import type { EventRegistration } from '@/types'

export function normalizeRegistrations(value: unknown): EventRegistration[] | undefined {
  if (!Array.isArray(value)) return undefined

  const rows: EventRegistration[] = []
  for (const item of value) {
    if (!item || typeof item !== 'object') continue
    const source = item as Record<string, unknown>
    const name = typeof source.name === 'string' ? source.name.trim() : ''
    if (!name) continue

    const registration: EventRegistration = { name }
    const url = typeof source.url === 'string' ? source.url.trim() : ''
    const deadline = typeof source.deadline === 'string' ? source.deadline.trim() : ''
    const deadlineText = typeof source.deadlineText === 'string' ? source.deadlineText.trim() : ''
    if (url) registration.url = url
    if (deadline) registration.deadline = deadline
    if (deadlineText) registration.deadlineText = deadlineText
    rows.push(registration)
  }
  return rows.length ? rows : undefined
}

// Registration links come from synced data, so only open a bare http(s) URL
// that is already present rather than guessing a path from the activity name.
export function safeRegistrationUrl(value: string | undefined): string | undefined {
  const raw = value?.trim()
  if (!raw || !/^https?:\/\//i.test(raw) || /[\s\\]/.test(raw)) return undefined
  try {
    const url = new URL(raw)
    if ((url.protocol !== 'http:' && url.protocol !== 'https:') || !url.hostname || url.username || url.password) return undefined
    return raw
  } catch {
    return undefined
  }
}

export function registrationDeadline(registration: EventRegistration): string {
  return registration.deadlineText?.trim() || registration.deadline?.trim() || ''
}
