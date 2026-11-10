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
    const submissionDeadline = typeof source.submissionDeadline === 'string' ? source.submissionDeadline.trim() : ''
    const submissionText = typeof source.submissionDeadlineText === 'string' ? source.submissionDeadlineText.trim() : ''
    if (submissionDeadline) registration.submissionDeadline = submissionDeadline
    if (submissionText) registration.submissionDeadlineText = submissionText
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

// Sort on the board day being viewed, including historic snapshots. A past
// application cutoff can still have a coming submission deadline.
export function orderedRegistrations(registrations: EventRegistration[], day: string): EventRegistration[] {
  return registrations.filter(item => item?.name).map(item => {
    const dates = [item.deadline, item.submissionDeadline].filter((value): value is string => Boolean(value)).sort()
    const next = dates.find(value => value >= day)
    return { item, group: next ? 0 : dates.length ? 2 : 1, date: next || dates[dates.length - 1] || '' }
  }).sort((a, b) => a.group - b.group || (a.group === 2 ? b.date.localeCompare(a.date) : a.date.localeCompare(b.date)))
    .map(row => row.item)
}
