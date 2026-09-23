# Platform configuration and model visibility

## Scope and source of truth

The bridge's manifest identifies a CPA endpoint and a Codex model catalog. The catalog's `visibility` field is the desired model policy. A platform is managed only when its discovered configuration actually points to that CPA endpoint, or when the user explicitly opts into initializing a new configuration. Existing unrelated providers and credentials must be preserved.

The desktop UI must show the target path, detected provider, management state, planned changes, and whether a restart or reconnect is needed. It must not imply that changing the catalog alone has updated every platform.

## User stories

1. As an operator, I can discover Codex, Claude Code, and xbot configuration on this host without exposing credentials.
2. I can preview an idempotent sync that enables visible models and hides disabled models in every supported CPA-backed platform.
3. I can apply the preview in one action, with backups, clear partial-failure reporting, and no mutation of unrelated providers.
4. I can connect a remote devbox over SSH, inspect its actual CPA endpoint/configuration, and synchronize it over a stable, authenticated channel.
5. I can install and use the project from the README without machine-specific paths, bundled secrets, or a required local development checkout.

## Platform contracts

- Codex: `$CODEX_HOME/config.toml` owns provider and catalog path; the referenced model catalog controls picker visibility. The CLI must discover the effective home, verify the configured catalog path, and report a reconnect requirement when a running app-server has loaded an older catalog.
- Claude Code: `~/.claude/settings.json` can constrain the picker with `availableModels` and `enforceAvailableModels`; only manage it if the current Anthropic base URL resolves to the manifest's CPA endpoint. Preserve all other settings. Do not infer that an OpenAI `/v1` URL is a working Anthropic API endpoint.
- Claude initialization: only an absent `settings.json` may be created, after a read-only preview. The operator supplies an Anthropic base URL on the same CPA origin and explicitly confirms that endpoint compatibility and a separately provisioned credential. The bridge writes the base URL and visible-model policy, never a token; the report states that actual Claude authentication and inference remain unverified. A regular file, symlink, or unrelated configuration is never replaced by initialization.
- xbot: `~/.xbot/config.json` identifies the fallback provider, but the interactive model picker is backed by subscription and per-model state in xbot's database. Do not claim sync from an edit to `config.json` alone. xbot v0.0.52's Web RPC exposes `set_model_enabled`, but requires a web login session; the current local adapter instead uses schema-gated SQLite transactions and online backups. A local reversible test confirmed that this adapter changes the database flag. The v0.0.52 TUI still listed the disabled model, and its upstream Web `ModelSelector` source also renders disabled entries greyed out. Exact hiding therefore requires a change in xbot's picker behavior; authenticated Web runtime refresh remains unverified. Prefer the authenticated management API when a non-browser credential path is available.

## Acceptance scenarios

- Scan is read-only, distinguishes absent, configured-to-CPA, and unrelated configs, and never prints secret values.
- Previewing Claude initialization leaves the filesystem unchanged; applying it creates a private file only when the target is still absent. An existing unrelated Claude relay is preserved byte-for-byte.
- Disabling a model in the bridge and syncing removes it from each managed platform's picker; enabling restores it. Repeating sync makes no changes.
- If one platform cannot be synchronized, the result names that platform and leaves its current config intact; successful platforms remain reported individually.
- Remote SSH scan verifies the resolved host, key authentication, endpoint health, and the effective `CODEX_HOME`, rather than treating an open TCP port as full readiness.
- A fresh clone can build and launch with a portable example manifest; the README states prerequisites, limits, and verification steps.

## Current constraints

The existing UI is a Tauri shell over a Go CLI. The initial implementation supports only one Codex catalog, and `models set` writes that file alone. The current xbot picker is DB-backed; its config file is not sufficient for model visibility. The current Claude settings on this machine point to an unrelated relay, so automatic mutation of that live file would be incorrect.
