import { expect, test } from '@playwright/test';

import {
  authToken,
  codexOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live availability labelling: open a retained session and assert honest
 * availability vocabulary is rendered (replaces the old mocked SPA route test).
 */
resetDaemonBetweenTests();

test('renders availability labels on a live session detail', async ({
  page,
}) => {
  await ingestOTLPLogs(codexOTLPLogs('tiq-live-availability-model'));
  await unlockDashboard(page, authToken);
  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await page.locator('table tbody a').first().click();
  await expect(page.getByRole('heading', { name: /^Session / })).toBeVisible();
  // Availability states render as plain-label status badges (issue #76),
  // matching the field glossary rather than raw machine enums.
  await expect(page.getByText('model: Seen in telemetry')).toBeVisible();
  await expect(page.getByText('tool: Seen in telemetry')).toBeVisible();
});
