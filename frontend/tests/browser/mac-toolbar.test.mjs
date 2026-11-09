import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { webkit } from 'playwright-core'
import { preview } from 'vite'

let server
before(async () => {
  server = await preview({ preview: { host: '127.0.0.1', port: 0 }, logLevel: 'silent' })
})
after(async () => {
  if (server) await new Promise(resolve => server.httpServer.close(resolve))
})

function seedMac() {
  window.runtime = {
    Environment: async () => ({ platform: 'darwin', arch: 'arm64', buildType: 'production' }),
    WindowIsFullscreen: async () => false,
    WindowSetAlwaysOnTop: () => {}
  }
  window.go = { main: {
    CalendarApp: { GetBoardEvents: async () => [], GetEvents: async () => [] },
    AISyncApp: { GetAISyncStatus: async () => ({ state: 'idle' }), GetConversationSync: async () => ({ state: 'idle' }), GetConversationActivity: async () => [] }
  } }
}

test('Mac window keeps the date and month controls reachable at its minimum width', async () => {
  const browser = await webkit.launch()
  try {
    const page = await browser.newPage({ viewport: { width: 1000, height: 650 } })
    await page.clock.setFixedTime(new Date(2026, 8, 30, 12))
    await page.addInitScript(seedMac)
    await page.goto(server.resolvedUrls.local[0])
    const toolbar = page.locator('header.toolbar')
    const clock = toolbar.locator('h1')
    const today = toolbar.getByRole('button', { name: 'Go to today', exact: true })
    await today.waitFor()
    await page.waitForFunction(() => getComputedStyle(document.querySelector('.toolbar')).getPropertyValue('--wails-draggable').trim() === 'drag')
    assert.equal(await toolbar.locator('.brand-name').isVisible(), false)
    const initial = await clock.innerText()
    await toolbar.getByRole('button', { name: 'Next day' }).click()
    assert.notEqual(await clock.innerText(), initial)
    await today.click()
    assert.equal(await clock.innerText(), initial)

    await page.setViewportSize({ width: 360, height: 550 })
    const more = toolbar.getByRole('button', { name: /more|actions/i })
    await more.waitFor()
    await toolbar.getByRole('button', { name: 'Next day' }).click()
    await more.focus()
    await page.keyboard.press('Enter')
    await page.getByRole('menuitem', { name: 'Today' }).waitFor()
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Enter')
    assert.equal(await clock.innerText(), initial, 'board shortcuts intercepted the open menu')
    await toolbar.getByRole('button', { name: 'Month', exact: true }).click()
    assert.equal(await clock.innerText(), 'September 2026')
    for (const label of ['Previous month', 'Next month']) await toolbar.getByRole('button', { name: label }).waitFor()
    const positions = await toolbar.evaluate(el => [...el.querySelectorAll('button,h1')].filter(b => b.getClientRects().length).map(b => {
      const box = b.getBoundingClientRect()
      return { left: box.left, right: box.right, middle: box.top + box.height / 2 }
    }))
    assert.ok(positions.length >= 5)
    for (const p of positions) {
      assert.ok(p.left >= 70 && p.right <= 360, `clipped control or traffic-light overlap: ${JSON.stringify(p)}`)
      assert.ok(Math.abs(p.middle - positions[0].middle) < 8, 'minimum-width toolbar gained another row')
    }
    await more.focus()
    await page.keyboard.press('Enter')
    const create = page.getByText('New event', { exact: true }).filter({ visible: true })
    await create.waitFor()
    await create.click()
    await page.locator('#event-title').waitFor()
  } finally {
    await browser.close()
  }
})
