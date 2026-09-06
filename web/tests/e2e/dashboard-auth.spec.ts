import { expect, test } from '@playwright/test';

import { authToken, unlockDashboard } from './live-ingest-helpers';

test('requires typed confirmation for bulk deletion', async ({ page }) => {
  await unlockDashboard(page, authToken);
  await page.getByRole('link', { name: 'Privacy', exact: true }).click();
  await page
    .getByRole('link', { name: 'Delete all retained telemetry' })
    .click();
  await expect(page.getByLabel('Type DELETE ALL to confirm')).toBeVisible();
  await page.getByLabel('Type DELETE ALL to confirm').fill('DELETE ALL');
  await page.getByRole('link', { name: 'Cancel' }).click();
  await expect(
    page.getByRole('heading', { name: 'Privacy' }),
  ).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'Delete all retained telemetry' }),
  ).toBeVisible();
});
