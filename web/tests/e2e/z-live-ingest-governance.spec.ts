import { expect, test, type Page } from '@playwright/test';

import {
  authToken,
  expectAllowlistDiscardConfirmation,
  claudeContentOTLPLogs,
  claudePromptTranscriptNDJSON,
  claudePromptTranscriptSessionID,
  claudeMCPConnectionOTLPLogs,
  claudeRiskyAccessOTLPLogs,
  claudeSkillOTLPLogs,
  expectFiveDestinationPrimaryNav,
  expectGovernanceAccessRulesShells,
  expectGovernanceMCPEditorInteractions,
  ingestClaudeTranscript,
  ingestOTLPLogs,
  openGovernanceFindings,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for Governance findings (#151) and the V1 enterprise IA
 * Playwright DoD (#155), updated for the #161 five-destination shell: ingest a
 * real Claude OTLP payload with credential-file evidence through the daemon
 * started by playwright.config.ts, then assert /governance renders the finding
 * and primary nav. Also covers the MCP allowlist Save round-trip and Access
 * Rules tab shells (#160). No page.route().fulfill() mocking
 * (QUALITY_GATES live-data DoD).
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
  await expectFiveDestinationPrimaryNav(page);
  await expect(
    page.getByRole('heading', { name: 'Governance Policies', exact: true }),
  ).toBeVisible();
  await openGovernanceFindings(page);
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
  const rail = page.getByLabel('Session summary');
  await expect(rail.getByRole('heading', { name: 'Governance' })).toBeVisible();
  const riskyAccess = rail
    .getByRole('listitem')
    .filter({ hasText: 'Risky access' });
  await expect(riskyAccess.getByText('Violation')).toBeVisible();
  await expect(riskyAccess.getByText('Seen in telemetry')).toBeVisible();
  await expect(riskyAccess.getByText('2 findings')).toBeVisible();
  const unapprovedMCP = rail
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
  await page.getByRole('button', { name: 'Save local changes' }).first().click();

  await expect(page).toHaveURL(/\/governance\?saved=1$/);
  await expect(page.getByRole('status')).toContainText('MCP allowlist saved');
  await openGovernanceFindings(page);
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
  await page.getByRole('button', { name: 'Save local changes' }).first().click();
  await expect(page.getByRole('status')).toContainText('MCP allowlist saved');
});

test('filters, resets, and discards MCP edits without mocks', async ({ page }) => {
  const serverName = 'tiq-live-filesystem-editor';
  await ingestOTLPLogs(claudeMCPConnectionOTLPLogs(serverName));
  await unlockDashboard(page, authToken);
  await page.goto('/governance');
  await expectGovernanceMCPEditorInteractions(page, serverName);
});

test('saves an explicit skill policy and reloads its finding without mocks', async ({
  page,
}) => {
  await ingestOTLPLogs(claudeSkillOTLPLogs());

  const before = await fetch(
    'http://localhost:18080/api/v1/insights/unapproved-skills',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(before.status).toBe(200);
  expect((await before.json()) as unknown).toMatchObject({
    data: { outcome: 'indeterminate', visibility: 'policy_unconfigured' },
  });

  await unlockDashboard(page, authToken);
  await page.goto('/governance?rules=skills');
  const checkbox = page.getByRole('checkbox', { name: 'tiq-probe' });
  await expect(checkbox).toBeVisible();
  await checkbox.check();
  await page.getByRole('button', { name: 'Save local changes' }).first().click();

  await expect(page).toHaveURL(/\/governance\?rules=skills&saved=1$/);
  await expect(page.getByRole('status')).toContainText('Skills allowlist saved');
  await openGovernanceFindings(page);
  await expect(page.getByText('All explicitly identified observed skills')).toBeVisible();
  await expect(checkbox).toBeChecked();

  const after = await fetch(
    'http://localhost:18080/api/v1/insights/unapproved-skills',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(after.status).toBe(200);
  expect((await after.json()) as unknown).toMatchObject({
    data: { outcome: 'not_violation', visibility: 'observed' },
  });
  await expectAllowlistDiscardConfirmation(page, 'tiq-probe', 'Skills');
});

test('saves raw-path rules and renders their detect-only finding without mocks', async ({
  page,
}) => {
  const rawPath = '/home/dev/secret-app/.env';
  await ingestOTLPLogs(claudeRiskyAccessOTLPLogs());
  await unlockDashboard(page, authToken);
  await page.goto('/governance?rules=paths');

  await page.getByLabel('Monitor all').check();
  await page.getByRole('button', { name: 'Add blocked path' }).click();
  const blockedRule = page.locator('.path-rule-list[data-group="blocked"] .path-rule-row');
  await blockedRule.getByRole('textbox', { name: 'Blocked path rule' }).fill(rawPath);
  await page.getByRole('button', { name: 'Save local changes' }).first().click();

  await expect(page).toHaveURL(/\/governance\?rules=paths&saved=1$/);
  await expect(page.getByRole('status')).toContainText('Files & Paths rules saved');
  await openGovernanceFindings(page);
  await expect(page.getByText(rawPath).first()).toBeVisible();
  await expect(page.getByText('detect only', { exact: false }).first()).toBeVisible();

  await page
    .getByLabel('Path rule findings')
    .getByRole('link')
    .first()
    .click();
  await expect(page).toHaveURL(
    /\/sessions\/claude-code:tiq-live-e2e-governance-session\?event=.+&inspector=details#event-inspector$/,
  );
  await expect(page.locator('#event-inspector')).toBeVisible();

  const after = await fetch(
    'http://localhost:18080/api/v1/insights/path-rules',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(after.status).toBe(200);
  expect((await after.json()) as unknown).toMatchObject({
    data: { outcome: 'violation', visibility: 'observed' },
  });
});

test('saves a prompt finding rule and records the retained match', async ({
  page,
}) => {
  await ingestOTLPLogs(claudeContentOTLPLogs());
  await unlockDashboard(page, authToken);
  await page.goto('/governance?rules=prompts');

  await page.getByRole('button', { name: 'Add credentials rule' }).click();
  const draft = page.locator('.prompt-rule-list[data-group="credentials"] .prompt-rule-row');
  await draft.getByRole('textbox', { name: 'Rule ID' }).fill('draft-rule');
  await page.getByRole('button', { name: 'Reset changes' }).click();
  await expect(draft).toHaveCount(0);

  await savePromptKeywordRule(page, 'retained-user', 'Retained user phrase', 'retained user');
  await expectPromptFindingOpensSession(page, 'tiq-live-e2e-conversation-content', 'retained-user');
});

test('records a prompt finding retained only by the session JSONL transcript', async ({
  page,
}) => {
  // #259: the prompt exists only on the transcript surface (user_message).
  await ingestClaudeTranscript(claudePromptTranscriptNDJSON());
  await unlockDashboard(page, authToken);
  await page.goto('/governance?rules=prompts');

  await savePromptKeywordRule(page, 'transcript-only', 'Transcript only phrase', 'transcript-only phrase');
  await expectPromptFindingOpensSession(page, claudePromptTranscriptSessionID, 'transcript-only');
});

async function savePromptKeywordRule(
  page: Page,
  id: string,
  label: string,
  pattern: string,
): Promise<void> {
  await page.getByRole('button', { name: 'Add credentials rule' }).click();
  // Saved rules persist in the daemon config across tests; edit the new draft row.
  const row = page.locator('.prompt-rule-list[data-group="credentials"] .prompt-rule-row').last();
  await row.getByRole('textbox', { name: 'Rule ID' }).fill(id);
  await row.getByRole('textbox', { name: 'Rule label' }).fill(label);
  await row.getByRole('textbox', { name: 'Pattern' }).fill(pattern);
  await page.getByRole('button', { name: 'Save local changes' }).first().click();

  await expect(page).toHaveURL(/\/governance\?rules=prompts&saved=1$/);
  await expect(page.getByRole('status')).toContainText('Prompt findings saved');
  await expect(page.getByText(label).first()).toBeVisible();
  await expect(page.getByText('Record finding').first()).toBeVisible();
}

// expectPromptFindingOpensSession follows the governance finding link to the
// session event inspector and asserts the live API reports the observed match.
async function expectPromptFindingOpensSession(
  page: Page,
  sessionID: string,
  ruleID: string,
): Promise<void> {
  await openGovernanceFindings(page);
  await page.getByLabel('Prompt keyword findings').getByRole('link').first().click();
  await expect(page).toHaveURL(
    new RegExp(`/sessions/claude-code:${sessionID}\\?event=.+&inspector=details#event-inspector$`),
  );
  await expect(page.locator('#event-inspector')).toBeVisible();

  const after = await fetch(
    'http://localhost:18080/api/v1/insights/prompt-keywords',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(after.status).toBe(200);
  expect((await after.json()) as unknown).toMatchObject({
    data: {
      outcome: 'violation',
      visibility: 'observed',
      findings: [{ rule_id: ruleID, group: 'credentials' }],
    },
  });
}
