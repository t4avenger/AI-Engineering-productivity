import { expect, test } from '@playwright/test';

test('renders the local dashboard journey', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('Local API token').fill('playwright-token');
  await page.getByRole('button', { name: 'Connect securely' }).click();
  await expect(
    page.getByRole('heading', { name: 'Your local AI activity' }),
  ).toBeVisible();
  await expect(page.getByText('Daemon healthy')).toBeVisible();
  await page.getByRole('button', { name: 'Sessions' }).click();
  await expect(
    page.getByRole('heading', { name: 'Sessions', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'No sessions yet' }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Privacy' }).click();
  await expect(page.getByRole('heading', { name: 'Privacy' })).toBeVisible();
  await expect(page.getByText('Prompts and responses')).toBeVisible();
});
