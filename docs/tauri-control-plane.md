# Desktop control plane

The macOS app is a local UI for `bridge-go`, not a second bridge implementation or a CPA daemon. Its Rust shell runs the Go CLI with a selected private manifest and parses its JSON reports. The Go CLI owns discovery, validation, backups, model policy, platform adapters, and SSH operations. CPA itself owns credentials, account pools, and model routing.

## Starting the app

Build the CLI and app as described in the [README](../README.md#desktop-ui). On first launch, load a private manifest path in Settings. If no manifest exists, **Initialize from this Mac** creates one from an existing CPA-backed Codex profile; it does not create a CPA service or overwrite an existing manifest. To preview manifest discovery without writing, use `bridge init --json` in the CLI. A missing model catalog and missing Claude settings have separate preview/create controls.

The app finds `bridge-go` from `CODEX_CPA_BRIDGE_BIN`, next to the app executable, or at `~/.local/bin/bridge-go`, in that order. A missing CLI is shown as an error rather than silently falling back to sample data. Browser-only development previews use sample data and cannot write real configuration.

## Views and data flow

| View | Read-only sources | Writes |
| --- | --- | --- |
| Overview | `status`, `doctor --json` | Setup/start/stop the bridge-owned loopback SSH endpoint; an external endpoint is verified, not stopped. |
| Models | `models list --json`, `platforms plan --json` | `models set`, then local platform sync and optional remote sync. A separate Sync button retries without changing the catalog. |
| Settings | `platforms scan --json`, `remote scan`, `remote sync` preview | Initialize a missing manifest/catalog/Claude file, explicitly adopt a different Claude provider, or apply remote sync after preview. |

After a model toggle, the catalog write happens first. Local and configured remote sync then run independently: one target's failure does not suppress the other's result. The UI preserves each target's outcome and reports blocked/skipped platforms and backup paths. If the catalog write fails, sync does not start. Refreshing status never initializes or changes a config file.

The source catalog controls Codex visibility directly. Claude's allowlist, picker, default, and environment model pins are compared with that catalog and synchronized only for settings already aimed at the chosen CPA endpoint. xbot's server-side model settings live in its SQLite subscription database, not just `config.json`; its adapter updates only matching CPA subscriptions. A disabled xbot model is still greyed out in xbot v0.0.52, so the app reports this as `limited`, not fully hidden. See [Ownership](../README.md#ownership) for the safety boundary.

## Remote target

Settings accepts an existing SSH alias such as `devbox`. Check resolves the alias and verifies SSH, the remote bridge, and authenticated CPA health. Preview reports remote catalog visibility differences and models the remote CPA does not advertise. Apply refreshes the remote catalog, changes shared model visibility, and syncs remote platforms with backups. Missing remote models remain unavailable; they are never counted as synchronized. The UI does not install a remote bridge or provision remote credentials; use the CLI's `remote install` workflow for a new host.

## Operational limits

- Config-ready is not the same as a working Claude session. Verify Claude authentication and the Anthropic Messages endpoint separately; its picker allowlist does not prevent an explicit `--model` request.
- Running Codex, Claude, and xbot sessions may need reconnect or refresh after a visibility change.
- A source model absent from the remote CPA cannot be made available by copying its catalog entry.
- The local `.app` build is unsigned. Signing, notarization, and clean-machine installation have not been verified.

For headless use, automation, and complete command flags, use the [CLI commands](../README.md#commands). Do not put tokens in the manifest, CLI arguments, or screenshots.
