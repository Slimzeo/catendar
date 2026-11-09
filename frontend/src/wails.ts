import { WindowSetAlwaysOnTop, BrowserOpenURL, type Environment } from '../wailsjs/runtime/runtime'

function hasWailsRuntime(): boolean {
  return 'runtime' in window
}

export async function detectNativeMac(): Promise<boolean> {
  const runtime = (window as Window & { runtime?: { Environment?: typeof Environment } }).runtime
  if (typeof runtime?.Environment !== 'function') return false
  try {
    return (await runtime.Environment()).platform === 'darwin'
  } catch {
    return false
  }
}

export function setAlwaysOnTop(on: boolean): boolean {
  if (!hasWailsRuntime()) return false
  WindowSetAlwaysOnTop(on)
  return true
}

export function openExternal(url: string): void {
  if (hasWailsRuntime()) {
    BrowserOpenURL(url)
    return
  }
  window.open(url, '_blank', 'noopener,noreferrer')
}
