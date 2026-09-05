import { expect, test } from '@playwright/test';

const codexSession = {
  schema_version: '0.1.0',
  session_id: 'codex-session',
  provider: 'openai',
  tool: 'codex',
  state: 'completed',
  started_at: '2026-07-24T12:00:00Z',
  completed_at: '2026-07-24T12:05:00Z',
  attributes: { event_count: 1, model: 'gpt-5-codex' },
  provider_extensions: {},
  availability: {
    provider: 'observed',
    tool: 'observed',
    outcome: 'observed',
    started_at: 'observed',
    completed_at: 'observed',
    model: 'observed',
    observed_events: 'observed',
    token_usage: 'partial',
  },
};

const claudeSession = {
  schema_version: '0.1.0',
  session_id: 'claude-session',
  provider: 'anthropic',
  tool: 'claude-code',
  state: 'completed',
  started_at: '2026-07-24T13:00:00Z',
  completed_at: null,
  attributes: { event_count: 1 },
  provider_extensions: {},
  availability: {
    provider: 'observed',
    tool: 'observed',
    outcome: 'observed',
    started_at: 'observed',
    completed_at: 'unavailable',
    model: 'unavailable',
    observed_events: 'observed',
    token_usage: 'unavailable',
  },
};

test('renders cross-tool session availability states', async ({ page }) => {
  await page.route('**/api/v1/health', async (route) => {
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        status: 'healthy',
        service: 'telemetryiq-daemon',
        timestamp: '2026-07-24T12:00:00Z',
      }),
    });
  });
  await page.route('**/api/v1/sessions?limit=100', async (route) => {
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        data: [codexSession, claudeSession],
        pagination: { limit: 100, next_cursor: null },
      }),
    });
  });
  await page.route('**/api/v1/sessions/claude-session', async (route) => {
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: claudeSession }),
    });
  });
  await page.route(
    '**/api/v1/sessions/claude-session/events?limit=100',
    async (route) => {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: [],
          pagination: { limit: 100, next_cursor: null },
        }),
      });
    },
  );

  await page.goto('/');
  await page.getByLabel('Local API token').fill('playwright-token');
  await page.getByRole('button', { name: 'Connect securely' }).click();
  await page.getByRole('button', { name: 'Sessions' }).click();

  await expect(page.getByText('gpt-5-codex')).toBeVisible();
  await expect(page.getByText('Model unavailable')).toBeVisible();
  await page.getByRole('button', { name: /claude-code/i }).click();

  await expect(
    page.getByRole('heading', { name: 'claude-code session' }),
  ).toBeVisible();
  await expect(page.getByText('Model unavailable')).toBeVisible();
  await expect(page.getByText('Token usage unavailable')).toBeVisible();
});
