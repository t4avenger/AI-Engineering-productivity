import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeOutcomeSuccessOTLPLogs,
  codexOutcomeOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for the model-performance scorecard: drive the real
 * daemon from playwright.config.ts with outcome-contract OTLP (no API mocks).
 */
resetDaemonBetweenTests();

test('renders model-performance scorecard for live outcome contracts', async ({
  page,
}) => {
  await ingestOTLPLogs(claudeOutcomeSuccessOTLPLogs());
  await ingestOTLPLogs(codexOutcomeOTLPLogs());

  const response = await fetch(
    'http://localhost:18080/api/v1/insights/model-performance',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(response.status).toBe(200);
  const body = (await response.json()) as {
    data: {
      models: Array<{ model: string; sample_size: number }>;
      ranking_available: boolean;
    };
  };
  expect(body.data.ranking_available).toBe(false);
  expect(body.data.models.some((row) => row.model.includes('haiku'))).toBe(
    true,
  );
  expect(body.data.models.some((row) => row.model === 'gpt-6-astra')).toBe(
    true,
  );

  await unlockDashboard(page, authToken);
  await page
    .getByLabel('Primary navigation')
    .getByRole('link', { name: 'Insights' })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Model performance' }),
  ).toBeVisible();
  await expect(page.getByText('gpt-6-astra').first()).toBeVisible();
  await expect(page.getByText('claude-haiku-4-5-20251001').first()).toBeVisible();
  await expect(page.getByText('Ranking available: no')).toBeVisible();
});
