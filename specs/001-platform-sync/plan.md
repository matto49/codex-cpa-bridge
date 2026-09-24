# Implementation plan

1. Add a Go platform discovery report with explicit target paths, CPA matching, readiness, and reasons. Expose it as `bridge platforms scan --json` and in the desktop UI.
2. Add `platforms plan` and `platforms sync` for Codex and Claude, preserving unrelated fields and creating backups. Require explicit initialization for absent files. Treat catalog visibility as desired state.
3. Add an xbot adapter for CPA-owned subscriptions and per-model flags. The initial adapter uses validated SQLite schema, online backup, and an atomic transaction; switch to authenticated Web RPC if a stable non-browser credential mechanism is confirmed. Verify runtime picker refresh before calling the integration complete.
4. Add remote manifest/SSH probes, connection lifecycle and recovery, then remote platform sync. Verify effective runtime configuration rather than only local files.
5. Redesign the Models interaction to show per-platform propagation, pending/reconnect state, errors, and a one-click sync action. Save catalog visibility first, attempt local and configured remote sync independently, show each result as it arrives, and retain it on the page even when the other fails. Harden loading, empty/error states and repeated clicks.
6. Make build paths portable, package the desktop app, replace placeholder branding, document setup and security boundaries, and run unit/integration/UI verification.
7. Add a separate Claude initialization preview/apply path for a missing settings file. Require an explicit same-origin Anthropic base URL plus operator confirmation of compatibility and external credentials, use exclusive creation, and surface the unverified transport/auth state in CLI and desktop UI.

No platform config is changed merely by scanning. A sync writes only a target that is CPA-matched or explicitly initialized; it makes a backup first. A failed write does not silently report success.
