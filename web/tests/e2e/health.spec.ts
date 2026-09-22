import { expect, test } from '@playwright/test';
import path from 'node:path';

import {
  authToken,
  expectFiveDestinationPrimaryNav,
  expectUtilityDestinations,
  followShellNavigation,
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

  await expectFiveDestinationPrimaryNav(page);
  await expectUtilityDestinations(page, { costs: false });
  const primaryNavigation = page.getByLabel('Primary navigation');
  const utilityNavigation = page.getByLabel('Utility navigation');

  await primaryNavigation
    .getByRole('link', { name: 'Governance', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Governance Policies', exact: true }),
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

  await utilityNavigation
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

  await expectUtilityDestinations(page);
  await primaryNavigation
    .getByRole('link', { name: 'Pull Requests', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Pull Requests' }),
  ).toBeVisible();
  await expect(
    page.getByText('No retained HTTP(S) pull-request URLs yet', {
      exact: false,
    }),
  ).toBeVisible();
  await followShellNavigation(page, 'Primary navigation', 'Models');
  await expect(
    page.getByText('No outcome-contract rows yet', { exact: false }),
  ).toBeVisible();
  await followShellNavigation(page, 'Utility navigation', 'Privacy');
  await expect(
    page.getByText('raw prompts, responses, source content, paths, and commands are retained locally', { exact: false }),
  ).toBeVisible();
  await followShellNavigation(page, 'Utility navigation', 'Costs');
});

test('dark shell renders at reference viewports without body overflow (#161)', async ({
  page,
}) => {
  await unlockDashboard(page, authToken);
  const evidenceDir = path.resolve(process.cwd(), '../docs/ui/evidence/161');
  const viewports = [
    { name: '1440x900', width: 1440, height: 900 },
    { name: '1024x768', width: 1024, height: 768 },
    { name: '390x844', width: 390, height: 844 },
  ] as const;

  for (const vp of viewports) {
    await page.setViewportSize({ width: vp.width, height: vp.height });
    if (vp.width < 768) {
      const menu = page.getByRole('button', { name: 'Menu' });
      await expect(menu).toBeVisible();
      if ((await menu.getAttribute('aria-expanded')) !== 'true') {
        await menu.click();
      }
      await expect(menu).toHaveAttribute('aria-expanded', 'true');
    }
    await expectFiveDestinationPrimaryNav(page);
    await expectUtilityDestinations(page, { costs: false });
    const overflowX = await page.evaluate(
      () =>
        document.documentElement.scrollWidth >
        document.documentElement.clientWidth,
    );
    expect(overflowX, `${vp.name} must not body-scroll horizontally`).toBe(
      false,
    );
    await page.screenshot({
      path: path.join(evidenceDir, `overview-${vp.name}.png`),
      fullPage: true,
    });
  }
});
