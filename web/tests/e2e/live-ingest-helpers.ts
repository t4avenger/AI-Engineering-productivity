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
  await expect(page.getByText('Session Trace', { exact: true }).first()).toBeVisible();
  await openSessionTraceDisclosures(page);
}

/** Chronological lists under the Session Trace (T10 accessible alternative). */
export function chronologicalLists(page: Page) {
  return page.locator('#chronological-list');
}

/** Five-lane Session Trace shell (#159 / T02); shared to avoid Sonar CPD. */
export async function expectSessionTraceLanes(page: Page): Promise<void> {
  await openSessionTraceDisclosures(page);
  const trace = page.getByLabel('Shared session time axis');
  await expect(trace).toBeVisible();
  await Promise.all(
    [
      'Conversation lane',
      'Agent lane',
      'Tools & MCP lane',
      'Files lane',
      'Spans lane',
    ].map((lane) => expect(page.getByLabel(lane)).toBeVisible()),
  );
}

/** Environment and chronological evidence stay closed until the reader opens them. */
export async function openSessionTraceDisclosures(page: Page): Promise<void> {
  await openDisclosure(page, '.session-trace-extra');
  await openDisclosure(page, '#chronological-list');
}

async function openDisclosure(page: Page, selector: string): Promise<void> {
  const details = page.locator(selector);
  if ((await details.count()) === 0) return;
  if ((await details.getAttribute('open')) === null) {
    await details.locator('summary').click();
  }
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
  await Promise.all(
    ['Insights', 'Privacy', 'Costs', 'Integrations'].map((name) =>
      expect(primaryNavigation.getByRole('link', { name, exact: true })).toHaveCount(0),
    ),
  );
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

/** Access Rules tabs: MCP and Skills are local editors; later policy areas remain unavailable. */
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
  await expect(page.getByRole('heading', { name: 'Governance Policies' })).toBeVisible();
  await expect(page.getByLabel('Local policy summary')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Policy Preview' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save local changes' }).first()).toBeVisible();

  await page.getByRole('tab', { name: 'Skills' }).click();
  await expect(page).toHaveURL(/\/governance\?rules=skills$/);
  await expect(page.locator('#skills-allowlist-form')).toBeVisible();
  await expect(
    page.locator('.governance-summary').getByText('Exact explicit skill identities', { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Publish' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Enforce' })).toHaveCount(0);
}

/** Reference layout keeps detect-and-report findings closed under the policy workspace. */
export async function openGovernanceFindings(page: Page): Promise<void> {
  const findings = page.locator('#governance-findings');
  await expect(findings).toBeVisible();
  if ((await findings.getAttribute('open')) === null) {
    await findings.locator('summary').click();
  }
  await expect(page.getByRole('heading', { name: 'Risky access' })).toBeVisible();
}

/**
 * Client-side MCP editor behaviours for #191 / G03+G05: dirty count/diff, filter
 * without dropping hidden selections, reset to the active baseline, and
 * keep/discard navigation. Shared to avoid Sonar CPD across live specs.
 */
export async function expectGovernanceMCPEditorInteractions(
  page: Page,
  serverName: string,
): Promise<void> {
  const row = page.locator('.mcp-rule-row', { hasText: serverName });
  const checkbox = row.locator('input[name="mcp_server"]');
  const filter = page.locator('#mcp-rule-filter');
  const summary = page.locator('#mcp-dirty-summary');
  const save = page.getByRole('button', { name: 'Save local changes' }).first();

  await checkbox.check();
  await expect(summary).toBeVisible();
  await expect(summary).toContainText('1 unsaved local MCP allowlist change(s)');
  await expect(summary).toContainText(serverName);
  await expect(save).toBeEnabled();

  await filter.selectOption('not-allowlisted');
  await expect(row).toBeHidden();
  // Row is aria-hidden while filtered out; assert via the row locator so the
  // selection is proven retained (getByRole skips hidden controls).
  await expect(checkbox).toBeChecked();
  await filter.selectOption('allowlisted');
  await expect(row).toBeVisible();
  await filter.selectOption('all');

  await page.getByRole('button', { name: 'Reset changes' }).click();
  await expect(summary).toBeHidden();
  await expect(checkbox).not.toBeChecked();
  await expect(save).toBeDisabled();

  await checkbox.check();
  page.once('dialog', (dialog) => dialog.dismiss());
  await page
    .getByLabel('Primary navigation')
    .getByRole('link', { name: 'Overview', exact: true })
    .click();
  await expect(page).toHaveURL(/\/governance/);
  await expect(checkbox).toBeChecked();

  page.once('dialog', (dialog) => dialog.accept());
  await page
    .getByLabel('Primary navigation')
    .getByRole('link', { name: 'Overview', exact: true })
    .click();
  await expect(page.getByRole('heading', { name: 'Orchestration overview' })).toBeVisible();
}

/** Assert the editor names its own policy in the unsaved-navigation prompt. */
export async function expectAllowlistDiscardConfirmation(
  page: Page,
  checkboxName: string,
  policyName: string,
): Promise<void> {
  await page.getByRole('checkbox', { name: checkboxName }).check();
  page.once('dialog', async (dialog) => {
    expect(dialog.message()).toBe(
      `Discard unsaved local ${policyName} allowlist changes?`,
    );
    await dialog.accept();
  });
  await page
    .getByLabel('Primary navigation')
    .getByRole('link', { name: 'Overview', exact: true })
    .click();
  await expect(page.getByRole('heading', { name: 'Orchestration overview' })).toBeVisible();
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

// Rich Codex log evidence for #233. Every value is synthetic; the live gate
// proves local raw retention while diagnostics remain metadata-only.
export function codexRawRetentionOTLPLogs(): string {
  return codexExecOTLPLogs(
    [
      {
        body: { stringValue: 'tiq-live-codex-body' },
        attributes: codexStringAttrs([
          ['event.name', 'codex.sandbox_outcome'],
          ['conversation.id', 'tiq-live-e2e-codex-raw-retention'],
          ['call_id', 'tiq-live-codex-call'],
          ['path', '.env'],
          ['command', 'cat .env'],
          ['cwd', '/workspace/tiq-live-codex-workspace'],
          ['input', 'tiq-live-codex-input'],
          ['output', 'tiq-live-codex-output'],
          ['prompt', 'tiq-live-codex-prompt'],
          ['response', 'tiq-live-codex-response'],
          ['source_code', 'tiq-live-codex-source'],
          ['user.email', 'tiq-live-codex@example.test'],
          ['authorization', 'Bearer tiq-live-codex-token'],
        ]),
      },
    ],
    '0.157.1',
    codexStringAttrs([
      ['host.name', 'tiq-live-codex-host'],
      ['user.account_id', 'tiq-live-codex-account'],
    ]),
  );
}

// Paired Codex trace/log evidence for #218. The trace thread.id exactly joins
// the log conversation.id, while turn.id identifies the one model response
// whose span interval may be shown as Model generation.
export function codexThreadTurnOTLPLogs(): string {
  return codexExecOTLPLogs(
    [
      {
        attributes: codexStringAttrs([
          ['event.name', 'codex.sse_event'],
          ['conversation.id', 'tiq-live-e2e-thread-218'],
          ['turn.id', 'tiq-live-e2e-turn-218'],
          ['model', 'gpt-5-codex-live'],
        ]),
      },
    ],
    '0.155.1',
  );
}

export function codexThreadTurnOTLPTraces(): string {
  return JSON.stringify({
    resourceSpans: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'codex_exec' } },
            { key: 'service.version', value: { stringValue: '0.155.1' } },
          ],
        },
        scopeSpans: [
          {
            scope: { name: 'codex_exec' },
            spans: [
              {
                traceId: 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee',
                spanId: '4444444444444444',
                parentSpanId: '',
                name: 'session_task.turn',
                startTimeUnixNano: '1790416800000000000',
                endTimeUnixNano: '1790416802000000000',
                attributes: [
                  { key: 'thread.id', value: { stringValue: 'tiq-live-e2e-thread-218' } },
                  { key: 'turn.id', value: { stringValue: 'tiq-live-e2e-turn-218' } },
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
  resourceAttributes: OTLPAttribute[] = [],
): string {
  return JSON.stringify({
    resourceLogs: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: serviceName } },
            { key: 'service.version', value: { stringValue: serviceVersion } },
            ...resourceAttributes,
          ],
        },
        scopeLogs: [{ logRecords }],
      },
    ],
  });
}

function codexExecOTLPLogs(
  logRecords: OTLPLogRecord[],
  version = '0.153.4',
  resourceAttributes: OTLPAttribute[] = [],
): string {
  return otlpLogs('codex_exec', version, logRecords, resourceAttributes);
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
// extraAttributes carries the surface under test (file_path / full_command);
// events carries OTLP span events such as tool.output (#253).
function claudeToolSpanOTLPTraces(opts: {
  traceId: string;
  spanId: string;
  sessionId: string;
  toolName: string;
  toolUseId: string;
  extraAttributes: Array<{ key: string; value: { stringValue: string } }>;
  events?: Array<{
    name: string;
    timeUnixNano: string;
    attributes: Array<{ key: string; value: { stringValue: string } }>;
  }>;
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
                events: opts.events ?? [],
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
export function claudeToolSpanFilePathOTLPTraces(
  sessionId = 'tiq-live-e2e-session-files',
): string {
  return claudeToolSpanOTLPTraces({
    traceId: 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee',
    spanId: 'aaaaaaaaaaaaaaaa',
    sessionId,
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

// claudeInteractionOTLPTraces builds a single claude_code.interaction root span.
// When sessionId is provided the span carries session.id (so it joins the
// matching content logs into one provider session); when it is omitted the span
// has no session.id and the normaliser keeps a trace-scoped observation
// (claude-code:trace:<traceId>). Used by the #210 correlation e2e for both the
// joined and trace-only-unavailable states.
export function claudeInteractionOTLPTraces(opts: {
  traceId: string;
  spanId: string;
  sessionId?: string;
}): string {
  const attributes: OTLPAttribute[] = [
    { key: 'span.type', value: { stringValue: 'interaction' } },
    { key: 'interaction.sequence', value: { intValue: '1' } },
    { key: 'interaction.duration_ms', value: { intValue: '1375' } },
  ];
  if (opts.sessionId) {
    attributes.unshift({
      key: 'session.id',
      value: { stringValue: opts.sessionId },
    });
  }
  return JSON.stringify({
    resourceSpans: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'claude-code' } },
            { key: 'service.version', value: { stringValue: '2.1.283' } },
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
                name: 'claude_code.interaction',
                kind: 1,
                startTimeUnixNano: '1790501729743000000',
                endTimeUnixNano: '1790501731117935312',
                attributes,
                status: { code: 0 },
              },
            ],
          },
        ],
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

// Claude Bash tool span whose PR URL appears only in the tool.output span event
// output (#253, OTEL_LOG_TOOL_CONTENT) — the full_command is a printf template,
// as in the live 2.1.287 capture
// (fixtures/claude/observed-sanitised/claude-code-2.1.287-tool-content-spans-otlp.json).
export const claudePRLinkToolOutputSessionID =
  'tiq-live-e2e-session-pr-link-tool-output';
export function claudePRLinkToolOutputOTLPTraces(): string {
  const command = String.raw`printf 'https://github.com/%s/pull/%s\n' acme-synthetic/telemetryiq 183`;
  return claudeToolSpanOTLPTraces({
    traceId: '00000000000000000000000000000253',
    spanId: '0000000000000b53',
    sessionId: claudePRLinkToolOutputSessionID,
    toolName: 'Bash',
    toolUseId: 'toolu_live_session_pr_link_tool_output',
    extraAttributes: [{ key: 'full_command', value: { stringValue: command } }],
    events: [
      {
        name: 'tool.output',
        timeUnixNano: '1789117600690000000',
        attributes: [
          { key: 'bash_command', value: { stringValue: command } },
          { key: 'output', value: { stringValue: claudeLivePRLinkURL } },
        ],
      },
    ],
  });
}

// Claude tool_decision whose retained tool_parameters full_command carries the
// PR URL (#251) — the OTEL_LOG_TOOL_DETAILS shape observed live on 2.1.286
// (fixtures/claude/observed-sanitised/claude-code-2.1.286-tool-params-pr-link-otlp.json).
export const claudePRLinkLogsSessionID = 'tiq-live-e2e-session-pr-link-logs';
export function claudePRLinkOTLPLogs(): string {
  return claudeOTLPLogs([
    {
      attributes: [
        { key: 'event.name', value: { stringValue: 'tool_decision' } },
        {
          key: 'event.timestamp',
          value: { stringValue: '2026-09-30T20:01:34.516Z' },
        },
        { key: 'event.sequence', value: { intValue: '17' } },
        { key: 'session.id', value: { stringValue: claudePRLinkLogsSessionID } },
        { key: 'decision', value: { stringValue: 'accept' } },
        { key: 'source', value: { stringValue: 'config' } },
        { key: 'tool_name', value: { stringValue: 'Bash' } },
        { key: 'tool_use_id', value: { stringValue: 'toolu_live_pr_link_logs' } },
        {
          key: 'tool_parameters',
          value: {
            stringValue: JSON.stringify({
              bash_command: 'gh',
              full_command: `gh pr view ${claudeLivePRLinkURL}`,
            }),
          },
        },
      ],
    },
  ], '2.1.286');
}

// Claude session JSONL where the PR URL exists only in the Bash tool_result
// output: the tool_use input is a printf template, as captured live on 2.1.286
// (fixtures/claude/observed-sanitised/claude-code-2.1.286-tool-output-pr-link-transcript.json, #251).
// claudeLivePRLinkBaseURL is the URL minus its PR number, so the printf template
// never contains a complete pull-request URL.
const claudeLivePRLinkBaseURL = claudeLivePRLinkURL.slice(
  0,
  claudeLivePRLinkURL.lastIndexOf('/') + 1,
);
export const claudePRLinkTranscriptSessionID =
  'tiq-live-e2e-session-pr-link-transcript';
export function claudePRLinkTranscriptNDJSON(): string {
  const record = {
    sessionId: claudePRLinkTranscriptSessionID,
    version: '2.1.286',
  };
  return [
    JSON.stringify({
      ...record,
      type: 'assistant',
      uuid: 'tiq-live-pr-link-assistant-1',
      timestamp: '2026-09-30T20:02:23.000Z',
      message: {
        role: 'assistant',
        model: 'claude-haiku-4-5-20251001',
        content: [
          {
            type: 'tool_use',
            id: 'toolu_live_pr_link_output',
            name: 'Bash',
            input: {
              command: String.raw`printf '${claudeLivePRLinkBaseURL}%s\n' 183`,
            },
          },
        ],
      },
    }),
    JSON.stringify({
      ...record,
      type: 'user',
      uuid: 'tiq-live-pr-link-user-1',
      parentUuid: 'tiq-live-pr-link-assistant-1',
      timestamp: '2026-09-30T20:02:25.000Z',
      toolUseResult: { stdout: claudeLivePRLinkURL, stderr: '' },
      message: {
        role: 'user',
        content: [
          {
            type: 'tool_result',
            tool_use_id: 'toolu_live_pr_link_output',
            content: claudeLivePRLinkURL,
            is_error: false,
          },
        ],
      },
    }),
  ].join('\n');
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

/** Operations insight totals + by_category, shared across live gates (CPD rule). */
export type LiveOperationStats = {
  totals: { total_operations: number; duration_observed_count?: number };
  by_category: Array<{ category: string; count: number }>;
};

export async function fetchLiveOperations(): Promise<LiveOperationStats> {
  const response = await fetch(`${daemonBase}/api/v1/insights/operations`, {
    headers: { Authorization: `Bearer ${authToken}` },
  });
  expect(response.status).toBe(200);
  const body = (await response.json()) as { data: LiveOperationStats };
  return body.data;
}

/** Assert an operations by_category bucket holds exactly the expected count. */
export function expectOperationCategory(
  stats: LiveOperationStats,
  category: string,
  count: number,
): void {
  expect(
    stats.by_category.some(
      (row) => row.category === category && row.count === count,
    ),
  ).toBe(true);
}

/** Files-lane rows for one session, shared across live gates (CPD rule). */
export type LiveSessionFile = { path: string | null; action: string | null };

export async function fetchLiveSessionFiles(
  sessionId: string,
): Promise<LiveSessionFile[]> {
  const response = await fetch(
    `${daemonBase}/api/v1/sessions/${encodeURIComponent(sessionId)}/files`,
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(response.status).toBe(200);
  const body = (await response.json()) as { data: LiveSessionFile[] };
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

/** Event-detail payload (#189 T08) exposing retained attributes + extensions. */
export type LiveEventDetail = {
  event_id: string;
  event_type: string;
  attributes: Record<string, unknown>;
  // provider_extensions is heterogeneous: object blocks like `correlation`/`event`
  // sit alongside scalar entries such as the namespaced `request_id` string
  // (see normaliseSampleEvent), so values are `unknown` and narrowed at the callsite.
  provider_extensions: Record<string, unknown>;
};

// fetchEventDetail reads one retained session event from the live daemon's
// inspector API and asserts a 200 before returning it, so specs can assert on the
// served attributes / provider_extensions without repeating the fetch boilerplate.
export async function fetchEventDetail(
  sessionId: string,
  eventId: string,
): Promise<LiveEventDetail> {
  const response = await fetch(
    `${daemonBase}/api/v1/sessions/${encodeURIComponent(sessionId)}/events/${encodeURIComponent(eventId)}`,
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(response.status).toBe(200);
  const body = (await response.json()) as { data: LiveEventDetail };
  return body.data;
}

export async function fetchLiveSessionEvents(
  sessionId: string,
): Promise<Array<{ event_id: string; event_type: string }>> {
  const response = await fetch(
    `${daemonBase}/api/v1/sessions/${encodeURIComponent(sessionId)}/events?limit=100`,
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(response.status).toBe(200);
  const body = (await response.json()) as {
    data: Array<{ event_id: string; event_type: string }>;
  };
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
    await Promise.all(
      (opts.categoryLabels ?? []).map((label) =>
        expect(rail.getByText(label, { exact: true })).toBeVisible(),
      ),
    );
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

// ingestCodexRollout exercises the authenticated local-only rollout receiver.
// Unlike OTLP intake, this body contains raw conversation and tool evidence and
// therefore requires the same token as the management APIs.
export async function ingestCodexRollout(body: string): Promise<void> {
  const ingest = await fetch(`${daemonBase}/v1/codex/rollout`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${authToken}`,
      'Content-Type': 'application/x-ndjson',
    },
    body,
  });
  expect(ingest.status).toBe(202);
}

export const codexRolloutSessionID = 'tiq-live-e2e-codex-rollout';

// Synchronized OTLP evidence carrying the exact provider-native identifier used
// by codexRolloutNDJSON; the daemon must merge these into one codex:* session.
export function codexRolloutOTLPLogs(): string {
  return codexExecOTLPLogs([
    {
      attributes: codexStringAttrs([
        ['event.name', 'codex.synthetic_rollout_probe'],
        ['conversation.id', codexRolloutSessionID],
      ]),
      body: { stringValue: 'synthetic rollout probe' },
    },
  ]);
}

// Minimal observed Codex 0.159.2 rollout shapes for the live daemon gate. The
// parser retains every record raw and projects conversation, MCP and file
// operations through the same authenticated ingest path.
export function codexRolloutNDJSON(): string {
  return [
    {
      ordinal: 0,
      timestamp: '2026-09-29T07:01:40.785Z',
      type: 'session_meta',
      payload: {
        id: codexRolloutSessionID,
        cli_version: '0.159.2',
        creator_user_id: 'tiq-live-synthetic-user',
        unknown_session_field: { retained: true },
      },
    },
    {
      ordinal: 1,
      timestamp: '2026-09-29T07:01:43.115Z',
      type: 'response_item',
      payload: {
        type: 'message',
        id: 'tiq-live-rollout-user',
        role: 'user',
        content: [{ type: 'input_text', text: 'tiq-live retained Codex prompt' }],
      },
    },
    {
      ordinal: 2,
      timestamp: '2026-09-29T07:01:46.209Z',
      type: 'response_item',
      payload: {
        type: 'message',
        id: 'tiq-live-rollout-assistant',
        role: 'assistant',
        content: [{ type: 'output_text', text: 'tiq-live retained Codex response' }],
      },
    },
    {
      ordinal: 3,
      timestamp: '2026-09-29T07:01:47.000Z',
      type: 'event_msg',
      payload: {
        type: 'item_completed',
        thread_id: codexRolloutSessionID,
        turn_id: 'tiq-live-rollout-turn',
        item: {
          type: 'McpToolCall',
          id: 'tiq-live-mcp-call',
          server: 'tiq_live_server',
          tool: 'echo_probe',
          arguments: { value: 'tiq-live-mcp-input' },
          status: 'completed',
          result: { content: [{ type: 'text', text: 'tiq-live-mcp-output' }] },
          duration: { secs: 0, nanos: 3000000 },
        },
      },
    },
    {
      ordinal: 4,
      timestamp: '2026-09-29T07:01:48.000Z',
      type: 'event_msg',
      payload: {
        type: 'item_completed',
        thread_id: codexRolloutSessionID,
        turn_id: 'tiq-live-rollout-turn',
        item: {
          type: 'FileChange',
          id: 'tiq-live-file-change',
          changes: {
            '/workspace/tiq-live-rollout/probe.txt': {
              type: 'update',
              unified_diff: '@@ -1 +1 @@\n-before\n+after\n',
              move_path: null,
            },
          },
          status: 'completed',
          stdout: 'Success. Updated synthetic file.',
          stderr: '',
        },
      },
    },
    {
      ordinal: 5,
      timestamp: '2026-09-29T07:02:32.300Z',
      type: 'future_provider_record',
      payload: { unknown_flag: true, nested: { retained: 'verbatim' } },
    },
  ]
    .map((record) => JSON.stringify(record))
    .join('\n');
}

/**
 * Synthetic Claude Code session JSONL for the live transcript gate. Under epic #87
 * the prompt/response/thinking text and the tool command + result are captured raw
 * and surface through the operations read API; only cwd — not a #105 signal — must
 * never appear on any surface. The Bash tool_use carries an id and a paired
 * tool_result so it reconstructs a shell-command operation.
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
          { type: 'thinking', thinking: 'tiq-canary-live-thinking' },
          {
            type: 'tool_use',
            id: 'toolu_live_transcript_bash',
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
    }),
    JSON.stringify({
      type: 'user',
      uuid: 'tiq-live-user-2',
      parentUuid: 'tiq-live-assistant-1',
      sessionId: session,
      timestamp: '2026-09-12T12:00:03.000Z',
      message: {
        role: 'user',
        content: [
          {
            type: 'tool_result',
            tool_use_id: 'toolu_live_transcript_bash',
            is_error: false,
            content: [{ type: 'text', text: 'tiq-canary-live-stdout' }],
          },
        ],
      },
    }),
  ].join('\n');
}

/**
 * Synthetic Claude Code session JSONL exercising the generic tool-call surface
 * (#105): a Read, a Write, and a Task tool_use on a main-line assistant record plus
 * a sub-agent (sidechain) Grep. Drives the transcript path to reconstruct one
 * Operation per tool_use (filesystem read/write, unknown for Task, filesystem read
 * for the sidechain Grep) and to serve the Read/Write paths through the Files-lane.
 */
export function claudeGenericToolTranscriptNDJSON(): string {
  const session = 'tiq-live-e2e-generic-tool-session';
  return [
    JSON.stringify({
      type: 'user',
      uuid: 'tiq-live-generic-user-1',
      sessionId: session,
      timestamp: '2026-09-12T14:00:00.000Z',
      version: '2.1.269',
      message: { role: 'user', content: 'tiq-canary-live-generic-prompt' },
    }),
    JSON.stringify({
      type: 'assistant',
      uuid: 'tiq-live-generic-assistant-1',
      parentUuid: 'tiq-live-generic-user-1',
      sessionId: session,
      timestamp: '2026-09-12T14:00:02.000Z',
      version: '2.1.269',
      cwd: '/repo',
      gitBranch: 'main',
      entrypoint: 'cli',
      requestId: 'req_live_generic_1',
      message: {
        role: 'assistant',
        model: 'claude-opus-4-8',
        stop_reason: 'tool_use',
        content: [
          {
            type: 'tool_use',
            id: 'toolu_live_generic_read',
            name: 'Read',
            input: { file_path: '/repo/tiq-live-generic-read.go' },
          },
          {
            type: 'tool_use',
            id: 'toolu_live_generic_write',
            name: 'Write',
            input: {
              file_path: '/repo/tiq-live-generic-write.go',
              content: 'package main',
            },
          },
          {
            type: 'tool_use',
            id: 'toolu_live_generic_task',
            name: 'Task',
            input: { description: 'investigate', subagent_type: 'Explore' },
          },
        ],
        usage: { input_tokens: 48, output_tokens: 12 },
      },
    }),
    JSON.stringify({
      type: 'assistant',
      uuid: 'tiq-live-generic-sidechain-1',
      parentUuid: 'tiq-live-generic-assistant-1',
      isSidechain: true,
      sessionId: session,
      timestamp: '2026-09-12T14:00:03.000Z',
      version: '2.1.269',
      message: {
        role: 'assistant',
        model: 'claude-opus-4-8',
        stop_reason: 'tool_use',
        content: [
          {
            type: 'tool_use',
            id: 'toolu_live_generic_grep',
            name: 'Grep',
            input: { pattern: 'func main', path: '/repo' },
          },
        ],
        usage: { input_tokens: 16, output_tokens: 4 },
      },
    }),
  ].join('\n');
}

/**
 * Synthetic Claude Code session JSONL bearing one MCP tool call (J17, #104): an
 * assistant `tool_use` named mcp__<server>__read_file alongside a non-MCP Bash
 * tool_use, paired with a later user `tool_result`. Drives the transcript path to
 * reconstruct an MCP-call operation (marking the server used) and, under #105, a
 * generic shell-command operation for the Bash call whose command is captured raw;
 * only cwd — not a #105 signal — must never reach any surface.
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

function claudeOTLPLogs(
  logRecords: Array<{ attributes: OTLPAttribute[] }>,
  serviceVersion = '2.1.263',
): string {
  return JSON.stringify({
    resourceLogs: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'claude-code' } },
            { key: 'service.version', value: { stringValue: serviceVersion } },
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
export function claudeContentOTLPLogs(
  sessionId = 'tiq-live-e2e-conversation-content',
  timestamps = [
    '2026-09-19T12:00:00Z',
    '2026-09-19T12:00:01Z',
    '2026-09-19T12:00:02Z',
  ],
): string {
  const contentEvents = [
    ['user_prompt', timestamps[0], '1', 'prompt', 'tiq-live-e2e retained user\nsecond line'],
    ['assistant_response', timestamps[1], '2', 'response', '<REDACTED>'],
    ['api_response_body', timestamps[2], '3', 'body', 'tiq-live-e2e raw API evidence'],
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
  promptId?: string,
): { attributes: OTLPAttribute[] } {
  const attributes: OTLPAttribute[] = [
    { key: 'event.name', value: { stringValue: eventName } },
    { key: 'event.timestamp', value: { stringValue: timestamp } },
    { key: 'event.sequence', value: { intValue: sequence } },
    { key: 'session.id', value: { stringValue: sessionId } },
    { key: contentKey, value: { stringValue: content } },
  ];
  if (promptId) {
    attributes.push({ key: 'prompt.id', value: { stringValue: promptId } });
  }
  return { attributes };
}

// claudeLiveCorrelationPromptID is the single prompt.id shared by the two events
// claudeCorrelationOTLPLogs builds, so the live ingest→read gate can assert #106
// retains it under provider_extensions.correlation on every event. Synthetic.
export const claudeLiveCorrelationPromptID = 'tiq-live-e2e-shared-prompt-id';

// claudeCorrelationOTLPLogs builds two user_prompt events in one session that
// share a single prompt.id, so the non-mocked live path can prove the correlation
// id is retained (and identical across the two events) end to end (#106 X19).
export function claudeCorrelationOTLPLogs(): string {
  const sessionId = 'tiq-live-e2e-correlation';
  return claudeOTLPLogs([
    claudeContentEvent('user_prompt', '2026-09-19T12:00:00Z', '1', sessionId, 'prompt', 'tiq-live-e2e first prompt', claudeLiveCorrelationPromptID),
    claudeContentEvent('user_prompt', '2026-09-19T12:00:03Z', '2', sessionId, 'prompt', 'tiq-live-e2e second prompt', claudeLiveCorrelationPromptID),
  ]);
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
