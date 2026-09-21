import { test, expect } from '@playwright/test'
import { createHash } from 'node:crypto'

const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64')
const asset = { version: 1, action: 'put', hash: createHash('sha256').update(png).digest('hex'), id: 42, cols: 20, rows: 5, bytes: png.length, mime: 'image/png' }
const osc = (value: object) => `\x1b]777;gmux-image;${JSON.stringify(value)}\x1b\\`

for (const width of [390, 1280]) {
  test.describe(`on-demand terminal images at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 }, hasTouch: width === 390 })

    test('renders a reference without fetching; loads only on activation and survives resize', async ({ page }) => {
      let requests = 0
      await page.route('**/v1/sessions/*/images/*', route => {
        requests++
        return route.fulfill({ contentType: 'image/png', body: png })
      })
      await page.goto('/my-project/pi/~1h46pdtl?mock')
      await page.waitForFunction(() => (window as any).__gmuxTerm)
      const position = await page.evaluate(async value => {
        const term = (window as any).__gmuxTerm
        await new Promise<void>(resolve => term.write('\x1b[H\x1b[2J\x1b[3J', resolve))
        const before = [term.buffer.active.cursorX, term.buffer.active.cursorY]
        await new Promise<void>(resolve => term.write(`\x1b]777;gmux-image;${JSON.stringify(value)}\x1b\\`, resolve))
        return { before, after: [term.buffer.active.cursorX, term.buffer.active.cursorY] }
      }, asset)
      expect(position.after).toEqual(position.before)
      const button = page.getByRole('button', { name: /Open PNG image/ })
      await expect(button).toBeVisible()
      expect(requests).toBe(0)
      await page.evaluate(() => {
        const term = (window as any).__gmuxTerm
        term.resize(term.cols - 1, term.rows)
      })
      await expect(button).toBeVisible()
      await button.click()
      const viewer = page.getByRole('dialog', { name: 'Terminal image preview' })
      await expect(viewer.locator('img')).toBeVisible()
      await expect.poll(() => viewer.locator('img').evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(1)
      expect(requests).toBe(1)
      await page.keyboard.press('Escape')
      await expect(viewer).toHaveCount(0)
      await expect(button).toBeVisible()
    })

    test('keeps tray ownership through resize and applies buffer erasure and deletion', async ({ page }) => {
      await page.goto('/my-project/pi/~1h46pdtl?mock')
      await page.waitForFunction(() => (window as any).__gmuxTerm)
      await page.evaluate(async ({ put, alternatePut }) => {
        const term = (window as any).__gmuxTerm
        await new Promise<void>(resolve => term.write(`\x1b[H\x1b[2J\x1b[3J${put}`, resolve))
        term.resize(term.cols - 1, term.rows)
        await new Promise<void>(resolve => term.write(`\x1b[?1049h${alternatePut}`, resolve))
      }, {
        put: osc(asset),
        alternatePut: osc({ ...asset, id: 43 }),
      })
      // Only the active alternate-buffer entry is presented; the normal tray
      // entry remains retained but hidden.
      await expect(page.getByRole('button', { name: /Open PNG image/ })).toHaveCount(1)
      await page.evaluate(async () => {
        const term = (window as any).__gmuxTerm
        await new Promise<void>(resolve => term.write('\x1b[2J', resolve))
      })
      await expect(page.getByRole('button', { name: /Open PNG image/ })).toHaveCount(0)
      await page.evaluate(async () => {
        const term = (window as any).__gmuxTerm
        await new Promise<void>(resolve => term.write('\x1b[?1049l', resolve))
      })
      await expect(page.getByRole('button', { name: /Open PNG image/ })).toHaveCount(1)
      await page.evaluate(async () => {
        const term = (window as any).__gmuxTerm
        await new Promise<void>(resolve => term.write('\x1b[H\x1b[2K', resolve))
      })
      await expect(page.getByRole('button', { name: /Open PNG image/ })).toHaveCount(0)

      await page.evaluate(async ({ put, remove }) => {
        const term = (window as any).__gmuxTerm
        await new Promise<void>(resolve => term.write(`\x1b[H${put}`, resolve))
        term.resize(term.cols - 1, term.rows)
        await new Promise<void>(resolve => term.write(remove, resolve))
      }, {
        put: osc({ ...asset, id: 44 }),
        remove: osc({ version: 1, action: 'delete', id: 44 }),
      })
      await expect(page.getByRole('button', { name: /Open PNG image/ })).toHaveCount(0)
    })

    test('shows an expired-image error without blocking later terminal output', async ({ page }) => {
      await page.route('**/v1/sessions/*/images/*', route => route.fulfill({
        status: 410, json: { ok: false, error: { code: 'image_expired', message: 'Image evicted' } },
      }))
      await page.goto('/my-project/pi/~1h46pdtl?mock')
      await page.waitForFunction(() => (window as any).__gmuxTerm)
      await page.evaluate(async value => {
        const term = (window as any).__gmuxTerm
        await new Promise<void>(resolve => term.write(`\x1b[H\x1b[2J\x1b[3J\x1b]777;gmux-image;${JSON.stringify(value)}\x1b\\`, resolve))
      }, asset)
      await page.getByRole('button', { name: /Open PNG image/ }).click()
      await expect(page.getByText('image_expired', { exact: true })).toBeVisible()
      await page.getByRole('button', { name: 'Close image preview' }).click()
      const text = await page.evaluate(async () => {
        const term = (window as any).__gmuxTerm
        await new Promise<void>(resolve => term.write('still-responsive', resolve))
        return term.buffer.active.getLine(term.buffer.active.baseY)?.translateToString(true)
      })
      expect(text).toBe('still-responsive')
    })
  })
}
