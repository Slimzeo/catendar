import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { chromium, webkit } from 'playwright-core'
import { preview } from 'vite'

let server
before(async () => {
  server = await preview({ preview: { host: '127.0.0.1', port: 0 }, logLevel: 'silent' })
})
after(async () => {
  if (server) await new Promise(resolve => server.httpServer.close(resolve))
})

function seedBoard() {
  const now = new Date()
  const day = [now.getFullYear(), String(now.getMonth() + 1).padStart(2, '0'), String(now.getDate()).padStart(2, '0')].join('-')
  const rows = [1, 2, 3].map(id => ({
    id, title: `Task ${id}`, start: day + 'T00:00:00', end: day + 'T00:00:00',
    allDay: true, phase: 'todo', description: '', result: '', color: '#7663b5'
  }))
  window.phaseWrites = []
  window.go = { main: {
    CalendarApp: {
      GetEvents: async () => rows,
      SetEventPhase: async (id, phase) => {
        const row = rows.find(task => task.id === id)
        window.phaseWrites.push([id, phase])
        row.phase = phase
        return row
      }
    },
    AISyncApp: { GetAISyncStatus: async () => ({ state: 'idle' }) }
  } }
}

for (const engine of [webkit, chromium]) {
  test(`${engine.name()} keeps card navigation separate from phase changes`, async () => {
    const browser = await engine.launch()
    try {
      const page = await browser.newPage()
      page.setDefaultTimeout(3500)
      await page.addInitScript(seedBoard)
      await page.goto(`http://127.0.0.1:${server.httpServer.address().port}`)
      const card = id => page.locator(`.board article[data-task-id="${id}"]`)
      const title = id => card(id).getByRole('button', { name: `Task ${id}`, exact: true })
      const trigger = id => card(id).getByLabel(new RegExp(`^Move Task ${id} to another phase`))
      const press = key => page.keyboard.press(key)
      const selected = (id, phase = 'todo') => page.locator(
        `section[data-phase="${phase}"][data-active="true"] article[data-task-id="${id}"][data-selected="true"]`
      ).waitFor()
      const writes = () => page.evaluate(() => window.phaseWrites)
      async function tabToPhase(id) {
        await title(id).focus()
        await press('Tab')
        const control = trigger(id)
        assert.ok(await control.evaluate(element => element === document.activeElement), 'Tab reached the phase control')
      }

      await selected(1)
      await press('2')
      await tabToPhase(2)
      await press('ArrowDown')
      assert.deepEqual(await writes(), [], 'ArrowDown on a phase control must not write a phase')
      await selected(3)
      await tabToPhase(3)
      await press('ArrowUp')
      await selected(2)
      assert.deepEqual(await writes(), [])

      // Reproduce the reported path with the chooser open and an option focused.
      await trigger(2).click()
      const option = card(2).getByRole('button', { name: 'done', exact: true })
      await option.focus()
      await press('ArrowUp')
      await selected(1)
      await option.waitFor({ state: 'hidden' })
      await trigger(1).click()
      await press('ArrowUp')
      await selected(1)
      await card(1).getByRole('button', { name: 'done', exact: true }).waitFor({ state: 'hidden' })
      assert.deepEqual(await writes(), [])

      // An unfinished quick-add draft only owns keys while its form has focus.
      await press('a')
      const draft = page.getByRole('textbox', { name: 'New task in todo' })
      await draft.fill('Draft in progress')
      await trigger(1).click()
      await press('ArrowDown')
      await selected(2)
      assert.equal(await draft.inputValue(), 'Draft in progress')
      await press('3')
      await selected(3)
      await press('a')
      await press('Escape')
      await draft.waitFor({ state: 'hidden' })
      assert.deepEqual(await writes(), [])

      // Retain both deliberate ways of changing phase.
      await trigger(1).click()
      await card(1).getByRole('button', { name: 'done', exact: true }).click()
      await selected(1, 'done')
      await trigger(1).focus()
      await press('Control+ArrowLeft')
      await selected(1, 'progressing')
      assert.deepEqual(await writes(), [[1, 'done'], [1, 'progressing']])

      await page.evaluate(() => localStorage.setItem('catendar-shortcuts-v1', JSON.stringify({ taskDown: 'J' })))
      await page.reload()
      await selected(1)
      await tabToPhase(1)
      await press('ArrowDown')
      await selected(1)
      await press('j')
      await selected(2)
      assert.deepEqual(await writes(), [])

      async function switchColumn(key, phase) {
        await page.keyboard.down('Tab')
        try { await press(key) } finally { await page.keyboard.up('Tab') }
        await page.locator(`section[data-phase="${phase}"][data-active="true"]`).waitFor()
      }
      await switchColumn('ArrowRight', 'progressing')
      await switchColumn('ArrowRight', 'done')
      await switchColumn('ArrowLeft', 'progressing')
      await switchColumn('ArrowLeft', 'todo')
      await selected(1)
    } finally {
      await browser.close()
    }
  })
}
