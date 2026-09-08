import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeContextWasteOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for the context-waste insight: drive the real daemon
 * started by playwright.config.ts with OTLP logs (no API mocks).
 */
resetDaemonBetweenTests();

test('renders context-waste insight for live cached-token logs', async ({
  page,
}) => {
  await ingestOTLPLogs(claudeContextWasteOTLPLogs());

  const response = await fetch('http://localhost:18080/api/v1/insights/context-waste', {
    headers: { Authorization: `Bearer ${authToken}` },
  });
  expect(response.status).toBe(200);
  const body = (await response.json()) as {
    data: { totals: { sessions: number; triggered_sessions: number } };
  };
  expect(body.data.totals.sessions).toBeGreaterThanOrEqual(1);
  expect(body.data.totals.triggered_sessions).toBeGreaterThanOrEqual(1);

  await unlockDashboard(page, authToken);
  await page
    .getByLabel('Primary navigation')
    .getByRole('link', { name: 'Insights' })
    .click();
  await expect(page.getByRole('heading', { name: 'Context pressure' })).toBeVisible();
  await expect(page.getByText('Triggered').first()).toBeVisible();
});

