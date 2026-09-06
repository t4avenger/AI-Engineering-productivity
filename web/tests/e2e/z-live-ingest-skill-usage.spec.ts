import { expect, test } from '@playwright/test';

/**
 * Live end-to-end gate for the skill usage insight: drive the real daemon
 * started by playwright.config.ts. No page.route().fulfill() mocking — ingest
 * real OTLP, then assert the Insights UI renders the honest skill-usage state.
 *
 * Codex does not stamp explicit skill identity, so the honest result is an
 * "unknown" detection-coverage row and a "no skill identity observed" empty
 * state — never a fabricated skill or a silent zero (QUALITY_GATES live-data DoD).
 */
const daemonBase = 'http://127.0.0.1:18080';
const authToken = 'playwright-token';

const rawCodexOTLPLogs = JSON.stringify({
  resourceLogs: [
    {
      resource: {
        attributes: [
          { key: 'service.name', value: { stringValue: 'codex_cli_rs' } },
          { key: 'service.version', value: { stringValue: '0.145.0' } },
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
                  value: { stringValue: 'tiq-live-e2e-skill-model' },
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
  await clearSessions();
});

test.afterEach(async () => {
  await clearSessions();
});

test('renders honest skill usage for data ingested through the live daemon', async ({
  page,
}) => {
  const ingest = await fetch(`${daemonBase}/v1/logs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: rawCodexOTLPLogs,
  });
  expect(ingest.status).toBe(202);

  await page.goto('/');
  await page.getByLabel('Local API token').fill(authToken);
  const skillUsage = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/insights/skill-usage') &&
      response.request().method() === 'GET' &&
      response.status() === 200,
  );
  await page.getByRole('button', { name: 'Connect securely' }).click();
  await page.getByRole('button', { name: 'Insights' }).click();

  const body = (await (await skillUsage).json()) as {
    data: {
      skills: unknown[];
      coverage: Array<{ tool: string; detection_state: string }>;
    };
  };
  expect(body.data.skills).toHaveLength(0);
  expect(
    body.data.coverage.some(
      (row) => row.tool === 'codex' && row.detection_state === 'unknown',
    ),
  ).toBe(true);

  const skillSection = page.locator(
    'section[aria-labelledby="skill-usage-title"]',
  );
  await expect(
    skillSection.getByRole('heading', { name: 'Skill usage' }),
  ).toBeVisible();
  await expect(
    skillSection.getByRole('heading', { name: 'No skill identity observed' }),
  ).toBeVisible();
  await expect(skillSection.getByText('codex')).toBeVisible();
  await expect(skillSection.getByText('unknown')).toBeVisible();
});
