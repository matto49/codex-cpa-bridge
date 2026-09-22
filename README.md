# codex-cpa-bridge

`codex-cpa-bridge` manages only the bridge between Codex and a local CPA-compatible endpoint.

It deliberately treats CPA as a black box. TraeX bridge, upstream OAuth, account pools, retry policy,
model routing, and gateway internals belong to CPA and are outside this tool's scope.

## Goals

- Keep the normal Codex home, usually `~/.codex`, available for native ChatGPT login.
- Create and validate a separate CPA-backed `CODEX_HOME`.
- Render a Codex `model_provider` that points at CPA through the Responses wire API.
- Manage the isolated profile's model catalog and which models are visible in Codex.
- Check the local CPA endpoint with bounded black-box probes.
- Check the loopback SSH entrypoint used by Codex Desktop or a remote host.

## Non-goals

- Do not copy, parse, or link the main `auth.json`.
- Do not modify the official Codex profile.
- Do not manage CPA internals or TraeX bridge.
- Do not store secrets in the manifest, command line, generated config, or logs.

## Quick Start

```sh
make build
./bin/bridge-go --manifest examples/mac.toml setup
./bin/bridge-go --manifest examples/mac.toml doctor
```

The project has one implementation: the Go CLI in `cmd/bridge`.

The default manifest path is `./bridge.toml`. If it does not exist, the CLI uses conservative defaults:

- official profile: `~/.codex`
- CPA profile: `~/.codex-cpa`
- CPA endpoint: `http://127.0.0.1:8317/v1`
- SSH endpoint: `127.0.0.1:2222`

## Manifest

```toml
[profiles.official]
home = "~/.codex"
mode = "native"
managed = false
# Mac/Desktop should keep this true so normal ChatGPT login remains native.
# Devbox/headless profiles that intentionally default to CPA can set it false.
enforce_native_login = true

[profiles.cpa]
management = "managed" # or "external" to preserve an existing config.toml
home = "~/.codex-cpa"
endpoint = "http://127.0.0.1:8317/v1"
model = "gpt-5.6-sol"
provider_name = "cpa-local"
# Optional catalog controlled by `bridge models` and the future desktop UI.
model_catalog_json = "~/.codex-cpa/model-catalogs/cpa.json"
# Optional: reference a local helper or env var; never put the token itself here.
auth_command = "/home/wangyang.49/.local/bin/cliproxy-auth-token"
# env_key = "CPA_API_KEY"

[ssh.cpa]
management = "managed" # or "external" to validate an existing sshd without controlling it
host = "127.0.0.1"
port = 2222
user = "bytedance"
profile = "cpa"
# Optional: used by diagnostics when probing the loopback sshd.
identity_file = "~/.ssh/id_ed25519_byted"

[runtime]
state_dir = "~/.codex-cpa-bridge"
codex_binary = "codex"
app_server_args = ["app-server"]
```

## Commands

- `bridge setup`: configure the isolated Codex profile and loopback sshd, start it, then verify an authenticated SSH probe.
- `bridge models list --json`: list model display names, visibility, API support, and priority for the UI.
- `bridge models set --slug MODEL --visibility list|hide`: change whether one catalog model is shown, with a backup.
- `bridge doctor`: report profile isolation, generated config drift, CPA black-box health, and SSH readiness.
- `bridge status`: compact status output for humans and scripts.
- `bridge render`: print the CPA Codex config that would be written.
- `bridge render --write`: atomically write the CPA profile config after backing up an existing unmanaged file.
- `bridge adopt`: inspect the current machine and emit an editable manifest suggestion.
- `bridge plan`: show the read-only create/update/blocked plan for bridge-owned files; pass `--json` for UI use.
- `bridge templates`: print wrapper, sshd, launchd, and systemd templates without installing them.
- `bridge install`: dry-run install bridge-owned files; pass `--write` to write them.
- `bridge up` / `bridge down`: start or stop the loopback sshd directly for local testing.
- `bridge logs`: show recent bridge-owned logs.
- `bridge rollback`: dry-run restore of the latest CPA profile config backup; pass `--write` to restore.

## Current Devbox Check

Use the devbox overlay when validating from this machine:

```sh
./bin/bridge-go --manifest examples/devbox.toml setup --authorized-key-file ~/.ssh/id_ed25519_byted.pub
./bin/bridge-go --manifest examples/devbox.toml doctor --json
./bin/bridge-go --manifest examples/devbox.toml down
```

`bridge_ready=true` means the isolated CPA profile, CPA black-box `/models` probe, and loopback SSH probe are ready.
`policy_ok=false` means the host's configured policy was violated. On Mac/Desktop overlays this usually means the
official Codex profile currently points at the CPA endpoint or bridge-managed state, so native Codex login should be
repaired separately. On devbox/headless overlays, set `profiles.official.enforce_native_login = false` when the default
Codex provider is intentionally CPA.

## Ownership

Generated files include an ownership marker. The tool refuses to overwrite an existing unmanaged CPA config unless
`--force` is passed.

Set `profiles.cpa.management = "external"` to adopt an existing CPA profile without rewriting its `config.toml`.
The bridge validates its model, provider, endpoint, and health while continuing to own only the Codex-to-CPA bridge.
Likewise, `ssh.cpa.management = "external"` makes setup validate an existing SSH endpoint without starting or stopping it.

## Desktop UI

Tauri is the local control plane. The Rust shell only invokes the Go CLI; all configuration logic stays in Go.
It consumes `status`, `doctor --json`, and `models list --json`, and calls `setup`, `up`, `down`, and
`models set` for writes. See `docs/tauri-control-plane.md`.

Build on macOS (requires Node and a Rust toolchain):

```sh
cd ui
npm install
npm run tauri build
open src-tauri/target/release/codex-cpa-bridge-ui
```


## Egress SOCKS5 Proxy (for Gemini & CloudCode)

When running Codex/CPA in a cloud environment (e.g., devbox in an internal IDC), upstream services like Google CloudCode (Gemini) may reject requests due to datacenter IP or geographic restrictions.

`codex-cpa-bridge` embeds a lightweight, zero-dependency SOCKS5 proxy server to facilitate egress routing through a local machine (such as a Mac running corporate VPN / Feilian):

1. **Start SOCKS5 Daemon on Mac**:
   ```sh
   ./bin/bridge-go socks5 --listen 127.0.0.1:19099
   ```
2. **Reverse Port Forwarding via SSH**:
   ```sh
   ssh -N -R 127.0.0.1:19909:127.0.0.1:19099 devbox
   ```
3. **Configure CPA Antigravity on Devbox**:
   In `~/.cli-proxy-api/account2.json`:
   ```json
   "proxy_url": "socks5://127.0.0.1:19909"
   ```
   This ensures only Gemini requests route through the Mac egress tunnel while other models continue direct or local bridging.
