# Tauri Control Plane Sketch

Tauri is useful as a local dashboard, but it should not become the bridge runtime.
The bridge core stays in Go so it can run headless on devbox, Mac, CI, and a plain SSH session.

## Boundary

The UI may call the bridge CLI or a small local Go daemon. It must not parse CPA internals, own CPA
credentials, or rewrite the official Codex profile.

The UI displays and edits only bridge-owned state:

- desired manifest values for official profile path, isolated CPA `CODEX_HOME`, CPA endpoint, model, and SSH endpoint
- generated file install plan and ownership status
- `doctor` results split into `policy_ok` and `bridge_ready`
- loopback sshd state, recent bridge logs, and copyable remediation commands

## Suggested Shape

- `codex-cpa-bridge` Go CLI remains the source of truth.
- Tauri invokes JSON-producing commands first: `status`, `doctor --json`, and later `plan --json`.
- Mutating actions call explicit commands: `render --write`, `install --write`, `up`, `down`, `rollback --write`.
- The UI never receives bearer tokens. Auth should remain referenced by `auth_command` or environment variable name.

## First UI Views

- Overview: official profile health, isolated CPA profile health, CPA black-box status, SSH status.
- Profiles: compare official vs CPA paths and show whether the official active provider points at CPA.
- SSH: install/start/stop loopback sshd and show the exact target string.
- Models: list catalog entries and toggle `visibility` between `list` and `hide` through `bridge models`.
- Logs: bridge-owned sshd stdout/stderr only.

## Go API

The first UI version should consume `plan --json`, `doctor --json`, and `models list --json`, then call `setup`,
`models set`, `up`, and `down` for writes. If shelling out becomes awkward, expose the same Go package through a
tiny local daemon without changing the contracts.
