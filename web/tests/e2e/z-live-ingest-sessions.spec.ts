import { expect, test } from '@playwright/test';

import {
  authToken,
  codexOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
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

  const list = await fetch('http://localhost:18080/api/v1/sessions?limit=100', {
    headers: { Authorization: `Bearer ${authToken}` },
  });
  expect(list.status).toBe(200);
  const listBody = (await list.json()) as {
    data: Array<{ tool: string; attributes?: { model?: string } }>;
  };
  expect(listBody.data.some((session) => session.tool === 'codex')).toBe(true);
  expect(
    listBody.data.some((session) => session.attributes?.model === liveModel),
  ).toBe(true);

  await unlockDashboard(page, authToken);
  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await expect(page.getByRole('cell', { name: 'codex' }).first()).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'No retained sessions yet.' }),
  ).toHaveCount(0);
});
