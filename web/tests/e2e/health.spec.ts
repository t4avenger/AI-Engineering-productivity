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

  const primaryNavigation = page.getByLabel('Primary navigation');
  await expect(primaryNavigation.getByRole('link')).toHaveText([
    'Home',
    'Sessions',
    'Governance',
    'Integrations',
  ]);
  await expect(
    primaryNavigation.getByRole('link', { name: 'Insights', exact: true }),
  ).toHaveCount(0);
  await expect(
    primaryNavigation.getByRole('link', { name: 'Privacy', exact: true }),
  ).toHaveCount(0);
  await expect(
    primaryNavigation.getByRole('link', { name: 'Costs', exact: true }),
  ).toHaveCount(0);

  await primaryNavigation
    .getByRole('link', { name: 'Governance', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Governance', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Governance findings are not available in this dashboard view yet.'),
  ).toBeVisible();

  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Sessions', exact: true }),
  ).toBeVisible();
  await expect(page.getByText('No retained sessions yet.')).toBeVisible();

  const secondaryNavigation = page.getByLabel('Secondary navigation');
  await expect(secondaryNavigation.getByRole('link')).toHaveText([
    'Privacy',
    'Costs',
  ]);
  await secondaryNavigation
    .getByRole('link', { name: 'Privacy', exact: true })
    .click();
  await expect(page.getByRole('heading', { name: 'Privacy' })).toBeVisible();
  await expect(
    page.getByText('Prompts, responses, and source code are not retained'),
  ).toBeVisible();
  await page
    .getByLabel('Secondary navigation')
    .getByRole('link', { name: 'Costs', exact: true })
    .click();
  await expect(page.getByRole('heading', { name: 'Costs' })).toBeVisible();
});
