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

function seedDescriptions() {
  const now = new Date()
  const day = [now.getFullYear(), String(now.getMonth() + 1).padStart(2, '0'), String(now.getDate()).padStart(2, '0')].join('-')
  const row = input => ({
    start: `${day}T00:00:00`,
    end: `${day}T00:00:00`,
    allDay: true,
    phase: 'todo',
    description: '',
    result: '',
    color: '#7663b5',
    bold: false,
    ...input
  })
  const notes = [
    'My original notes',
    'Orbit Jam\nRegistration deadline: Fri, Feb 6, 6:00 PM\nSubmission deadline: Feb 10, 7:00 PM\nhttps://events.example.test/orbit',
    'Roster form\nhttps://events.example.test/roster',
    'Unsafe signup\njavascript:alert(1)\nhttps://guest:password@events.example.test/private'
  ].join('\n\n')
  const rows = [row({ id: 1, title: 'Orbit Jam', description: notes })]
  const idleStatus = {
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
  const activity = {
    id: 21,
    current: row({
      id: 7,
      title: 'Orbit Jam planning',
      phase: 'progressing',
      description: notes
    }),
    day,
    state: 'pending',
    reason: 'The registration details changed.',
    beforePhase: 'progressing',
    change: {
      source: {
        id: 'm21',
        threadId: 't21',
        threadTitle: 'Orbit Jam planning',
        at: 1762710000,
        quote: 'Registration closes Friday and badges need a deadline.'
      },
      taskKey: 'orbit-1',
      eventId: 7,
      title: 'Orbit Jam planning',
      phase: 'progressing',
      description: notes + '\n\nBadge pickup\nRegistration deadline: Feb 7\nhttps://events.example.test/badges',
      result: '',
      needsReview: true,
      baseUpdated: '2026-11-09T07:00:00Z'
    }
  }
  let status = { ...idleStatus }
  window.openedUrls = []
  window.runtime = {
    BrowserOpenURL: url => window.openedUrls.push(url),
    WindowSetAlwaysOnTop: () => {}
  }
  window.eventUpdates = []
  window.conversationStarts = 0
  window.go = { main: {
    CalendarApp: {
      GetEvents: async () => [],
      GetBoardEvents: async () => rows.map(item => ({ ...item })),
      UpdateEvent: async (id, input) => {
        window.eventUpdates.push({ id, ...input })
        const target = rows.find(item => item.id === id)
        Object.assign(target, input)
        return { ...target }
      }
    },
    AISyncApp: {
      GetAISyncStatus: async () => ({
        state: 'idle', emailCount: 0, readEmails: 0, unreadableEmails: 0,
        created: 0, skipped: 0, rejected: 0, conflicts: 0
      }),
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
      SaveEmailAccount: async input => ({ id: 'primary', ...input, configured: true, hasCredential: Boolean(input.secret) }),
      SaveAISettings: async () => {},
      TestEmailAccount: async () => {},
      StartAISync: async () => ({
        state: 'idle', emailCount: 0, readEmails: 0, unreadableEmails: 0,
        created: 0, skipped: 0, rejected: 0, conflicts: 0
      }),
      CancelAISync: async () => true,
      GetSourceEmail: async () => ({
        id: 'm21',
        messageId: 'm21',
        subject: '',
        sender: '',
        receivedAt: '',
        sourceUrl: '',
        text: ''
      }),
      GetConversationSync: async () => ({ ...status }),
      StartConversationSync: async () => {
        window.conversationStarts++
        status = {
          ...status,
          state: 'succeeded',
          message: 'Conversation review complete',
          runId: 'run-registrations',
          sources: 1,
          pending: 1
        }
        return { ...status }
      },
      CancelConversationSync: async () => {
        status = { ...status, state: 'cancelled' }
        return true
      },
      SaveConversationSyncSettings: async codexPath => {
        status = { ...status, codexPath }
        return { ...status }
      },
      GetConversationActivity: async () => [structuredClone(activity)],
      ResolveConversationChange: async (_, apply) => {
        activity.state = apply ? 'applied' : 'dismissed'
        return structuredClone(activity)
      }
    }
  } }
}

test('one editable description owns all notes, safe links and deadlines, including manual removal', async () => {
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    await page.addInitScript(seedDescriptions)
    await page.goto(server.resolvedUrls.local[0])
    const title = page.getByRole('button', {name:'Orbit Jam', exact:true})
    await title.click()
    const preview = page.locator('.description-field .event-description.full')
    assert.match(await preview.innerText(), /Registration deadline: Fri, Feb 6[\s\S]*Submission deadline: Feb 10/)
    assert.equal(await preview.getByRole('link').count(), 2)
    await preview.getByRole('link', {name:'https://events.example.test/orbit'}).click()
    assert.deepEqual(await page.evaluate(() => window.openedUrls), ['https://events.example.test/orbit'])
    assert.equal(await page.getByText(/^Registrations(?: |$)/).count(), 0)
    await page.getByRole('button', {name:'Edit description'}).click()
    const input = page.locator('#event-description')
    const original = await input.inputValue()
    const edited = original.replace('Roster form\nhttps://events.example.test/roster', 'My call notes')
    await input.fill(edited)
    const save = page.getByRole('button', {name:'Save task',exact:true})
    await save.click()
    await save.waitFor({state:'hidden'})
    await title.focus()
    await page.keyboard.press('u')
    await input.waitFor()
    assert.equal(await input.inputValue(), edited)
    await input.fill('')
    await save.click()
    await save.waitFor({state:'hidden'})
    await title.click()
    assert.equal(await input.inputValue(), '')
    const updates = await page.evaluate(() => window.eventUpdates)
    assert.deepEqual(updates.map(row=>row.description), [edited, ''])
    assert.ok(updates.every(row=>!Object.hasOwn(row, 'registrations')))
  } finally { await browser.close() }
})

test('conversation review compares the same full description that is saved', async () => {
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    await page.addInitScript(seedDescriptions)
    await page.goto(server.resolvedUrls.local[0])
    await page.locator('.ai-sync-button').click()
    const activity = page.locator('.conversation-panel [data-activity-id="21"]')
    const current = activity.locator('.current-card')
    const proposed = activity.locator('.change-description')
    assert.match(await current.innerText(), /My original notes[\s\S]*Roster form/)
    assert.doesNotMatch(await current.innerText(), /Badge pickup/)
    assert.match(await proposed.innerText(), /Badge pickup[\s\S]*Registration deadline: Feb 7/)
    assert.equal(await activity.getByText('Proposed registrations').count(), 0)
    assert.equal(await proposed.getByRole('link').count(), 3)
    await proposed.getByRole('link', {name:'https://events.example.test/badges'}).click()
    assert.deepEqual(await page.evaluate(() => window.openedUrls), ['https://events.example.test/badges'])
    await activity.getByRole('button', {name:'Keep current Orbit Jam planning'}).waitFor()
    await activity.getByRole('button', {name:'Apply Orbit Jam planning'}).click()
    await activity.getByRole('button', {name:'Apply Orbit Jam planning'}).waitFor({state:'hidden'})
  } finally { await browser.close() }
})
