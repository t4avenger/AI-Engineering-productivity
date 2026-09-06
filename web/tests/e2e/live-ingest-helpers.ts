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
      attributes: [
        { key: 'event.name', value: { stringValue: 'api_request' } },
        {
          key: 'event.timestamp',
          value: { stringValue: '2026-09-06T18:05:00.100Z' },
        },
        { key: 'event.sequence', value: { intValue: '3' } },
        {
          key: 'session.id',
          value: { stringValue: 'tiq-live-e2e-outcome-session' },
        },
        {
          key: 'model',
          value: { stringValue: 'claude-haiku-4-5-20251001' },
        },
        { key: 'input_tokens', value: { intValue: '12' } },
        { key: 'output_tokens', value: { intValue: '4' } },
        { key: 'duration_ms', value: { intValue: '842' } },
      ],
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

/** Claude api_request logs carrying cache-read tokens for the context-waste insight. */
export function claudeContextWasteOTLPLogs(): string {
  const session = 'tiq-live-e2e-context-waste-session';
  return claudeOTLPLogs([
    {
      attributes: [
        { key: 'event.name', value: { stringValue: 'api_request' } },
        {
          key: 'event.timestamp',
          value: { stringValue: '2026-09-06T19:00:00.000Z' },
        },
        { key: 'event.sequence', value: { intValue: '1' } },
        { key: 'session.id', value: { stringValue: session } },
        { key: 'request_id', value: { stringValue: 'synthetic-request-1' } },
        { key: 'model', value: { stringValue: 'claude-opus-4-8' } },
        { key: 'input_tokens', value: { intValue: '100' } },
        { key: 'cache_read_tokens', value: { intValue: '75' } },
      ],
    },
    {
      attributes: [
        { key: 'event.name', value: { stringValue: 'api_request' } },
        {
          key: 'event.timestamp',
          value: { stringValue: '2026-09-06T19:00:01.000Z' },
        },
        { key: 'event.sequence', value: { intValue: '2' } },
        { key: 'session.id', value: { stringValue: session } },
        { key: 'request_id', value: { stringValue: 'synthetic-request-2' } },
        { key: 'model', value: { stringValue: 'claude-opus-4-8' } },
        { key: 'input_tokens', value: { intValue: '200' } },
        { key: 'cache_read_tokens', value: { intValue: '150' } },
      ],
    },
  ]);
}
