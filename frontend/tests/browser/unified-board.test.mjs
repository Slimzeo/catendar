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

function seedUnifiedBoard() {
  const now = new Date()
  const day = [now.getFullYear(), String(now.getMonth() + 1).padStart(2, '0'), String(now.getDate()).padStart(2, '0')].join('-')
  // Older records may still carry workspace ids; the unified board ignores them.
  const rows = [
    { id: 1, title: 'Routine task', workspaceId: 1 },
    { id: 2, title: 'Seekino task', workspaceId: 2, phase: 'progressing' },
    { id: 3, title: '整理发布清单', workspaceId: 3 },
    { id: 4, title: 'Legacy task' }
  ].map(row => ({
    start: `${day}T00:00:00`,
    end: `${day}T00:00:00`,
    allDay: true,
    phase: 'todo',
    description: '',
    result: '',
    color: '#7663b5',
    bold: false,
    ...row
  }))
  let nextEventId = 100
  window.eventCreates = []
  window.eventUpdates = []
  window.workspaceCalls = []
  window.boardReads = 0
  window.monthReads = 0
  window.go = { main: {
    CalendarApp: {
      GetEvents: async () => {
        window.monthReads++
        return []
      },
      GetBoardEvents: async () => {
        window.boardReads++
        return rows.map(item => ({ ...item }))
      },
      CreateEvent: async input => {
        window.eventCreates.push({ ...input })
        const created = { ...input, id: nextEventId++, result: input.result || '', createdAt: '', updatedAt: '' }
        rows.push(created)
        return created
      },
      UpdateEvent: async (id, input) => {
        window.eventUpdates.push({ id, ...input })
        const row = rows.find(item => item.id === id)
        Object.assign(row, input)
        return row
      },
      DeleteEvent: async () => {},
      ListWorkspaces: async () => {
        window.workspaceCalls.push('list')
        return []
      },
      CreateWorkspace: async (name, hint) => {
        window.workspaceCalls.push(['create', name, hint])
        return { id: 99, name, hint }
      },
      UpdateWorkspace: async (id, name, hint) => {
        window.workspaceCalls.push(['update', id, name, hint])
        return { id, name, hint }
      }
    },
    AISyncApp: {
      GetAISyncStatus: async () => ({ state: 'idle', emailCount: 0, readEmails: 0, unreadableEmails: 0, created: 0, skipped: 0, rejected: 0, conflicts: 0 }),
      GetConversationSync: async () => ({
        state: 'idle',
        message: '',
        codexPath: '',
        runId: '',
        lastSyncedAt: '',
        sources: 0,
        created: 0,
        updated: 0,
        pending: 0,
        skipped: 0
      }),
      GetConversationActivity: async () => []
    }
  } }
}

for (const engine of [webkit, chromium]) {
  test(`${engine.name()} shows every board task in one daily kanban and never sends workspace ids`, async () => {
    const browser = await engine.launch()
    try {
      const page = await browser.newPage()
      page.setDefaultTimeout(4000)
      await page.addInitScript(seedUnifiedBoard)
      await page.goto(`http://127.0.0.1:${server.httpServer.address().port}`)

      const card = id => page.locator(`article[data-task-id="${id}"]`)
      await card(1).waitFor()
      await page.waitForFunction(() => document.querySelectorAll('.board article[data-task-id]').length === 4)
      await card(2).waitFor()
      await card(3).waitFor()
      await card(4).waitFor()
      assert.equal(await card(3).locator('.edit-title').innerText(), '整理发布清单')
      assert.equal(await page.locator('.board .workspace-label').count(), 0)
      assert.equal(await page.locator('.workspace-bar').count(), 0)
      assert.equal(await page.getByRole('button', { name: 'All', exact: true }).count(), 0)
      assert.equal(await page.getByRole('button', { name: 'New project board' }).count(), 0)
      assert.ok(await page.evaluate(() => window.boardReads > 0), 'the board is read from GetBoardEvents')
      assert.equal(await page.evaluate(() => window.monthReads), 0, 'the daily board never reads the month range')

      // Quick add is keyboard-only and the payload has no workspace id.
      await page.keyboard.press('a')
      const draft = page.getByRole('textbox', { name: 'New task in todo' })
      await draft.waitFor()
      await page.keyboard.type('Quick keyboard task')
      await page.keyboard.press('Enter')
      await page.getByRole('button', { name: 'Quick keyboard task', exact: true }).waitFor()
      const quickCreate = await page.evaluate(() => window.eventCreates.at(-1))
      assert.equal(quickCreate.title, 'Quick keyboard task')
      assert.equal(Object.prototype.hasOwnProperty.call(quickCreate, 'workspaceId'), false)
      await draft.focus()
      await page.keyboard.press('Escape')
      await draft.waitFor({ state: 'hidden' })

      // Opening and saving through the keyboard keeps the same restriction.
      const createdId = await page.locator('.board article', { hasText: 'Quick keyboard task' }).getAttribute('data-task-id')
      await page.locator(`article[data-task-id="${createdId}"] .edit-title`).focus()
      await page.keyboard.press('Enter')
      const titleInput = page.locator('#event-title')
      await titleInput.waitFor()
      assert.equal(await page.getByLabel('Project board').count(), 0)
      const modifier = await page.evaluate(() => /^Mac/i.test(navigator.platform) ? 'Meta' : 'Control')
      await titleInput.press(`${modifier}+A`)
      await page.keyboard.type('Keyboard edited task')
      await page.getByRole('button', { name: 'Save task', exact: true }).focus()
      await page.keyboard.press('Enter')
      await titleInput.waitFor({ state: 'hidden' })
      await page.getByRole('button', { name: 'Keyboard edited task', exact: true }).waitFor()
      const edit = await page.evaluate(() => window.eventUpdates.at(-1))
      assert.equal(edit.id, Number(createdId))
      assert.equal(edit.title, 'Keyboard edited task')
      assert.equal(Object.prototype.hasOwnProperty.call(edit, 'workspaceId'), false)

      assert.deepEqual(await page.evaluate(() => window.workspaceCalls), [])
      assert.deepEqual(await page.evaluate(() => window.eventCreates.map(item => item.title)), ['Quick keyboard task'])
    } finally {
      await browser.close()
    }
  })
}
