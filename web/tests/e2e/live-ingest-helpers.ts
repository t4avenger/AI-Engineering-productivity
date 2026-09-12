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


export function codexToolDecisionOTLPLogs(): string {
  return JSON.stringify({
    resourceLogs: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'codex_exec' } },
            { key: 'service.version', value: { stringValue: '0.153.4' } },
          ],
        },
        scopeLogs: [
          {
            logRecords: [
              {
                attributes: [
                  {
                    key: 'event.name',
                    value: { stringValue: 'codex.tool_decision' },
                  },
                  {
                    key: 'conversation.id',
                    value: { stringValue: 'tiq-live-e2e-decision-session' },
                  },
                  {
                    key: 'call_id',
                    value: { stringValue: 'tiq-live-e2e-decision-call' },
                  },
                  { key: 'decision', value: { stringValue: 'allow' } },
                  { key: 'source', value: { stringValue: 'policy' } },
                  { key: 'tool_name', value: { stringValue: 'exec_command' } },
                  { key: 'tool_namespace', value: { stringValue: 'functions' } },
                  {
                    key: 'arguments',
                    value: { stringValue: '--token=tiq-canary-live-decision' },
                  },
                  {
                    key: 'user.email',
                    value: { stringValue: 'decision-live@example.test' },
                  },
                ],
                body: { stringValue: 'tiq-canary-live-decision-body' },
              },
            ],
          },
        ],
      },
    ],
  });
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
  return JSON.stringify({
    resourceLogs: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: 'codex_exec' } },
            { key: 'service.version', value: { stringValue: '0.153.4' } },
          ],
        },
        scopeLogs: [
          {
            logRecords: [
              {
                attributes: [
                  {
                    key: 'event.name',
                    value: { stringValue: 'codex.tool_result' },
                  },
                  { key: 'tool_name', value: { stringValue: 'exec_command' } },
                  { key: 'success', value: { stringValue: 'true' } },
                  { key: 'model', value: { stringValue: 'gpt-6-astra' } },
                  { key: 'duration_ms', value: { stringValue: '92' } },
                  { key: 'call_id', value: { stringValue: 'synthetic-call' } },
                ],
                severityText: 'INFO',
              },
              {
                attributes: [
                  {
                    key: 'event.name',
                    value: { stringValue: 'codex.api_request' },
                  },
                  { key: 'success', value: { boolValue: false } },
                  { key: 'model', value: { stringValue: 'gpt-6-astra' } },
                  { key: 'attempt', value: { stringValue: '2' } },
                  { key: 'duration_ms', value: { stringValue: '268' } },
                  {
                    key: 'http.status_code',
                    value: { stringValue: '401' },
                  },
                  { key: 'error_code', value: { stringValue: 'http_401' } },
                ],
                severityText: 'INFO',
              },
            ],
          },
        ],
      },
    ],
  });
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
