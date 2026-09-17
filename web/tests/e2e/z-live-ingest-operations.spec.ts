import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeToolResultOTLPLogs,
  codexToolResultOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('renders operation stats for data ingested through the live daemon', async ({
  page,
}) => {
  await ingestOTLPLogs(codexToolResultOTLPLogs());
  await ingestOTLPLogs(claudeToolResultOTLPLogs());

  const stats = await fetch('http://localhost:18080/api/v1/insights/operations', {
    headers: { Authorization: `Bearer ${authToken}` },
  });
  expect(stats.status).toBe(200);
  const body = (await stats.json()) as {
    data: {
      totals: { total_operations: number; duration_observed_count: number };
      by_category: Array<{ category: string; count: number }>;
    };
  };
  expect(body.data.totals.total_operations).toBe(2);
  expect(body.data.totals.duration_observed_count).toBe(2);
  expect(
    body.data.by_category.some(
      (row) => row.category === 'shell command' && row.count === 2,
    ),
  ).toBe(true);

  await unlockDashboard(page, authToken);
  await page.goto('/insights');
  await expect(page.getByRole('heading', { name: 'Operations' })).toBeVisible();
  await expect(page.getByText('Total operations 2')).toBeVisible();
  await expect(page.getByRole('cell', { name: 'shell command' })).toBeVisible();
  await expect(page.getByRole('cell', { name: '2 operations' }).first()).toBeVisible();
  await expect(page.getByText('tiq-canary-live-operation')).toHaveCount(0);
});
