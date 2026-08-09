import { expect, test } from '@playwright/test';

test('connects with the local token and requires bulk-deletion confirmation', async ({
  page,
}) => {
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: 'Connect your dashboard' }),
  ).toBeVisible();
  await page.getByLabel('Local API token').fill('playwright-token');
  const sessionList = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/sessions?limit=100') &&
      response.request().method() === 'GET',
  );
  await page.getByRole('button', { name: 'Connect securely' }).click();
  expect((await sessionList).status()).toBe(200);
  await page.getByRole('button', { name: 'Privacy' }).click();
  await page
    .getByRole('button', { name: 'Delete all retained telemetry' })
    .click();
  await expect(
    page.getByRole('button', { name: 'Delete all permanently' }),
  ).toBeDisabled();
  await page.getByLabel('Confirmation').fill('DELETE ALL');
  await expect(
    page.getByRole('button', { name: 'Delete all permanently' }),
  ).toBeEnabled();
  await page.getByRole('button', { name: 'Cancel' }).click();
  await expect(page.getByRole('dialog')).toBeHidden();
});
