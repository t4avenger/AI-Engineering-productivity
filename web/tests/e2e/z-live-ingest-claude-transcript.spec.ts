import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeLivePRLinkURL,
  claudeMCPTranscriptNDJSON,
  claudePRLinkOTLPTraces,
  claudeTranscriptNDJSON,
  expectSessionDetailHeading,
  fetchLiveSessions,
  ingestClaudeTranscript,
  ingestOTLPTraces,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for Claude session JSONL transcript import (F4, #91).
 * Drives the real daemon from playwright.config.ts — no page.route().fulfill()
 * mocking. POSTs NDJSON to /v1/claude/transcript, then asserts the UI renders
 * the session + assistant_message model/token counts without content canaries.
 * A second gate (#183) proves a tool-span full_command PR URL promotes to an
 * observed pr_link and renders on /pull-requests.
 */
const liveModel = 'tiq-live-e2e-transcript-model';

resetDaemonBetweenTests();

test('renders a Claude transcript session ingested through the live daemon', async ({
  page,
}) => {
  await ingestClaudeTranscript(claudeTranscriptNDJSON(liveModel));

  const sessions = await fetchLiveSessions();
  const transcriptSession = sessions.find(
    (session) => session.attributes?.model === liveModel,
  );
  expect(transcriptSession?.tool).toBe('claude-code');
  expect(transcriptSession?.attributes?.entrypoint).toBe('cli');
  expect(transcriptSession?.attributes?.git_branch).toBe('main');
  expect(transcriptSession?.availability?.entrypoint).toBe('observed');
  expect(transcriptSession?.availability?.git_branch).toBe('observed');
  expect(transcriptSession?.availability?.pr_link).toBe('unavailable');

  await unlockDashboard(page);
  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await expect(
    page.getByRole('cell', { name: 'claude-code' }).first(),
  ).toBeVisible();
  await page.locator('table tbody a').first().click();
  await expectSessionDetailHeading(page);
  await expect(page.getByText('claude-code').first()).toBeVisible();
  await expect(page.getByText(liveModel).first()).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Environment' })).toBeVisible();
  await expect(page.getByText('cli', { exact: true }).first()).toBeVisible();
  await expect(page.getByText('main', { exact: true }).first()).toBeVisible();
  const timeline = page.locator('#timeline');
  await expect(
    timeline.getByRole('heading', { name: 'Assistant message' }),
  ).toBeVisible();
  await expect(timeline.getByText('assistant_message')).toBeVisible();
  await expect(timeline.getByText('2048 tokens')).toBeVisible();
  await expect(timeline.getByText('256 tokens')).toBeVisible();

  const pageText = await page.locator('body').innerText();
  for (const canary of [
    'tiq-canary-live-user-prompt',
    'tiq-canary-live-response',
    'tiq-canary-live-command',
    'tiq-canary-live-stdout',
    'tiq-canary-live-cwd',
  ]) {
    expect(pageText).not.toContain(canary);
  }
});

// #183: a Claude tool span whose raw full_command carries a verbatim
// pull-request URL must promote pr_link to observed through the same
// provider-agnostic aggregation the Codex path uses, and render the URL on
// /pull-requests — closing the always-unavailable Claude cell #158 shipped.
test('promotes a Claude tool-span PR URL to an observed pr_link on the live daemon', async ({
  page,
}) => {
  await unlockDashboard(page);
  await page.goto('/pull-requests');
  await expect(page.getByRole('link', { name: /github\.com/ })).toHaveCount(0);

  await ingestOTLPTraces(claudePRLinkOTLPTraces());

  const sessions = await fetchLiveSessions();
  const prSession = sessions.find(
    (session) =>
      session.session_id === 'claude-code:tiq-live-e2e-session-pr-link',
  );
  expect(prSession?.tool).toBe('claude-code');
  expect(prSession?.attributes?.pr_link).toBe(claudeLivePRLinkURL);
  expect(prSession?.availability?.pr_link).toBe('observed');

  await page.goto('/pull-requests');
  await expect(
    page.getByRole('link', { name: claudeLivePRLinkURL }),
  ).toBeVisible();
});

// J17 (#104): an MCP tool call reconstructed from the JSONL transcript must
// surface as an MCP-call operation and mark its server used with an invocation
// count on the Insights UI — the connected-but-unused vs used state the
// MCP-inventory matcher was previously dead scaffolding for. No content mocking.
const liveMCPServer = 'tiq-live-mcp-fs';

test('surfaces an MCP tool call from a transcript as a used server and MCP-call operation', async ({
  page,
}) => {
  await ingestClaudeTranscript(claudeMCPTranscriptNDJSON(liveMCPServer));

  const inventory = await fetch(
    'http://localhost:18080/api/v1/insights/mcp-inventory',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(inventory.status).toBe(200);
  const inventoryBody = (await inventory.json()) as {
    data: {
      servers: Array<{
        server_name: string;
        used: boolean;
        invocation_count: number;
        tool_names: string[];
        usage_state: string;
      }>;
      totals: { used_servers: number };
    };
  };
  const usedServer = inventoryBody.data.servers.find(
    (server) => server.server_name === liveMCPServer,
  );
  expect(usedServer?.used).toBe(true);
  expect(usedServer?.invocation_count).toBe(1);
  expect(usedServer?.usage_state).toBe('observed');
  expect(usedServer?.tool_names).toContain('read_file');

  const operations = await fetch(
    'http://localhost:18080/api/v1/insights/operations',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(operations.status).toBe(200);
  const operationsBody = (await operations.json()) as {
    data: {
      by_category: Array<{ category: string; count: number }>;
      totals: { total_operations: number };
    };
  };
  expect(operationsBody.data.totals.total_operations).toBe(1);
  expect(
    operationsBody.data.by_category.some(
      (row) => row.category === 'MCP call' && row.count === 1,
    ),
  ).toBe(true);

  await unlockDashboard(page, authToken);
  await page.goto('/insights');
  await expect(page.getByRole('heading', { name: 'MCP inventory' })).toBeVisible();
  await expect(page.getByText(liveMCPServer).first()).toBeVisible();
  await expect(page.getByText('1 invocations').first()).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Operations' })).toBeVisible();
  await expect(page.getByText('MCP call').first()).toBeVisible();

  const pageText = await page.locator('body').innerText();
  for (const canary of [
    'tiq-canary-live-mcp-prompt',
    'tiq-canary-live-mcp-cwd',
    'tiq-canary-live-mcp-command',
    'toolu_live_bash',
  ]) {
    expect(pageText).not.toContain(canary);
  }
});
