import { expect, test } from '@playwright/test';

test('connects with the local token and confirms bulk telemetry deletion', async ({
  page,
}) => {
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: 'Connect your dashboard' }),
  ).toBeVisible();
  await page.getByLabel('Local API token').fill('playwright-token');
  await page.getByRole('button', { name: 'Connect securely' }).click();
  await page.getByRole('button', { name: 'Privacy' }).click();
  await page
    .getByRole('button', { name: 'Delete all retained telemetry' })
    .click();
  await expect(
    page.getByRole('button', { name: 'Delete all permanently' }),
  ).toBeDisabled();
  await page.getByLabel('Confirmation').fill('DELETE ALL');
  const deletion = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/sessions') &&
      response.request().method() === 'DELETE',
  );
  await page.getByRole('button', { name: 'Delete all permanently' }).click();
  expect((await deletion).status()).toBe(204);
  await expect(page.getByRole('dialog')).toBeHidden();
});
