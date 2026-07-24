import { expect, test } from '@playwright/test';

test('renders the production health journey', async ({ page }) => {
  await page.goto('/');

  await expect(
    page.getByRole('heading', { name: 'TelemetryIQ' }),
  ).toBeVisible();
  await expect(page.getByText('Daemon healthy')).toBeVisible();
  await expect(page.getByText('telemetryiq-daemon')).toBeVisible();
});
