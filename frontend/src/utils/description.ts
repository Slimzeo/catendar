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
