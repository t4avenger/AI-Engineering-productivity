import { expect, test } from '@playwright/test';

import {
  authToken,
  codexOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for the skill usage insight: drive the real daemon
 * started by playwright.config.ts. No page.route().fulfill() mocking — ingest
 * real OTLP, then assert the Insights UI renders the honest skill-usage state.
 *
 * Codex does not stamp explicit skill identity, so the honest result is an
 * "unknown" detection-coverage row and a "no skill identity observed" empty
 * state — never a fabricated skill or a silent zero (QUALITY_GATES live-data DoD).
 */
resetDaemonBetweenTests();

test('renders honest skill usage for data ingested through the live daemon', async ({
  page,
}) => {
  await ingestOTLPLogs(codexOTLPLogs('tiq-live-e2e-skill-model'));

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
