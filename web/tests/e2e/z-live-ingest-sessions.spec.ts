import { expect, test } from '@playwright/test';

/**
 * Live end-to-end gate: drive the real daemon started by playwright.config.ts.
 * No page.route().fulfill() mocking — ingest real OTLP, assert the UI renders
 * the resulting session data (issue #49 / #51).
 */
const daemonBase = 'http://127.0.0.1:18080';
const authToken = 'playwright-token';
const liveModel = 'tiq-live-e2e-codex-model';

const rawCodexOTLPLogs = JSON.stringify({
  resourceLogs: [
    {
      resource: {
        attributes: [
          {
            key: 'service.name',
            value: { stringValue: 'codex_cli_rs' },
          },
          {
            key: 'service.version',
            value: { stringValue: '0.145.0' },
          },
        ],
      },
      scopeLogs: [
        {
          logRecords: [
            {
              attributes: [
                {
                  key: 'event.name',
                  value: { stringValue: 'codex.sse_event' },
                },
                {
                  key: 'model',
                  value: { stringValue: liveModel },
                },
                {
                  key: 'input_token_count',
                  value: { stringValue: '11' },
                },
                {
                  key: 'output_token_count',
                  value: { stringValue: '3' },
                },
              ],
            },
          ],
        },
      ],
    },
  ],
});

async function clearSessions(): Promise<void> {
  const response = await fetch(`${daemonBase}/api/v1/sessions`, {
    method: 'DELETE',
    headers: { Authorization: `Bearer ${authToken}` },
  });
  expect(response.status).toBe(204);
}

test.beforeEach(async () => {
  // Clean slate so retries cannot pass on leftover sessions/models.
  await clearSessions();
});

test.afterEach(async () => {
  // Leave the shared daemon empty for any later specs / re-runs.
  await clearSessions();
});

test('renders a session ingested through the live daemon', async ({ page }) => {
  const ingest = await fetch(`${daemonBase}/v1/logs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: rawCodexOTLPLogs,
  });
  expect(ingest.status).toBe(202);

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
