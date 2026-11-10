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

function seedLongTask() {
  const date = new Date()
  const day = [date.getFullYear(), String(date.getMonth() + 1).padStart(2, '0'), String(date.getDate()).padStart(2, '0')].join('-')
  const task = {
    id: 7, title: 'Open calls', start: day, allDay: true, phase: 'progressing',
    description: 'Collect links', result: 'Initial shortlist', color: '#7663b5', bold: false,
    deadline: day, registrations: Array.from({length: 31}, (_, i) => ({
      name: i === 30 ? 'Last studio opportunity' : '很长的工作坊报名和作品征集活动名称 '.repeat(3) + (i + 1),
      url: 'https://events.example.test/' + i,
      deadline: day, deadlineText: '报名截止 17:00（按主办方当地时间）',
      submissionDeadline: day, submissionDeadlineText: '作品截止 23:59（按主办方当地时间）'
    }))
  }
  window.savedTasks = []
  window.openedURLs = []
  window.runtime = { BrowserOpenURL: url => window.openedURLs.push(url), WindowSetAlwaysOnTop() {} }
  window.go = {main:{
    CalendarApp:{
      GetEvents: async () => [], GetBoardEvents: async () => [task],
      UpdateEvent: async (id, input) => { window.savedTasks.push({id, color:input.color, deadline:input.deadline}); Object.assign(task, input); return task }
    },
    AISyncApp:{GetAISyncStatus:async()=>({state:'idle'}),GetConversationSync:async()=>({state:'idle'}),GetConversationActivity:async()=>[]}
  }}
}

for (const engine of [webkit, chromium]) {
  test(engine.name() + ' keeps long task details, deadlines and actions reachable at window sizes', async () => {
    const browser = await engine.launch()
    try {
      const page = await browser.newPage({viewport:{width:1000,height:680}})
      await page.addInitScript(seedLongTask)
      page.setDefaultTimeout(5000)
      await page.goto(server.resolvedUrls.local[0])
      const open = page.getByRole('button', {name:'View all 31 activities'})
      const save = page.getByRole('button', {name:'Save task',exact:true})
      for (const [width, height] of [[1000,680], [360,280]]) {
        await page.setViewportSize({width, height})
        await open.click()
        const last = page.getByRole('link', {name:'Last studio opportunity'})
        await last.waitFor()
        const pinned = async () => {
          for (const locator of [page.getByText('Edit task', {exact:true}), save, page.getByRole('button',{name:'Cancel',exact:true})]) {
            const box = await locator.boundingBox()
            assert.ok(box && box.y >= -1 && box.y+box.height<=height+1 && box.x>=-1 && box.x+box.width<=width+1, 'editor action escaped the viewport at '+width+'x'+height)
          }
        }
        await page.waitForFunction(() => document.activeElement?.getAttribute('aria-label') === 'Activity deadlines')
        await pinned()
        await last.click()
        assert.equal(await page.evaluate(() => window.openedURLs.at(-1)), 'https://events.example.test/30')
        await pinned()
        await page.getByRole('button', {name:'Show fewer',exact:true}).click()
        assert.equal(await page.locator('.registrations-field .registration').count(), 4)
        assert.equal(await page.locator('.task-heading-name').innerText(), 'Open calls')
        await page.locator('#event-deadline').fill('2026-12-01')
        await page.locator('#event-deadline').scrollIntoViewIfNeeded()
        const date = await page.locator('#event-deadline').boundingBox()
        assert.ok(date.y >= 0 && date.y+date.height <= height, 'deadline clipped')
        assert.ok(await page.locator('html').evaluate(el=>el.scrollWidth<=window.innerWidth), 'horizontal overflow')
        await pinned()
        if (width === 1000) await page.keyboard.press('Escape')
        else {
          await page.getByRole('radio',{name:'Rose',exact:true}).click()
          await save.click()
        }
        await save.waitFor({state:'hidden'})
      }
      assert.deepEqual(await page.evaluate(()=>window.savedTasks), [{id:7,color:'#ad617d',deadline:'2026-12-01'}])
    } finally { await browser.close() }
  })
}
