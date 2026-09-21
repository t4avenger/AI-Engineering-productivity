import { expect, type Page, test } from '@playwright/test';

/**
 * Shared plumbing for the live-ingest e2e gates: both the sessions and the
 * skill-usage specs drive the real daemon from playwright.config.ts, so the
 * daemon address, auth token, Codex OTLP payload shape and daemon-reset hooks
 * live here once instead of being copied per spec.
 */
const daemonBase = 'http://localhost:18080';
export const authToken = 'playwright-token';

export async function unlockDashboard(
  page: Page,
  token: string = authToken,
): Promise<void> {
  await page.goto('/');
  await page.getByLabel('Local API token').fill(token);
  await page.getByRole('button', { name: 'Unlock' }).click();
  await expect(
    page.getByRole('heading', { name: 'Orchestration overview' }),
  ).toBeVisible();
}

/** Session detail h1; level-scoped so "Session Breakdown" rail does not match. */
export async function expectSessionDetailHeading(page: Page): Promise<void> {
  await expect(
    page.getByRole('heading', { level: 1, name: /^Session / }),
  ).toBeVisible();
}

/** Follow a shell navigation link and verify the destination heading. */
export async function followShellNavigation(
  page: Page,
  navigationLabel: 'Primary navigation' | 'Utility navigation',
  destination: string,
): Promise<void> {
  await page
    .getByLabel(navigationLabel)
    .getByRole('link', { name: destination, exact: true })
    .click();
  await expect(page.getByRole('heading', { name: destination })).toBeVisible();
}

/** Five-destination primary nav (#161 / S01); shared to avoid Sonar CPD. */
export async function expectFiveDestinationPrimaryNav(page: Page): Promise<void> {
  const primaryNavigation = page.getByLabel('Primary navigation');
  await expect(primaryNavigation.getByRole('link')).toHaveText([
    'Overview',
    'Sessions',
    'Pull Requests',
    'Models',
    'Governance',
  ]);
  for (const name of ['Insights', 'Privacy', 'Costs', 'Integrations']) {
    await expect(
      primaryNavigation.getByRole('link', { name, exact: true }),
    ).toHaveCount(0);
  }
}

/** Utility destinations after #161 primary shell. */
export async function expectUtilityDestinations(
  page: Page,
  opts: { costs?: boolean } = {},
): Promise<void> {
  const wantCosts = opts.costs !== false;
  const utilityNavigation = page.getByLabel('Utility navigation');
  const labels = wantCosts
    ? ['Integrations', 'Insights', 'Privacy', 'Costs']
    : ['Integrations', 'Insights', 'Privacy'];
  await expect(utilityNavigation.getByRole('link')).toHaveText(labels);
}

/** Access Rules tab shells (#160): MCP editable; others honest unavailable. */
export async function expectGovernanceAccessRulesShells(
  page: Page,
): Promise<void> {
  await expect(
    page.getByRole('heading', { name: 'Access Rules' }),
  ).toBeVisible();
  const tablist = page.getByRole('tablist', { name: 'Access Rules categories' });
  await expect(tablist.getByRole('tab')).toHaveText([
    'MCP servers',
    'Skills',
    'Files & Paths',
    'Prompt Keywords',
  ]);
  await expect(page.getByRole('button', { name: 'Save allowlist' })).toBeVisible();

  await page.getByRole('tab', { name: 'Skills' }).click();
  await expect(page).toHaveURL(/\/governance\?rules=skills$/);
  await expect(page.getByText('No allow or block counts are shown')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save allowlist' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Publish' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Enforce' })).toHaveCount(0);
}

// codexOTLPLogs builds a raw codex_cli_rs OTLP/HTTP log payload carrying a
// single sse_event that reports the given model, in the observed wire shape.
export function codexOTLPLogs(model: string): string {
  return JSON.stringify({
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
                    key: 'conversation.id',
                    value: { stringValue: 'tiq-live-e2e-codex-session' },
                  },
                  { key: 'model', value: { stringValue: model } },
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
}

// codexPRLinkOTLPLogs is the reviewed 0.155.1 tool-result shape where Codex
// retains a provider-emitted pull-request URL inside the tool arguments.
export function codexPRLinkOTLPLogs(): string {
  return JSON.stringify({
    resourceLogs: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'codex_exec' } },
            { key: 'service.version', value: { stringValue: '0.155.1' } },
          ],
        },
        scopeLogs: [
          {
            logRecords: [
              {
                attributes: [
                  { key: 'event.name', value: { stringValue: 'codex.tool_result' } },
                  { key: 'conversation.id', value: { stringValue: 'tiq-live-e2e-pr-link' } },
                  { key: 'tool_name', value: { stringValue: 'exec_command' } },
                  {
                    key: 'arguments',
                    value: {
                      stringValue:
                        'gh pr view https://gitlab.example.test/group/project/-/merge_requests/184',
                    },
                  },
                ],
              },
            ],
          },
        ],
      },
    ],
  });
}

// Codex token metrics do not carry a conversation identifier in the observed
// 0.153.4 surface. They must remain inspectable observations without inflating
// the primary session list.
export function codexTurnTokenOTLPMetrics(): string {
  const tokenTypes: Array<[string, string]> = [
    ['input', '1200'],
    ['cached_input', '300'],
    ['cache_write_input', '75'],
    ['output', '144'],
    ['reasoning_output', '55'],
    ['total', '1774'],
  ];
  return JSON.stringify({
    resourceMetrics: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'codex_exec' } },
            { key: 'service.version', value: { stringValue: '0.153.4' } },
          ],
        },
        scopeMetrics: [
          {
            metrics: [
              {
                name: 'codex.turn.token_usage',
                histogram: {
                  dataPoints: tokenTypes.map(([tokenType, sum], index) => ({
                    attributes: [
                      {
                        key: 'model',
                        value: { stringValue: 'gpt-5-codex-live' },
                      },
                      {
                        key: 'token_type',
                        value: { stringValue: tokenType },
                      },
                    ],
                    count: '1',
                    sum,
                    timeUnixNano: `178904216000000000${index}`,
                  })),
                },
              },
            ],
          },
        ],
      },
    ],
  });
}

// Codex CLI 0.154.0 was observed exporting this resource/scope/span shape via
// the separate otel.trace_exporter. The prompt used during capture is absent
// because log_user_prompt=false.
export function codexOTLPTraces(conversationID?: string): string {
  return JSON.stringify({
    resourceSpans: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'codex_exec' } },
            { key: 'service.version', value: { stringValue: '0.154.0' } },
            {
              key: 'env',
              value: { stringValue: 'telemetryiq-synthetic' },
            },
            ...(conversationID
              ? [
                  {
                    key: 'conversation.id',
                    value: { stringValue: conversationID },
                  },
                ]
              : []),
          ],
        },
        scopeSpans: [
          {
            scope: { name: 'codex_exec' },
            spans: [
              {
                traceId: 'dddddddddddddddddddddddddddddddd',
                spanId: '1111111111111111',
                parentSpanId: '',
                name: 'turn/start',
                startTimeUnixNano: '1789671946326852067',
                endTimeUnixNano: '1789671946357351775',
                attributes: [],
                status: { code: 0 },
              },
              {
                traceId: 'dddddddddddddddddddddddddddddddd',
                spanId: '2222222222222222',
                parentSpanId: '1111111111111111',
                name: 'session_task.turn',
                startTimeUnixNano: '1789671946355925969',
                endTimeUnixNano: '1789671953383319471',
                attributes: [
                  {
                    key: 'codex.turn.token_usage.input_tokens',
                    value: { intValue: '1200' },
                  },
                  {
                    key: 'codex.turn.token_usage.output_tokens',
                    value: { intValue: '12' },
                  },
                ],
                status: { code: 0 },
              },
            ],
          },
        ],
      },
    ],
  });
}

type OTLPLogRecord = {
  attributes: OTLPAttribute[];
  body?: { stringValue: string };
  severityText?: string;
  timeUnixNano?: string;
};

function otlpLogs(
  serviceName: string,
  serviceVersion: string,
  logRecords: OTLPLogRecord[],
): string {
  return JSON.stringify({
    resourceLogs: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: serviceName } },
            { key: 'service.version', value: { stringValue: serviceVersion } },
          ],
        },
        scopeLogs: [{ logRecords }],
      },
    ],
  });
}

function codexExecOTLPLogs(logRecords: OTLPLogRecord[]): string {
  return otlpLogs('codex_exec', '0.153.4', logRecords);
}

function codexExecOTLPLog(attributes: OTLPAttribute[], body: string): string {
  return codexExecOTLPLogs([{ attributes, body: { stringValue: body } }]);
}

function codexStringAttrs(
  entries: Array<[string, string]>,
): Array<{ key: string; value: { stringValue: string } }> {
  return entries.map(([key, stringValue]) => ({ key, value: { stringValue } }));
}

export function codexCachedReasoningOTLPLogs(): string {
  return codexExecOTLPLog(
    codexStringAttrs([
      ['event.name', 'codex.sse_event'],
      ['conversation.id', 'tiq-live-e2e-codex-token-session'],
      ['model', 'gpt-5-codex-live'],
      ['input_token_count', '1200'],
      ['cached_token_count', '300'],
      ['output_token_count', '144'],
      ['reasoning_token_count', '55'],
      ['arguments', '--token=tiq-canary-live-codex-token'],
    ]),
    'tiq-canary-live-codex-token-body',
  );
}

export function codexToolResultOTLPLogs(): string {
  return codexToolResultOTLPLog({
    conversationId: 'tiq-live-e2e-operation-codex',
    toolName: 'exec_command',
    callId: 'tiq-live-e2e-operation-call',
    durationMs: '92',
    canary: 'tiq-canary-live-operation',
  });
}

export function claudeToolResultOTLPLogs(): string {
  return JSON.stringify({
    resourceLogs: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'claude-code' } },
            { key: 'service.version', value: { stringValue: '2.1.263' } },
          ],
        },
        scopeLogs: [
          {
            logRecords: [
              {
                attributes: [
                  { key: 'event.name', value: { stringValue: 'tool_result' } },
                  {
                    key: 'event.timestamp',
                    value: { stringValue: '2026-09-06T14:50:01Z' },
                  },
                  { key: 'event.sequence', value: { intValue: '11' } },
                  {
                    key: 'session.id',
                    value: { stringValue: 'tiq-live-e2e-operation-claude' },
                  },
                  { key: 'tool_name', value: { stringValue: 'Bash' } },
                  {
                    key: 'tool_use_id',
                    value: { stringValue: 'toolu_live_operation_bash' },
                  },
                  { key: 'duration_ms', value: { intValue: '1234' } },
                  { key: 'success', value: { stringValue: 'true' } },
                  {
                    key: 'tool_input',
                    value: { stringValue: 'tiq-canary-live-operation-input' },
                  },
                ],
                body: { stringValue: 'tiq-canary-live-operation-claude-body' },
              },
            ],
          },
        ],
      },
    ],
  });
}

// Builds a single Claude enhanced-telemetry tool span, parameterised so the
// file-path (#156) and PR-link (#183) live gates share one OTLP shape instead
// of pasting two near-identical span builders (SonarCloud CPD hard rule).
// extraAttributes carries the surface under test (file_path / full_command).
function claudeToolSpanOTLPTraces(opts: {
  traceId: string;
  spanId: string;
  sessionId: string;
  toolName: string;
  toolUseId: string;
  extraAttributes: Array<{ key: string; value: { stringValue: string } }>;
}): string {
  return JSON.stringify({
    resourceSpans: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'claude-code' } },
            { key: 'service.version', value: { stringValue: '2.1.268' } },
            { key: 'host.arch', value: { stringValue: 'amd64' } },
            { key: 'os.type', value: { stringValue: 'linux' } },
          ],
        },
        scopeSpans: [
          {
            scope: {
              name: 'com.anthropic.claude_code.tracing',
              version: '1.0.0',
            },
            spans: [
              {
                traceId: opts.traceId,
                spanId: opts.spanId,
                name: 'claude_code.tool',
                kind: 1,
                startTimeUnixNano: '1789117600500000000',
                endTimeUnixNano: '1789117600700000000',
                attributes: [
                  {
                    key: 'session.id',
                    value: { stringValue: opts.sessionId },
                  },
                  { key: 'span.type', value: { stringValue: 'tool' } },
                  { key: 'tool_name', value: { stringValue: opts.toolName } },
                  {
                    key: 'tool_name_safe',
                    value: { stringValue: opts.toolName },
                  },
                  {
                    key: 'tool_use_id',
                    value: { stringValue: opts.toolUseId },
                  },
                  {
                    key: 'gen_ai.tool.call.id',
                    value: { stringValue: opts.toolUseId },
                  },
                  ...opts.extraAttributes,
                  { key: 'duration_ms', value: { intValue: '200' } },
                  { key: 'result_tokens', value: { intValue: '64' } },
                ],
                status: { code: 0 },
              },
            ],
          },
        ],
      },
    ],
  });
}

// Claude enhanced-telemetry tool span with a retained file_path (#156 / T06).
// Session id is live-e2e-specific so file evidence assertions stay isolated.
export function claudeToolSpanFilePathOTLPTraces(): string {
  return claudeToolSpanOTLPTraces({
    traceId: 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee',
    spanId: 'aaaaaaaaaaaaaaaa',
    sessionId: 'tiq-live-e2e-session-files',
    toolName: 'Read',
    toolUseId: 'toolu_live_session_files_read',
    extraAttributes: [
      {
        key: 'file_path',
        value: { stringValue: '/workspace/tiq-live-e2e-session-files.go' },
      },
    ],
  });
}

// Claude enhanced-telemetry Bash tool span whose raw full_command carries a
// verbatim pull-request URL (#183). Proves the shared provider-agnostic
// extractor (normalize.AttachPRLinkEvidence) promotes availability.pr_link to
// observed from a tool span — the same header cell the Codex path fills. A
// distinct session id keeps the /pull-requests assertion isolated.
export const claudeLivePRLinkURL =
  'https://github.com/acme-synthetic/telemetryiq/pull/183';
export function claudePRLinkOTLPTraces(): string {
  return claudeToolSpanOTLPTraces({
    traceId: '00000000000000000000000000000183',
    spanId: '0000000000000b02',
    sessionId: 'tiq-live-e2e-session-pr-link',
    toolName: 'Bash',
    toolUseId: 'toolu_live_session_pr_link_bash',
    extraAttributes: [
      {
        key: 'full_command',
        value: {
          stringValue: `gh pr view ${claudeLivePRLinkURL} --json state`,
        },
      },
    ],
  });
}

// Codex apply_patch tool_result proves filesystem write category without a path.
export function codexFilesystemWriteOTLPLogs(): string {
  return codexToolResultOTLPLog({
    conversationId: 'tiq-live-e2e-session-files-codex',
    toolName: 'apply_patch',
    callId: 'tiq-live-e2e-session-files-write',
    durationMs: '40',
    canary: 'tiq-canary-live-session-files',
  });
}

function codexToolResultOTLPLog(opts: {
  conversationId: string;
  toolName: string;
  callId: string;
  durationMs: string;
  canary: string;
}): string {
  return codexExecOTLPLog(
    [
      { key: 'event.name', value: { stringValue: 'codex.tool_result' } },
      {
        key: 'conversation.id',
        value: { stringValue: opts.conversationId },
      },
      { key: 'tool_name', value: { stringValue: opts.toolName } },
      { key: 'tool_namespace', value: { stringValue: 'functions' } },
      { key: 'call_id', value: { stringValue: opts.callId } },
      { key: 'duration_ms', value: { stringValue: opts.durationMs } },
      { key: 'success', value: { stringValue: 'true' } },
      {
        key: 'arguments',
        value: { stringValue: `--token=${opts.canary}` },
      },
    ],
    `${opts.canary}-body`,
  );
}


export function codexLifecycleOTLPLogs(): string {
  return codexExecOTLPLogs([
    {
      attributes: codexStringAttrs([
        ['event.name', 'codex.conversation_starts'],
        ['conversation.id', 'tiq-live-e2e-lifecycle-session'],
        ['model', 'gpt-6-astra'],
        ['approval_policy', 'on-request'],
        ['sandbox_policy', 'workspace-write'],
        ['auth_mode', 'api-key'],
        ['terminal.type', 'pty'],
        ['slug', 'tiq-canary-live-lifecycle-slug'],
        ['user.email', 'lifecycle-live@example.test'],
      ]),
      body: { stringValue: 'tiq-canary-live-lifecycle-body' },
      timeUnixNano: '1788717763000000000',
    },
    {
      attributes: codexStringAttrs([
        ['event.name', 'codex.startup_phase'],
        ['conversation.id', 'tiq-live-e2e-lifecycle-session'],
        ['startup.phase', 'init'],
        ['startup.status', 'ok'],
        ['duration_ms', '17'],
      ]),
      timeUnixNano: '1788717763000000001',
    },
    {
      attributes: [
        ...codexStringAttrs([
          ['event.name', 'codex.websocket_connect'],
          ['conversation.id', 'tiq-live-e2e-lifecycle-session'],
          ['duration_ms', '23'],
        ]),
        { key: 'success', value: { boolValue: true } },
      ],
      timeUnixNano: '1788717763000000002',
    },
  ]);
}

export function codexToolDecisionOTLPLogs(): string {
  return codexExecOTLPLogs([
    {
      attributes: codexStringAttrs([
        ['event.name', 'codex.tool_decision'],
        ['conversation.id', 'tiq-live-e2e-decision-session'],
        ['call_id', 'tiq-live-e2e-decision-call'],
        ['decision', 'allow'],
        ['source', 'policy'],
        ['tool_name', 'exec_command'],
        ['tool_namespace', 'functions'],
        ['arguments', '--token=tiq-canary-live-decision'],
        ['user.email', 'decision-live@example.test'],
      ]),
      body: { stringValue: 'tiq-canary-live-decision-body' },
    },
  ]);
}

// clearSessions empties the shared daemon so retries cannot pass on leftovers.
async function clearSessions(): Promise<void> {
  const response = await fetch(`${daemonBase}/api/v1/sessions`, {
    method: 'DELETE',
    headers: { Authorization: `Bearer ${authToken}` },
  });
  expect(response.status).toBe(204);
}

// resetDaemonBetweenTests wires clearSessions around every test in the calling
// spec so specs start and leave the shared daemon empty.
export function resetDaemonBetweenTests(): void {
  test.beforeEach(clearSessions);
  test.afterEach(clearSessions);
}

// A retained session row as returned by GET /api/v1/sessions, narrowed to the
// fields the live gates assert. Kept here so specs share one fetch+parse shape
// instead of copying the list request (SonarCloud CPD hard rule).
export interface LiveSessionRow {
  tool?: string;
  session_id?: string;
  state?: string;
  completed_at?: string | null;
  attributes?: Record<string, string>;
  provider_extensions?: Record<string, Record<string, string>>;
  availability?: Record<string, string>;
}

// fetchLiveSessions reads the authenticated session list from the live daemon
// and asserts a 200 before returning the rows.
export async function fetchLiveSessions(limit = 100): Promise<LiveSessionRow[]> {
  const response = await fetch(
    `${daemonBase}/api/v1/sessions?limit=${limit}`,
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(response.status).toBe(200);
  const body = (await response.json()) as { data: LiveSessionRow[] };
  return body.data;
}

/** Full-session duration breakdown (#190 / T09); never page-scoped. */
export type LiveBreakdown = {
  availability: string;
  calculation_version: string;
  window: { duration_ms: number } | null;
  categories: Array<{
    id: string;
    label: string;
    duration_ms: number;
    source_event_ids: string[];
  }>;
  overlap: { duration_ms: number } | null;
  unclassified: { duration_ms: number } | null;
  unavailable_reason: string | null;
};

export async function fetchSessionBreakdown(
  sessionId: string,
): Promise<LiveBreakdown> {
  const response = await fetch(
    `${daemonBase}/api/v1/sessions/${encodeURIComponent(sessionId)}/breakdown`,
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(response.status).toBe(200);
  const body = (await response.json()) as { data: LiveBreakdown };
  return body.data;
}

/** Right-rail T09 assertions shared by live breakdown coverage. */
export async function expectSessionBreakdownRail(
  page: Page,
  opts: { available: boolean; categoryLabels?: string[] },
): Promise<void> {
  const rail = page.getByLabel('Session summary');
  await expect(rail.getByRole('heading', { name: 'Session Breakdown' })).toBeVisible();
  await expect(rail.getByRole('heading', { name: 'Governance' })).toBeVisible();
  await expect(rail.getByRole('heading', { name: 'Event Legend' })).toBeVisible();
  if (opts.available) {
    for (const label of opts.categoryLabels ?? []) {
      await expect(rail.getByText(label, { exact: true })).toBeVisible();
    }
    await expect(rail.getByRole('link', { name: 'Evidence' }).first()).toBeVisible();
  } else {
    await expect(rail.getByText(/Duration breakdown unavailable/)).toBeVisible();
    await expect(rail.locator('.breakdown-donut')).toHaveCount(0);
  }
}

// ingestOTLPLogs POSTs a raw OTLP log payload to the live /v1/logs receiver and
// asserts it was accepted.
export async function ingestOTLPLogs(body: string): Promise<void> {
  const ingest = await fetch(`${daemonBase}/v1/logs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body,
  });
  expect(ingest.status).toBe(202);
}

// ingestOTLPMetrics POSTs a raw OTLP metrics payload to /v1/metrics.
export async function ingestOTLPMetrics(body: string): Promise<void> {
  const ingest = await fetch(`${daemonBase}/v1/metrics`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body,
  });
  expect(ingest.status).toBe(202);
}

// ingestOTLPTraces POSTs a raw OTLP trace payload to /v1/traces.
export async function ingestOTLPTraces(body: string): Promise<void> {
  const ingest = await fetch(`${daemonBase}/v1/traces`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body,
  });
  expect(ingest.status).toBe(202);
}

// ingestClaudeTranscript POSTs a session JSONL transcript to /v1/claude/transcript
// (F4, #91). Content-Type must be application/x-ndjson — the route rejects JSON.
export async function ingestClaudeTranscript(body: string): Promise<void> {
  const ingest = await fetch(`${daemonBase}/v1/claude/transcript`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-ndjson' },
    body,
  });
  expect(ingest.status).toBe(202);
}

/**
 * Synthetic Claude Code session JSONL for the live transcript UI gate. Content
 * bodies carry canaries that must never appear in the dashboard; model + token
 * counts must surface.
 */
export function claudeTranscriptNDJSON(model: string): string {
  const session = 'tiq-live-e2e-transcript-session';
  return [
    JSON.stringify({
      type: 'user',
      uuid: 'tiq-live-user-1',
      sessionId: session,
      timestamp: '2026-09-12T12:00:00.000Z',
      version: '2.1.269',
      message: { role: 'user', content: 'tiq-canary-live-user-prompt' },
    }),
    JSON.stringify({
      type: 'assistant',
      uuid: 'tiq-live-assistant-1',
      parentUuid: 'tiq-live-user-1',
      sessionId: session,
      timestamp: '2026-09-12T12:00:02.500Z',
      version: '2.1.269',
      cwd: '/home/tiq-canary-live-cwd/project',
      gitBranch: 'main',
      entrypoint: 'cli',
      requestId: 'req_live_e2e_1',
      message: {
        role: 'assistant',
        model,
        stop_reason: 'end_turn',
        content: [
          { type: 'text', text: 'tiq-canary-live-response' },
          {
            type: 'tool_use',
            name: 'Bash',
            input: { command: 'tiq-canary-live-command' },
          },
        ],
        usage: {
          input_tokens: 2048,
          output_tokens: 256,
          cache_read_input_tokens: 4096,
          cache_creation_input_tokens: 64,
          output_tokens_details: { thinking_tokens: 32 },
        },
      },
      toolUseResult: { stdout: 'tiq-canary-live-stdout' },
    }),
  ].join('\n');
}

/**
 * Synthetic Claude Code session JSONL bearing one MCP tool call (J17, #104): an
 * assistant `tool_use` named mcp__<server>__read_file alongside a non-MCP Bash
 * tool_use, paired with a later user `tool_result`. Drives the transcript path to
 * reconstruct one MCP-call operation and mark the server used. The prompt, cwd,
 * and the non-MCP Bash command carry canaries that must never reach the UI; the
 * MCP arguments/result are synthetic, ordinary data (captured raw per #104).
 */
export function claudeMCPTranscriptNDJSON(serverName: string): string {
  const session = 'tiq-live-e2e-mcp-transcript-session';
  return [
    JSON.stringify({
      type: 'user',
      uuid: 'tiq-live-mcp-user-1',
      sessionId: session,
      timestamp: '2026-09-12T13:00:00.000Z',
      version: '2.1.269',
      message: { role: 'user', content: 'tiq-canary-live-mcp-prompt' },
    }),
    JSON.stringify({
      type: 'assistant',
      uuid: 'tiq-live-mcp-assistant-1',
      parentUuid: 'tiq-live-mcp-user-1',
      sessionId: session,
      timestamp: '2026-09-12T13:00:02.000Z',
      version: '2.1.269',
      cwd: '/home/tiq-canary-live-mcp-cwd/project',
      gitBranch: 'main',
      entrypoint: 'cli',
      requestId: 'req_live_mcp_e2e_1',
      message: {
        role: 'assistant',
        model: 'claude-opus-4-8',
        stop_reason: 'tool_use',
        content: [
          {
            type: 'tool_use',
            id: 'toolu_live_mcp_read',
            name: `mcp__${serverName}__read_file`,
            input: { path: 'docs/overview.md' },
          },
          {
            type: 'tool_use',
            id: 'toolu_live_bash',
            name: 'Bash',
            input: { command: 'tiq-canary-live-mcp-command' },
          },
        ],
        usage: { input_tokens: 64, output_tokens: 8 },
      },
    }),
    JSON.stringify({
      type: 'user',
      uuid: 'tiq-live-mcp-user-2',
      parentUuid: 'tiq-live-mcp-assistant-1',
      sessionId: session,
      timestamp: '2026-09-12T13:00:03.000Z',
      message: {
        role: 'user',
        content: [
          {
            type: 'tool_result',
            tool_use_id: 'toolu_live_mcp_read',
            is_error: false,
            content: [{ type: 'text', text: '# Overview' }],
          },
        ],
      },
    }),
  ].join('\n');
}

type OTLPAttributeValue =
  | { stringValue: string }
  | { intValue: string }
  | { boolValue: boolean };

type OTLPAttribute = { key: string; value: OTLPAttributeValue };

function claudeOTLPLogs(logRecords: Array<{ attributes: OTLPAttribute[] }>): string {
  return JSON.stringify({
    resourceLogs: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'claude-code' } },
            { key: 'service.version', value: { stringValue: '2.1.263' } },
          ],
        },
        scopeLogs: [{ logRecords }],
      },
    ],
  });
}

// claudeContentOTLPLogs uses the reviewed content-bearing Claude log shape.
// Its values are synthetic and intentionally asserted as retained local content
// by the #188 daemon-to-UI gate.
export function claudeContentOTLPLogs(): string {
  const sessionId = 'tiq-live-e2e-conversation-content';
  const contentEvents = [
    ['user_prompt', '2026-09-19T12:00:00Z', '1', 'prompt', 'tiq-live-e2e retained user\nsecond line'],
    ['assistant_response', '2026-09-19T12:00:01Z', '2', 'response', '<REDACTED>'],
    ['api_response_body', '2026-09-19T12:00:02Z', '3', 'body', 'tiq-live-e2e raw API evidence'],
  ];
  return claudeOTLPLogs(
    contentEvents.map(([eventName, timestamp, sequence, contentKey, content]) =>
      claudeContentEvent(eventName, timestamp, sequence, sessionId, contentKey, content),
    ),
  );
}

function claudeContentEvent(
  eventName: string,
  timestamp: string,
  sequence: string,
  sessionId: string,
  contentKey: string,
  content: string,
): { attributes: OTLPAttribute[] } {
  return {
    attributes: [
      { key: 'event.name', value: { stringValue: eventName } },
      { key: 'event.timestamp', value: { stringValue: timestamp } },
      { key: 'event.sequence', value: { intValue: sequence } },
      { key: 'session.id', value: { stringValue: sessionId } },
      { key: contentKey, value: { stringValue: content } },
    ],
  };
}

type ClaudeApiRequestOptions = {
  timestamp: string;
  sequence: string;
  sessionId: string;
  requestId?: string;
  model: string;
  inputTokens: string;
  outputTokens?: string;
  durationMs?: string;
  cacheReadTokens?: string;
};

function claudeApiRequestAttrs(options: ClaudeApiRequestOptions): OTLPAttribute[] {
  const attrs: OTLPAttribute[] = [
    { key: 'event.name', value: { stringValue: 'api_request' } },
    { key: 'event.timestamp', value: { stringValue: options.timestamp } },
    { key: 'event.sequence', value: { intValue: options.sequence } },
    { key: 'session.id', value: { stringValue: options.sessionId } },
    { key: 'model', value: { stringValue: options.model } },
    { key: 'input_tokens', value: { intValue: options.inputTokens } },
  ];
  if (options.requestId) {
    attrs.push({ key: 'request_id', value: { stringValue: options.requestId } });
  }
  if (options.outputTokens) {
    attrs.push({ key: 'output_tokens', value: { intValue: options.outputTokens } });
  }
  if (options.durationMs) {
    attrs.push({ key: 'duration_ms', value: { intValue: options.durationMs } });
  }
  if (options.cacheReadTokens) {
    attrs.push({
      key: 'cache_read_tokens',
      value: { intValue: options.cacheReadTokens },
    });
  }
  return attrs;
}

// claudeSkillOTLPLogs is a sanitised Claude Code skill_activated OTLP log payload.
export function claudeSkillOTLPLogs(): string {
  return claudeOTLPLogs([
    {
      attributes: [
        { key: 'event.name', value: { stringValue: 'skill_activated' } },
        {
          key: 'event.timestamp',
          value: { stringValue: '2026-09-06T14:50:00.962Z' },
        },
        { key: 'event.sequence', value: { intValue: '9' } },
        {
          key: 'session.id',
          value: { stringValue: 'tiq-live-e2e-skill-session' },
        },
        { key: 'skill.name', value: { stringValue: 'tiq-probe' } },
        { key: 'invocation_trigger', value: { stringValue: 'user-slash' } },
        { key: 'skill.source', value: { stringValue: 'projectSettings' } },
      ],
    },
  ]);
}

// codexSkillOTLPMetrics is a sanitised Codex skill.injected OTLP metrics payload.
export function codexSkillOTLPMetrics(): string {
  return JSON.stringify({
    resourceMetrics: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'codex_exec' } },
            { key: 'service.version', value: { stringValue: '0.153.4' } },
          ],
        },
        scopeMetrics: [
          {
            metrics: [
              {
                name: 'codex.skill.injected',
                sum: {
                  dataPoints: [
                    {
                      attributes: [
                        { key: 'skill', value: { stringValue: 'tiq-probe' } },
                        { key: 'status', value: { stringValue: 'ok' } },
                        {
                          key: 'invoke_type',
                          value: { stringValue: 'explicit' },
                        },
                      ],
                      asInt: 1,
                      timeUnixNano: '1788706421601372612',
                    },
                  ],
                },
              },
            ],
          },
        ],
      },
    ],
  });
}

/** Claude api_request provider-completion success contract for scorecard e2e. */
export function claudeOutcomeSuccessOTLPLogs(): string {
  return claudeOTLPLogs([
    {
      attributes: claudeApiRequestAttrs({
        timestamp: '2026-09-06T18:05:00.100Z',
        sequence: '3',
        sessionId: 'tiq-live-e2e-outcome-session',
        model: 'claude-haiku-4-5-20251001',
        inputTokens: '12',
        outputTokens: '4',
        durationMs: '842',
      }),
    },
  ]);
}

/** Codex tool_result + api_request outcome contracts for scorecard e2e. */
export function codexOutcomeOTLPLogs(): string {
  return codexExecOTLPLogs([
    {
      attributes: codexStringAttrs([
        ['event.name', 'codex.tool_result'],
        ['tool_name', 'exec_command'],
        ['success', 'true'],
        ['model', 'gpt-6-astra'],
        ['duration_ms', '92'],
        ['call_id', 'synthetic-call'],
      ]),
      severityText: 'INFO',
    },
    {
      attributes: [
        ...codexStringAttrs([
          ['event.name', 'codex.api_request'],
          ['model', 'gpt-6-astra'],
          ['attempt', '2'],
          ['duration_ms', '268'],
          ['http.status_code', '401'],
          ['error_code', 'http_401'],
        ]),
        { key: 'success', value: { boolValue: false } },
      ],
      severityText: 'INFO',
    },
  ]);
}

// cursorEnterpriseAPIRequestLogs is a sanitised Cursor Enterprise OTEL
// api.request log payload for the live e2e gate (#130).
export function cursorEnterpriseAPIRequestLogs(model: string): string {
  return JSON.stringify({
    resourceLogs: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'cursor' } },
            { key: 'service.version', value: { stringValue: '1.2.3-synthetic' } },
            { key: 'cursor.team.id', value: { intValue: '424242' } },
            { key: 'cursor.user.id', value: { intValue: '434343' } },
            { key: 'cursor.surface', value: { stringValue: 'cli' } },
            { key: 'cursor.entrypoint', value: { stringValue: 'cli' } },
          ],
        },
        scopeLogs: [
          {
            scope: { name: 'cursor.telemetry', version: '0.1.0' },
            logRecords: [
              {
                timeUnixNano: '1790000200000000000',
                severityNumber: 9,
                body: { stringValue: 'api_request' },
                attributes: [
                  {
                    key: 'cursor.event.id',
                    value: {
                      stringValue: 'customer-telemetry:v1:tiq-live-cursor-event',
                    },
                  },
                  {
                    key: 'cursor.source_event.id',
                    value: { stringValue: 'tiq-live-cursor-source' },
                  },
                  {
                    key: 'cursor.request.id',
                    value: { stringValue: 'tiq-live-cursor-request' },
                  },
                  {
                    key: 'cursor.conversation.id',
                    value: { stringValue: 'tiq-live-e2e-cursor-session' },
                  },
                  {
                    key: 'cursor.api.request.input_tokens',
                    value: { intValue: '88' },
                  },
                  {
                    key: 'cursor.api.request.output_tokens',
                    value: { intValue: '22' },
                  },
                  {
                    key: 'cursor.api.request.cache_read_tokens',
                    value: { intValue: '0' },
                  },
                  {
                    key: 'cursor.api.request.cache_creation_tokens',
                    value: { intValue: '0' },
                  },
                  { key: 'cursor.model.name', value: { stringValue: model } },
                  { key: 'cursor.api.billable', value: { boolValue: true } },
                ],
              },
            ],
          },
        ],
      },
    ],
  });
}

/** Claude api_request logs carrying cache-read tokens for the context-waste insight. */
export function claudeContextWasteOTLPLogs(): string {
  const session = 'tiq-live-e2e-context-waste-session';
  return claudeOTLPLogs([
    {
      attributes: claudeApiRequestAttrs({
        timestamp: '2026-09-06T19:00:00.000Z',
        sequence: '1',
        sessionId: session,
        requestId: 'synthetic-request-1',
        model: 'claude-opus-4-8',
        inputTokens: '100',
        cacheReadTokens: '75',
      }),
    },
    {
      attributes: claudeApiRequestAttrs({
        timestamp: '2026-09-06T19:00:01.000Z',
        sequence: '2',
        sessionId: session,
        requestId: 'synthetic-request-2',
        model: 'claude-opus-4-8',
        inputTokens: '200',
        cacheReadTokens: '150',
      }),
    },
  ]);
}

/** Claude MCP lifecycle event carrying a provider-reported server identity. */
export function claudeMCPConnectionOTLPLogs(serverName: string): string {
  return claudeOTLPLogs([
    {
      attributes: [
        {
          key: 'event.name',
          value: { stringValue: 'mcp_server_connection' },
        },
        {
          key: 'event.timestamp',
          value: { stringValue: '2026-09-06T19:05:00.000Z' },
        },
        { key: 'event.sequence', value: { intValue: '2' } },
        {
          key: 'session.id',
          value: { stringValue: 'tiq-live-e2e-mcp-policy-session' },
        },
        { key: 'status', value: { stringValue: 'connected' } },
        { key: 'transport_type', value: { stringValue: 'stdio' } },
        { key: 'server_scope', value: { stringValue: 'user' } },
        { key: 'is_plugin', value: { boolValue: false } },
        { key: 'server_name', value: { stringValue: serverName } },
      ],
    },
  ]);
}

/** Claude api_request carrying raw .env path/command evidence for Governance (#151). */
export function claudeRiskyAccessOTLPLogs(): string {
  return JSON.stringify({
    resourceLogs: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'claude-code' } },
            { key: 'service.version', value: { stringValue: '2.1.263' } },
          ],
        },
        scopeLogs: [
          {
            logRecords: [
              {
                attributes: [
                  {
                    key: 'event.name',
                    value: { stringValue: 'api_request' },
                  },
                  {
                    key: 'event.timestamp',
                    value: { stringValue: '2026-09-06T19:00:00.000Z' },
                  },
                  {
                    key: 'event.sequence',
                    value: { intValue: '1' },
                  },
                  {
                    key: 'session.id',
                    value: { stringValue: 'tiq-live-e2e-governance-session' },
                  },
                  {
                    key: 'model',
                    value: { stringValue: 'claude-opus-4-8' },
                  },
                  {
                    key: 'file_path',
                    value: {
                      stringValue: '/home/dev/secret-app/.env',
                    },
                  },
                  {
                    key: 'command',
                    value: {
                      stringValue:
                        'cat /home/dev/secret-app/.env --password s3cr3t-value',
                    },
                  },
                ],
              },
            ],
          },
        ],
      },
    ],
  });
}
