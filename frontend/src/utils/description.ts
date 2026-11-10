export interface DescriptionSegment {
  type: 'text' | 'url'
  value: string
  href?: string
}

const urlPattern = /(https?:\/\/[^\s]+|catendar:\/\/[^\s]+|www\.[^\s]+)/gi
const emailPattern = /catendar:\/\/email\/[^\s]+/i

export function descriptionSegments(text: string): DescriptionSegment[] {
  const segments: DescriptionSegment[] = []
  let previousIndex = 0

  for (const match of text.matchAll(urlPattern)) {
    const index = match.index ?? 0
    if (index > previousIndex) {
      segments.push({ type: 'text', value: text.slice(previousIndex, index) })
    }
    const value = match[0]
    segments.push({
      type: 'url',
      value,
      href: value.startsWith('www.') ? `https://${value}` : value
    })
    previousIndex = index + value.length
  }

  if (previousIndex < text.length) {
    segments.push({ type: 'text', value: text.slice(previousIndex) })
  }
  return segments
}

export function sourceEmailUrl(text: string): string | undefined {
  return text.match(emailPattern)?.[0]
}

export function isEmailLink(url: string): boolean {
  return url.toLowerCase().startsWith('catendar://email/')
}

// Only bare web links and the app's own catendar:// links may become anchors.
// Credentials and other schemes are shown as plain text.
export function safeDescriptionHref(value: string): string | undefined {
  const raw = value.trim()
  if (!raw || /[\s\\]/.test(raw)) return undefined
  const candidate = /^www\./i.test(raw) ? `https://${raw}` : raw
  try {
    const url = new URL(candidate)
    if (!url.hostname || url.username || url.password) return undefined
    if (url.protocol === 'http:' || url.protocol === 'https:' || isEmailLink(candidate)) {
      return candidate
    }
    return undefined
  } catch {
    return undefined
  }
}
