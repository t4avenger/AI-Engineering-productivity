import { useEffect, useState } from 'react';

import { fetchHealth, type HealthResponse } from './health';

type HealthState =
  | { status: 'loading' }
  | { status: 'healthy'; data: HealthResponse }
  | { status: 'unhealthy'; message: string };

const defaultHealthUrl = 'http://127.0.0.1:8080/api/v1/health';
const env = import.meta.env as { readonly VITE_HEALTH_URL?: string };
const healthUrl: string = env.VITE_HEALTH_URL ?? defaultHealthUrl;

export function App() {
  const [health, setHealth] = useState<HealthState>({ status: 'loading' });

  useEffect(() => {
    let cancelled = false;

    fetchHealth(healthUrl)
      .then((data) => {
        if (!cancelled) {
          setHealth({ status: 'healthy', data });
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setHealth({
            status: 'unhealthy',
            message:
              error instanceof Error ? error.message : 'Unable to reach daemon',
          });
        }
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <main className="shell">
      <section className="status-panel" aria-labelledby="app-title">
        <div>
          <p className="eyebrow">Local developer edition</p>
          <h1 id="app-title">TelemetryIQ</h1>
          <p className="summary">
            Local-first AI engineering intelligence starts with a loopback-only
            daemon.
          </p>
        </div>
        <HealthCard health={health} />
      </section>
    </main>
  );
}

function HealthCard({ health }: { health: HealthState }) {
  if (health.status === 'loading') {
    return (
      <div
        className="health-card health-card--loading"
        role="status"
        aria-live="polite"
      >
        <span className="pulse" aria-hidden="true" />
        <div>
          <h2>Checking daemon</h2>
          <p>Loading health state.</p>
        </div>
      </div>
    );
  }

  if (health.status === 'unhealthy') {
    return (
      <div className="health-card health-card--unhealthy" role="alert">
        <span className="indicator" aria-hidden="true" />
        <div>
          <h2>Daemon unhealthy</h2>
          <p>{health.message}</p>
        </div>
      </div>
    );
  }

  return (
    <div
      className="health-card health-card--healthy"
      role="status"
      aria-live="polite"
    >
      <span className="indicator" aria-hidden="true" />
      <div>
        <h2>Daemon healthy</h2>
        <dl>
          <div>
            <dt>Service</dt>
            <dd>{health.data.service}</dd>
          </div>
          <div>
            <dt>Checked</dt>
            <dd>{health.data.timestamp}</dd>
          </div>
        </dl>
      </div>
    </div>
  );
}
