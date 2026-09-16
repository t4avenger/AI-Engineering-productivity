import { expect, test } from '@playwright/test';

import {
  authToken,
  codexOTLPLogs,
  codexTurnTokenOTLPMetrics,
  ingestOTLPLogs,
  ingestOTLPMetrics,
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
  await ingestOTLPMetrics(codexTurnTokenOTLPMetrics());

  const list = await fetch('http://localhost:18080/api/v1/sessions?limit=100', {
    headers: { Authorization: `Bearer ${authToken}` },
  });
  expect(list.status).toBe(200);
  const listBody = (await list.json()) as {
    data: Array<{
      tool: string;
      identity_scope: string;
      attributes?: { model?: string };
    }>;
  };
  expect(listBody.data).toHaveLength(1);
  expect(listBody.data[0]).toMatchObject({
    tool: 'codex',
    identity_scope: 'provider',
    attributes: { model: liveModel },
  });

  const observations = await fetch(
    'http://localhost:18080/api/v1/sessions?limit=100&scope=observation',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(observations.status).toBe(200);
  const observationBody = (await observations.json()) as {
    data: Array<{ identity_scope: string; identity_source: string }>;
  };
  expect(observationBody.data).toHaveLength(6);
  expect(
    observationBody.data.every(
      (session) =>
        session.identity_scope === 'observation' &&
        session.identity_source === 'content-derived',
    ),
  ).toBe(true);

  await unlockDashboard(page, authToken);
  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await expect(
    page.getByRole('link', { name: /codex · started/ }).first(),
  ).toBeVisible();
  await expect(page.getByRole('cell', { name: 'codex' }).first()).toBeVisible();
  await expect(page.getByText('Seen in telemetry').first()).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'No retained sessions yet.' }),
  ).toHaveCount(0);

  await page.getByRole('link', { name: 'Observations' }).click();
  await expect(page.getByText('Observation only')).toHaveCount(6);
  await expect(page.getByText('content-derived')).toHaveCount(6);
});
