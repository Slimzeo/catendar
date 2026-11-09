import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { chromium } from 'playwright-core'
import { preview } from 'vite'

let server
before(async () => {
  server = await preview({ preview: { host: '127.0.0.1', port: 0 }, logLevel: 'silent' })
})
after(async () => {
  if (server) await new Promise(resolve => server.httpServer.close(resolve))
})

function seedConversationSync() {
  const now = new Date()
  const day = [now.getFullYear(), String(now.getMonth() + 1).padStart(2, '0'), String(now.getDate()).padStart(2, '0')].join('-')
  const workspaces = [
    { id: 1, name: 'Daily Routine', hint: '' },
    { id: 2, name: 'Seekino', hint: 'Launch planning' },
    { id: 3, name: '个人 IP 打造', hint: 'Content' }
  ]
  const row = input => ({
    start: `${day}T00:00:00`,
    end: `${day}T00:00:00`,
    allDay: true,
    phase: 'todo',
    description: '',
    result: '',
    color: '#7663b5',
    bold: false,
    workspaceId: 1,
    ...input
  })
  let boardRows = [
    row({ id: 7, title: 'Follow up with Mina', workspaceId: 2 }),
    row({ id: 8, title: 'Old brief', workspaceId: 3 }),
    row({ id: 9, title: 'Publish checklist', workspaceId: 1 })
  ]
  const copyActivity = activity => ({
    ...activity,
    change: { ...activity.change, source: { ...activity.change.source } }
  })
  const activities = [
    {
      id: 11,
      current: { ...boardRows[0], phase: 'progressing', description: 'My original review notes' },
      day,
      state: 'pending',
      reason: 'Task changed since the conversation.',
      beforePhase: 'progressing',
      change: {
        source: {
          id: 'm1',
          threadId: 't1',
          threadTitle: 'Seekino weekly',
          at: 1762700000,
          quote: 'The launch checklist moved to Friday.'
        },
        taskKey: 'seekino-1',
        eventId: 7,
        workspaceId: 2,
        title: 'Plan launch review',
        phase: 'done',
        description: 'Full proposed description line one.\nFull proposed description line two.',
        result: 'Full proposed result line one.\nFull proposed result line two.',
        needsReview: true,
        baseUpdated: '2026-11-09T07:00:00Z'
      }
    },
    {
      id: 12,
      day,
      state: 'pending',
      reason: 'The phase changed after the conversation.',
      beforePhase: 'todo',
      change: {
        source: {
          id: 'm2',
          threadId: 't2',
          threadTitle: 'IP notes',
          at: 1762710000,
          quote: 'Archive the old brief before the next draft.'
        },
        taskKey: 'ip-1',
        eventId: 8,
        workspaceId: 3,
        title: 'Archive old brief',
        phase: 'progressing',
        description: '',
        result: '',
        needsReview: true,
        baseUpdated: '2026-11-09T07:10:00Z'
      }
    },
    {
      id: 13,
      day,
      state: 'pending',
      reason: 'This proposal no longer matches the task.',
      beforePhase: 'todo',
      staleAccept: true,
      change: {
        source: {
          id: 'm4',
          threadId: 't4',
          threadTitle: 'Routine catch-up',
          at: 1762715000,
          quote: 'Finish the publish checklist before noon.'
        },
        taskKey: 'routine-2',
        eventId: 9,
        workspaceId: 1,
        title: 'Stale proposal',
        phase: 'done',
        description: '',
        result: '',
        needsReview: true,
        baseUpdated: '2026-11-09T07:20:00Z'
      }
    },
    {
      id: 10,
      day,
      state: 'applied',
      reason: 'Applied by conversation sync.',
      beforePhase: 'todo',
      change: {
        source: {
          id: 'm3',
          threadId: 't3',
          threadTitle: 'Routine catch-up',
          at: 1762690000,
          quote: 'Move the publish checklist to done today.'
        },
        taskKey: 'routine-1',
        eventId: 9,
        workspaceId: 1,
        title: 'Draft publish checklist',
        phase: 'done',
        description: '',
        result: '',
        needsReview: false,
        baseUpdated: '2026-11-09T06:30:00Z'
      }
    }
  ]
  const idleStatus = {
    state: 'idle',
    message: '',
    codexPath: '',
    runId: '',
    lastSyncedAt: '2026-11-08T08:00:00Z',
    sources: 0,
    created: 0,
    updated: 0,
    pending: 0,
    skipped: 0
  }
  let status = { ...idleStatus }
  let nextEventId = 100
  window.conversationCalls = []
  window.emailSaves = []
  window.aiSettingsSaves = []
  window.emailStarts = []
  window.go = { main: {
    CalendarApp: {
      ListWorkspaces: async () => workspaces.map(item => ({ ...item })),
      GetEvents: async () => boardRows.map(item => ({ ...item })),
      GetBoardEvents: async () => boardRows.map(item => ({ ...item })),
      CreateEvent: async input => {
        const created = row({ ...input, id: nextEventId++, result: input.result || '' })
        boardRows.push(created)
        return created
      },
      UpdateEvent: async (id, input) => {
        const updated = boardRows.find(item => item.id === id)
        Object.assign(updated, input)
        return { ...updated }
      },
      SetBoardEventPhase: async (id, phase) => {
        const updated = boardRows.find(item => item.id === id)
        updated.phase = phase
        return { ...updated }
      },
      DeleteEvent: async () => {},
      CreateWorkspace: async (name, hint) => ({ id: 4, name, hint }),
      UpdateWorkspace: async (id, name, hint) => ({ id, name, hint })
    },
    AISyncApp: {
      GetAISyncStatus: async () => ({ state: 'idle', emailCount: 0, readEmails: 0, unreadableEmails: 0, created: 0, skipped: 0, rejected: 0, conflicts: 0 }),
      GetAISyncSetup: async () => ({
        email: {
          id: 'primary',
          address: '',
          username: '',
          imapHost: '',
          imapPort: 993,
          folder: 'INBOX',
          useTLS: true,
          configured: false,
          hasCredential: false
        },
        agent: {
          settings: { provider: 'codex', codexPath: '', claudePath: '' },
          agents: [
            { provider: 'codex', installed: false, path: '', error: 'Desktop runtime required' },
            { provider: 'claude', installed: false, path: '', error: 'Desktop runtime required' }
          ]
        }
      }),
      SaveEmailAccount: async input => {
        window.emailSaves.push({ ...input })
        return { id: 'primary', ...input, configured: true, hasCredential: Boolean(input.secret) }
      },
      SaveAISettings: async input => {
        window.aiSettingsSaves.push({ ...input })
      },
      TestEmailAccount: async () => {},
      StartAISync: async () => {
        window.emailStarts.push('start')
        return { state: 'running', emailCount: 0, readEmails: 0, unreadableEmails: 0, created: 0, skipped: 0, rejected: 0, conflicts: 0 }
      },
      CancelAISync: async () => true,
      GetSourceEmail: async () => ({
        id: 'm1',
        messageId: 'm1',
        subject: '',
        sender: '',
        receivedAt: '',
        sourceUrl: '',
        text: ''
      }),
      GetConversationSync: async () => ({ ...status }),
      StartConversationSync: async () => {
        window.conversationCalls.push('start')
        if (status.state !== 'running') {
          status = {
            ...status,
            state: 'running',
            message: 'Reading recent conversations…',
            runId: 'run-2',
            sources: 4,
            pending: activities.filter(item => item.state === 'pending').length
          }
        }
        return { ...status }
      },
      CancelConversationSync: async () => {
        window.conversationCalls.push('cancel')
        status = { ...status, state: 'cancelled', message: 'Sync cancelled' }
        boardRows.push(row({ id: 99, title: 'Created during sync', workspaceId: 1 }))
        return true
      },
      SaveConversationSyncSettings: async codexPath => {
        window.conversationCalls.push(['save', codexPath])
        status = { ...status, codexPath }
        return { ...status }
      },
      GetConversationActivity: async () => activities.map(copyActivity),
      ResolveConversationChange: async (id, accept) => {
        window.conversationCalls.push(['resolve', id, accept])
        const activity = activities.find(item => item.id === id)
        if (!activity) throw new Error('Unknown conversation change')
        if (accept && activity.staleAccept) {
          activity.state = 'dismissed'
          activity.reason = 'The task changed after the conversation; nothing was applied.'
        } else {
          activity.state = accept ? 'applied' : 'dismissed'
          activity.reason = accept ? 'Applied from the conversation.' : 'Kept the current task.'
          if (accept) {
            boardRows = boardRows.map(item => item.id === activity.change.eventId
              ? {
                  ...item,
                  title: activity.change.title,
                  phase: activity.change.phase,
                  workspaceId: activity.change.workspaceId,
                  description: activity.change.description,
                  result: activity.change.result
                }
              : item)
          }
        }
        const pending = Math.max(0, status.pending - 1)
        status = {
          ...status,
          pending,
          message: pending
            ? `${pending} conversation change${pending === 1 ? '' : 's'} need review`
            : 'Conversation review complete'
        }
        return copyActivity(activity)
      }
    }
  } }
}

async function waitForResolve(page, id, accept) {
  await page.waitForFunction(({ id, accept }) => window.conversationCalls.some(call => (
    Array.isArray(call) && call[0] === 'resolve' && call[1] === id && call[2] === accept
  )), { id, accept })
}

async function startCount(page) {
  return page.evaluate(() => window.conversationCalls.filter(call => call === 'start').length)
}

test('toolbar AI Sync starts one manual run, leaves idle paths alone, and keeps the board keyboard usable', async () => {
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    page.setDefaultTimeout(5000)
    await page.addInitScript(seedConversationSync)
    await page.goto(`http://127.0.0.1:${server.httpServer.address().port}`)

    await page.locator('article[data-task-id="7"]').waitFor()
    assert.equal(await startCount(page), 0, 'application start must not scan conversations')

    // The gear opens the shortcuts tab without touching conversation sync.
    await page.getByRole('button', { name: 'Settings' }).click()
    await page.locator('#settings-title').waitFor()
    assert.equal(await page.getByRole('tab', { name: 'Keyboard shortcuts' }).getAttribute('aria-selected'), 'true')
    assert.equal(await startCount(page), 0, 'opening settings must not scan conversations')
    await page.keyboard.press('Escape')
    await page.locator('#settings-title').waitFor({ state: 'hidden' })

    // Date changes and project switches only reload the board.
    await page.getByRole('button', { name: 'Next day' }).click()
    await page.getByRole('button', { name: 'Previous day' }).click()
    await page.getByRole('button', { name: 'Seekino', exact: true }).click()
    await page.getByRole('button', { name: 'All', exact: true }).click()
    assert.equal(await startCount(page), 0, 'date and project changes must not scan conversations')

    // Two synchronous toolbar clicks must still create exactly one run.
    await page.locator('.ai-sync-button').evaluate(button => {
      button.click()
      button.click()
    })
    await page.getByRole('heading', { name: 'Codex conversations' }).waitFor()
    assert.equal(await page.getByRole('tab', { name: 'AI Sync' }).getAttribute('aria-selected'), 'true')
    await page.waitForFunction(() => window.conversationCalls.filter(call => call === 'start').length === 1)
    await page.waitForTimeout(100)
    assert.equal(await startCount(page), 1, 'the toolbar must not double-run')

    const panel = page.locator('.conversation-panel')
    await panel.locator('.state-pill').filter({ hasText: 'Syncing' }).waitFor()
    await panel.getByRole('button', { name: 'Cancel', exact: true }).waitFor()
    assert.equal(await panel.getByRole('switch').count(), 0, 'there is no automatic-sync toggle')
    assert.equal(await panel.getByText(/automatic/i).count(), 0)

    // A toolbar click during a run only reopens the status panel.
    await page.keyboard.press('Escape')
    await page.getByRole('heading', { name: 'Codex conversations' }).waitFor({ state: 'hidden' })
    await page.locator('.ai-sync-button').click()
    await page.getByRole('heading', { name: 'Codex conversations' }).waitFor()
    await page.waitForTimeout(100)
    assert.equal(await startCount(page), 1, 'a toolbar click during a run must not start another run')

    const runningPanel = page.locator('.conversation-panel')
    await runningPanel.getByRole('button', { name: 'Cancel', exact: true }).click()
    await page.waitForFunction(() => window.conversationCalls.includes('cancel'))
    await runningPanel.locator('.state-pill').filter({ hasText: 'Cancelled' }).waitFor()
    assert.equal(await runningPanel.getByRole('button', { name: 'Sync conversations', exact: true }).count(), 1)

    // Cancelling is terminal, so the board reload sees the applied batch.
    await page.locator('article[data-task-id="99"]').waitFor()

    await page.keyboard.press('Escape')
    await page.getByRole('heading', { name: 'Codex conversations' }).waitFor({ state: 'hidden' })
    await page.keyboard.press('a')
    const draft = page.getByRole('textbox', { name: 'New task in todo' })
    await draft.waitFor()
    await page.keyboard.press('Escape')
    await draft.waitFor({ state: 'hidden' })
  } finally {
    await browser.close()
  }
})

test('conversation review shows full proposals, saves only the Codex path, and reports genuine outcomes', async () => {
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    page.setDefaultTimeout(5000)
    // Exercise both sources together after the user enables Email on Board.
    await page.addInitScript(() => localStorage.setItem('catendar-sync-sources-v1', JSON.stringify({ board: { email: true } })))
    await page.addInitScript(seedConversationSync)
    await page.goto(`http://127.0.0.1:${server.httpServer.address().port}`)

    await page.locator('article[data-task-id="7"]').waitFor()
    await page.locator('.ai-sync-button').click()
    await page.getByRole('heading', { name: 'Codex conversations' }).waitFor()
    await page.waitForFunction(() => window.conversationCalls.filter(call => call === 'start').length === 1)

    const panel = page.locator('.conversation-panel')
    const first = panel.locator('[data-activity-id="11"]')
    await first.waitFor()
    const firstText = await first.innerText()
    assert.match(firstText, /Plan launch review/)
    assert.match(firstText, /Seekino · done/)
    assert.match(firstText, /Before progressing/)
    assert.match(firstText, /My original review notes/)
    assert.match(firstText, /Task changed since the conversation\./)
    assert.match(firstText, /The launch checklist moved to Friday\./)
    assert.match(firstText, /Seekino weekly/)

    const description = first.locator('.change-description')
    assert.equal(
      await description.innerText(),
      'Full proposed description line one.\nFull proposed description line two.'
    )
    assert.equal(await description.evaluate(element => getComputedStyle(element).webkitLineClamp), 'none')
    assert.equal(await description.evaluate(element => getComputedStyle(element).whiteSpace), 'pre-wrap')
    assert.equal(
      await first.locator('.change-result').innerText(),
      'Full proposed result line one.\nFull proposed result line two.'
    )
    assert.equal(
      await first.locator('.activity-quote').evaluate(element => getComputedStyle(element).webkitLineClamp),
      'none'
    )
    assert.equal(await panel.getByRole('combobox').count(), 0, 'provider selection belongs to email only')

    // The panel Save persists the Codex path alone; it never saves email or agent settings.
    await panel.getByPlaceholder('Auto detect').fill('/opt/homebrew/bin/codex')
    await panel.getByRole('button', { name: 'Save', exact: true }).click()
    await page.waitForFunction(() => window.conversationCalls.some(call => Array.isArray(call) && call[0] === 'save'))
    assert.deepEqual(
      await page.evaluate(() => window.conversationCalls.find(call => Array.isArray(call) && call[0] === 'save')),
      ['save', '/opt/homebrew/bin/codex']
    )
    assert.equal(await page.evaluate(() => window.emailSaves.length), 0)
    assert.equal(await page.evaluate(() => window.aiSettingsSaves.length), 0)
    assert.equal(await page.evaluate(() => window.emailStarts.length), 0)
    await page.getByRole('button', { name: 'Sync email', exact: true }).waitFor()

    await panel.getByRole('button', { name: 'Apply Plan launch review' }).click()
    await waitForResolve(page, 11, true)
    await panel.locator('[data-activity-id="11"][data-activity-state="applied"]').waitFor()
    const updatedCard = page.locator('section[data-phase="done"] article[data-task-id="7"]')
    await updatedCard.waitFor()
    assert.match(await updatedCard.innerText(), /Plan launch review/)

    await panel.getByRole('button', { name: 'Keep current Archive old brief' }).click()
    await waitForResolve(page, 12, false)
    await panel.locator('[data-activity-id="12"][data-activity-state="dismissed"]').waitFor()
    await page.locator('.n-message__content').filter({ hasText: 'Kept the current task.' }).first().waitFor()

    // Accepting a stale proposal must report the backend's genuine reason.
    await panel.getByRole('button', { name: 'Apply Stale proposal' }).click()
    await waitForResolve(page, 13, true)
    await panel.locator('[data-activity-id="13"][data-activity-state="dismissed"]').waitFor()
    await page.locator('.n-message__content')
      .filter({ hasText: 'The task changed after the conversation; nothing was applied.' })
      .first()
      .waitFor()
    assert.equal(
      await page.evaluate(() => window.conversationCalls.filter(call => Array.isArray(call) && call[0] === 'resolve').length),
      3
    )

    await panel.getByRole('button', { name: 'Cancel', exact: true }).click()
    await page.waitForFunction(() => window.conversationCalls.includes('cancel'))
    await panel.locator('.state-pill').filter({ hasText: 'Cancelled' }).waitFor()

    // No idle/background status polling after the run is terminal.
    await page.waitForTimeout(300)
    const statusCalls = await page.evaluate(() => window.conversationCalls.filter(call => call === 'status').length)
    await page.waitForTimeout(1800)
    assert.equal(
      await page.evaluate(() => window.conversationCalls.filter(call => call === 'status').length),
      statusCalls,
      'status must not be polled outside a run'
    )
  } finally {
    await browser.close()
  }
})
