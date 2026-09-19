import { expect, test } from '@playwright/test';

import {
  authToken,
  expectFourTabPrimaryNav,
  unlockDashboard,
} from './live-ingest-helpers';

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

  await expectFourTabPrimaryNav(page);
  const primaryNavigation = page.getByLabel('Primary navigation');

  await primaryNavigation
    .getByRole('link', { name: 'Governance', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Governance', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Risky access' }),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Unapproved MCP' }),
  ).toBeVisible();
  await expect(
    page.getByText('does not enforce or publish', { exact: false }),
  ).toBeVisible();

  await primaryNavigation
    .getByRole('link', { name: 'Integrations', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Integrations', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Capability matrix' }),
  ).toBeVisible();
  await expect(page.getByRole('columnheader', { name: 'Codex' })).toBeVisible();
  await expect(
    page.getByRole('columnheader', { name: 'Claude Code' }),
  ).toBeVisible();
  await expect(page.getByRole('columnheader', { name: 'Cursor' })).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Tools observed' }),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Privacy', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'Privacy', exact: true }).first(),
  ).toBeVisible();

  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Sessions', exact: true }),
  ).toBeVisible();
  await expect(page.getByText('No retained sessions yet.')).toBeVisible();

  const secondaryNavigation = page.getByLabel('Secondary navigation');
  await expect(secondaryNavigation.getByRole('link')).toHaveText([
    'Models',
    'Privacy',
    'Costs',
  ]);
  await secondaryNavigation
    .getByRole('link', { name: 'Models', exact: true })
    .click();
  await expect(page.getByRole('heading', { name: 'Models' })).toBeVisible();
  await expect(
    page.getByText('No outcome-contract rows yet', { exact: false }),
  ).toBeVisible();
  await page
    .getByLabel('Secondary navigation')
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
