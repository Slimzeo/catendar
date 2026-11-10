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
    result: 'Initial shortlist', color: '#7663b5', bold: false, deadline: day,
    description: ['Collect links', ...Array.from({length: 31}, (_, i) => [
      i === 30 ? 'Last studio opportunity' : '很长的工作坊报名和作品征集活动名称 '.repeat(3) + (i + 1),
      '报名截止：17:00（按主办方当地时间）',
      '作品截止：23:59（按主办方当地时间）',
      'https://events.example.test/' + i
    ].join('\n'))].join('\n\n')
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
      // Give native scrollbars a visible width, as when an external mouse is attached.
      await page.addStyleTag({ content: '*::-webkit-scrollbar { width: 16px; height: 16px; }' })
      const open = page.getByRole('button', {name:'Open calls', exact:true})
      const save = page.getByRole('button', {name:'Save task',exact:true})
      for (const [width, height] of [[1000,680], [360,280]]) {
        await page.setViewportSize({width, height})
        await open.click()
        const description = page.locator('.description-field .event-description.full')
        const last = description.getByRole('link', {name:'https://events.example.test/30', exact:true})
        await last.waitFor()
        const pinned = async () => {
          for (const locator of [page.getByText('Edit task', {exact:true}), save, page.getByRole('button',{name:'Cancel',exact:true})]) {
            const box = await locator.boundingBox()
            assert.ok(box && box.y >= -1 && box.y+box.height<=height+1 && box.x>=-1 && box.x+box.width<=width+1, 'editor action escaped the viewport at '+width+'x'+height)
          }
        }
        assert.equal(await description.getByRole('link').count(), 31)
        assert.match(await description.innerText(), /Last studio opportunity[\s\S]*作品截止：23:59/)
        await pinned()
        const body = page.getByRole('region', { name: 'Task details' })
        const scroll = await body.evaluate(el => {
          el.scrollTop = 0
          return {
            width: el.offsetWidth - el.clientWidth,
            scrollable: el.scrollHeight > el.clientHeight,
            hidden: getComputedStyle(el).scrollbarWidth === 'none' ||
              getComputedStyle(el, '::-webkit-scrollbar').display === 'none'
          }
        })
        assert.deepEqual(scroll, { width: 0, scrollable: true, hidden: true })
        await body.focus()
        await page.keyboard.press('PageDown')
        await page.waitForFunction(() => document.querySelector('.form-body').scrollTop > 20)
        await body.evaluate(el => { el.scrollTop = 0 })
        const bounds = await body.boundingBox()
        await page.mouse.move(bounds.x + bounds.width - 7, bounds.y + 20)
        await page.mouse.wheel(0, 240)
        await page.waitForFunction(() => document.querySelector('.form-body').scrollTop > 20)
        const visibleRails = await body.evaluate(el =>
          [...el.closest('.n-modal-body-wrapper').querySelectorAll('[data-scrollbar-rail]')]
            .filter(rail => rail.getClientRects().length > 0).length)
        assert.equal(visibleRails, 0)
        await last.click()
        assert.equal(await page.evaluate(() => window.openedURLs.at(-1)), 'https://events.example.test/30')
        await pinned()
        await page.getByRole('button', { name: 'Edit description', exact: true }).click()
        assert.match(await page.locator('#event-description').inputValue(), /https:\/\/events\.example\.test\/30$/)
        await page.getByRole('button', { name: 'Preview', exact: true }).click()
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

test('the month sidebar scrolls and resizes from an invisible edge', async () => {
  const browser = await webkit.launch()
  try {
    const page = await browser.newPage({ viewport: { width: 1000, height: 600 }, reducedMotion: 'reduce' })
    await page.clock.setFixedTime(new Date(2026, 1, 1, 12))
    await page.addInitScript(seedLongTask)
    await page.goto(server.resolvedUrls.local[0])
    await page.evaluate(() => {
      window.go.main.CalendarApp.GetEvents = async () => Array.from({ length: 30 }, (_, index) => ({
        id: index + 1, title: 'Day event ' + (index + 1), start: '2026-02-01',
        allDay: true, phase: 'todo', color: '#7663b5', description: '', bold: false
      }))
    })
    await page.getByRole('button', { name: 'Month', exact: true }).click()
    const day = page.getByRole('button', { name: /February 1, 2026, 30 events/ })
    await day.focus()
    await page.keyboard.press('Enter')
    const list = page.getByRole('region', { name: 'Day events' })
    await list.waitFor()
    assert.equal(await list.evaluate(el => getComputedStyle(el).scrollbarWidth === 'none' ||
      getComputedStyle(el, '::-webkit-scrollbar').display === 'none'), true)
    await list.focus()
    await page.keyboard.press('PageDown')
    await page.waitForFunction(() => document.querySelector('.events-list').scrollTop > 0)
    const panel = page.locator('.n-drawer').filter({has:list})
    const edge = panel.locator('.n-drawer__resize-trigger')
    await edge.hover()
    const edgeStyle = await edge.evaluate(el => getComputedStyle(el).backgroundColor)
    assert.match(edgeStyle, /transparent|rgba\(0, 0, 0, 0\)/)
    const [initial, handle] = await Promise.all([panel.boundingBox(), edge.boundingBox()])
    const x = handle.x + handle.width / 2
    const y = handle.y + handle.height / 2
    await page.mouse.move(x, y)
    await page.mouse.down()
    await page.mouse.move(x + 56, y, { steps: 4 })
    await page.mouse.up()
    await page.waitForFunction(w => document.querySelector('.n-drawer').getBoundingClientRect().width >= w + 40, initial.width)
  } finally { await browser.close() }
})
