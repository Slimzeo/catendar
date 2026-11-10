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

function seedOrderedBoard() {
  const day = '2026-02-01'
  let rows = JSON.parse(localStorage.getItem('fixture-rows') || 'null') ??
    [1,2,3,4,5,6].map(id => ({id, title: 'Studio task '+id, phase: id<5?'todo':'progressing', start:day, allDay:true, description:'', result:'', color:'#687386', bold:false}))
  window.priorityWrites = []
  window.phaseWrites = []
  window.go = {main:{
    CalendarApp:{
      GetEvents: async () => [], GetBoardEvents: async () => rows.map(row => ({...row})),
      ReorderBoardEvent: async (date,phase,id,before) => {
        window.priorityWrites.push([date,phase,id,before])
        if (window.failNextMove) { window.failNextMove=false; throw new Error('The card has moved') }
        const task = rows.find(row=>row.id===id && row.phase===phase)
        rows = rows.filter(row=>row.id!==id)
        rows.splice(before ? rows.findIndex(row=>row.id===before) : rows.length, 0, task)
        localStorage.setItem('fixture-rows', JSON.stringify(rows))
      },
      SetBoardEventPhase: async (id,phase,date) => {
        window.phaseWrites.push([id,phase,date])
        const row = rows.find(event=>event.id===id)
        row.phase=phase
        localStorage.setItem('fixture-rows',JSON.stringify(rows))
        return row
      }
    },
    AISyncApp:{GetAISyncStatus:async()=>({state:'idle'}),GetConversationSync:async()=>({state:'idle'}),GetConversationActivity:async()=>[]}
  }}
}

for (const engine of [webkit, chromium]) {
  test(engine.name() + ' preserves drop placement, whole-card tint, selection, reload and phase drags', async () => {
    const browser = await engine.launch()
    try {
      const page = await browser.newPage({viewport:{width:1060,height:700},reducedMotion:'reduce'})
      await page.clock.setFixedTime(new Date(2026,1,1,12))
      page.setDefaultTimeout(4500)
      await page.addInitScript(seedOrderedBoard)
      await page.goto(server.resolvedUrls.local[0])
      const column=phase=>page.locator('section[data-phase="'+phase+'"]')
      const card=id=>page.locator('article[data-task-id="'+id+'"]')
      const ordered = async (phase, ids) => {
        await page.waitForFunction(({phase, ids}) => Array.from(document.querySelectorAll('section[data-phase="'+phase+'"] article[data-task-id]')).map(el=>Number(el.dataset.taskId)).join(',')===ids.join(','), {phase,ids})
      }
      const tint = id=>card(id).evaluate(el=>getComputedStyle(el).backgroundColor)
      const rail = id=>card(id).evaluate(el=>getComputedStyle(el,'::before').backgroundColor)
      const drag = async (from,target,at) => {
        await card(from).locator('.edit-title').dragTo(target, { targetPosition: at })
      }

      await ordered('todo',[1,2,3,4])
      const firstTint=await tint(1), lastTint=await tint(4)
      assert.notEqual(firstTint,lastTint,'first and last have the same tint')
      const originalRail=await rail(4)
      await drag(4,card(1),{x:30,y:3})
      await ordered('todo',[4,1,2,3])
      assert.equal(await tint(4), firstTint)
      assert.equal(await page.locator('.drag-grip, .rank-first').count(), 0)
      assert.equal(await card(1).locator('h3').innerText(), 'Studio task 1')
      assert.equal(await rail(4), originalRail)
      assert.equal(await card(4).getAttribute('data-selected'),'true')
      await ordered('progressing',[5,6])
      assert.deepEqual(await page.evaluate(()=>window.phaseWrites),[])
      assert.deepEqual(await page.evaluate(()=>window.priorityWrites.at(-1)),['2026-02-01','todo',4,1])

      await page.keyboard.press('Alt+ArrowDown')
      await ordered('todo',[1,4,2,3])
      const bottom = await card(3).boundingBox()
      await drag(1,card(3),{x:30,y:Math.max(5,Math.floor(bottom.height)-3)})
      await ordered('todo',[4,2,3,1])
      assert.deepEqual(await page.evaluate(()=>window.priorityWrites.at(-1)),['2026-02-01','todo',1,0])
      await page.reload()
      await ordered('todo',[4,2,3,1])

      const swatch = column('todo').getByRole('button',{name:/colou?r|palette/i}).first()
      const unchanged = await tint(5)
      await swatch.click()
      const rose = page.getByRole('radio',{name:/rose/i}).or(page.getByRole('button',{name:/^rose$/i})).first()
      await rose.click()
      await page.waitForFunction(({old})=>getComputedStyle(document.querySelector('section[data-phase="todo"] article')).backgroundColor!==old,{old:firstTint})
      const changed = await tint(4)
      assert.equal(await tint(5), unchanged)
      assert.equal(await rail(4), originalRail)
      await page.reload()
      await ordered('todo',[4,2,3,1])
      assert.equal(await tint(4),changed)

      await card(2).locator('.edit-title').focus()
      await page.evaluate(()=>{window.failNextMove=true})
      await page.keyboard.press('Alt+ArrowUp')
      await page.waitForFunction(()=>window.priorityWrites.length===1)
      await ordered('todo',[4,2,3,1])
      assert.equal(await card(2).getAttribute('data-selected'),'true')
      await drag(2,column('done'),{x:90,y:165})
      await ordered('done',[2])
      await ordered('todo',[4,3,1])
      assert.deepEqual(await page.evaluate(()=>window.phaseWrites.at(-1)),[2,'done','2026-02-01'])
    } finally { await browser.close() }
  })
}

test('the whole surface fades evenly down each column across both themes and the full palette', async () => {
  const browser = await webkit.launch()
  try {
    const page = await browser.newPage({ viewport: { width: 1000, height: 680 }, reducedMotion: 'reduce' })
    await page.addInitScript(seedOrderedBoard)
    await page.goto(server.resolvedUrls.local[0])
    await page.evaluate(() => {
      localStorage.setItem('fixture-rows', JSON.stringify(Array.from({ length: 18 }, (_, i) => ({
        id: i + 1, title: 'Studio task ' + (i + 1), start: '2026-02-01', phase: ['todo','progressing','done'][Math.floor(i / 6)],
        allDay: true, description: 'https://studio.example/apply', result: '', color: '#687386', bold: false
      }))))
    })
    for (const theme of ['light', 'dark']) {
      await page.evaluate(value => localStorage.setItem('catendar-theme', value), theme)
      await page.reload()
      await page.locator('article[data-task-id="18"]').waitFor({ state: 'attached' })
      const result = await page.evaluate(() => {
        const ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true })
        function luminance(color) {
          ctx.fillStyle = color
          ctx.fillRect(0, 0, 1, 1)
          const values = [...ctx.getImageData(0, 0, 1, 1).data].slice(0, 3).map(v => {
            const c = v / 255
            return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
          })
          return 0.2126 * values[0] + 0.7152 * values[1] + 0.0722 * values[2]
        }
        const lightness = y => y > 0.008856 ? 116 * Math.cbrt(y) - 16 : 903.3 * y
        const samples = []
        for (const column of document.querySelectorAll('section[data-phase]')) {
          const cards = [...column.querySelectorAll('article[data-task-id]')]
          for (const hue of ['#7663b5', '#5968b8', '#3f83b8', '#2f8a8c', '#3d8a70', '#66844f', '#ad762d', '#bd6758', '#ad617d', '#687386']) {
            column.style.setProperty('--column-hue', hue)
            const ys = cards.map(card => luminance(getComputedStyle(card).backgroundColor))
            const levels = ys.map(lightness)
            const steps = levels.slice(1).map((level, i) => level - levels[i])
            const inks = cards.flatMap((card, i) => ['.edit-title', '.phase-trigger', '.card-description a'].map(selector => {
              const ink = luminance(getComputedStyle(card.querySelector(selector)).color)
              return (Math.max(ys[i], ink) + .05) / (Math.min(ys[i], ink) + .05)
            }))
            samples.push({ phase: column.dataset.phase, hue, steps, contrast: Math.min(...inks) })
          }
        }
        return samples
      })
      assert.equal(await page.locator('.drag-grip, .rank-first, [aria-label^="Priority "]').count(), 0)
      for (const { phase, hue, steps, contrast } of result) {
        const context = theme + ' ' + phase + ' ' + hue
        const gaps = steps.map(d => theme === 'light' ? d : -d)
        assert.ok(Math.min(...gaps) >= 2, context + ' insufficient fade ' + gaps)
        assert.ok(Math.max(...gaps) / Math.min(...gaps) < 1.7, context + ' uneven fade ' + gaps)
        assert.ok(contrast >= 4.5, context + ' text contrast ' + contrast)
      }
    }
  } finally { await browser.close() }
})
