import { expect, test } from '@playwright/test';

import {
  authToken,
  codexOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate: drive the real daemon started by playwright.config.ts.
 * No page.route().fulfill() mocking — ingest real OTLP, assert the UI renders
 * the resulting session data (issue #49 / #51).
 */
const liveModel = 'tiq-live-e2e-codex-model';

resetDaemonBetweenTests();

test('renders a session ingested through the live daemon', async ({ page }) => {
  await ingestOTLPLogs(codexOTLPLogs(liveModel));

  await page.goto('/');
  await page.getByLabel('Local API token').fill(authToken);
  const sessionList = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/sessions?limit=100') &&
      response.request().method() === 'GET' &&
      response.status() === 200,
  );
  await page.getByRole('button', { name: 'Connect securely' }).click();
  const listBody = (await (await sessionList).json()) as {
    data: Array<{ tool: string; attributes?: { model?: string } }>;
  };
  expect(listBody.data.some((session) => session.tool === 'codex')).toBe(true);
  expect(
    listBody.data.some((session) => session.attributes?.model === liveModel),
  ).toBe(true);

  await page.getByRole('button', { name: 'Sessions' }).click();
  await expect(page.getByRole('button', { name: /codex/i })).toBeVisible();
  await expect(page.getByText(liveModel)).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'No sessions yet' }),
  ).toHaveCount(0);
});
