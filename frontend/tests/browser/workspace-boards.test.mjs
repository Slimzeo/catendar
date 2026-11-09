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

function seedWorkspaces() {
  const now = new Date()
  const day = [now.getFullYear(), String(now.getMonth() + 1).padStart(2, '0'), String(now.getDate()).padStart(2, '0')].join('-')
  const workspaces = [
    { id: 1, name: 'Daily Routine', hint: '' },
    { id: 2, name: 'Seekino', hint: 'Launch planning' },
    { id: 3, name: '个人 IP 打造', hint: 'Content' }
  ]
  const rows = [
    { id: 1, title: 'Routine task', workspaceId: 1 },
    { id: 2, title: 'Seekino task', workspaceId: 2, phase: 'progressing' },
    { id: 3, title: 'IP task', workspaceId: 3 },
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
  let nextWorkspaceId = 4
  window.eventCreates = []
  window.eventUpdates = []
  window.workspaceCreates = []
  window.workspaceUpdates = []
  window.conversationStarts = []
  window.go = { main: {
    CalendarApp: {
      ListWorkspaces: async () => workspaces.map(item => ({ ...item })),
      GetEvents: async () => rows,
      GetBoardEvents: async () => rows,
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
      SetBoardEventPhase: async (id, phase) => {
        const row = rows.find(item => item.id === id)
        row.phase = phase
        return row
      },
      DeleteEvent: async () => {},
      CreateWorkspace: async (name, hint) => {
        window.workspaceCreates.push([name, hint])
        const workspace = { id: nextWorkspaceId++, name, hint }
        workspaces.push(workspace)
        return workspace
      },
      UpdateWorkspace: async (id, name, hint) => {
        window.workspaceUpdates.push([id, name, hint])
        const workspace = workspaces.find(item => item.id === id)
        workspace.name = name
        workspace.hint = hint
        return workspace
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
      StartConversationSync: async () => {
        window.conversationStarts.push('start')
        return {
          state: 'running',
          message: 'Reading recent conversations…',
          codexPath: '',
          runId: 'run-1',
          lastSyncedAt: '',
          sources: 0,
          created: 0,
          updated: 0,
          pending: 0,
          skipped: 0
        }
      },
      GetConversationActivity: async () => [],
      CancelConversationSync: async () => true
    }
  } }
}

for (const engine of [webkit, chromium]) {
  test(`${engine.name()} filters project boards and carries the original task workspace`, async () => {
    const browser = await engine.launch()
    try {
      const page = await browser.newPage()
      page.setDefaultTimeout(4000)
      await page.addInitScript(seedWorkspaces)
      await page.goto(`http://127.0.0.1:${server.httpServer.address().port}`)

      const card = id => page.locator(`article[data-task-id="${id}"]`)
      await card(1).waitFor()
      await page.waitForFunction(() => document.querySelectorAll('.board article[data-task-id]').length === 4)
      assert.equal((await card(2).locator('.workspace-label').textContent()).trim(), 'Seekino')
      assert.equal((await card(4).locator('.workspace-label').textContent()).trim(), 'Daily Routine')
      assert.deepEqual(await page.evaluate(() => window.conversationStarts), [], 'application start must not scan conversations')

      await page.getByRole('button', { name: 'Settings' }).click()
      await page.locator('#settings-title').waitFor()
      await page.keyboard.press('Escape')
      await page.locator('#settings-title').waitFor({ state: 'hidden' })
      assert.deepEqual(await page.evaluate(() => window.conversationStarts), [], 'the settings gear must not scan conversations')

      await page.getByRole('button', { name: 'Next day' }).click()
      await page.getByRole('button', { name: 'Previous day' }).click()
      assert.deepEqual(await page.evaluate(() => window.conversationStarts), [], 'date changes must not scan conversations')

      await page.getByRole('button', { name: 'Seekino', exact: true }).click()
      await page.waitForFunction(() => document.querySelectorAll('.board article[data-task-id]').length === 1)
      await card(2).waitFor()
      assert.deepEqual(await page.evaluate(() => window.conversationStarts), [], 'project switches must not scan conversations')

      // The active project should accept board shortcuts immediately after its tab is clicked.
      await page.keyboard.press('a')
      await page.locator('.quick-input').waitFor()
      await page.keyboard.press('Escape')
      await page.locator('section[data-phase="progressing"] .add-button').click()
      const projectDraft = page.getByRole('textbox', { name: 'New task in progressing' })
      await projectDraft.fill('Seekino follow up')
      await projectDraft.press('Enter')
      await page.getByRole('button', { name: 'Seekino follow up', exact: true }).waitFor()
      const projectCreate = await page.evaluate(() => window.eventCreates.at(-1))
      assert.equal(projectCreate.workspaceId, 2)

      await page.getByRole('button', { name: 'All', exact: true }).click()
      await card(100).waitFor()
      await page.waitForFunction(() => document.querySelectorAll('.board .workspace-label').length === 5)

      await card(2).locator('.edit-title').click()
      const titleInput = page.locator('#event-title')
      await titleInput.waitFor()
      await titleInput.fill('Seekino renamed')
      await page.getByRole('button', { name: 'Save task', exact: true }).click()
      await titleInput.waitFor({ state: 'hidden' })
      const edit = await page.evaluate(() => window.eventUpdates.at(-1))
      assert.equal(edit.id, 2)
      assert.equal(edit.workspaceId, 2)

      // A keyboard project switch must clear the hidden selection instead of
      // silently re-selecting a card in the new board.
      await card(1).click()
      await page.locator('#event-title').waitFor()
      await page.keyboard.press('Escape')
      await page.locator('#event-title').waitFor({ state: 'hidden' })
      const seekinoTab = page.getByRole('button', { name: 'Seekino', exact: true })
      await seekinoTab.focus()
      await page.keyboard.press('Enter')
      await page.waitForFunction(() => document.querySelectorAll('.board article[data-task-id]').length === 2)
      await card(2).waitFor()
      await page.waitForFunction(() => document.querySelectorAll('.board article[data-selected="true"]').length === 0)

      await page.getByRole('button', { name: 'New project board' }).click()
      await page.locator('.workspace-card h2').waitFor()
      await page.locator('#workspace-name').click()
      await page.keyboard.type('ada drops')
      await page.locator('#workspace-hint').fill('Codex keyword')
      const createProject = page.getByRole('button', { name: 'Create project', exact: true })
      await createProject.focus()
      await page.keyboard.press('a')
      assert.equal(await page.locator('.quick-input').count(), 0)
      await createProject.click()
      await page.getByRole('button', { name: 'ada drops', exact: true }).waitFor()
      assert.deepEqual(await page.evaluate(() => window.workspaceCreates.at(-1)), ['ada drops', 'Codex keyword'])
      await page.waitForFunction(() => document.querySelectorAll('.board article[data-task-id]').length === 0)

      await page.locator('#workspace-dialog-title').waitFor({ state: 'hidden' })
      await page.getByRole('button', { name: 'Edit ada drops' }).click()
      await page.getByRole('dialog', { name: 'Project board settings' }).waitFor()
      await page.locator('#workspace-name').click()
      await page.locator('#workspace-name').fill('ada drops 2')
      await page.locator('#workspace-hint').fill('Updated keyword')
      await page.getByRole('button', { name: 'Save project', exact: true }).click()
      await page.getByRole('button', { name: 'ada drops 2', exact: true }).waitFor()
      assert.equal((await page.evaluate(() => window.workspaceUpdates.at(-1)))[1], 'ada drops 2')

      // An unfinished quick draft belongs to the project that opened it.
      await page.locator('section[data-phase="todo"] .add-button').click()
      await page.getByRole('textbox', { name: 'New task in todo' }).fill('Draft in project')
      await page.getByRole('button', { name: 'All', exact: true }).click()
      await page.locator('.quick-input').waitFor({ state: 'hidden' })

      await page.locator('section[data-phase="todo"] .add-button').click()
      const allDraft = page.getByRole('textbox', { name: 'New task in todo' })
      await allDraft.fill('Routine quick')
      await allDraft.press('Enter')
      await page.getByRole('button', { name: 'Routine quick', exact: true }).waitFor()
      assert.equal((await page.evaluate(() => window.eventCreates.at(-1))).workspaceId, 1)

      await page.getByRole('button', { name: 'ada drops 2', exact: true }).click()
      await page.waitForFunction(() => document.querySelectorAll('.board .workspace-label').length === 0)
    } finally {
      await browser.close()
    }
  })
}
