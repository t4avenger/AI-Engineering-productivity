# Codex Capability Matrix

Observed with Codex CLI 0.153.4 in isolated synthetic sessions on 2026-09-10. The capture used a temporary `CODEX_HOME`, prompt logging disabled, a loopback OTLP HTTP/JSON receiver, non-interactive `codex_exec` probes, and a PTY-backed interactive `codex_cli_rs` probe. Raw captures stayed in `/tmp` and were not committed.

| Capability | State | Evidence |
|---|---|---|
| OTLP log export | supported | `/v1/logs` accepted current payloads from both `codex_exec` and `codex_cli_rs`; see log signal fixtures below. |
| OTLP metric export | supported | `/v1/metrics` accepted 74 current metric instruments from `codex_exec` and `codex_cli_rs`; see metric signal fixtures below. |
| OTLP trace export | unsupported | No `/v1/traces` POSTs were observed during the same current-CLI capture; see `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-traces-endpoint.json`. |
| Tool version | supported | `service.version` reported `0.153.4` in current log and metric resource attributes. |
| Log exporter service.name | supported | Current fixtures include `codex_exec` and `codex_cli_rs`. |
| Model identity | supported | `model` remains present on current `codex.sse_event`, `codex.api_request`, `codex.tool_decision`, `codex.tool_result`, and related log signals. |
| Token usage | supported | `codex.turn.token_usage` metric exposes `token_type` values `input`, `output`, `cached_input`, `cache_write_input`, `reasoning_output`, and `total`; log `codex.sse_event` still carries input/output token attributes. |
| Session identifier | partial | Current log signals retain `conversation.id` as a provider-native local session identifier; task boundaries remain unknown without a reviewed provider task-boundary signal. |
| Tool execution | partial | `codex.tool_result` logs now map to first-class tool-call signals and `canonical.Operation` records with evidence from `fixtures/codex/observed-sanitised/codex-0.153.4-outcome-contracts-otlp.json` and golden output `fixtures/codex/expected/codex-0.153.4-tool-result.operations.json`; `codex.tool_decision`, `codex.sandbox_outcome`, and metric-only tool counters remain follow-up surfaces. |
| MCP/tool cache activity | partial | Current metrics expose MCP protocol discovery and tool-cache instruments, but no reviewed MCP invocation identity is promoted to a canonical call in this discovery ticket. |
| Skill invocations | version-dependent | `codex.skill.injected` and `codex.skill.turn.duration_seconds` were observed on 0.153.4; the synthetic skill capture recorded safe values `skill=tiq-probe`, `status=ok`, `invoke_type=implicit`. |
| Prompt/response content | unsupported | Prompt logging was disabled. `codex.user_prompt` exists as an event name, but log bodies/values were removed and content remains out of scope by default. |
| Account, email, hostname, paths, command arguments, output | unsupported | Sensitive values were stripped from committed fixtures; some raw attribute key names are retained only as structural evidence. |

No capability not listed here is inferred from this run. `unknown` means not proven; `unsupported` is used only where this capture produced explicit absence evidence, such as no trace endpoint posts.

## Log signals

| Event name | State | Service names | Evidence |
|---|---|---|---|
| `codex.api_request` | supported | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-api-request.json` |
| `codex.conversation_starts` | supported | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-conversation-starts.json` |
| `codex.sandbox_outcome` | supported | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-sandbox-outcome.json` |
| `codex.sse_event` | supported | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-sse-event.json` |
| `codex.startup_phase` | supported | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-startup-phase.json` |
| `codex.tool_decision` | supported | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-tool-decision.json` |
| `codex.tool_result` | supported | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-tool-result.json` |
| `codex.turn_ttft` | supported | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-turn-ttft.json` |
| `codex.user_prompt` | supported event; content unsupported | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-user-prompt.json` |
| `codex.websocket_connect` | supported | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-websocket-connect.json` |
| `codex.websocket_request` | supported | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-log-codex-websocket-request.json` |

## Metric instruments

| Metric name | State | Kind | Service names | Evidence |
|---|---|---|---|---|
| `codex.apps.installed.connector_count` | supported | `histogram` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-apps-installed-connector-count.json` |
| `codex.apps.installed.duration_ms` | supported | `histogram` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-apps-installed-duration-ms.json` |
| `codex.apps.installed.response_bytes` | supported | `histogram` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-apps-installed-response-bytes.json` |
| `codex.apps.installed.tool_count` | supported | `histogram` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-apps-installed-tool-count.json` |
| `codex.apps.read.duration_ms` | supported | `histogram` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-apps-read-duration-ms.json` |
| `codex.apps.refresh.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-apps-refresh-duration-ms.json` |
| `codex.apps.snapshot.age_ms` | supported | `histogram` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-apps-snapshot-age-ms.json` |
| `codex.conversation.turn.count` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-conversation-turn-count.json` |
| `codex.db.backfill.duration_ms` | supported | `histogram` | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-db-backfill-duration-ms.json` |
| `codex.db.backfill` | supported | `sum` | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-db-backfill.json` |
| `codex.feature.state` | supported | `sum` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-feature-state.json` |
| `codex.mcp.protocol_discovery.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-mcp-protocol-discovery-duration-ms.json` |
| `codex.mcp.protocol_discovery` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-mcp-protocol-discovery.json` |
| `codex.mcp.tools.cache_publish.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-mcp-tools-cache-publish-duration-ms.json` |
| `codex.mcp.tools.cache_write.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-mcp-tools-cache-write-duration-ms.json` |
| `codex.mcp.tools.fetch_uncached.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-mcp-tools-fetch-uncached-duration-ms.json` |
| `codex.mcp.tools.list.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-mcp-tools-list-duration-ms.json` |
| `codex.plugins.loaded_cache.event` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-plugins-loaded-cache-event.json` |
| `codex.plugins.loaded_cache.load.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-plugins-loaded-cache-load-duration-ms.json` |
| `codex.plugins.loaded_cache.request` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-plugins-loaded-cache-request.json` |
| `codex.plugins.loaded_cache.wait.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-plugins-loaded-cache-wait-duration-ms.json` |
| `codex.process.start` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-process-start.json` |
| `codex.remote_models.fetch_update.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-remote-models-fetch-update-duration-ms.json` |
| `codex.remote_models.load_cache.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-remote-models-load-cache-duration-ms.json` |
| `codex.responses_api_engine_iapi_tbt.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-responses-api-engine-iapi-tbt-duration-ms.json` |
| `codex.responses_api_engine_iapi_ttft.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-responses-api-engine-iapi-ttft-duration-ms.json` |
| `codex.responses_api_engine_service_tbt.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-responses-api-engine-service-tbt-duration-ms.json` |
| `codex.responses_api_engine_service_ttft.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-responses-api-engine-service-ttft-duration-ms.json` |
| `codex.responses_api_inference_time.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-responses-api-inference-time-duration-ms.json` |
| `codex.responses_api_overhead.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-responses-api-overhead-duration-ms.json` |
| `codex.rollout_compression.materialize` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-rollout-compression-materialize.json` |
| `codex.rollout.size_bytes` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-rollout-size-bytes.json` |
| `codex.shell_snapshot.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-shell-snapshot-duration-ms.json` |
| `codex.shell_snapshot` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-shell-snapshot.json` |
| `codex.skill.injected` | version-dependent | `sum` | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-skill-injected.json` |
| `codex.skill.turn.duration_seconds` | supported | `histogram` | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-skill-turn-duration-seconds.json` |
| `codex.skills.shadow_selection.catalog_entries` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-skills-shadow-selection-catalog-entries.json` |
| `codex.skills.shadow_selection.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-skills-shadow-selection-duration-ms.json` |
| `codex.skills.shadow_selection.invocation` | supported | `sum` | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-skills-shadow-selection-invocation.json` |
| `codex.skills.shadow_selection.query_terms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-skills-shadow-selection-query-terms.json` |
| `codex.skills.shadow_selection.reduction_bps` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-skills-shadow-selection-reduction-bps.json` |
| `codex.skills.shadow_selection.selected_entries` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-skills-shadow-selection-selected-entries.json` |
| `codex.skills.shadow_selection` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-skills-shadow-selection.json` |
| `codex.sqlite.init.count` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-sqlite-init-count.json` |
| `codex.sqlite.init.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-sqlite-init-duration-ms.json` |
| `codex.sqlite.logs.write.bytes` | supported | `histogram` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-sqlite-logs-write-bytes.json` |
| `codex.sqlite.logs.write.count` | supported | `sum` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-sqlite-logs-write-count.json` |
| `codex.sqlite.logs.write.duration_ms` | supported | `histogram` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-sqlite-logs-write-duration-ms.json` |
| `codex.sqlite.logs.write.entries` | supported | `histogram` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-sqlite-logs-write-entries.json` |
| `codex.sqlite.logs.write.max_entry_bytes` | supported | `histogram` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-sqlite-logs-write-max-entry-bytes.json` |
| `codex.startup.phase.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-startup-phase-duration-ms.json` |
| `codex.startup_prewarm.age_at_first_turn_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-startup-prewarm-age-at-first-turn-ms.json` |
| `codex.startup_prewarm.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-startup-prewarm-duration-ms.json` |
| `codex.thread.skills.description_truncated_chars` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-thread-skills-description-truncated-chars.json` |
| `codex.thread.skills.enabled_total` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-thread-skills-enabled-total.json` |
| `codex.thread.skills.kept_total` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-thread-skills-kept-total.json` |
| `codex.thread.skills.truncated` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-thread-skills-truncated.json` |
| `codex.thread.started` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-thread-started.json` |
| `codex.tool.call.duration_ms` | supported | `histogram` | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-tool-call-duration-ms.json` |
| `codex.tool.call` | supported | `sum` | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-tool-call.json` |
| `codex.tool.unified_exec` | supported | `sum` | `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-tool-unified-exec.json` |
| `codex.tui.start` | supported | `sum` | `codex_cli_rs` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-tui-start.json` |
| `codex.turn.e2e_duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-turn-e2e-duration-ms.json` |
| `codex.turn.memory` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-turn-memory.json` |
| `codex.turn.network_proxy` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-turn-network-proxy.json` |
| `codex.turn.token_usage` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-turn-token-usage.json` |
| `codex.turn.tool.call` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-turn-tool-call.json` |
| `codex.turn.ttfm.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-turn-ttfm-duration-ms.json` |
| `codex.turn.ttft.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-turn-ttft-duration-ms.json` |
| `codex.turn.unified_exec.running_processes` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-turn-unified-exec-running-processes.json` |
| `codex.websocket.event.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-websocket-event-duration-ms.json` |
| `codex.websocket.event` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-websocket-event.json` |
| `codex.websocket.request.duration_ms` | supported | `histogram` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-websocket-request-duration-ms.json` |
| `codex.websocket.request` | supported | `sum` | `codex_cli_rs`, `codex_exec` | `fixtures/codex/observed-sanitised/current-0.153.4-surface/codex-0.153.4-metric-codex-websocket-request.json` |

## Capture notes

- `codex_exec` probes covered model interaction, synthetic skill injection, and synthetic command/tool execution.
- `codex_cli_rs` was captured through a PTY-backed `--no-alt-screen` interactive run after non-PTY startup failed with `stdin is not a terminal`.
- The nested Codex sandbox in this environment could not read the temporary skill or execute shell commands under normal sandbox settings, so those two synthetic probes used Codex's explicit bypass flag inside the temporary synthetic workspace; committed fixtures retain only sanitised structural telemetry.
- Raw OTLP payloads and temporary `CODEX_HOME` data remained outside the repository and must not be committed.
