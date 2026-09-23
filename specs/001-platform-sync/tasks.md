# Tasks

- [x] Discover Codex, Claude and xbot config sources with a read-only JSON report.
- [x] Implement Codex and Claude sync preview, backups, apply, and idempotency for already-CPA-backed configs.
- [ ] Add explicit initialization for missing Codex/Claude configs with safe auth sources. Host manifest discovery, fresh model-catalog bootstrap, and missing Claude settings preview/apply are implemented; actual Anthropic compatibility and runtime authentication still need verification on a compatible CPA host.
- [ ] Verify xbot runtime picker behavior and integrate a supported authenticated API; schema-gated local DB adapter is implemented but not yet runtime-verified.
- [x] Confirm the local xbot service exposes a login-protected Web RPC (`POST /api/rpc` returns 401 without a session), so an unauthenticated direct API call cannot replace the DB adapter. Runtime picker verification still needs an authenticated session.
- [x] Expose platform state and one-click sync in Tauri, including partial failures.
- [ ] Verify remote first-run recovery on a fresh devbox. Authenticated scan, Linux/amd64 binary upload with backup, manifest discovery, catalog refresh and cross-host visibility sync are implemented; `remote install --setup` now configures bridge-owned SSH files and can generate a dedicated client key without replacing existing identities. A clean-host end-to-end run remains outstanding.
- [x] Replace machine-specific example manifests and add CI checks, an MIT license, and vector icon source.
- [x] Fix forced-command SSH probe detection; live Mac doctor now reports the correct CPA `CODEX_HOME` with zero issues.
- [ ] Verify the macOS `.app` visually on an unlocked desktop and on a clean machine; WebView DOM startup and sidecar have been verified, but signing/notarization remain unverified.
- [ ] Complete README and interaction audit against all scenarios in `spec.md`.
