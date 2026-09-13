import { expect, test } from '@playwright/test';

import {
  authToken,
  cursorEnterpriseAPIRequestLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for Cursor Enterprise OTEL (#130/#132): ingest a synthetic
 * api.request log through the real daemon and assert Sessions + Integrations UI
 * render the Cursor tool/session and Enterprise observed status without mocking
 * API responses.
 */
const liveModel = 'tiq-live-e2e-cursor-model';

resetDaemonBetweenTests();

test('renders a Cursor Enterprise OTEL session ingested through the live daemon', async ({
  page,
}) => {
  await ingestOTLPLogs(cursorEnterpriseAPIRequestLogs(liveModel));

  const list = await fetch('http://localhost:18080/api/v1/sessions?limit=100', {
    headers: { Authorization: `Bearer ${authToken}` },
  });
  expect(list.status).toBe(200);
  const listBody = (await list.json()) as {
    data: Array<{
      tool: string;
      session_id?: string;
      attributes?: { model?: string };
    }>;
  };
  expect(listBody.data.some((session) => session.tool === 'cursor')).toBe(true);
  expect(
    listBody.data.some(
      (session) =>
        session.session_id === 'cursor:tiq-live-e2e-cursor-session' &&
        session.attributes?.model === liveModel,
    ),
  ).toBe(true);

  const encoded = JSON.stringify(listBody);
  expect(encoded).not.toContain('424242');
  expect(encoded).not.toContain('434343');
  expect(encoded).not.toContain('cursor.team.id');

  await unlockDashboard(page, authToken);
  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await expect(
    page.getByRole('link', { name: /cursor · started/ }).first(),
  ).toBeVisible();
  await expect(page.getByRole('cell', { name: 'cursor' }).first()).toBeVisible();
  await expect(page.getByText('Seen in telemetry').first()).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'No retained sessions yet.' }),
  ).toHaveCount(0);

  // Integrations (#132): Enterprise setup + honest observed status from retained
  // tool=cursor sessions (never a fabricated "connected" state).
  await page.getByRole('link', { name: 'Integrations', exact: true }).click();
  await expect(
    page.getByRole('heading', {
      name: 'Cursor Enterprise OpenTelemetry Export',
    }),
  ).toBeVisible();
  await expect(page.getByText('Team Settings').first()).toBeVisible();
  await expect(page.getByText('Seen in telemetry').first()).toBeVisible();
  await expect(page.getByText('cursor (cursor)').first()).toBeVisible();
  await expect(page.getByText('Not seen in telemetry')).toHaveCount(0);
});
