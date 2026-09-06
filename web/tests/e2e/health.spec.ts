import { expect, test } from '@playwright/test';

import { authToken, unlockDashboard } from './live-ingest-helpers';

test('unlocks and walks the local dashboard journey', async ({ page }) => {
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: 'Unlock local dashboard' }),
  ).toBeVisible();
  await unlockDashboard(page, authToken);
  await expect(
    page.getByRole('heading', { name: 'Orchestration overview' }),
  ).toBeVisible();
  await expect(page.getByText('Daemon: Healthy')).toBeVisible();
  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Sessions', exact: true }),
  ).toBeVisible();
  await expect(page.getByText('No retained sessions yet.')).toBeVisible();
  await page.getByRole('link', { name: 'Privacy', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Privacy' })).toBeVisible();
  await expect(
    page.getByText('Prompts, responses, and source code are not retained'),
  ).toBeVisible();
});
