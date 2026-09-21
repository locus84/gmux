import { test, expect } from '@playwright/test'

// Compatibility with old runners/replay: references are consumed by xterm as
// unknown OSC, not rendered as decorations, trays, or a second image viewer.
const asset = { version: 1, action: 'put', hash: 'a'.repeat(64), id: 42, cols: 60, rows: 30, bytes: 1024, mime: 'image/png' }
const osc = (value: object) => `\x1b]777;gmux-image;${JSON.stringify(value)}\x1b\\`

for (const width of [390, 1280]) {
  test.describe(`existing viewer links only at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 }, hasTouch: width === 390 })

    test('ignores image refs without adding rows, controls, or requests', async ({ page }) => {
      let requests = 0
      await page.route('**/v1/sessions/*/images/*', route => { requests++; return route.abort() })
      await page.goto('/my-project/pi/~1h46pdtl?mock')
      await page.waitForFunction(() => (window as any).__gmuxTerm)
      const screen = await page.evaluate(async sequence => {
        const term = (window as any).__gmuxTerm
        term.reset()
        await new Promise<void>(resolve => term.write(`Viewer: /tmp/image.png\r\n${sequence}Next output`, resolve))
        return [0, 1].map(row => term.buffer.active.getLine(row).translateToString(true))
      }, osc(asset))
      expect(screen).toEqual(['Viewer: /tmp/image.png', 'Next output'])
      await expect(page.locator('.terminal-image-button, .terminal-image-tray, .terminal-image-viewer')).toHaveCount(0)
      await page.screenshot({ path: `/tmp/gmux-viewer-links-only-${width}.png` })
      expect(requests).toBe(0)
    })

    test('scrolling, resize and alternate redraws never create image rectangles', async ({ page }) => {
      await page.goto('/my-project/pi/~1h46pdtl?mock')
      await page.waitForFunction(() => (window as any).__gmuxTerm)
      await page.evaluate(async ({ put, remove }) => {
        const term = (window as any).__gmuxTerm
        const write = (text: string) => new Promise<void>(resolve => term.write(text, resolve))
        for (let i = 0; i < 10; i++) await write(`${put}line ${i}\r\n`.repeat(10))
        term.scrollToTop()
        term.scrollToBottom()
        term.resize(term.cols - 1, term.rows)
        await write(`\x1b[?1049h\x1b[?2026h${remove}${put}\x1b[?2026l\x1b[?1049l`)
      }, { put: osc(asset), remove: osc({ version: 1, action: 'delete', all: true }) })
      await expect(page.locator('.terminal-image-button, .terminal-image-tray, .terminal-image-viewer')).toHaveCount(0)
    })
  })
}
