import { MantineProvider } from '@mantine/core';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import { App } from '../src/App';
import type { Session } from '../src/sessions';

const session: Session = {
  schema_version: '0.1.0',
  session_id: 'session-1',
  provider: 'openai',
  tool: 'codex',
  state: 'completed',
  started_at: '2026-07-24T12:00:00Z',
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
    token_usage: 'partial',
  },
};

function mockAPI(
  sessions = [session],
  options: {
    detailFails?: boolean;
    deleteFails?: boolean;
    healthFails?: boolean;
    costFails?: boolean;
    insightFails?: boolean;
    insightEmpty?: boolean;
    skillFails?: boolean;
  } = {},
) {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = input instanceof Request ? input.url : input.toString();
      if (init?.method === 'DELETE' && options.deleteFails)
        return Promise.resolve(new Response(null, { status: 500 }));
      if (init?.method === 'DELETE')
        return Promise.resolve(new Response(null, { status: 204 }));
      if (url.endsWith('/health') && options.healthFails)
        return Promise.reject(new Error('Daemon offline'));
      if (url.endsWith('/health'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              status: 'healthy',
              service: 'telemetryiq-daemon',
              timestamp: '2026-07-24T12:00:00Z',
            }),
            { status: 200 },
          ),
        );
      if (url.endsWith('/costs/summary') && options.costFails)
        return Promise.reject(new Error('Cost service offline'));
      if (url.endsWith('/costs/summary'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: {
                currency: 'USD',
                calculated_amount_microusd: 1250000,
                statuses: { calculated: 1, unknown_price: 2 },
              },
            }),
            { status: 200 },
          ),
        );
      if (url.endsWith('/insights/mcp-inventory') && options.insightFails)
        return Promise.reject(new Error('Insight service offline'));
      if (url.endsWith('/insights/mcp-inventory') && options.insightEmpty)
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: {
                schema_version: '0.1.0',
                totals: {
                  connected_servers: 0,
                  used_servers: 1,
                  unused_servers: 0,
                  usage_unavailable_servers: 0,
                  request_input_tokens: null,
                  request_output_tokens: null,
                  request_cached_input_tokens: null,
                  request_cache_created_tokens: null,
                  token_context_label:
                    'request-level context only; not exact per-MCP allocation',
                },
                servers: [],
                notes: [],
              },
            }),
            { status: 200 },
          ),
        );
      if (url.endsWith('/insights/mcp-inventory'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: {
                schema_version: '0.1.0',
                totals: {
                  connected_servers: 2,
                  used_servers: 1,
                  unused_servers: 1,
                  usage_unavailable_servers: 0,
                  request_input_tokens: 10,
                  request_output_tokens: 5,
                  request_cached_input_tokens: null,
                  request_cache_created_tokens: null,
                  token_context_label:
                    'request-level context only; not exact per-MCP allocation',
                },
                servers: [
                  {
                    server_fingerprint: 'mcp:hmac:filesystem',
                    server_name: 'filesystem',
                    identity_state: 'fingerprinted',
                    provider: 'anthropic',
                    tool: 'claude-code',
                    session_id: 'session-1',
                    connection_status: 'connected',
                    connection_scope: 'user',
                    transport_type: 'stdio',
                    is_plugin: false,
                    invocation_count: 2,
                    tool_names: ['read_file', 'list_directory'],
                    used: true,
                    usage_state: 'observed',
                    context_waste_state: 'used',
                    request_input_tokens: 10,
                    request_output_tokens: 5,
                    request_cached_input_tokens: null,
                    request_cache_created_tokens: null,
                    token_context_label:
                      'request-level context only; not exact per-MCP allocation',
                  },
                  {
                    server_fingerprint: 'mcp:hmac:git',
                    server_name: 'git',
                    identity_state: 'fingerprinted',
                    provider: 'anthropic',
                    tool: 'claude-code',
                    session_id: 'session-1',
                    connection_status: 'connected',
                    connection_scope: 'project',
                    transport_type: 'stdio',
                    is_plugin: true,
                    invocation_count: 0,
                    tool_names: [],
                    used: false,
                    usage_state: 'not_observed',
                    context_waste_state: 'connected_but_unused',
                    request_input_tokens: 10,
                    request_output_tokens: 5,
                    request_cached_input_tokens: null,
                    request_cache_created_tokens: null,
                    token_context_label:
                      'request-level context only; not exact per-MCP allocation',
                  },
                ],
                notes: [
                  'Request token context is session/request-level only and is not an exact per-MCP allocation.',
                ],
              },
            }),
            { status: 200 },
          ),
        );
      if (url.endsWith('/insights/skill-usage') && options.skillFails)
        return Promise.reject(new Error('Skill service offline'));
      if (url.endsWith('/insights/skill-usage'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: {
                schema_version: '0.1.0',
                totals: {
                  observed_skills: options.insightEmpty ? 0 : 2,
                  invocations: options.insightEmpty ? 0 : 3,
                  explicit_detection: options.insightEmpty ? 0 : 1,
                  inferred_detection: 0,
                  unavailable_detection: 1,
                  unknown_detection: 0,
                },
                skills: options.insightEmpty
                  ? []
                  : [
                      {
                        skill_name: 'pdf',
                        provider: 'anthropic',
                        tool: 'claude-code',
                        detection_state: 'explicit',
                        invocation_count: 2,
                        outcomes: { success: 1, failed: 1 },
                        outcome_state: 'observed',
                      },
                      {
                        skill_name: 'diagram',
                        provider: 'anthropic',
                        tool: 'claude-code',
                        detection_state: 'explicit',
                        invocation_count: 1,
                        outcomes: {},
                        outcome_state: 'unavailable',
                      },
                    ],
                coverage: [
                  {
                    provider: 'openai',
                    tool: 'codex-cli',
                    detection_state: 'unavailable',
                  },
                ],
                notes: [
                  'Skill identity is reported only where the provider explicitly stamps it; inferred and unavailable states are shown, never guessed.',
                ],
              },
            }),
            { status: 200 },
          ),
        );
      if (url.includes('/sessions?'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: sessions,
              pagination: { limit: 100, next_cursor: null },
            }),
            { status: 200 },
          ),
        );
      const sessionEventMatch = url.match(/\/sessions\/([^/?]+)\/events\?/);
      if (sessionEventMatch)
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: [],
              pagination: { limit: 100, next_cursor: null },
            }),
            { status: 200 },
          ),
        );
      const sessionDetail = sessions.find((candidate) =>
        url.endsWith('/sessions/' + candidate.session_id),
      );
      if (sessionDetail && options.detailFails)
        return Promise.resolve(new Response(null, { status: 500 }));
      if (sessionDetail)
        return Promise.resolve(
          new Response(JSON.stringify({ data: sessionDetail }), {
            status: 200,
          }),
        );
      return Promise.reject(new Error(`Unexpected request: ${url}`));
    }),
  );
}

// renderInsights mounts the app with a mocked API and opens the Insights page,
// the shared setup for every insight test.
function renderInsights(options: Parameters<typeof mockAPI>[1] = {}) {
  mockAPI([session], options);
  render(
    <MantineProvider env="test">
      <App />
    </MantineProvider>,
  );
  fireEvent.click(screen.getByRole('button', { name: 'Insights' }));
}

// expectInsightContent asserts an insight section renders its heading, its
// exact single-occurrence labels, and its labels that appear at least once.
async function expectInsightContent(
  heading: string,
  exactTexts: string[],
  someTexts: string[],
) {
  expect(
    await screen.findByRole('heading', { name: heading }),
  ).toBeInTheDocument();
  for (const text of exactTexts) {
    expect(screen.getByText(text)).toBeInTheDocument();
  }
  for (const text of someTexts) {
    expect(screen.getAllByText(text).length).toBeGreaterThan(0);
  }
}

describe('App dashboard', () => {
  beforeEach(() => {
    sessionStorage.setItem('telemetryiq-auth-token', 'test-token');
  });
  afterEach(() => {
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  test('shows an empty session journey without inventing integration data', async () => {
    mockAPI([]);
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    expect(
      await screen.findByRole('heading', { name: 'No sessions yet' }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Integrations' }));
    expect(
      await screen.findByRole('heading', { name: 'Awaiting telemetry' }),
    ).toBeInTheDocument();
  });

  test('shows unavailable fields and requires confirmation before deleting', async () => {
    mockAPI();
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    fireEvent.click(await screen.findByRole('button', { name: /codex/i }));
    expect(await screen.findByText('Model')).toBeInTheDocument();
    expect(screen.getByText('Model unavailable')).toBeInTheDocument();
    expect(screen.getByText('Completed unavailable')).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', { name: 'Delete this session' }),
    );
    expect(screen.getByRole('alertdialog')).toHaveTextContent(
      'Delete this session?',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Delete permanently' }));
    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'Sessions' }),
      ).toBeInTheDocument();
    });
  });

  test('renders mixed-provider availability states honestly', async () => {
    mockAPI([
      {
        ...session,
        attributes: { event_count: 1, model: 'gpt-5-codex' },
        availability: { ...session.availability, model: 'observed' },
      },
      {
        ...session,
        session_id: 'session-2',
        provider: 'anthropic',
        tool: 'claude-code',
        attributes: { event_count: 1 },
        availability: {
          ...session.availability,
          model: 'unavailable',
          token_usage: 'unavailable',
        },
      },
    ]);
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    expect(await screen.findByText('gpt-5-codex')).toBeInTheDocument();
    expect(screen.getByText('Model unavailable')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /claude-code/i }));
    expect(await screen.findByText('Model unavailable')).toBeInTheDocument();
    expect(screen.getByText('Token usage unavailable')).toBeInTheDocument();
  });

  test('labels partial and legacy-missing availability by field', async () => {
    mockAPI([
      session,
      {
        ...session,
        session_id: 'session-2',
        tool: 'legacy-tool',
        attributes: { event_count: 1 },
        availability: undefined,
      },
    ]);
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    fireEvent.click(await screen.findByRole('button', { name: /codex/i }));
    expect(
      await screen.findByText('Token usage partially available'),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '← Sessions' }));
    fireEvent.click(
      await screen.findByRole('button', { name: /legacy-tool/i }),
    );
    expect(await screen.findByText('Model unavailable')).toBeInTheDocument();
  });

  test('shows the privacy defaults', async () => {
    mockAPI();
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Privacy' }));
    expect(
      await screen.findByText('Prompts and responses'),
    ).toBeInTheDocument();
    expect(screen.getAllByText('Not retained')).toHaveLength(2);
  });

  test('shows observed integrations and non-success outcomes on the home page', async () => {
    mockAPI([
      { ...session, state: 'failed' },
      {
        ...session,
        session_id: 'session-2',
        tool: 'claude',
        state: 'abandoned',
      },
    ]);
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );
    expect(await screen.findByText('Failed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Integrations' }));
    expect(
      await screen.findByRole('heading', { name: 'codex' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'claude' })).toBeInTheDocument();
  });

  test('shows daemon and session-detail errors and can cancel deletion', async () => {
    mockAPI([session], { detailFails: true, healthFails: true });
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Daemon offline',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    fireEvent.click(await screen.findByRole('button', { name: /codex/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Request failed',
    );
  });

  test('keeps the detail view when deletion fails and allows cancellation', async () => {
    mockAPI([session], { deleteFails: true });
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    fireEvent.click(await screen.findByRole('button', { name: /codex/i }));
    await screen.findByRole('heading', { name: 'codex session' });
    fireEvent.click(
      screen.getByRole('button', { name: 'Delete this session' }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', { name: 'Delete this session' }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Delete permanently' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Delete failed');
  });

  test('shows calculated and unknown costs without treating unknown as zero', async () => {
    mockAPI();
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Costs' }));
    expect(
      await screen.findByRole('heading', { name: 'Costs' }),
    ).toBeInTheDocument();
    expect(screen.getByText(/1\.25/)).toBeInTheDocument();
    expect(
      screen.getByText('Unknown prices are not shown as zero.'),
    ).toBeInTheDocument();
    expect(screen.getByText('unknown_price')).toBeInTheDocument();
  });

  test('shows MCP inventory with explicit context labels', async () => {
    renderInsights();
    await expectInsightContent(
      'MCP inventory',
      ['Connected MCPs', 'used', 'read_file, list_directory'],
      ['filesystem', '2', 'unavailable'],
    );
    expect(
      screen.getAllByText(/not exact per-MCP allocation/).length,
    ).toBeGreaterThan(0);
  });

  test('shows an empty MCP inventory', async () => {
    renderInsights({ insightEmpty: true });
    expect(
      await screen.findByRole('heading', {
        name: 'No MCP connections observed',
      }),
    ).toBeInTheDocument();
  });

  test('shows an MCP inventory loading failure', async () => {
    renderInsights({ insightFails: true });
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Insight service offline',
    );
  });

  test('shows skill usage with explicit and unavailable states', async () => {
    renderInsights();
    // The provider with no explicit skill signal is shown, not silently dropped.
    await expectInsightContent(
      'Skill usage',
      ['Observed skills', 'failed: 1, success: 1'],
      ['pdf', 'unavailable'],
    );
  });

  test('shows an empty skill usage state honestly', async () => {
    renderInsights({ insightEmpty: true });
    expect(
      await screen.findByRole('heading', {
        name: 'No skill identity observed',
      }),
    ).toBeInTheDocument();
  });

  test('shows a skill usage loading failure', async () => {
    renderInsights({ skillFails: true });
    expect(
      await screen.findByText('Skill service offline'),
    ).toBeInTheDocument();
  });

  test('shows a cost loading failure', async () => {
    mockAPI([session], { costFails: true });
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Costs' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Cost service offline',
    );
  });
});
