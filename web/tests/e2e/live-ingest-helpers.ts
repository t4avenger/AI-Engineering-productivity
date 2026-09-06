import { expect, type Page, test } from '@playwright/test';

/**
 * Shared plumbing for the live-ingest e2e gates: both the sessions and the
 * skill-usage specs drive the real daemon from playwright.config.ts, so the
 * daemon address, auth token, Codex OTLP payload shape and daemon-reset hooks
 * live here once instead of being copied per spec.
 */
const daemonBase = 'http://127.0.0.1:18080';
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

// claudeSkillOTLPLogs is a sanitised Claude Code skill_activated OTLP log payload.
export function claudeSkillOTLPLogs(): string {
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
                    value: { stringValue: 'skill_activated' },
                  },
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
                  {
                    key: 'invocation_trigger',
                    value: { stringValue: 'user-slash' },
                  },
                  {
                    key: 'skill.source',
                    value: { stringValue: 'projectSettings' },
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
