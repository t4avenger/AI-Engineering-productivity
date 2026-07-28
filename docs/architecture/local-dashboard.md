# Local dashboard

Task 010 implements a local React dashboard with Home, Sessions, Session
Detail, Integrations, and Privacy pages. It consumes only the loopback API and
does not send analytics or dashboard data to third parties.

The Sessions page uses the stable Task 009 read API. A session detail is loaded
only after selection. Deletion uses `DELETE /api/v1/sessions/{id}` and requires
an in-app confirmation before the request is sent; the repository removes the
session and all retained events transactionally.

The dashboard never converts unavailable telemetry into zero. Examples include
an unavailable model or completion time. Integration status is derived only
from observed local sessions: with none observed it says `Awaiting telemetry`,
rather than claiming that a provider has been detected.

The Privacy page documents the enforced local-only defaults: no prompt,
response, or source-code retention; hashed file paths; redacted command
arguments; no sharing; and a default 30-day retention period. These values are
not editable in the dashboard because configuration remains file-based and is
validated by the daemon.
