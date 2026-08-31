# Product Map: AI Engineering Intelligence & Governance

**Working name:** TelemetryIQ  
**Document status:** Build specification  
**Primary implementation agent:** OpenAI Codex  
**Initial product:** Local-first individual developer edition  
**Future product:** Managed team and enterprise platform

---

## 0. Reorientation directive (2026-08-30)

This directive overrides conflicting emphasis and ordering elsewhere in this document.
Where a later section disagrees with this one, this section wins until it is deliberately revised.

**The MVP is behaviour & efficiency observability, not cost accounting.** The headline
questions are:

1. Which tools, models, MCP servers, and skills are being used?
2. Are they token- and context-efficient, or are unused MCPs/skills wasting context?
3. Are the models actually performing (task outcomes, retries/errors, efficiency, latency)?
4. Are agents doing risky things (e.g. reading `.env`/credential files)?
5. Exactly what data is collected, retained, and shared?

**What changed and why.** The written principles said "outcomes, not activity," but the
build order front-loaded the cost engine while the normaliser stayed a skeleton that
extracts no model, token, tool-call, MCP, or file-operation data. Cost was being priced on
fields the pipeline never populated. Cost is therefore **demoted to a secondary, versioned,
optional estimate** — kept, never deleted, never on the Home headline, never gating a phase.

**First-class providers:** Claude Code, Codex, and Cursor. Devin is deferred (audit-log only).

**Guiding correction.** Do not design canonical types or dashboards for a signal until a
real, version-pinned fixture proves the provider emits it. Type only stable primitives; keep
provider-specific shapes in `provider_extensions` until two independent providers produce
semantically equivalent data. Separate immutable **observations** from versioned derived
**findings** (each finding carries detector version, source event IDs, confidence, and an
`observed | inferred | unknown` marker).

**Revised delivery order (replaces the phase order in section 19):**

- **P0 — Capability & privacy spike (gate).** Capture version-pinned fixtures for Codex,
  Claude Code, and Cursor. Fill the multi-provider capability matrix
  (`docs/integrations/capability-matrix.md`). Settle the ingestion-wide redaction boundary
  and privacy threat model (`docs/privacy/threat-model.md`) *before* extraction. No canonical
  type or dashboard is committed before this passes.
- **P1 — Minimal observation envelope + real Codex extraction.** Add only stable-primitive
  records (model interaction; generic operation/invocation). Make the Codex normaliser
  extract real model/token/tool data. Make correlation (dedup keys, ordering, task
  boundaries) first-class. Redaction lands here, proven by canary-string leakage tests.
- **P2 — Claude Code adapter + capability-driven conformance suite.** OTLP + session JSONL.
  Skill detection marked `explicit | inferred | unavailable`. Cross-tool session view with
  honest "unavailable" cells.
- **P3 — Behaviour, efficiency & model-performance insights.** MCP inventory & context cost
  (incl. "connected but unused"); skill usage; model-performance scorecard on outcome
  contracts; context-waste insight.
- **P4 — Risky-behaviour governance (detect-and-report).** Observed risky access to
  credential/secret files (filesystem *and* shell forms; allowlist `.env.example`;
  `indeterminate` when blind). Unapproved MCP server. Privacy-safe path handling only.
- **P5 — Cursor partial adapter.** Only its verified capability subset; no empty scorecards.
- **Cost — secondary, parallel, never gating** throughout.

---

## 1. Product mission

Build a vendor-neutral platform that collects, normalises, and analyses telemetry from AI coding tools and model providers so developers and organisations can understand:

1. Which AI tools, models, MCP servers, and skills are being used.
2. Whether AI-assisted work is token- and context-efficient (including MCPs/skills that waste context).
3. Whether the models are actually performing (task outcomes, retries/errors, efficiency, latency).
4. Whether agents are doing risky things (e.g. reading `.env`/credential files).
5. Whether usage complies with organisational policy.
6. Exactly what data is collected, retained, and shared.
7. What those tools cost — a secondary, optional estimate (see section 0).

The initial product must be useful to one developer running locally. The same architecture must later support voluntary cloud sharing, teams, managed enterprise deployment, and self-hosting.

---

## 2. Product principles

### 2.1 Local-first
The first usable release must run entirely on a developer's machine. Cloud connectivity must not be required.

### 2.2 Privacy by design
Prompt text, response text, source code, filenames, and command arguments must not be collected by default.

### 2.3 Progressive telemetry
Users and administrators must be able to select increasing levels of telemetry detail.

### 2.4 Evidence before scoring
Every recommendation or score must link to the observable events and calculations that produced it.

### 2.5 Outcomes, not activity
Do not treat prompt counts, token counts, generated lines, or time in a tool as productivity.

### 2.6 Vendor neutrality
Raw provider events may differ, but the internal canonical model must be stable and provider-independent.

### 2.7 One collector, multiple modes
Do not create separate collection products for individual and enterprise use.

Supported modes will eventually be:

- `local-only`
- `personal-cloud`
- `team-managed`
- `enterprise-managed`
- `self-hosted`

Only `local-only` is required for the first release.

---

## 3. Target users

### Initial user
A developer using one or more AI coding tools who wants to understand personal usage, cost, failure patterns, and governance risks.

### Early team buyer
An engineering-platform, developer-productivity, FinOps, security, or AI-governance leader running a controlled coding-agent pilot.

### Enterprise user
An organisation that needs central deployment, policy enforcement, audit evidence, role-based access, data residency, and integration with security and engineering systems.

---

## 4. Product promise by stage

### Stage 1: Local developer
> See every supported AI coding session, understand its cost and behaviour, identify wasted work, and keep all telemetry on your machine.

### Stage 2: Personal cloud
> Compare usage across devices and tools while retaining control over what is uploaded.

### Stage 3: Team
> Understand team adoption, cost, workflow effectiveness, and policy compliance without creating developer surveillance.

### Stage 4: Enterprise
> Govern AI engineering activity across approved tools, models, repositories, data classes, and autonomous actions.

---

## 5. MVP boundaries

### Included in MVP

- Local application
- Local OpenTelemetry ingestion
- Codex integration as the first provider
- Claude Code integration (first-class; MCP + skill signals)
- Cursor integration (partial adapter, verified capability subset only)
- Canonical event normalisation with real model/token/tool/MCP/file extraction
- Local analytical storage
- Session explorer
- MCP inventory & context-cost insight (incl. "connected but unused")
- Skill usage insight
- Model-performance scorecard (outcomes, retries/errors, efficiency, latency)
- Context-waste insight
- Risky-behaviour detection (observed access to `.env`/credential files)
- Cost estimation (secondary, optional, versioned — see section 0)
- Integration health
- Privacy controls
- Data deletion
- Sanitised diagnostic export
- Initial policy evaluation
- Synthetic integration test harness

### Explicitly excluded from MVP

- Enterprise SSO or SCIM
- Managed endpoint deployment
- Multi-tenant SaaS
- Employee rankings
- Universal productivity score
- Prompt or source-code storage by default
- Billing system
- Mobile application
- Devin integration (audit-log only; deferred)
- SIEM export
- Automated blocking of tool actions
- Machine-learning recommendations
- Cross-company benchmarking

Do not implement excluded features unless this document is deliberately revised.

---

## 6. Success criteria for the first usable release

A developer can:

1. Install and start the application locally.
2. Configure Codex to export supported telemetry.
3. See a new session appear in the dashboard.
4. Inspect model calls, duration, token usage, retries, and errors.
5. See an estimated session cost with calculation details.
6. see whether a session succeeded, failed, or was abandoned.
7. Inspect which data fields were retained or discarded.
8. change privacy settings.
9. delete one session or all local data.
10. export a sanitised support bundle.
11. run a synthetic test suite that verifies ingestion and normalisation.
12. stop the product without affecting Codex operation.

A release is not complete until these flows are covered by automated tests and documented.

---

## 7. System context

```text
AI coding tool
    |
    | OTLP, local API, file import, or vendor API
    v
Local ingest gateway
    |
    v
Privacy and redaction pipeline
    |
    +----> Raw quarantine/debug stream (optional, disabled by default)
    |
    v
Canonical normalisation
    |
    v
Local event store
    |
    +----> Metrics and insight engine
    |
    v
Local web API
    |
    v
Local dashboard
```

Future hosted architecture:

```text
Endpoint collector
    |
    v
Regional ingest gateway
    |
    +----> Encrypted raw object storage
    |
    +----> Stream processing and normalisation
                |
                +----> Analytical store
                +----> Policy engine
                +----> Metrics engine
                              |
                              v
                    Dashboards and exports
```

---

## 8. Recommended technology choices

Codex may change an implementation choice when justified in an architecture decision record.

### Local agent and backend
- Go
- Single local daemon
- Structured logging
- OTLP HTTP receiver first
- OTLP gRPC receiver second
- REST API for dashboard

### Web application
- TypeScript
- React
- Vite
- Mantine Core and Hooks for accessible dashboard primitives (ADR 0001)
- Localhost-only by default
- No third-party analytics in local mode

### Storage
- SQLite for configuration and initial local event storage
- Abstract repository interface so ClickHouse can be introduced later
- JSON column or equivalent for provider-specific extensions
- Migrations required from the beginning

### Testing and quality engineering
- Go unit, component, integration, contract, race, fuzz, and benchmark tests
- TypeScript unit and component tests
- Playwright functional end-to-end tests
- Golden fixtures and adapter conformance tests for provider telemetry
- Schema compatibility and database migration tests
- Privacy leakage and diagnostics sanitisation tests
- Performance, reliability, security, accessibility, and installation tests
- Docker Compose only where required for deterministic integration environments
- Quality gates run locally, in pre-commit, and in CI

### Static analysis and supply-chain controls
- `golangci-lint` with a checked-in, reviewed configuration
- `go vet`, `staticcheck`, `govulncheck`, and the Go race detector
- TypeScript strict mode and `tsc --noEmit`
- ESLint with type-aware rules
- Prettier format verification
- `knip` or an equivalent unused-code/dependency check
- `gitleaks` for secret scanning
- `osv-scanner` or equivalent dependency vulnerability scanning
- Semgrep for security-focused static analysis
- Trivy for filesystem, dependency, container, and configuration scanning when applicable
- ShellCheck for shell scripts
- actionlint for GitHub Actions workflows
- Hadolint when Dockerfiles are introduced
- SBOM generation for release artifacts
- Dependency pinning and automated dependency update review

Do not add every tool merely to create a long toolchain. Each tool must have a documented purpose, stable configuration, and an actionable failure policy. Duplicate checks should be consolidated.

### Policy evaluation
- Start with deterministic application code and declarative YAML policies
- Avoid introducing OPA or another policy runtime until policy complexity justifies it

---

## 9. Repository layout

```text
/
├── AGENTS.md
├── PRODUCT_MAP.md
├── README.md
├── Makefile
├── go.mod
├── cmd/
│   └── telemetryiq/
├── internal/
│   ├── config/
│   ├── ingest/
│   │   ├── otlphttp/
│   │   └── fixtures/
│   ├── privacy/
│   ├── normalize/
│   │   ├── canonical/
│   │   ├── codex/
│   │   └── claude/
│   ├── identity/
│   ├── storage/
│   │   └── sqlite/
│   ├── cost/
│   ├── policy/
│   ├── insights/
│   ├── diagnostics/
│   └── api/
├── web/
│   ├── src/
│   ├── tests/
│   └── package.json
├── schemas/
│   ├── canonical-event.schema.json
│   ├── policy.schema.json
│   └── config.schema.json
├── fixtures/
│   ├── codex/
│   ├── claude/
│   └── synthetic/
├── test-harness/
├── docs/
│   ├── architecture/
│   ├── integrations/
│   ├── privacy/
│   └── decisions/
└── scripts/
```

---

## 10. Canonical domain model

### 10.1 Core entities

- Organisation
- Workspace
- Actor
- Device
- Provider
- Tool
- Model
- Session
- Task
- ModelRequest
- AgentTurn
- ToolInvocation
- FileOperation
- CommandExecution
- Approval
- PolicyDecision
- Artifact
- Repository
- Commit
- PullRequest
- CostEvent
- Insight
- IntegrationHealthEvent

For local mode, Organisation and Workspace may use generated local identifiers.

### 10.2 Canonical event envelope

```json
{
  "schema_version": "0.1.0",
  "event_id": "uuid",
  "event_type": "genai.model.request.completed",
  "occurred_at": "2026-07-24T12:00:00Z",
  "received_at": "2026-07-24T12:00:01Z",
  "provider": "openai",
  "tool": "codex",
  "source_schema": "otel",
  "source_version": "unknown",
  "actor_id": "local-pseudonymous-id",
  "device_id": "local-device-id",
  "session_id": "session-id",
  "task_id": null,
  "repository_id": null,
  "privacy_level": "operational",
  "attributes": {},
  "provider_extensions": {}
}
```

The envelope schema stays at `0.1.0`. The stable-primitive records below — the
model-interaction record (§10.3) and the generic operation record (§10.5) — are
versioned independently at their own `0.1.0` (`schemas/model-interaction.schema.json`,
`schemas/operation.schema.json`), so introducing them does not bump the envelope.
Each record carries a `provenance` marker (`observed | inferred | unknown`).
Provider-specific detail (MCP/skill/file specifics) stays in `provider_extensions`
until two providers validate identical semantics (roadmap §0).

### 10.3 Model request record

```json
{
  "request_id": "uuid",
  "session_id": "uuid",
  "provider": "openai",
  "tool": "codex",
  "model": "reported-model-name",
  "started_at": "2026-07-24T12:00:00Z",
  "completed_at": "2026-07-24T12:00:02Z",
  "duration_ms": 2000,
  "input_tokens": 9000,
  "output_tokens": 1200,
  "cached_input_tokens": 5000,
  "reasoning_tokens": null,
  "estimated_cost_usd": null,
  "cost_status": "unknown_price",
  "result": "success",
  "error_code": null,
  "content_capture": "none",
  "policy_context": {}
}
```

The typed `ModelInteraction` record (`internal/normalize/canonical/records.go`,
`schemas/model-interaction.schema.json`) implements the stable primitives of this
shape plus a `provenance` marker. Cost fields (`estimated_cost_usd`, `cost_status`)
are deliberately excluded per the reorientation (roadmap §0); token categories are
nullable so an absent value stays distinct from a genuine zero, and `error_code`
is nullable so "no error" (null) stays distinct from a reported empty code.

### 10.4 Session states

- `created`
- `active`
- `completed`
- `failed`
- `cancelled`
- `abandoned`
- `unknown`

State transitions must be explicit and tested.

### 10.5 Tool invocation categories

- filesystem read
- filesystem write
- filesystem delete
- shell command
- network request
- browser action
- MCP call
- source-control action
- test execution
- build execution
- deployment action
- unknown

Store category independently from provider-specific tool names.

The typed `Operation` record (`internal/normalize/canonical/records.go`,
`schemas/operation.schema.json`) draws its `category` from this enum (all twelve
values, including the `unknown` fallback) and carries a `provenance` marker.

---

## 11. Privacy model

### 11.1 Telemetry levels

#### Level 0: Local disabled
No collection.

#### Level 1: Aggregate
- event counts
- duration
- provider
- tool
- model family
- token counts
- estimated cost
- success or failure

#### Level 2: Operational
Everything in Level 1 plus:
- sessions
- tool-call category
- error codes
- retry relationships
- pseudonymous repository identity
- file-operation counts
- command risk category

#### Level 3: Governed content
Optional and explicit:
- redacted prompts
- redacted responses
- redacted tool arguments
- redacted file paths

#### Level 4: Forensic
Full authorised content for regulated or incident use.

MVP supports Levels 0, 1, and 2. Levels 3 and 4 are schema placeholders only.

### 11.2 Default local configuration

```yaml
mode: local-only

collection:
  level: operational
  prompts: false
  responses: false
  source_code: false
  file_paths: hash
  command_arguments: redact
  tool_calls: true
  model_usage: true

storage:
  destination: local
  retention_days: 30

sharing:
  diagnostics: false
  anonymous_analytics: false
  research_sessions: explicit-only
```

### 11.3 Required safeguards

- Bind the local API to loopback only.
- Generate a local authentication token.
- Never log captured prompt, response, source-code, or secret values.
- Redact before persistent storage.
- Redact before diagnostics.
- Provide field-level provenance showing why a field was retained.
- Support complete local deletion.
- Use synthetic secrets in tests.
- Add tests that prove prohibited fields never reach storage.

---

## 12. Cost model

### 12.1 Requirements

- Price configuration is data, not hard-coded business logic.
- Every price record has provider, model matcher, effective date, currency, and token categories.
- Preserve the raw reported token fields.
- Store the calculation version.
- Mark estimates clearly.
- Never present unknown cost as zero.
- Support input, cached input, output, and any provider-specific token category.
- Historical sessions must retain the price version used at calculation time.
- Allow manual price overrides.

### 12.2 Cost statuses

- `calculated`
- `partial`
- `unknown_model`
- `unknown_price`
- `missing_usage`
- `not_applicable`

---

## 13. Initial insights

All insights must be deterministic and explainable. Per section 0, the headline insights are
the behaviour/efficiency/performance ones below (13.7–13.10); cost-only insights are secondary.

### 13.7 MCP inventory & context-cost insight
Show how many MCP servers are connected, which were actually used, and the request-level
tokens for requests where an MCP was used. Flag "connected but unused" MCP servers as context
waste. Per-MCP token *allocation* is shown only as an explicitly labelled heuristic — a single
MCP call has no defensible standalone token cost.

Evidence:
- connected MCP servers (identity hashed)
- used vs unused
- request-level tokens where MCP present
- confidence marker (`observed | inferred | unknown`)

### 13.8 Skill usage insight
Show which skills were invoked, frequency, and outcome — only where the provider exposes skill
identity. Skill detection is marked `explicit | inferred | unavailable`.

### 13.9 Model-performance scorecard
Per model, report raw metrics with sample size: success/failed/abandoned, retry rate, error
codes, tokens-per-completed-task, latency p50/p95. Built on **outcome contracts** (test/build
result, reverted patch, PR result, or provider completion status), not raw session state. No
cross-model ranking without workload caveats and sufficient sample size.

### 13.10 Context-waste insight
Trigger when cached-context ratio or input-token growth exceeds a configurable threshold.
(Supersedes/absorbs 13.2 as a first-class behaviour signal.)

### 13.1 Repeated-attempt insight
Trigger when multiple failed or superseded attempts occur within one session.

Evidence:
- attempt count
- error sequence
- duration
- estimated failed-attempt cost

### 13.2 Excessive-context insight
Trigger when input-token growth or repeated cached context exceeds a configurable threshold.

Evidence:
- token trend
- cached-token ratio
- threshold
- estimated avoidable cost range

### 13.3 Premium-model mismatch
Do not claim a cheaper model would definitely succeed.

Allowed wording:
> This task used a higher-cost model for a workflow category that has previously completed successfully with a lower-cost model in your own history.

Only enable after sufficient local comparison data exists.

### 13.4 Failure-cost insight
Show the cost and elapsed time consumed by failed or abandoned sessions.

### 13.5 Governance-risk insight
Show detected policy events with severity, evidence, and remediation.

### 13.6 Insight feedback
Every insight supports:
- useful
- not useful
- incorrect

Optional reason:
- task misclassified
- outcome incorrect
- recommendation impractical
- insufficient context
- other

---

## 14. Initial governance policies

MVP policies are detect-and-report only.

### 14.1 Unapproved provider or model
Configuration contains approved providers and model patterns.

### 14.2 Sensitive repository
A repository may be tagged with a local classification.

### 14.3 Secret pattern detected
Run detection only on content that is intentionally available to the privacy pipeline. Persist only secret type, confidence, and a non-reversible fingerprint.

### 14.4 Unapproved MCP server
Compare reported MCP endpoint identity against an allowlist.

### 14.5 Dangerous command category
Categorise commands without retaining raw arguments by default.

Initial categories:
- destructive filesystem
- privilege escalation
- credential access
- external data transfer
- package or dependency mutation
- source-control history rewrite
- deployment or infrastructure mutation

### 14.6 Excessive session spend
Evaluate against a configurable estimated-cost threshold.

### 14.7 Missing approval
Record when a high-risk action is observed without a corresponding approval event, where the provider exposes sufficient evidence.

Never infer a violation when the underlying telemetry is insufficient. Use `indeterminate`.

---

## 15. Integration strategy

### 15.1 Integration interface

Each provider adapter must implement conceptually:

```go
type Adapter interface {
    Name() string
    DetectVersion(ctx context.Context, resource SourceResource) (string, error)
    CanHandle(ctx context.Context, input RawEnvelope) bool
    Normalize(ctx context.Context, input RawEnvelope) ([]CanonicalEvent, []NormalizationWarning, error)
    Capabilities() CapabilitySet
}
```

### 15.2 Capability reporting

Every adapter exposes:

- model usage
- token usage
- cache usage
- session lifecycle
- tool calls
- file operations
- command execution
- approvals
- prompt content
- response content
- repository context
- task outcome
- cost supplied by provider

Capability values:
- `supported`
- `partial`
- `unsupported`
- `unknown`
- `version-dependent`

### 15.3 Raw fixtures

For every supported provider version:

- Store sanitised raw event fixtures.
- Store expected canonical output.
- Store expected warnings.
- Record tool version and fixture origin.
- Never include real user content.

---

## 16. Test harness

### 16.1 Standard scenarios

1. Start and successfully complete a simple coding task.
2. Fix a failing test.
3. Refactor a function.
4. Generate unit tests.
5. Run a shell command.
6. Read and modify files.
7. Trigger a tool error.
8. Retry after failure.
9. Cancel a task.
10. Leave a session without a terminal event.
11. Use a synthetic secret.
12. Attempt a dangerous synthetic command.
13. Invoke an approved MCP server.
14. Invoke an unapproved MCP server.
15. Run with missing token information.
16. Run with an unknown model.
17. Send duplicate events.
18. Send events out of order.
19. Restart the collector during a session.
20. Delete the session and verify removal.

### 16.2 Test contract

Each scenario specifies:

```yaml
id: codex-simple-success
provider: openai
tool: codex
fixture_version: 1
input:
  - fixture-001.json
expected:
  session_state: completed
  minimum_model_requests: 1
  policy_decisions: []
  prohibited_persisted_fields:
    - prompt
    - response
    - source_code
assertions:
  - no_duplicate_events
  - deterministic_normalisation
  - cost_status_is_valid
```

### 16.3 Required test layers

- Schema validation
- Adapter unit tests
- Golden normalisation tests
- Privacy leakage tests
- Storage migration tests
- API contract tests
- End-to-end browser tests
- Installation smoke test
- Upgrade smoke test
- Diagnostics sanitisation test

---

## 17. Dashboard information architecture

### 17.1 Home
- sessions today
- active integration status
- estimated cost
- successful, failed, and abandoned sessions
- current privacy mode
- new governance events
- high-confidence insights

### 17.2 Sessions
Filter by:
- date
- tool
- provider
- model
- outcome
- repository pseudonym
- policy status

Session detail:
- timeline
- model calls
- token and cost breakdown
- retries and errors
- tool categories
- policy decisions
- data-retention view
- delete action

### 17.3 Costs
- cost by day
- cost by tool
- cost by model
- cost by session outcome
- failed-work cost
- unknown-cost records

### 17.4 Governance
- policy events
- severity
- status
- evidence
- remediation
- indeterminate checks

### 17.5 Privacy
- current telemetry level
- retained field categories
- excluded categories
- retention period
- sharing settings
- inspect outgoing diagnostic payload
- delete all data

### 17.6 Integrations
- detected tool
- adapter version
- last event received
- capability matrix
- warnings
- setup instructions
- run test event

### 17.7 Diagnostics
- service health
- queue depth
- storage status
- parser warnings
- export sanitised bundle

---

---

## 18A. Quality assurance strategy

Quality is part of the product, not a final hardening phase. Every milestone must leave the repository runnable, testable, and releasable at that milestone's intended capability level.

### 18A.1 Test pyramid

#### Unit tests
Cover deterministic business logic in isolation:
- schema validation
- configuration defaults and validation
- privacy field classification and transformations
- adapter mapping functions
- session state transitions
- cost calculation
- policy evaluation
- insight rules
- pagination and filtering

Expectations:
- fast enough to run on every commit
- table-driven tests where useful
- explicit success, boundary, malformed-input, and failure cases
- no network dependency
- deterministic clocks, identifiers, and randomness

#### Component tests
Test a subsystem with real internal dependencies and controlled external boundaries:
- OTLP receiver plus privacy pipeline
- normaliser plus schema validator
- repository implementation against temporary SQLite
- API handlers against test storage
- React components with mocked HTTP boundaries

#### Integration tests
Test real boundaries:
- OTLP payload to persisted canonical event
- schema migration from each supported prior version
- API to SQLite
- support-bundle generation and sanitisation
- provider fixture replay
- graceful restart during ingestion

Integration tests must use isolated temporary resources and be repeatable locally and in CI.

#### Contract and conformance tests
- Validate JSON and YAML against checked-in schemas.
- Verify API request and response contracts.
- Run every provider adapter through a shared conformance suite.
- Verify backward compatibility for supported API and schema versions.
- Detect accidental breaking changes in generated OpenAPI or JSON Schema artifacts.

#### Functional end-to-end tests
Use Playwright against a packaged or production-like local application. Cover:
- first-run health and setup
- receive a synthetic telemetry session
- view and filter sessions
- inspect event provenance
- inspect cost status
- change privacy settings
- observe a policy event
- delete a session
- delete all local data
- preview and export a sanitised diagnostic bundle
- restart the service and verify retained state
- handle backend unavailable and malformed-data states

E2E tests must verify user-observable behaviour, not internal implementation details.

#### Exploratory and usability testing
Before each alpha checkpoint, execute a written exploratory charter covering:
- installation and setup friction
- misleading or ambiguous metrics
- privacy comprehension
- failure recovery
- incomplete provider telemetry
- accessibility using keyboard and screen-reader smoke checks

Record findings and disposition them before release.

### 18A.2 Non-functional testing

#### Performance
Define and test budgets by phase. Initial local targets:
- health endpoint p95 below 100 ms on a typical development machine
- session-list API p95 below 300 ms with 10,000 sessions
- session-detail API p95 below 500 ms with 10,000 events in one session
- dashboard initial usable render below 2 seconds on the reference test machine
- sustained synthetic ingest without data loss at the documented target rate
- bounded memory use during large payload rejection and fixture replay

Targets may be revised through an ADR after measurement. They must not be silently relaxed.

Required performance tests:
- Go benchmarks for hot normalisation and privacy paths
- load tests for ingest and read APIs
- large-dataset UI test
- memory and file-descriptor checks
- startup and shutdown timing

#### Reliability and resilience
Test:
- duplicate delivery
- out-of-order delivery
- malformed payloads
- oversized payloads
- database busy and disk-full behaviour where safely simulatable
- interrupted writes
- collector restart
- corrupted configuration
- partial migration failure
- browser refresh during active ingestion
- clock skew
- unsupported provider versions

The application must fail safely, preserve diagnosability, and avoid affecting the AI tool being observed.

#### Security
Test:
- loopback-only default binding
- authentication-token enforcement once introduced
- request size and rate limits
- path traversal
- archive and decompression abuse
- injection into logs, SQL, HTML, and exported files
- cross-site request risks for local services
- unsafe file permissions
- secret leakage into logs, database, crash reports, and diagnostics
- dependency and container vulnerabilities
- malicious or adversarial telemetry fixtures

Run threat-model reviews before private alpha and before any cloud mode.

#### Privacy
Privacy tests are release-blocking. Verify:
- prohibited fields never cross the persistence boundary
- logs contain no captured content
- diagnostics contain no prohibited content
- hashing is installation-specific and non-reversible
- deletion removes primary records, derived records, indexes, and exports
- retention removes expired data
- telemetry-level changes take effect predictably
- consent and sharing defaults remain off

Use canary strings in tests and scan every output location for them.

#### Accessibility
- automated accessibility checks in component and E2E suites
- keyboard-only navigation for core journeys
- visible focus states
- meaningful headings, labels, and error messages
- no information conveyed solely through colour
- target WCAG 2.2 AA for the supported interface

#### Compatibility
Test supported combinations of:
- current and previous supported Go/toolchain versions where practical
- supported Node LTS versions
- latest stable Chrome, Firefox, and Safari/WebKit through Playwright
- supported macOS and Linux packaging targets
- current and explicitly supported provider-tool versions

Maintain a checked-in compatibility matrix.

### 18A.3 Coverage policy

Coverage is a diagnostic and regression guard, not a substitute for meaningful assertions.

Initial gates:
- Go changed-package line coverage: at least 80%
- privacy, cost, policy, and normalisation core packages: at least 90%
- frontend changed-file line coverage: at least 80%
- no reduction in repository-wide coverage without documented approval
- all critical privacy and security branches require explicit tests, regardless of aggregate coverage

Mutation testing may be introduced selectively for privacy, policy, and cost logic after the core implementation stabilises.

### 18A.4 Test data rules

- Use only synthetic or irreversibly sanitised fixtures.
- Label fixture provenance and provider/tool version.
- Include valid, invalid, boundary, adversarial, duplicate, and out-of-order fixtures.
- Seed generators for reproducibility.
- Never copy production telemetry into the repository.
- Scan fixtures for secrets and high-entropy values in pre-commit and CI.

---

## 18B. Development quality gates

### 18B.1 Pre-commit framework

Use the `pre-commit` framework with checked-in `.pre-commit-config.yaml`. Pin hook revisions. Provide `make hooks-install` and `make precommit`.

Fast commit-time hooks should include:
- trailing whitespace and end-of-file checks
- YAML, JSON, TOML, and Markdown validation where applicable
- merge-conflict marker detection
- oversized-file prevention
- generated-file consistency checks
- `gofmt` or `goimports` verification
- targeted `golangci-lint` on changed Go code where practical
- Prettier verification
- ESLint on changed frontend files
- TypeScript type-check for affected workspace
- ShellCheck for changed scripts
- gitleaks secret scan
- schema validation
- fast unit tests for affected packages

Do not put slow E2E, full vulnerability databases, or long load tests in the commit hook. Those belong in pre-push and CI.

### 18B.2 Pre-push checks

Provide `make verify-push` including:
- full formatting check
- full lint and type-check
- all unit and component tests
- Go race tests for relevant packages
- integration tests
- schema and migration tests
- production builds
- selected smoke E2E tests
- local secret scan

### 18B.3 Continuous integration gates

Required PR jobs:
1. repository hygiene and generated-file check
2. Go format, vet, lint, staticcheck, and tests
3. TypeScript format, lint, strict type-check, and tests
4. schema and API compatibility
5. integration and migration tests
6. Playwright E2E on supported browsers
7. race detector
8. secret scanning
9. SAST
10. dependency vulnerability scanning
11. license-policy check
12. build and package smoke tests
13. coverage collection and threshold enforcement

Scheduled or release jobs:
- deeper vulnerability scan with refreshed databases
- full E2E matrix
- fuzzing for ingestion, parsing, redaction, and schema boundaries
- load and soak tests
- SBOM generation
- artifact signing and provenance when releases begin
- backup/restore and upgrade rehearsal when persistent releases begin

No required CI check may be bypassed silently. Emergency bypasses require a recorded decision, owner, risk, and follow-up issue.

### 18B.4 Static-analysis baseline

Backend baseline:
- `gofmt` / `goimports`
- `go vet`
- `golangci-lint`
- `staticcheck`
- `govulncheck`
- `go test -race`
- targeted fuzz tests

Frontend baseline:
- Prettier
- ESLint with type-aware rules
- TypeScript strict mode
- `tsc --noEmit`
- dependency and unused-export analysis
- accessibility lint rules

Repository baseline:
- gitleaks
- Semgrep
- OSV or equivalent dependency scanning
- actionlint
- ShellCheck
- Trivy when containers or deployable filesystem artifacts exist
- Hadolint when Dockerfiles exist

Each finding category must define whether it is blocking, warning-only, or subject to an expiring suppression. Suppressions require a reason, owner, and expiry date.

---

## 18C. Delivery checkpoints and stop/go reviews

Every phase ends with a checkpoint. Codex must not begin the next phase merely because code exists. The checkpoint evidence must be committed under `docs/checkpoints/phase-N.md` or attached to the corresponding tracked issue.

### Checkpoint template

1. Scope completed and explicitly deferred items
2. Demonstrated user journeys
3. Acceptance-criteria mapping
4. Unit, component, integration, contract, and E2E results
5. Coverage results and exceptions
6. Static-analysis and vulnerability results
7. Performance results against current budgets
8. Privacy and security test evidence
9. Accessibility evidence
10. Known defects and severity
11. Architecture or schema changes
12. Upgrade and rollback considerations
13. Open risks with owners
14. Recommendation: `GO`, `GO WITH CONDITIONS`, or `NO-GO`

### Universal phase gate

A phase is `GO` only when:
- all acceptance criteria pass
- required test suites pass
- no open critical or high-severity security/privacy defects exist
- no unexplained data loss or corruption exists
- no known prohibited-field leakage exists
- required static-analysis checks pass
- documentation and schemas match behaviour
- the product can be demonstrated from a clean checkout
- rollback or data-reset procedure is documented where applicable

`GO WITH CONDITIONS` is allowed only for non-critical limitations with owners and deadlines. A privacy leakage, data corruption, unsafe default, or inability to reproduce the build is always `NO-GO`.

### Phase-specific checkpoints

#### Phase 0 checkpoint: telemetry feasibility
Demonstrate real and replayed Codex telemetry. Confirm observed capabilities, unknowns, sensitive-field exposure, and fixture sanitisation. No production architecture assumptions may depend on unverified fields.

#### Phase 1 checkpoint: canonical data integrity
Replay all fixtures twice and out of order. Verify deterministic normalisation, deduplication, state reconstruction, privacy leakage tests, migrations, and deletion.

#### Phase 2 checkpoint: usable local product
From a clean machine or clean environment, install and complete the core session journey. Run API contract, E2E, accessibility, restart, and failure-state tests.

#### Phase 3 checkpoint: defensible calculations
Independently verify cost examples. Confirm unknown costs never become zero and every insight links to evidence. Run boundary, property-based where useful, and regression tests.

#### Phase 4 checkpoint: safe governance reporting
Run all synthetic policy scenarios. Confirm `indeterminate` handling, no false certainty, no raw secret persistence, and no blocking action is performed.

#### Phase 5 checkpoint: private-alpha readiness
Complete installation, uninstall, upgrade, rollback, support-bundle, threat-model, compatibility, performance, and exploratory usability testing. Resolve all release-blocking defects.

#### Phase 6 checkpoint: vendor-neutral proof
Run the same conformance suite against Codex and Claude adapters. Demonstrate that the canonical model and core UI did not require provider-specific redesign.

#### Phase 7 checkpoint: cloud trust boundary
Perform tenant-isolation, consent, deletion, encryption, authentication, authorisation, abuse, and threat-model tests. Commission independent security review before broader external use.

#### Phase 8 checkpoint: ethical team analytics
Validate anonymity thresholds, role access, metric interpretation, GitHub least privilege, correlation confidence, and absence of individual ranking. Include pilot-user and administrator sign-off.

---

## 18. API outline

Prefix all endpoints with `/api/v1`.

### System
- `GET /health`
- `GET /version`
- `GET /capabilities`

### Sessions
- `GET /sessions`
- `GET /sessions/{id}`
- `DELETE /sessions/{id}`
- `DELETE /sessions`

### Events
- `GET /sessions/{id}/events`
- `GET /events/{id}/provenance`

### Costs
- `GET /costs/summary`
- `GET /costs/timeseries`
- `GET /costs/unknown`

### Insights
- `GET /insights`
- `POST /insights/{id}/feedback`

### Governance
- `GET /policy-decisions`
- `GET /policies`
- `PUT /policies`

### Privacy
- `GET /privacy/settings`
- `PUT /privacy/settings`
- `GET /privacy/retained-fields`

### Integrations
- `GET /integrations`
- `POST /integrations/{id}/test`

### Diagnostics
- `GET /diagnostics/preview`
- `POST /diagnostics/export`

Use pagination and stable response envelopes from the start.

---

## 19. Delivery roadmap

**Superseded by section 0.** The reorientation directive's P0–P5 order replaces the phase
order below. The phases here remain as a source of task detail, but their sequencing and
cost-first emphasis no longer govern delivery — follow section 0 for order and priority.

## Phase 0: Discovery and proof

### Objective
Prove that a local Codex telemetry flow can be captured and understood.

### Deliverables
- Repository scaffold
- Architecture decision records
- Minimal OTLP HTTP receiver
- Raw event inspector in development mode
- Sanitised Codex fixtures
- Initial capability matrix
- Findings document covering missing and unreliable fields

### Exit criteria
- A real local Codex session produces at least one received telemetry record.
- The record can be replayed from a sanitised fixture.
- No sensitive content is committed.
- Unknown fields are preserved in a provider extension object.
- The team knows which MVP metrics are possible from observed data.

---

## Phase 1: Canonical ingestion

### Objective
Reliably ingest, redact, normalise, deduplicate, and store events.

### Deliverables
- Canonical JSON Schema
- Schema versioning rules
- Codex adapter
- Privacy pipeline
- SQLite storage
- Event deduplication
- Out-of-order event handling
- Session reconstruction
- Migration framework
- Golden fixture tests

### Exit criteria
- Fixture replay produces deterministic canonical records.
- Duplicate fixture replay creates no duplicate canonical events.
- Prohibited fields fail a privacy test if they reach persistence.
- Unknown models and missing usage are represented safely.
- Session state is correct for standard scenarios.

---

## Phase 2: Local API and session explorer

### Objective
Allow a developer to understand collected sessions.

### Deliverables
- Local authenticated API
- React dashboard
- Home page
- Session list
- Session detail timeline
- Integration health page
- Privacy page
- Delete session
- Delete all data

### Exit criteria
- A non-technical test user can find a session and explain its outcome.
- The dashboard clearly labels unknown or incomplete data.
- Deletion is verified at the database level.
- The API binds only to loopback by default.
- End-to-end tests cover the core journey.

---

## Phase 3: Cost engine and explainable insights

### Objective
Calculate defensible costs and surface useful observations.

### Deliverables
- Versioned price configuration
- Cost calculator
- Cost provenance
- Cost dashboard
- Failure-cost insight
- repeated-attempt insight
- excessive-context insight
- insight feedback

### Exit criteria
- Every displayed cost shows its status and calculation inputs.
- Unknown cost never appears as `$0`.
- Cost calculations have unit tests for every token category.
- Insights link to evidence.
- Users can mark insights incorrect.

---

## Phase 4: Governance detection

### Objective
Detect and explain initial governance risks.

### Deliverables
- Declarative local policy file
- Policy validation
- Provider/model allowlists
- MCP allowlist
- synthetic secret detection
- command risk classification
- session-spend threshold
- policy decision page
- `indeterminate` evaluation result

### Exit criteria
- Every policy decision includes policy version and evidence.
- Synthetic violations are detected in the test harness.
- Insufficient telemetry produces `indeterminate`, not violation.
- Raw synthetic secret values are not persisted.
- Policies are detect-and-report only.

---

## Phase 5: Packaging and private alpha

### Objective
Make the application safe and easy enough for outside testers.

### Deliverables
- macOS installer or Homebrew package
- Linux installation package or script
- first-run setup
- automatic local tool detection where possible
- setup verification
- application update strategy
- crash-safe storage
- diagnostic preview
- sanitised support-bundle export
- privacy documentation
- alpha feedback workflow

### Exit criteria
- A new tester can install, configure, run, and uninstall the product.
- Uninstall documentation explains whether local data remains.
- Diagnostic export contains no prohibited fields.
- Startup and shutdown do not interrupt supported AI tools.
- At least ten alpha users complete the core journey.

---

## Phase 6: Claude Code adapter

### Objective
Prove that the canonical model supports a second provider without redesign.

### Deliverables
- Claude Code fixtures
- Claude adapter
- capability comparison
- integration setup
- cross-tool dashboard filtering
- adapter conformance suite

### Exit criteria
- Both adapters pass the same conformance tests.
- Provider differences remain in extensions or capability metadata.
- Core session and cost views require no provider-specific UI branching beyond labels.
- Unsupported capabilities are clearly represented.

---

## Phase 7: Personal cloud beta

### Objective
Add optional synchronisation without weakening local privacy.

### Deliverables
- account model
- explicit device enrolment
- end-to-end upload consent flow
- encrypted transport
- hosted ingest
- tenant isolation
- synchronisation conflict handling
- exact upload-payload preview
- retention and deletion APIs

### Exit criteria
- Local-only mode remains fully functional.
- No data is uploaded before explicit consent.
- A user can inspect and delete hosted data.
- Local and cloud schemas use the same canonical model.
- Cross-tenant access tests pass.

---

## Phase 8: Small-team product

### Objective
Validate team analytics and governance without employee ranking.

### Deliverables
- organisations and workspaces
- invitations
- team-level aggregation
- anonymity thresholds
- GitHub integration
- repository and pull-request correlation
- role-based access
- shared policy configuration
- disclosure and consent records

### Exit criteria
- Team reporting suppresses groups below the configured anonymity threshold.
- Individual ranking is absent.
- AI session correlation is probabilistic and confidence-labelled where necessary.
- GitHub permissions are least-privilege.
- A pilot team can answer adoption, cost, outcome, and governance questions.

---

## Phase 9: Observed telemetry capability planning

### Objective
Plan and validate, before implementation, two currently unscheduled capabilities:
observed skill usage and service degradation under high context. This phase is a
discovery and design gate; it does not authorise collection or presentation of
either capability.

### Deliverables
- sanitised, versioned fixtures showing whether supported tools expose skill
  identity or invocation evidence
- a capability matrix that distinguishes observed, supported, partial,
  unsupported, and unknown skill data
- a privacy data inventory and retention decision for every candidate field
- explicit measurement definitions for context pressure and service degradation,
  including token, cache, latency, error, retry, and availability signals
- controlled synthetic workload plan, baseline, threshold method, and false
  certainty safeguards
- UX wording and evidence requirements that label unavailable or insufficient
  data rather than inferring skill use or degraded service
- approved architecture decision and implementation-ready technical plan

### Exit criteria
- No candidate field is retained without observed fixture evidence and a
  documented privacy transformation.
- Skill usage is represented only when the provider exposes it; absent evidence
  remains unavailable.
- Service-degradation claims define their baseline, window, threshold, and
  confidence; high context alone is never treated as degradation.
- The plan includes deterministic fixtures and acceptance tests for each
  approved capability.
- A product, privacy, and architecture review explicitly authorises any
  subsequent implementation work.

---

## 20. Backlog after MVP

- Cursor analytics integration
- Devin audit-log integration
- GitHub Copilot integration
- GitLab and Bitbucket
- Jira and Linear
- CI/CD correlation
- code-quality and security scanner correlation
- ClickHouse analytical backend
- managed endpoint policies
- MDM/Jamf/Intune packaging
- SAML/OIDC
- SCIM
- SIEM export
- data warehouse export
- customer-managed encryption keys
- regional residency
- self-hosted deployment
- air-gapped deployment
- active policy enforcement
- approval workflows
- longitudinal task benchmarking

---

## 21. Product metrics

### Reliability
- accepted event rate
- rejected event rate
- normalisation warning rate
- duplicate suppression rate
- collector uptime
- processing latency
- fixture conformance rate

### Privacy
- prohibited-field persistence incidents
- redaction test pass rate
- diagnostic sanitisation pass rate
- deletion completion rate
- opt-in rate by sharing category

### Product utility
- weekly active local users
- connected sessions per user
- session detail views
- useful insight rate
- incorrect insight rate
- diagnostic export success rate
- integration setup success rate

### Business validation
- private alpha retention
- users connecting more than one tool
- teams requesting aggregate views
- organisations requesting policy controls
- conversion from personal to team pilot

Do not optimise for number of prompts or number of captured events.

---

## 22. Security requirements

- Threat-model the local collector before alpha.
- Use dependency scanning and lockfiles.
- Never execute received telemetry content.
- Apply body-size and request-rate limits.
- Validate OTLP payloads.
- Protect against decompression bombs.
- Prevent path traversal in exports.
- Use secure temporary files.
- Sign release artifacts when packaging begins.
- Never include secrets in test fixtures.
- Maintain a security reporting process.
- Record security-relevant configuration changes.
- Treat local telemetry as sensitive even when content capture is disabled.

---

## 23. Definition of done

A task is complete only when:

1. Code is implemented.
2. Tests cover successful and failure paths.
3. Privacy implications are reviewed.
4. Relevant schemas are updated.
5. Documentation is updated.
6. Errors are observable and actionable.
7. No secrets or personal data appear in fixtures or logs.
8. The change passes applicable formatting, linting, type-checking, static analysis, unit, component, integration, contract, E2E, race, security, and build checks.
9. Coverage thresholds and changed-code quality gates pass.
10. Acceptance criteria are demonstrated with reproducible evidence.
11. Non-functional budgets are measured when the change affects them.
12. New architectural assumptions are captured in an ADR.
13. No unowned suppression, skipped test, or unexplained warning is introduced.

---

## 24. Codex operating instructions

Codex must:

1. Read `PRODUCT_MAP.md`, `AGENTS.md`, and relevant ADRs before modifying code.
2. Work on one milestone or narrowly defined issue at a time.
3. Inspect the repository before proposing implementation.
4. State assumptions in the task summary.
5. Prefer the smallest change that satisfies acceptance criteria.
6. Add tests with every behavioural change.
7. Never weaken privacy defaults to simplify implementation.
8. Never silently discard unknown provider fields.
9. Never invent provider capabilities.
10. Mark incomplete or uncertain provider data explicitly.
11. Update documentation in the same change.
12. Run the required verification commands before finishing.
13. Report changed files, test results, residual risks, and next recommended task.
14. Stop and document evidence when real provider behaviour contradicts this map.

---

## 25. First implementation sequence

Codex should execute these tasks in order.

### Task 001: Repository scaffold
Create the repository layout, build commands, basic Go daemon, React application, linting, tests, and CI.

Acceptance criteria:
- `make build` succeeds.
- `make test` succeeds.
- `make lint` succeeds.
- `make static-analysis` succeeds.
- `make precommit` succeeds.
- `make verify` succeeds from a clean checkout.
- local daemon exposes `GET /api/v1/health`.
- web app renders a health state.
- README contains local setup.
- no telemetry ingestion yet.

### Task 002: Configuration and privacy defaults
Implement validated YAML configuration and default local-only privacy settings.

Acceptance criteria:
- invalid configuration fails with actionable errors.
- default configuration matches this document.
- configuration schema is versioned.
- prompts, responses, and source code are disabled by default.
- tests assert defaults.

### Task 003: Canonical schemas
Create canonical event, session, policy, and configuration schemas.

Acceptance criteria:
- schemas include explicit versions.
- valid and invalid fixtures exist.
- schema compatibility rules are documented.
- provider extensions are supported.

### Task 004: OTLP HTTP ingest proof
Implement a minimal loopback OTLP HTTP receiver with size limits and structured errors.

Acceptance criteria:
- accepts a supported synthetic payload.
- rejects malformed and oversized payloads.
- does not persist raw content.
- exposes ingest counters.
- integration tests pass.

### Task 005: Privacy pipeline
Implement field classification, removal, hashing, and redaction before persistence.

Acceptance criteria:
- prohibited-field leakage tests pass.
- redaction occurs before storage and logs.
- retained fields expose provenance.
- hashing uses a local installation-specific salt.

### Task 006: Codex fixture capture procedure
Document and implement a safe process to capture and sanitise real Codex events.

Acceptance criteria:
- fixture origin and tool version are recorded.
- fixture validation rejects likely secrets.
- sanitisation is reviewed by tests.
- no real prompt or source code is committed.

### Task 007: Codex normaliser
Normalise supported Codex fixture records into canonical events.

Acceptance criteria:
- deterministic output.
- unknown fields preserved in extensions.
- unsupported fields marked unavailable.
- conformance and golden tests pass.

### Task 008: SQLite persistence
Persist canonical events and reconstructed sessions.

Acceptance criteria:
- migrations work from an empty database.
- duplicate events are idempotent.
- out-of-order fixtures are handled.
- deletion is complete.
- repository interface is storage-agnostic.

### Task 009: Session API
Implement paginated session list and session detail endpoints.

Acceptance criteria:
- stable response envelopes.
- filters for tool, model, outcome, and date.
- unknown fields are distinguishable from zero.
- API contract tests pass.

### Task 010: Local dashboard
Implement Home, Sessions, Session Detail, Integrations, and Privacy pages.

Acceptance criteria:
- core journey works end to end.
- incomplete data is visibly labelled.
- session deletion requires confirmation.
- accessibility checks pass.
- Playwright tests pass.

Only begin cost and governance work after Tasks 001–010 meet their acceptance criteria.

---

## 26. Initial risks

### Provider telemetry instability
Mitigation:
- version fixtures
- capability metadata
- adapter isolation
- preserve unknown fields
- conformance suite

### Surveillance perception
Mitigation:
- local-first
- transparent retained-field view
- no rankings
- aggregation thresholds
- documented purpose limitation
- evidence-based insights

### Weak productivity claims
Mitigation:
- start with cost, reliability, and session outcomes
- avoid universal scores
- add SDLC correlation later
- expose uncertainty and confidence

### Sensitive-data leakage
Mitigation:
- no content capture by default
- redact before persistence
- leakage tests
- diagnostic preview
- local-only MVP

### Overbuilding enterprise features
Mitigation:
- enforce MVP exclusions
- phase gates
- one collector architecture
- postpone multi-tenancy and SSO

### Cost inaccuracies
Mitigation:
- effective-dated prices
- provenance
- unknown status
- manual overrides
- calculation-version retention

---

## 27. Open questions log

Maintain `docs/open-questions.md`.

Initial questions:

1. Which exact Codex telemetry records are emitted in the current supported versions?
2. Can reliable session boundaries be derived?
3. Which token categories are available?
4. Are tool calls and approvals represented consistently?
5. Can repository context be obtained without collecting file paths?
6. How should abandoned sessions be detected?
7. What stable pseudonymous identifiers are available?
8. Which data can be observed without enabling prompt or response capture?
9. How do Codex CLI, IDE, app, and cloud telemetry differ?
10. Which events remain accessible when the application is offline?
11. What is the safest installation and configuration workflow?
12. Which initial insights do alpha users consider genuinely useful?
13. Does each supported tool expose a stable, privacy-safe skill identity or
    invocation record?
14. Which observed latency, error, retry, token, and cache signals can support
    a bounded service-degradation measurement without making causal claims?

Never resolve these by assumption. Resolve them through fixtures, experiments, or authoritative documentation.

---

## 28. Immediate goal

The immediate goal is not to build the enterprise platform.

The immediate goal is:

> Produce a privacy-safe local application that receives real Codex, Claude Code, and Cursor telemetry, converts it into stable canonical sessions, and lets one developer inspect what happened — which tools, models, MCP servers, and skills were used, whether they were token- and context-efficient, whether the model performed, and whether any risky access (e.g. reading `.env`) occurred — plus what data was retained. Cost is shown only as a secondary, optional estimate where calculable.

Everything else follows from proving this vertical slice. The gating first step is the P0
capability & privacy spike in section 0 — no canonical types or dashboards are built before it.
