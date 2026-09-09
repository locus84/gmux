import { test, expect } from '@playwright/test'
import { openApp, gotoTestSession } from '../helpers'

test('OSC 8 labels and plain URLs open through the configured proxy', async ({ page }) => {
  await page.route('**/v1/frontend-config', route => route.fulfill({
    json: { data: { settings: { vsCodeServerUrl: 'https://code.example.test/' } } },
  }))
  await openApp(page)
  await gotoTestSession(page)
  await page.evaluate(async () => {
    const term = (window as any).__gmuxTerm
    ;(window as any).__openedLinks = []
    window.open = ((url: string) => { (window as any).__openedLinks.push(url); return null }) as typeof window.open
    term.reset()
    await new Promise<void>(resolve => term.write(
      '\x1b]8;;http://127.0.0.1:18080/proxy/3000/?v=scrollbar-startup\x07WebGL 확인\x1b]8;;\x07\r\nhttp://localhost:3000/preview', resolve,
    ))
  })
  const screen = page.locator('.xterm-screen')
  const box = (await screen.boundingBox())!
  const { cols, rows } = await page.evaluate(() => {
    const t = (window as any).__gmuxTerm
    return { cols: t.cols, rows: t.rows }
  })
  for (const row of [0, 1]) {
    await page.mouse.click(box.x + box.width / cols * 2.5, box.y + box.height / rows * (row + 0.5))
  }
  await expect.poll(() => page.evaluate(() => (window as any).__openedLinks)).toEqual([
    'https://code.example.test/proxy/18080/proxy/3000/?v=scrollbar-startup',
    'https://code.example.test/proxy/3000/preview',
  ])
})
