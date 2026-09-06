# Privacy Threat Model & Redaction Boundary

P0 gate artifact. The behaviour-observability reorientation widens what we ingest (tool
calls, MCP/skill invocations, file operations, command categories). This document defines
the redaction boundary that must be settled **before** those signals are extracted, so
privacy is designed into ingestion rather than bolted onto dashboards later.

Companion: [pipeline.md](pipeline.md) (current transformations), the sanitizer in
`internal/privacy/sanitizer.go`.

## Assets to protect
- Prompt / response / source-code content (already removed by default).
- Secret values: tokens, API keys, passwords, private keys.
- Sensitive paths that reveal secrets by name: `.env`, `~/.ssh/id_rsa`, `*.pem`, `credentials`.
- Command arguments and shell command lines (can embed any of the above).
- Identifiers: account, email, hostname, conversation id.

## Threats specific to the reorientation
1. **Redaction gaps in new raw fields.** New sources — command lines, tool arguments, and
   Claude Code session JSONL blocks — are raw text that can carry secrets. Redacting only a
   single canonical `path` field is insufficient. **Redaction must run at ingestion across
   every raw field of every source**, before normalisation, persistence, logs, or diagnostics.
2. **Reversible path fingerprints.** A plain `hmac-sha256` of a path looks opaque, but the key
   is a local, on-disk secret and the universe of interesting paths (`.env`, `~/.ssh/id_rsa`,
   `credentials.json`) is a tiny dictionary. Anyone who obtains both the local key and the
   persisted output can hash the dictionary and match it back to the exact file. A finding that
   only says "credential access" is also hard for the user to verify or dismiss.
   **Mitigation (implemented):** file paths are never persisted as a per-path fingerprint at
   all. `privacy.ClassifyPath` maps a path to a coarse class (`dotenv`, `ssh_key`, `cert`,
   `credentials_file`, `project_relative`, `non_project`) and a syntactic project boundary
   (`project`, `external`, `indeterminate`), persisted as the token `path-class:<class>;boundary:<boundary>`.
   Classification is OS-independent and case-insensitive (both separators normalised; Windows
   drive-letter and `~` paths recognised). Distinct files of the same class collapse to one
   token, so the specific path cannot be recovered even with the key. The class token *is*
   retained semantic signal — the category of access, deliberately kept for governance — but
   never the file identity.
3. **Indirect leakage.** The same secret path can appear in a command line, a tool-arg blob,
   and a JSONL message. Redacting one representation while persisting another defeats the point.
4. **Risky-access blind spots.** `cat .env` via a shell tool is a filesystem read the
   filesystem-read category will miss unless shell command intent is normalised too.

## Redaction boundary (required)
- **Single choke point, before persistence.** Every provider adapter routes raw records
  through the sanitizer before any storage/diagnostics/log call. No adapter persists raw.
- **Field-agnostic classification.** Classification is by field semantics (secret-bearing,
  path-like, command-arg, content) applied recursively to *all* fields, including
  provider-extension and JSONL-derived fields — not a fixed whitelist of key names.
- **Paths:** do not persist raw paths. Persist a coarse class (`dotenv`, `ssh_key`, `cert`,
  `credentials_file`, `project_relative`, `non_project`) plus project-boundary metadata via
  `privacy.ClassifyPath` — no per-path fingerprint. If a stable identifier is needed elsewhere,
  use a **rotatable, scoped keyed HMAC** (`Sanitizer.FingerprintScoped` + `RotateSalt`) — not a
  bare hash — so a leaked value cannot be dictionary-matched across installs or over time.
  `FingerprintScoped` domain-separates by scope (`privacy-hmac-v1|scope|<scope>`), and
  `RotateSalt` re-keys the installation (atomic temp-file/fsync/rename). Rotation is
  forward-only: it does not re-key already-persisted identifiers; complete local deletion
  (the salt file suffices) severs their linkage.
- **Command args / shell lines:** redacted to `[REDACTED]`; intent (category) is derived and
  kept, raw text is not.
- **Findings carry privacy-safe evidence only:** rule id, access method, project boundary,
  timestamp, confidence — never the raw path, arg, or secret.

## Verification requirements
- Canary-string leakage tests: seed synthetic secrets into *every* raw source shape (OTLP
  attrs, command lines, tool args, JSONL) and scan all output locations — none may appear.
- Path-class tests: prove a small dictionary of known secret paths cannot be recovered from
  persisted output — paths become class+boundary tokens, distinct files of one class collapse
  to one token, and no per-path fingerprint is emitted. Rotation tests prove re-keying changes
  scoped fingerprints and cross-install fingerprints differ.
- Redaction runs before persistence AND before diagnostics export (both boundaries tested).
- `indeterminate` is returned when visibility is absent — no fabricated certainty.
