import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeGenericToolTranscriptNDJSON,
  claudeLivePRLinkURL,
  claudeMCPTranscriptNDJSON,
  claudePRLinkOTLPTraces,
  claudeTranscriptNDJSON,
  expectOperationCategory,
  expectSessionDetailHeading,
  fetchLiveOperations,
  fetchLiveSessionFiles,
  fetchLiveSessions,
  ingestClaudeTranscript,
  ingestOTLPTraces,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for Claude session JSONL transcript import (F4, #91; #105).
 * Drives the real daemon from playwright.config.ts — no page.route().fulfill()
 * mocking. POSTs NDJSON to /v1/claude/transcript, then asserts the UI renders the
 * session + assistant_message model/token counts, that the Bash tool_use surfaces
 * as a shell-command operation (#105 raw capture), and that cwd — not a #105 signal
 * — never reaches any surface. A second gate (#183) proves a tool-span full_command
 * PR URL promotes to an observed pr_link and renders on /pull-requests.
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

  // #105: the Bash tool_use is reconstructed as a shell-command operation.
  const operations = await fetchLiveOperations();
  expect(operations.totals.total_operations).toBe(1);
  expectOperationCategory(operations, 'shell command', 1);

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

  // cwd is not one of the six #105 signals: it must never reach any surface.
  const pageText = await page.locator('body').innerText();
  expect(pageText).not.toContain('tiq-canary-live-cwd');
});

// #105: every non-MCP tool_use — including an unrecognised Task and a sub-agent
// (sidechain) call — must be promoted to an Operation and classified through the
// shared helper, and the Read/Write target paths must flow through the Files-lane
// read API with no session_files.go changes. No content mocking.
test('surfaces generic tool calls and sub-agent file paths from a transcript', async () => {
  await ingestClaudeTranscript(claudeGenericToolTranscriptNDJSON());

  const operations = await fetchLiveOperations();
  expect(operations.totals.total_operations).toBe(4);
  // Read + sidechain Grep both classify as filesystem read.
  expectOperationCategory(operations, 'filesystem read', 2);
  expectOperationCategory(operations, 'filesystem write', 1);
  // Task is unrecognised but still promoted (never dropped).
  expectOperationCategory(operations, 'unknown', 1);

  const files = await fetchLiveSessionFiles(
    'claude-code:tiq-live-e2e-generic-tool-session',
  );
  const paths = files.map((file) => file.path);
  expect(paths).toContain('/repo/tiq-live-generic-read.go');
  expect(paths).toContain('/repo/tiq-live-generic-write.go');
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

  // #105: the sibling non-MCP Bash tool_use now also becomes a generic shell
  // operation, so the transcript yields two operations.
  const operations = await fetchLiveOperations();
  expect(operations.totals.total_operations).toBe(2);
  expectOperationCategory(operations, 'MCP call', 1);
  expectOperationCategory(operations, 'shell command', 1);

  await unlockDashboard(page, authToken);
  await page.goto('/insights');
  await expect(page.getByRole('heading', { name: 'MCP inventory' })).toBeVisible();
  await expect(page.getByText(liveMCPServer).first()).toBeVisible();
  await expect(page.getByText('1 invocations').first()).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Operations' })).toBeVisible();
  await expect(page.getByText('MCP call').first()).toBeVisible();

  // cwd is not a #105 signal: it must never reach any surface.
  const pageText = await page.locator('body').innerText();
  expect(pageText).not.toContain('tiq-canary-live-mcp-cwd');
});
