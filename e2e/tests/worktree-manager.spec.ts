import { expect, test } from '@playwright/test'
import { openApp } from '../helpers'

async function openMock(page: import('@playwright/test').Page) {
  await openApp(page, '/my-project/claude/~fam2kid?mock=clean')
  await page.waitForSelector('.sidebar-list')
}

test.describe('managed worktrees', () => {
  test('shows the linked checkout count and includes family children in their actual checkout', async ({ page }) => {
    await openMock(page)

    const project = page.locator('.folder').filter({ has: page.locator('.folder-name-label', { hasText: 'my-project' }) })
    await expect(project.locator('.folder-worktree-count')).toHaveText('WT 2')
    await expect(project.locator('.folder-worktree-count')).toHaveAttribute('title', '2 linked worktrees')

    await project.getByRole('button', { name: /Project actions for my-project/ }).click()
    await page.getByText('Manage worktrees…', { exact: true }).click()

    const manager = page.locator('.worktree-sheet')
    await expect(manager).toBeVisible()
    await expect(manager.locator('.worktree-card')).toHaveCount(3)

    const linked = manager.locator('.worktree-card').filter({ hasText: 'version-clear-confirm' })
    await expect(linked).toContainText('1 session · 1 working')
    await expect(linked).toContainText('confirm version reset behavior')
    await expect(linked).toContainText('↳ family')

    await linked.locator('.worktree-session').click()
    await expect(manager).toHaveCount(0)
    await expect(page).toHaveURL(/~fam2kid/)
  })
})

test.describe('managed worktrees on touch', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  test('opens cleanly from the sidebar and provides tap-sized session rows', async ({ page }) => {
    await openMock(page)

    await page.locator('button[title="Open sessions"]').tap()
    const sidebar = page.locator('.sidebar')
    await expect(sidebar).toHaveClass(/open/)

    const project = sidebar.locator('.folder').filter({ hasText: 'my-project' })
    await project.getByRole('button', { name: /Project actions for my-project/ }).tap()
    await page.getByText('Manage worktrees…', { exact: true }).tap()

    await expect(sidebar).not.toHaveClass(/open/)
    const row = page.locator('.worktree-card').filter({ hasText: 'version-clear-confirm' }).locator('.worktree-session')
    await expect(row).toBeVisible()
    expect((await row.boundingBox())?.height).toBeGreaterThanOrEqual(44)
  })
})
