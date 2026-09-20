import { expect, test } from '@playwright/test';

import {
  authToken,
  codexOTLPLogs,
  codexOTLPTraces,
  fetchLiveSessions,
  ingestOTLPLogs,
  ingestOTLPTraces,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('renders Codex trace evidence ingested through the live daemon', async ({
  page,
}) => {
  await ingestOTLPTraces(codexOTLPTraces());

  const observations = await fetch(
    'http://localhost:18080/api/v1/sessions?scope=observation&limit=10',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(observations.status).toBe(200);
  const body = (await observations.json()) as {
    data: Array<{
      session_id: string;
      identity_scope: string;
      identity_source: string;
    }>;
  };
  expect(body.data).toEqual([
    expect.objectContaining({
      session_id: 'codex:trace:dddddddddddddddddddddddddddddddd',
      identity_scope: 'observation',
      identity_source: 'trace.id',
    }),
  ]);

  await unlockDashboard(page, authToken);
  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await page.getByRole('link', { name: 'Observations' }).click();
  await expect(page.getByText('Observation only')).toBeVisible();
  await expect(page.getByText('trace.id')).toBeVisible();

  await page.getByRole('link', { name: /codex · started/ }).click();
  await expect(page.getByText('codex_exec')).toBeVisible();
  await expect(page.getByText('0.154.0')).toBeVisible();
  await expect(page.getByText('codex exec')).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'turn/start', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'session task turn', exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/session_task\.turn ·/)).toBeVisible();
  await expect(page.getByRole("heading", { name: "Span evidence", exact: true })).toBeVisible();
  await expect(page.getByText("Trace / span").first()).toBeVisible();
  await expect(page.getByText("loaded").first()).toBeVisible();
  await expect(page.getByText('TRACE_CAPTURE_COMPLETE')).toHaveCount(0);
});


test("renders Codex trace evidence joined to its provider conversation", async ({
  page,
}) => {
  await ingestOTLPLogs(codexOTLPLogs("tiq-live-e2e-codex-model"));
  await ingestOTLPTraces(codexOTLPTraces("tiq-live-e2e-codex-session"));

  await expect
    .poll(async () => fetchLiveSessions())
    .toContainEqual(
      expect.objectContaining({
        session_id: "codex:tiq-live-e2e-codex-session",
        identity_scope: "provider",
        identity_source: "conversation.id",
      }),
    );

  await unlockDashboard(page, authToken);
  await page.getByRole("link", { name: "Sessions", exact: true }).click();
  await expect(page.getByText("conversation.id")).toBeVisible();
  await page.getByRole("link", { name: /codex · started/ }).click();
  await expect(
    page.getByRole("heading", { name: "Span evidence", exact: true }),
  ).toBeVisible();
});
