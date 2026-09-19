import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeMCPConnectionOTLPLogs,
  claudeRiskyAccessOTLPLogs,
  expectFourTabPrimaryNav,
  expectGovernanceAccessRulesShells,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for Governance findings (#151) and the V1 enterprise IA
 * Playwright DoD (#155): ingest a real Claude OTLP payload with credential-file
 * evidence through the daemon started by playwright.config.ts, then assert
 * /governance renders the finding and the four-tab primary nav. Also covers the
 * MCP allowlist Save round-trip and Access Rules tab shells (#160). No
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
  await expectFourTabPrimaryNav(page);
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
  await expectGovernanceAccessRulesShells(page);

  await page.goto('/sessions/claude-code:tiq-live-e2e-governance-session');
  await expect(
    page.getByRole('heading', { name: 'Governance checklist' }),
  ).toBeVisible();
  const riskyAccess = page
    .getByRole('listitem')
    .filter({ hasText: 'Risky access' });
  await expect(riskyAccess.getByText('Violation')).toBeVisible();
  await expect(riskyAccess.getByText('Seen in telemetry')).toBeVisible();
  await expect(riskyAccess.getByText('2 findings')).toBeVisible();
  const unapprovedMCP = page
    .getByRole('listitem')
    .filter({ hasText: 'Unapproved MCP' });
  await expect(unapprovedMCP.getByText('Indeterminate')).toBeVisible();
  await expect(unapprovedMCP.getByText('Allowlist not configured')).toBeVisible();
});

test('saves an observed MCP server and reloads findings without mocks', async ({
  page,
}) => {
  const serverName = 'tiq-live-filesystem';
  await ingestOTLPLogs(claudeMCPConnectionOTLPLogs(serverName));

  const before = await fetch(
    'http://localhost:18080/api/v1/insights/unapproved-mcp',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(before.status).toBe(200);
  expect((await before.json()) as unknown).toMatchObject({
    data: { outcome: 'indeterminate', visibility: 'policy_unconfigured' },
  });

  await unlockDashboard(page, authToken);
  await page.goto('/governance');
  const checkbox = page.getByRole('checkbox', { name: serverName });
  await expect(checkbox).toBeVisible();
  await checkbox.check();
  await page.getByRole('button', { name: 'Save allowlist' }).click();

  await expect(page).toHaveURL(/\/governance\?saved=1$/);
  await expect(page.getByRole('status')).toContainText('MCP allowlist saved');
  await expect(page.getByText('All identifiable observed MCP servers')).toBeVisible();
  await expect(checkbox).toBeChecked();

  const after = await fetch(
    'http://localhost:18080/api/v1/insights/unapproved-mcp',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(after.status).toBe(200);
  expect((await after.json()) as unknown).toMatchObject({
    data: { outcome: 'not_violation', visibility: 'observed' },
  });

  await checkbox.uncheck();
  await page.getByRole('button', { name: 'Save allowlist' }).click();
  await expect(page.getByRole('status')).toContainText('MCP allowlist saved');
});
