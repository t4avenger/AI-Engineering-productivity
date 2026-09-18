import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeRiskyAccessOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for Governance findings (#151): ingest a real Claude
 * OTLP payload with credential-file evidence through the daemon started by
 * playwright.config.ts, then assert /governance renders the finding. No
 * page.route().fulfill() mocking (QUALITY_GATES live-data DoD).
 */
resetDaemonBetweenTests();

test('renders risky-access findings after live OTLP ingest', async ({
  page,
}) => {
  await ingestOTLPLogs(claudeRiskyAccessOTLPLogs());

  const risky = await fetch(
    'http://localhost:18080/api/v1/insights/risky-access',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(risky.status).toBe(200);
  const body = (await risky.json()) as {
    data: {
      outcome: string;
      findings: Array<{ reference: string; session_id?: string }>;
    };
  };
  expect(body.data.outcome).toBe('violation');
  expect(
    body.data.findings.some((f) =>
      f.reference.includes('/home/dev/secret-app/.env'),
    ),
  ).toBe(true);

  await unlockDashboard(page, authToken);
  await page.goto('/governance');
  await expect(page.getByRole('heading', { name: 'Governance' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Risky access' })).toBeVisible();
  await expect(page.getByText('Violation').first()).toBeVisible();
  await expect(page.getByText('/home/dev/secret-app/.env').first()).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Unapproved MCP' }),
  ).toBeVisible();
  await expect(
    page.getByText('does not enforce or publish', { exact: false }),
  ).toBeVisible();
});
