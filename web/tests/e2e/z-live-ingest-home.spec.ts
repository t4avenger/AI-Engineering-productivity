import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeContextWasteOTLPLogs,
  claudeMCPConnectionOTLPLogs,
  claudeOutcomeSuccessOTLPLogs,
  claudeRiskyAccessOTLPLogs,
  claudeSkillOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live gate for #150: send wire-shaped OTLP through the daemon and prove Home
 * composes retained behaviour, governance, and integration evidence. This test
 * intentionally uses no page.route().fulfill() API mocking.
 */
resetDaemonBetweenTests();

test('renders Home highlights from live retained telemetry', async ({ page }) => {
  await ingestOTLPLogs(claudeMCPConnectionOTLPLogs('tiq-live-home-mcp'));
  await ingestOTLPLogs(claudeSkillOTLPLogs());
  await ingestOTLPLogs(claudeOutcomeSuccessOTLPLogs());
  await ingestOTLPLogs(claudeContextWasteOTLPLogs());
  await ingestOTLPLogs(claudeRiskyAccessOTLPLogs());

  await unlockDashboard(page, authToken);
  await page.goto('/');

  await expect(
    page.getByRole('heading', { name: 'Behaviour and efficiency' }),
  ).toBeVisible();
  await expect(page.getByRole('heading', { name: 'MCP usage' })).toBeVisible();
  await expect(page.getByText(/Connected 1 · Invoked 0 · Unused 1/)).toBeVisible();
  await expect(page.getByText(/Observed skills 1 · Invocations 1/)).toBeVisible();
  await expect(page.getByText('claude-haiku-4-5-20251001')).toBeVisible();
  await expect(page.getByText(/Triggered sessions 1 \/ 2/)).toBeVisible();

  const governance = page.locator('#governance-highlights');
  await expect(governance.getByText('Risky access:', { exact: false })).toBeVisible();
  await expect(governance.getByText('Violation')).toBeVisible();
  await expect(
    governance.getByRole('link', { name: 'Review governance evidence' }),
  ).toHaveAttribute('href', '/governance');

  const integrations = page.locator('#integration-health');
  const claudeRow = integrations.getByRole('row').filter({ hasText: 'claude-code' });
  await expect(claudeRow).toContainText('anthropic');
  await expect(claudeRow).toContainText('Seen in telemetry');
  await expect(claudeRow.locator('time')).toHaveAttribute(
    'datetime',
    '2026-09-06T19:05:00Z',
  );

  await expect(page.getByRole('link', { name: 'Costs' })).toHaveCount(0);
  await expect(page.getByText(/Cost estimates|Calculated amount/)).toHaveCount(0);
});
