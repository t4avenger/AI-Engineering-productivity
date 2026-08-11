import {
  Badge,
  Button,
  Card,
  Modal,
  TextInput,
  Code,
  Group,
  PasswordInput,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import { useEffect, useState } from 'react';

import { fetchHealth, type HealthResponse } from './health';
import {
  deleteAllSessions,
  deleteSession,
  fetchEventProvenance,
  fetchSession,
  fetchSessionEvents,
  fetchSessions,
  type Provenance,
  type Session,
  type TimelineEvent,
  SessionAPIError,
} from './sessions';

type Page = 'home' | 'sessions' | 'integrations' | 'privacy';
type HealthState =
  | { status: 'loading' }
  | { status: 'healthy'; data: HealthResponse }
  | { status: 'unhealthy'; message: string };
type SessionState =
  | { status: 'loading'; data: Session[] }
  | { status: 'ready'; data: Session[] }
  | { status: 'error'; data: Session[]; message: string };
type TimelineState =
  | { status: 'loading'; data: TimelineEvent[]; nextCursor: string | null }
  | { status: 'ready'; data: TimelineEvent[]; nextCursor: string | null }
  | {
      status: 'error';
      data: TimelineEvent[];
      nextCursor: string | null;
      message: string;
    };

const healthURL =
  (import.meta.env as { readonly VITE_HEALTH_URL?: string }).VITE_HEALTH_URL ??
  'http://127.0.0.1:8080/api/v1/health';

export function App() {
  const [page, setPage] = useState<Page>('home');
  const [health, setHealth] = useState<HealthState>({ status: 'loading' });
  const [sessions, setSessions] = useState<SessionState>({
    status: 'loading',
    data: [],
  });
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [token, setToken] = useState<string | null>(() =>
    sessionStorage.getItem('telemetryiq-auth-token'),
  );

  useEffect(() => {
    fetchHealth(healthURL)
      .then((data) => {
        setHealth({ status: 'healthy', data });
      })
      .catch((error: unknown) => {
        setHealth({
          status: 'unhealthy',
          message:
            error instanceof Error ? error.message : 'Unable to reach daemon',
        });
      });
    if (token) {
      void refreshSessions();
    }
  }, [token]);

  async function refreshSessions() {
    try {
      const data = await fetchSessions();
      setSessions({ status: 'ready', data });
    } catch (error) {
      if (error instanceof SessionAPIError && error.status === 401) {
        sessionStorage.removeItem('telemetryiq-auth-token');
        setToken(null);
        return;
      }
      setSessions((current) => ({
        status: 'error',
        data: current.data,
        message:
          error instanceof Error ? error.message : 'Unable to load sessions',
      }));
    }
  }

  if (!token) {
    return (
      <AuthSetup
        onAuthenticated={(value) => {
          sessionStorage.setItem('telemetryiq-auth-token', value);
          setToken(value);
        }}
      />
    );
  }

  const content = selectedID ? (
    <SessionDetail
      id={selectedID}
      onBack={() => {
        setSelectedID(null);
      }}
      onDeleted={() => {
        setSelectedID(null);
        void refreshSessions();
      }}
    />
  ) : (
    <PageContent
      page={page}
      health={health}
      sessions={sessions}
      onSelectSession={setSelectedID}
      onAllDeleted={() => {
        setSelectedID(null);
        void refreshSessions();
      }}
    />
  );

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      <header className="app-header">
        <p className="brand">
          TelemetryIQ <span>Local developer edition</span>
        </p>
        <HealthIndicator health={health} />
      </header>
      <nav aria-label="Primary navigation" className="navigation">
        {(['home', 'sessions', 'integrations', 'privacy'] as const).map(
          (item) => (
            <button
              aria-current={!selectedID && page === item ? 'page' : undefined}
              className={
                !selectedID && page === item ? 'nav-link active' : 'nav-link'
              }
              key={item}
              onClick={() => {
                setSelectedID(null);
                setPage(item);
              }}
              type="button"
            >
              {item[0].toUpperCase() + item.slice(1)}
            </button>
          ),
        )}
      </nav>
      <main id="main-content">{content}</main>
    </div>
  );
}

function PageContent({
  page,
  health,
  sessions,
  onSelectSession,
  onAllDeleted,
}: Readonly<{
  page: Page;
  health: HealthState;
  sessions: SessionState;
  onSelectSession: (id: string) => void;
  onAllDeleted: () => void;
}>) {
  if (page === 'sessions')
    return <SessionsPage sessions={sessions} onSelect={onSelectSession} />;
  if (page === 'integrations')
    return <IntegrationsPage sessions={sessions.data} />;
  if (page === 'privacy') return <PrivacyPage onDeleted={onAllDeleted} />;
  return <HomePage health={health} sessions={sessions.data} />;
}

function HomePage({
  health,
  sessions,
}: Readonly<{
  health: HealthState;
  sessions: Session[];
}>) {
  const outcomes = sessions.reduce<Partial<Record<Session['state'], number>>>(
    (counts, session) => {
      counts[session.state] = (counts[session.state] ?? 0) + 1;
      return counts;
    },
    {},
  );
  return (
    <section aria-labelledby="home-title" className="page">
      <p className="eyebrow">Overview</p>
      <h1 id="home-title">Your local AI activity</h1>
      <p className="lede">
        Only privacy-safe telemetry retained by this device appears here.
      </p>
      <dl className="metrics">
        <Metric label="Sessions" value={sessions.length} />
        <Metric label="Completed" value={outcomes.completed ?? 0} />
        <Metric label="Failed" value={outcomes.failed ?? 0} />
        <Metric label="Abandoned" value={outcomes.abandoned ?? 0} />
      </dl>
      <section className="panel">
        <h2>Local service</h2>
        <HealthSummary health={health} />
      </section>
      <p className="notice">
        Cost and governance data are not available yet. They will be clearly
        labelled when implemented.
      </p>
    </section>
  );
}

function SessionsPage({
  sessions,
  onSelect,
}: Readonly<{
  sessions: SessionState;
  onSelect: (id: string) => void;
}>) {
  return (
    <section aria-labelledby="sessions-title" className="page">
      <p className="eyebrow">Session explorer</p>
      <h1 id="sessions-title">Sessions</h1>
      <p className="lede">
        Sessions are shown newest first. Missing fields are labelled unavailable
        rather than treated as zero.
      </p>
      {sessions.status === 'loading' ? (
        <output>Loading sessions…</output>
      ) : null}
      {sessions.status === 'error' ? (
        <p role="alert">{sessions.message}</p>
      ) : null}
      {sessions.status !== 'loading' && sessions.data.length === 0 ? (
        <div className="empty">
          <h2>No sessions yet</h2>
          <p>
            TelemetryIQ has not received a retained session. Check Integrations
            for the next step.
          </p>
        </div>
      ) : null}
      {sessions.data.length > 0 ? (
        <ul className="session-list">
          {sessions.data.map((session) => (
            <li key={session.session_id}>
              <button
                className="session-row"
                onClick={() => {
                  onSelect(session.session_id);
                }}
                type="button"
              >
                <span>
                  <strong>{session.tool}</strong>
                  <small>{formatDate(session.started_at)}</small>
                </span>
                <span className={`state state--${session.state}`}>
                  {session.state}
                </span>
                <span>{modelOf(session) ?? 'Model unavailable'}</span>
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

function SessionDetail({
  id,
  onBack,
  onDeleted,
}: Readonly<{
  id: string;
  onBack: () => void;
  onDeleted: () => void;
}>) {
  const [session, setSession] = useState<Session | null>(null);
  const [timeline, setTimeline] = useState<TimelineState>({
    status: 'loading',
    data: [],
    nextCursor: null,
  });
  const [provenance, setProvenance] = useState<{
    eventID: string;
    data: Provenance[];
  } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);

  useEffect(() => {
    fetchSession(id)
      .then(setSession)
      .catch((reason: unknown) => {
        setError(
          reason instanceof Error ? reason.message : 'Unable to load session',
        );
      });
    fetchSessionEvents(id)
      .then((page) => {
        setTimeline({
          status: 'ready',
          data: page.data,
          nextCursor: page.pagination.next_cursor,
        });
      })
      .catch((reason: unknown) => {
        setTimeline({
          status: 'error',
          data: [],
          nextCursor: null,
          message:
            reason instanceof Error
              ? reason.message
              : 'Unable to load session timeline',
        });
      });
  }, [id]);

  async function loadMore() {
    if (!timeline.nextCursor) return;
    try {
      const page = await fetchSessionEvents(id, timeline.nextCursor);
      setTimeline((current) => ({
        status: 'ready',
        data: [...current.data, ...page.data],
        nextCursor: page.pagination.next_cursor,
      }));
    } catch (reason) {
      setTimeline((current) => ({
        status: 'error',
        data: current.data,
        nextCursor: current.nextCursor,
        message:
          reason instanceof Error
            ? reason.message
            : 'Unable to load more session events',
      }));
    }
  }

  async function loadProvenance(eventID: string) {
    try {
      setProvenance({ eventID, data: await fetchEventProvenance(eventID) });
    } catch (reason) {
      setError(
        reason instanceof Error
          ? reason.message
          : 'Unable to load event provenance',
      );
    }
  }

  async function confirmDelete() {
    try {
      await deleteSession(id);
      onDeleted();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : 'Unable to delete session',
      );
      setConfirming(false);
    }
  }

  return (
    <section aria-labelledby="detail-title" className="page">
      <button className="back" onClick={onBack} type="button">
        ← Sessions
      </button>
      {error ? <p role="alert">{error}</p> : null}
      {!session ? (
        <output>Loading session…</output>
      ) : (
        <>
          <p className="eyebrow">Session detail</p>
          <h1 id="detail-title">{session.tool} session</h1>
          <dl className="details">
            <Detail label="Outcome" value={session.state} />
            <Detail label="Started" value={formatDate(session.started_at)} />
            <Detail
              label="Completed"
              value={
                session.completed_at
                  ? formatDate(session.completed_at)
                  : 'Unavailable'
              }
            />
            <Detail label="Provider" value={session.provider} />
            <Detail label="Model" value={modelOf(session) ?? 'Unavailable'} />
            <Detail
              label="Observed events"
              value={numberAttribute(session, 'event_count') ?? 'Unavailable'}
            />
          </dl>
          <section aria-labelledby="timeline-title" className="panel">
            <h2 id="timeline-title">Event timeline</h2>
            <p>Only privacy-safe, retained event metadata appears here.</p>
            {timeline.status === 'loading' ? (
              <output>Loading event timeline…</output>
            ) : null}
            {timeline.status === 'error' ? (
              <p role="alert">{timeline.message}</p>
            ) : null}
            {timeline.status !== 'loading' && timeline.data.length === 0 ? (
              <p>No retained events are available for this session.</p>
            ) : null}
            {timeline.data.length > 0 ? (
              <ol className="event-timeline">
                {timeline.data.map((event) => (
                  <li key={event.event_id}>
                    <h3>{event.event_type}</h3>
                    <p>{formatDate(event.occurred_at)}</p>
                    <p>
                      Model: {event.model ?? 'Unavailable'}; input tokens:{' '}
                      {event.input_token_count ?? 'Unavailable'}; output tokens:{' '}
                      {event.output_token_count ?? 'Unavailable'}
                    </p>
                    {event.unavailable_fields.length > 0 ? (
                      <p>Unavailable: {event.unavailable_fields.join(', ')}</p>
                    ) : null}
                    <button
                      onClick={() => {
                        void loadProvenance(event.event_id);
                      }}
                      type="button"
                    >
                      View provenance
                    </button>
                    {provenance?.eventID === event.event_id ? (
                      <dl aria-label="Event provenance" className="details">
                        {provenance.data.map((entry) => (
                          <div key={entry.path}>
                            <dt>{entry.path}</dt>
                            <dd>
                              {entry.action}: {entry.reason}
                            </dd>
                          </div>
                        ))}
                      </dl>
                    ) : null}
                  </li>
                ))}
              </ol>
            ) : null}
            {timeline.nextCursor ? (
              <button
                onClick={() => {
                  void loadMore();
                }}
                type="button"
              >
                Load more events
              </button>
            ) : null}
          </section>
          <section className="panel">
            <h2>Data retention</h2>
            <p>
              Prompt text, responses, source code, and raw command arguments are
              not retained by default.
            </p>
          </section>
          <button
            className="danger"
            onClick={() => {
              setError(null);
              setConfirming(true);
            }}
            type="button"
          >
            Delete this session
          </button>
          {confirming ? (
            <div
              aria-labelledby="delete-title"
              aria-modal="true"
              className="dialog"
              role="alertdialog"
            >
              <h2 id="delete-title">Delete this session?</h2>
              <p>
                This permanently removes the session and its retained events
                from local storage.
              </p>
              <button
                className="danger"
                onClick={() => {
                  void confirmDelete();
                }}
                type="button"
              >
                Delete permanently
              </button>
              <button
                onClick={() => {
                  setConfirming(false);
                }}
                type="button"
              >
                Cancel
              </button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

function IntegrationsPage({ sessions }: Readonly<{ sessions: Session[] }>) {
  const tools = [...new Set(sessions.map((session) => session.tool))];
  return (
    <section aria-labelledby="integrations-title" className="page">
      <p className="eyebrow">Integration health</p>
      <h1 id="integrations-title">Integrations</h1>
      {tools.length === 0 ? (
        <div className="empty">
          <h2>Awaiting telemetry</h2>
          <p>
            No supported tool has supplied retained session data yet. Configure
            Codex telemetry, then return here to confirm receipt.
          </p>
        </div>
      ) : (
        <ul className="cards">
          {tools.map((tool) => (
            <li key={tool}>
              <h2>{tool}</h2>
              <p>
                <span className="state state--completed">
                  Receiving retained session data
                </span>
              </p>
              <p>
                Capabilities are limited to observed fields; unavailable
                information is not inferred.
              </p>
            </li>
          ))}
        </ul>
      )}
      <p className="notice">
        Only observed tools are listed. This avoids reporting a detection that
        has not occurred.
      </p>
    </section>
  );
}

function PrivacyPage({ onDeleted }: Readonly<{ onDeleted: () => void }>) {
  const [confirming, setConfirming] = useState(false);
  const [confirmation, setConfirmation] = useState('');
  const [error, setError] = useState<string | null>(null);
  async function confirmDeleteAll() {
    try {
      await deleteAllSessions();
      setError(null);
      setConfirming(false);
      setConfirmation('');
      onDeleted();
    } catch (reason) {
      setError(
        reason instanceof Error
          ? reason.message
          : 'Unable to delete retained telemetry',
      );
    }
  }
  return (
    <section aria-labelledby="privacy-title" className="page">
      <p className="eyebrow">Privacy controls</p>
      <h1 id="privacy-title">Privacy</h1>
      <p className="lede">
        TelemetryIQ is local-only by default. These enforced defaults describe
        what this version retains.
      </p>
      <dl className="details">
        <Detail label="Telemetry level" value="Operational" />
        <Detail label="Prompts and responses" value="Not retained" />
        <Detail label="Source code" value="Not retained" />
        <Detail label="File paths" value="Hashed" />
        <Detail label="Command arguments" value="Redacted" />
        <Detail label="Sharing" value="Disabled" />
        <Detail label="Local retention" value="30 days by default" />
      </dl>
      <section className="panel">
        <h2>What this means</h2>
        <p>
          Privacy transformations happen before local persistence and
          diagnostics. Changes to the configuration file are validated so unsafe
          collection cannot be enabled in this local-only release.
        </p>
      </section>
      {error ? <p role="alert">{error}</p> : null}
      <section className="panel">
        <h2>Delete retained telemetry</h2>
        <p>
          Permanently remove every retained session and event from this device.
          Configuration and local privacy identity remain.
        </p>
        <button
          className="danger"
          onClick={() => {
            setError(null);
            setConfirming(true);
          }}
          type="button"
        >
          Delete all retained telemetry
        </button>
        <Modal
          centered
          onClose={() => {
            setConfirming(false);
            setConfirmation('');
            setError(null);
          }}
          opened={confirming}
          title="Delete all retained telemetry?"
        >
          <Stack>
            <Text>This cannot be undone. Type DELETE ALL to continue.</Text>
            <TextInput
              id="delete-all-confirmation"
              label="Confirmation"
              onChange={(event) => {
                setConfirmation(event.target.value);
              }}
              value={confirmation}
            />
            <Group justify="flex-end">
              <Button
                onClick={() => {
                  setConfirming(false);
                  setConfirmation('');
                  setError(null);
                }}
                variant="default"
              >
                Cancel
              </Button>
              <Button
                color="red"
                disabled={confirmation !== 'DELETE ALL'}
                onClick={() => {
                  void confirmDeleteAll();
                }}
              >
                Delete all permanently
              </Button>
            </Group>
          </Stack>
        </Modal>
      </section>
    </section>
  );
}

function AuthSetup({
  onAuthenticated,
}: Readonly<{ onAuthenticated: (token: string) => void }>) {
  const [value, setValue] = useState('');
  return (
    <main id="main-content">
      <Card maw={480} mx="auto" p="xl" radius="lg" shadow="lg" withBorder>
        <Stack gap="lg">
          <Badge variant="light" w="fit-content">
            Local access
          </Badge>
          <div>
            <Title order={1}>Connect your dashboard</Title>
            <Text c="dimmed" mt="sm">
              Run <Code>telemetryiq auth-token</Code> in a terminal, then paste
              the token below. It remains only for this browser session.
            </Text>
          </div>
          <PasswordInput
            autoComplete="off"
            id="auth-token"
            label="Local API token"
            onChange={(event) => {
              setValue(event.target.value);
            }}
            placeholder="Paste your local token"
            size="md"
            value={value}
          />
          <Button
            disabled={value.trim() === ''}
            onClick={() => {
              onAuthenticated(value.trim());
            }}
            size="md"
          >
            Connect securely
          </Button>
        </Stack>
      </Card>
    </main>
  );
}

function HealthIndicator({ health }: Readonly<{ health: HealthState }>) {
  let label = 'Checking daemon';
  if (health.status === 'healthy') {
    label = 'Daemon healthy';
  } else if (health.status === 'unhealthy') {
    label = 'Daemon unavailable';
  }
  return (
    <output
      className={health.status === 'healthy' ? 'health healthy' : 'health'}
    >
      {label}
    </output>
  );
}

function HealthSummary({ health }: Readonly<{ health: HealthState }>) {
  if (health.status === 'healthy') {
    return (
      <p>
        <strong>Healthy.</strong> {health.data.service} was checked at{' '}
        {formatDate(health.data.timestamp)}.
      </p>
    );
  }
  if (health.status === 'unhealthy') {
    return <p role="alert">Daemon unavailable: {health.message}</p>;
  }
  return <output>Checking daemon…</output>;
}

function Metric({ label, value }: Readonly<{ label: string; value: number }>) {
  return (
    <div className="metric">
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}
function Detail({
  label,
  value,
}: Readonly<{ label: string; value: string | number }>) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}
function modelOf(session: Session): string | null {
  return typeof session.attributes.model === 'string'
    ? session.attributes.model
    : null;
}
function numberAttribute(
  session: Session,
  key: string,
): number | string | null {
  const value = session.attributes[key];
  return typeof value === 'number' || typeof value === 'string' ? value : null;
}
function formatDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'Unavailable' : date.toLocaleString();
}
