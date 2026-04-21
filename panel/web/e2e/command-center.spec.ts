import { expect, test } from '@playwright/test'

test('desktop command center shows status strip and node cards', async ({ page }) => {
  await page.goto('/')

  await expect(page.getByText('Command Center')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Active' })).toBeVisible()
  await expect(page.getByText('Showing 3 nodes')).toBeVisible()
  await expect(page.getByRole('button', { name: /Rotate/i }).first()).toBeVisible()
})

test('mobile opens user bottom sheet from FAB', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')

  await page.getByRole('button', { name: /^Users$/ }).last().click()
  await expect(page.locator('input[placeholder="Search user"]:visible')).toBeVisible()
})
