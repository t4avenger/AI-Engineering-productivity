# Privacy Threat Model & No-Hiding Invariant

P0 gate artifact. The behaviour-observability reorientation widens what we ingest (tool
calls, MCP/skill invocations, file operations, command categories). Epic #87 (issue #88)
reversed the earlier ingest-time-hiding stance: for the local-only individual edition, raw
provider-native identifiers, file paths, and command lines are **persisted verbatim and shown
to the local user**, because the product now needs real session/request/MCP identity and real
paths to be useful, and the operator running on their own machine must see the actual data —
never a hashed or tokenised placeholder.

Companion: [pipeline.md](pipeline.md) (current transformations). The classifiers that governance
runs over raw values live in `internal/privacy/sanitizer.go` (`ClassifyPath`,
`ClassifyCommandAccess`).

## Trust model
The individual edition is local-only and single-user: the SQLite store lives on the operator's
own machine and is theirs to read. There is no ingest-time obfuscation "defence in depth" — the
value of the store is that it faithfully reflects what the tools did. Cloud, team, or
cross-device sharing is a different trust boundary and MUST re-evaluate every field before any
upload or aggregation; nothing in this document authorises off-machine transmission.

## Assets and how they are handled
- **Prompt / response / source-code content** — still not captured by default. The normalisers
  declare these fields unavailable and refuse content at the ingest boundary (e.g. Cursor
  rejects message-content fields). Configurable capture of this content is tracked separately in
  #94 and remains gated by `collection.prompts` / `collection.responses` / `collection.source_code`.
- **Provider-native identifiers** (session/conversation/request IDs, MCP server names) — retained
  raw with a stable provider prefix (`codex:`, `claude-code:`, `cursor-agent:`) so session lists,
  detail pages, and insights expose real correlation keys. No HMAC fingerprint is emitted; when a
  native ID is genuinely absent a non-keyed positional/content ID is used for uniqueness only
  (explicitly not a hiding transform).
- **File paths and command lines** — retained raw. They are the evidence risky-access governance
  needs, and the operator must be able to see exactly which file or command tripped a policy.
- **Sensitive paths** (`.env`, `~/.ssh/id_rsa`, `*.pem`, `credentials`) — retained raw *and*
  classified. `privacy.ClassifyPath` / `privacy.ClassifyCommandAccess` run over the raw stored
  value to add a governance signal (class + project boundary); they no longer replace the value.

## Governance over raw values
`privacy.ClassifyPath` maps a raw path to a coarse class (`dotenv`, `ssh_key`, `cert`,
`credentials_file`, `project_relative`, `non_project`) and a syntactic project boundary
(`project`, `external`, `indeterminate`). Classification is OS-independent and case-insensitive
(both separators normalised; Windows drive-letter and `~` paths recognised). This class is an
*additional* signal layered on top of the retained raw path — governance findings reference the
real path/command so the operator can verify or dismiss them.

`cat .env` via a shell tool is a filesystem read that `ClassifyCommandAccess` recognises, so
shell-command intent is classified alongside direct file operations.

## Verification requirements
- Raw-survival tests: a seeded raw identifier, file path, and command line survive ingest →
  storage → read verbatim, with no HMAC/`path-class:`/`command-access:` placeholder anywhere.
- Governance evidence tests: risky-access findings carry the raw path/command as evidence.
- Content-gating tests: prompt/response/source-code content is not captured by default (refused
  at the ingest boundary), pending the configurable-capture work in #94.
- `indeterminate` is returned when visibility is absent — no fabricated certainty.
