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

function sourceSwitch(page, source, view = 'Board') {
  return page.getByRole('switch', { name: `${source} on ${view}`, exact: true })
}

function seedSyncSources() {
  const now = new Date()
  const day = [now.getFullYear(), String(now.getMonth() + 1).padStart(2, '0'), String(now.getDate()).padStart(2, '0')].join('-')
  const rows = [{
    id: 1,
    title: 'Plan the week',
    start: `${day}T00:00:00`,
    end: `${day}T00:00:00`,
    allDay: true,
    phase: 'todo',
    description: 'Source email: catendar://email/primary/message-1',
    result: '',
    color: '#7663b5',
    bold: false
  }]
  const idleConversations = {
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
  }
  const idleEmail = {
    state: 'idle',
    emailCount: 0,
    readEmails: 0,
    unreadableEmails: 0,
    created: 0,
    skipped: 0,
    rejected: 0,
    conflicts: 0
  }
  const setup = {
    email: {
      id: 'primary',
      address: 'inbox@example.test',
      username: 'inbox@example.test',
      imapHost: 'imap.example.test',
      imapPort: 993,
      folder: 'INBOX',
      useTLS: true,
      configured: true,
      hasCredential: true
    },
    agent: {
      settings: { provider: 'codex', codexPath: '', claudePath: '' },
      agents: [
        { provider: 'codex', installed: true, path: '/opt/homebrew/bin/codex', version: 'codex 1.0' },
        { provider: 'claude', installed: false, path: '', error: 'Not installed' }
      ]
    }
  }
  let conversations = { ...idleConversations }
  let email = { ...idleEmail }
  window.syncCalls = []
  const record = (name, args = []) => window.syncCalls.push({ name, args })

  const calendarApp = {
    GetEvents: async () => rows.map(item => ({ ...item })),
    GetBoardEvents: async () => rows.map(item => ({ ...item }))
  }
  const aiSyncApp = {
    GetAISyncStatus: async () => {
      record('GetAISyncStatus')
      return { ...email }
    },
    GetConversationSync: async () => {
      record('GetConversationSync')
      return { ...conversations }
    },
    GetConversationActivity: async () => [],
    StartConversationSync: async () => {
      record('StartConversationSync')
      conversations = {
        ...idleConversations,
        state: window.keepRunning ? 'running' : 'succeeded',
        message: 'AI Sync complete',
        runId: 'run-conversations',
        sources: 1
      }
      return { ...conversations }
    },
    CancelConversationSync: async () => {
      record('CancelConversationSync')
      conversations = { ...conversations, state: 'cancelled' }
      return true
    },
    SaveConversationSyncSettings: async codexPath => {
      conversations = { ...conversations, codexPath }
      return { ...conversations }
    },
    GetAISyncSetup: async () => {
      record('GetAISyncSetup')
      return setup
    },
    SaveEmailAccount: async input => ({ id: 'primary', ...input, configured: true, hasCredential: Boolean(input.secret) }),
    TestEmailAccount: async () => {
      record('TestEmailAccount')
    },
    StartAISync: async () => {
      record('StartAISync')
      email = {
        ...idleEmail,
        state: window.keepRunning ? 'running' : 'succeeded',
        message: 'AI Sync complete',
        created: 1
      }
      return { ...email }
    },
    CancelAISync: async () => {
      record('CancelAISync')
      email = { ...email, state: 'cancelled' }
      return true
    },
    GetSourceEmail: async () => {
      record('GetSourceEmail')
      return { id: 'm1', messageId: 'm1', subject: '', sender: '', receivedAt: '', sourceUrl: '', text: '' }
    },
    SaveAISettings: async () => {}
  }

  window.go = { main: { CalendarApp: calendarApp, AISyncApp: aiSyncApp } }
}

async function countCalls(page, name) {
  return page.evaluate(name => window.syncCalls.filter(call => call.name === name).length, name)
}

async function settleCall(page, name, expected = 1) {
  await page.waitForFunction(([name, expected]) => window.syncCalls.filter(call => call.name === name).length === expected, [name, expected])
}

async function openSettings(page, tab) {
  await page.getByRole('button', { name: 'Settings', exact: true }).click()
  await page.getByRole('tab', { name: tab, exact: true }).click()
}

async function closeSettings(page) {
  await page.keyboard.press('Escape')
  await page.locator('#settings-title').waitFor({ state: 'hidden' })
}

test('Board and Month keep independent source preferences across page switches and reload', async () => {
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    await page.addInitScript(seedSyncSources)
    await page.goto(`http://127.0.0.1:${server.httpServer.address().port}`)
    const toolbar = page.locator('.ai-sync-button')
    const mailLink = page.getByRole('button', { name: 'Open source email for Plan the week' })
    const syncEmail = page.getByRole('button', { name: 'Sync email', exact: true })
    const aiTab = page.getByRole('tab', { name: 'AI Sync', exact: true })
    const components = page.getByRole('tab', { name: 'Components' })
    await page.locator('article[data-task-id="1"]').waitFor()
    assert.equal(await mailLink.count(), 0)
    await openSettings(page, 'AI Sync')
    assert.equal(await syncEmail.count(), 0)
    await closeSettings(page)
    await toolbar.click()
    await settleCall(page, 'StartConversationSync')
    for (const call of ['GetAISyncStatus', 'GetAISyncSetup', 'TestEmailAccount', 'StartAISync', 'GetSourceEmail']) {
      assert.equal(await countCalls(page, call), 0, 'disabled Board email: ' + call)
    }

    await page.getByRole('button', { name: 'Manage sources', exact: true }).click()
    assert.equal(await sourceSwitch(page, 'Email').getAttribute('aria-checked'), 'false')
    assert.equal(await sourceSwitch(page, 'Codex conversations').getAttribute('aria-checked'), 'true')
    assert.equal(await sourceSwitch(page, 'Email', 'Month').getAttribute('aria-checked'), 'true')
    await sourceSwitch(page, 'Email').click()
    await aiTab.click()
    await syncEmail.waitFor()
    await settleCall(page, 'GetAISyncSetup')
    assert.equal(await countCalls(page, 'StartAISync'), 0)
    await components.click()
    await sourceSwitch(page, 'Codex conversations').click()
    await closeSettings(page)
    await mailLink.waitFor()
    await toolbar.click()
    await syncEmail.waitFor()
    assert.equal(await page.locator('.conversation-panel').count(), 0)
    assert.equal(await countCalls(page, 'StartConversationSync'), 1)
    assert.equal(await countCalls(page, 'StartAISync'), 0)
    await syncEmail.click()
    await settleCall(page, 'StartAISync')
    await components.click()
    await sourceSwitch(page, 'Email').click()
    await closeSettings(page)
    assert.equal(await toolbar.count(), 0)
    assert.equal(await mailLink.count(), 0)

    await page.getByRole('button', { name: 'Month', exact: true }).click()
    await toolbar.waitFor()
    await openSettings(page, 'AI Sync')
    await syncEmail.waitFor()
    await page.locator('.conversation-panel').waitFor()
    assert.equal(await countCalls(page, 'StartConversationSync'), 1)
    assert.equal(await countCalls(page, 'StartAISync'), 1)
    await components.click()
    await sourceSwitch(page, 'Codex conversations', 'Month').click()
    await closeSettings(page)
    await page.reload()
    await page.locator('article[data-task-id="1"]').waitFor()
    assert.equal(await toolbar.count(), 0, 'Board both-off survives reload')
    await openSettings(page, 'Components')
    assert.equal(await sourceSwitch(page, 'Email').getAttribute('aria-checked'), 'false')
    assert.equal(await sourceSwitch(page, 'Codex conversations', 'Month').getAttribute('aria-checked'), 'false')
    assert.equal(await sourceSwitch(page, 'Email', 'Month').getAttribute('aria-checked'), 'true')
    await sourceSwitch(page, 'Codex conversations').click()
    await closeSettings(page)
    await toolbar.waitFor()
    assert.equal(await countCalls(page, 'StartConversationSync'), 0)
    assert.equal(await countCalls(page, 'GetAISyncSetup'), 0)
  } finally { await browser.close() }
})

test('switching pages keeps an explicit run alive; disabling its originating source cancels it', async () => {
  const browser = await webkit.launch()
  try {
    const page = await browser.newPage()
    await page.addInitScript(seedSyncSources)
    await page.addInitScript(() => { window.keepRunning = true })
    await page.goto(`http://127.0.0.1:${server.httpServer.address().port}`)
    await page.locator('.ai-sync-button').click()
    await settleCall(page, 'StartConversationSync')
    await closeSettings(page)
    await page.getByRole('button', { name: 'Month', exact: true }).click()
    await openSettings(page, 'AI Sync')
    await page.getByRole('button', { name: 'Sync email', exact: true }).click()
    await settleCall(page, 'StartAISync')
    await closeSettings(page)
    await page.getByRole('button', { name: 'Board', exact: true }).click()
    await openSettings(page, 'Components')
    assert.equal(await countCalls(page, 'CancelConversationSync'), 0)
    assert.equal(await countCalls(page, 'CancelAISync'), 0)
    await sourceSwitch(page, 'Codex conversations', 'Month').click()
    assert.equal(await countCalls(page, 'CancelConversationSync'), 0)
    await sourceSwitch(page, 'Codex conversations').click()
    await settleCall(page, 'CancelConversationSync')
    assert.equal(await countCalls(page, 'CancelAISync'), 0)
    await sourceSwitch(page, 'Email', 'Month').click()
    await settleCall(page, 'CancelAISync')
  } finally { await browser.close() }
})
