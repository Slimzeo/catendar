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

function seedRegistrations() {
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
  const orbitRegistrations = [
    {
      name: 'Orbit Jam registration',
      url: 'https://events.example.test/orbit-jam/register',
      deadline: '2026-02-06',
      deadlineText: 'Fri, Feb 6, 6:00 PM'
    },
    { name: 'Roster form', url: 'https://events.example.test/orbit-jam/roster' },
    { name: 'Travel notes', deadline: '2026-02-02' },
    { name: 'Announcement thread' },
    { name: 'Unsafe signup', url: 'javascript:alert(1)', deadlineText: 'Not a real deadline' }
  ]
  let rows = [
    row({ id: 1, title: 'Orbit Jam', registrations: orbitRegistrations }),
    row({
      id: 2,
      title: 'Poster drop',
      phase: 'progressing',
      registrations: [{
        name: 'Poster entry',
        url: 'https://events.example.test/poster/entry',
        deadline: '2026-02-09',
        deadlineText: 'Mon, Feb 9'
      }]
    }),
    row({
      id: 3,
      title: 'Lineup follow-up',
      phase: 'done',
      result: 'Shared',
      registrations: [{ name: 'Orbit Jam lineup' }]
    })
  ]
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
      description: 'My original notes',
      registrations: [
        {
          name: 'Current Orbit Jam signup',
          url: 'https://events.example.test/orbit-jam/current',
          deadline: '2026-02-05',
          deadlineText: 'Thu, Feb 5'
        },
        { name: 'Current travel note' }
      ]
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
      description: 'My original notes',
      result: '',
      registrations: [
        {
          name: 'Orbit Jam registration',
          url: 'https://events.example.test/orbit-jam/register',
          deadline: '2026-02-06',
          deadlineText: 'Fri, Feb 6, 6:00 PM'
        },
        { name: 'Badge pickup', deadline: '2026-02-07' },
        { name: 'Lineup note' },
        { name: 'Unsafe signup', url: 'javascript:alert(1)' }
      ],
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
      GetConversationActivity: async () => [{
        ...activity,
        current: { ...activity.current, registrations: activity.current.registrations.map(item => ({ ...item })) },
        change: {
          ...activity.change,
          source: { ...activity.change.source },
          registrations: activity.change.registrations.map(item => ({ ...item }))
        }
      }],
      ResolveConversationChange: async () => activity
    }
  } }
}

test('board cards and the task editor show registration labels, deadlines, and unknown values without opening giant URLs', async () => {
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    page.setDefaultTimeout(5000)
    await page.addInitScript(seedRegistrations)
    await page.goto(`http://127.0.0.1:${server.httpServer.address().port}`)

    const card = id => page.locator(`article[data-task-id="${id}"]`)
    await card(1).waitFor()
    await card(2).waitFor()
    await card(3).waitFor()

    const compactRows = card(1).locator('.registration')
    assert.equal(await compactRows.count(), 3)
    assert.equal((await compactRows.nth(0).locator('.registration-deadline').innerText()).trim(), 'Due Fri, Feb 6, 6:00 PM')
    assert.equal((await compactRows.nth(1).locator('.registration-deadline').innerText()).trim(), 'Deadline unknown')
    assert.equal((await compactRows.nth(2).locator('.registration-deadline').innerText()).trim(), 'Due 2026-02-02')
    assert.equal((await card(1).locator('.registration-more').innerText()).trim(), '+2 more in the editor')
    assert.equal(await card(1).locator('.registration-link').count(), 2)
    assert.equal(
      await card(1).locator('.registration-link').first().getAttribute('href'),
      'https://events.example.test/orbit-jam/register'
    )
    assert.equal(await card(1).getByRole('link', { name: 'Roster form', exact: true }).count(), 1)
    assert.equal(
      await card(2).getByRole('link', { name: 'Poster entry', exact: true }).getAttribute('href'),
      'https://events.example.test/poster/entry'
    )
    assert.match(await card(2).innerText(), /Due Mon, Feb 9/)
    assert.equal((await page.locator('.board').innerText()).includes('events.example.test'), false)

    const unknown = card(3).locator('.registration')
    assert.equal(await unknown.count(), 1)
    assert.equal((await unknown.locator('.registration-name').innerText()).trim(), 'Orbit Jam lineup')
    assert.equal((await unknown.locator('.registration-deadline').innerText()).trim(), 'Deadline unknown')
    assert.equal(await card(3).locator('.registration-link').count(), 0)

    const link = card(1).getByRole('link', { name: 'Orbit Jam registration', exact: true })
    await link.click()
    assert.deepEqual(
      await page.evaluate(() => window.openedUrls),
      ['https://events.example.test/orbit-jam/register']
    )
    assert.equal(await page.locator('#event-title').count(), 0, 'a registration link must not open the editor')

    // Arrow shortcuts stay with the link instead of moving board focus.
    await link.focus()
    await page.keyboard.press('ArrowDown')
    assert.equal(await link.evaluate(element => element === document.activeElement), true)
    assert.equal(await card(1).getAttribute('data-selected'), 'true')
    assert.equal(await page.locator('article[data-selected="true"]').count(), 1)

    // The editor exposes the full verified list, including link-only and deadline-only rows.
    await card(1).locator('.edit-title').click()
    await page.locator('#event-title').waitFor()
    assert.equal(await page.locator('#event-description').inputValue(), '')
    const editor = page.locator('.registrations-field')
    await editor.waitFor()
    assert.equal(await editor.locator('.registration').count(), 5)
    assert.match(await editor.innerText(), /Orbit Jam registration/)
    assert.match(await editor.innerText(), /Due Fri, Feb 6, 6:00 PM/)
    assert.match(await editor.innerText(), /Roster form[\s\S]*Deadline unknown/)
    assert.match(await editor.innerText(), /Travel notes[\s\S]*Due 2026-02-02/)
    assert.match(await editor.innerText(), /Announcement thread[\s\S]*Deadline unknown/)
    assert.match(await editor.innerText(), /Unsafe signup/)
    assert.equal(await editor.locator('.registration-link').count(), 2)
    assert.equal(await editor.locator('a[href^="javascript"]').count(), 0)
    assert.equal(await editor.locator('.registration-more').count(), 0)

    await editor.getByRole('link', { name: 'Roster form', exact: true }).click()
    assert.deepEqual(
      await page.evaluate(() => window.openedUrls.at(-1)),
      'https://events.example.test/orbit-jam/roster'
    )
    assert.equal(await page.locator('#event-title').isVisible(), true)

    await page.getByRole('button', { name: 'Save task', exact: true }).click()
    await page.locator('#event-title').waitFor({ state: 'hidden' })
    const update = await page.evaluate(() => window.eventUpdates.at(-1))
    assert.equal(update.description, '')
    assert.equal(Object.prototype.hasOwnProperty.call(update, 'registrations'), false)
    await card(1).getByRole('link', { name: 'Orbit Jam registration', exact: true }).waitFor()
  } finally {
    await browser.close()
  }
})

test('pending AI details show proposed and current registrations with safe links', async () => {
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    page.setDefaultTimeout(5000)
    await page.addInitScript(seedRegistrations)
    await page.goto(`http://127.0.0.1:${server.httpServer.address().port}`)

    await page.locator('article[data-task-id="1"]').waitFor()
    await page.locator('.ai-sync-button').click()
    await page.getByRole('heading', { name: 'Codex conversations' }).waitFor()
    await page.waitForFunction(() => window.conversationStarts === 1)

    const activity = page.locator('.conversation-panel [data-activity-id="21"]')
    await activity.waitFor()
    const current = activity.locator('.current-card')
    const proposed = activity.locator('.change-registrations')

    assert.match(await current.innerText(), /Current Orbit Jam signup/)
    assert.match(await current.innerText(), /Due Thu, Feb 5/)
    assert.match(await current.innerText(), /Current travel note[\s\S]*Deadline unknown/)
    assert.equal(await current.locator('.registration-link').count(), 1)

    assert.match(await activity.innerText(), /Proposed registrations/i)
    assert.match(await proposed.innerText(), /Orbit Jam registration/)
    assert.match(await proposed.innerText(), /Due Fri, Feb 6, 6:00 PM/)
    assert.match(await proposed.innerText(), /Badge pickup[\s\S]*Due 2026-02-07/)
    assert.match(await proposed.innerText(), /Lineup note[\s\S]*Deadline unknown/)
    assert.match(await proposed.innerText(), /Unsafe signup/)
    assert.equal(await proposed.locator('.registration-link').count(), 1)
    assert.equal(
      await proposed.locator('.registration-link').getAttribute('href'),
      'https://events.example.test/orbit-jam/register'
    )
    assert.equal(await proposed.locator('a[href^="javascript"]').count(), 0)

    await proposed.getByRole('link', { name: 'Orbit Jam registration', exact: true }).click()
    assert.deepEqual(
      await page.evaluate(() => window.openedUrls),
      ['https://events.example.test/orbit-jam/register']
    )
    await page.getByRole('heading', { name: 'Codex conversations' }).waitFor()
    await activity.getByRole('button', { name: 'Apply Orbit Jam planning' }).waitFor()
  } finally {
    await browser.close()
  }
})
