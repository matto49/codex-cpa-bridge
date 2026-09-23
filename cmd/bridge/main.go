package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	bridge "codex-cpa-bridge/internal/bridge"
)

const defaultManifest = "bridge.toml"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	flags := flag.NewFlagSet("bridge", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	manifestPath := flags.String("manifest", defaultManifest, "manifest path, default: bridge.toml")
	if err := flags.Parse(argv); err != nil {
		return 2
	}
	if flags.NArg() == 0 {
		usage()
		return 2
	}
	cmd := flags.Arg(0)
	args := flags.Args()[1:]
	if cmd == "init" {
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		write := fs.Bool("write", false, "create a manifest at --manifest after discovery")
		refresh := fs.Bool("refresh", false, "back up and refresh a bridge-generated manifest from host discovery")
		jsonOut := fs.Bool("json", false, "print machine-readable JSON")
		if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
			return 2
		}
		if *write && *refresh {
			fmt.Fprintln(os.Stderr, "bridge init: --write and --refresh are mutually exclusive")
			return 2
		}
		var report bridge.InitReport
		var err error
		if *refresh {
			report, err = bridge.RefreshHostManifest(*manifestPath)
		} else {
			report, err = bridge.InitManifestFromHost(*manifestPath, *write)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "bridge init: %v\n", err)
			return 1
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				return 1
			}
		} else {
			fmt.Printf("%s %s from %s\n", report.Action, report.ManifestPath, report.ProfilePath)
		}
		return 0
	}
	m, err := bridge.LoadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bridge: %v\n", err)
		return 1
	}

	switch cmd {
	case "render":
		fs := flag.NewFlagSet("render", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		write := fs.Bool("write", false, "write rendered config into CPA CODEX_HOME")
		force := fs.Bool("force", false, "overwrite unmanaged CPA config after backing it up")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		if *write {
			path, err := bridge.WriteRenderedConfig(m, *force)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bridge render: %v\n", err)
				return 1
			}
			fmt.Printf("wrote %s\n", path)
			return 0
		}
		fmt.Print(bridge.RenderCodexConfig(m))
		return 0

	case "doctor":
		fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		probeResponses := fs.Bool("probe-responses", false, "send a minimal /v1/responses request to CPA")
		jsonOut := fs.Bool("json", false, "print machine-readable JSON")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		report, failures := bridge.CollectDoctorReport(m, *probeResponses)
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.SetEscapeHTML(false)
			if err := enc.Encode(report); err != nil {
				fmt.Fprintf(os.Stderr, "bridge doctor: %v\n", err)
				return 1
			}
		} else {
			bridge.PrintDoctorReport(os.Stdout, report)
		}
		if failures == 0 {
			return 0
		}
		return 1

	case "status":
		if err := bridge.PrintStatus(os.Stdout, m); err != nil {
			fmt.Fprintf(os.Stderr, "bridge status: %v\n", err)
			return 1
		}
		return 0

	case "adopt":
		if err := bridge.PrintAdopt(os.Stdout, m); err != nil {
			fmt.Fprintf(os.Stderr, "bridge adopt: %v\n", err)
			return 1
		}
		return 0

	case "templates":
		name := ""
		if len(args) > 1 {
			fmt.Fprintln(os.Stderr, "bridge templates: expected at most one template name")
			return 2
		}
		if len(args) == 1 {
			name = args[0]
		}
		if err := bridge.PrintTemplates(os.Stdout, m, name); err != nil {
			fmt.Fprintf(os.Stderr, "bridge templates: %v\n", err)
			return 1
		}
		return 0

	case "plan":
		fs := flag.NewFlagSet("plan", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		force := fs.Bool("force", false, "show overwrite plan for unmanaged files")
		authorizedKeyFile := fs.String("authorized-key-file", "", "public key to include in the install plan")
		jsonOut := fs.Bool("json", false, "print machine-readable JSON")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		plan, err := bridge.CollectPlan(m, *force, *authorizedKeyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bridge plan: %v\n", err)
			return 1
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.SetEscapeHTML(false)
			if err := enc.Encode(plan); err != nil {
				fmt.Fprintf(os.Stderr, "bridge plan: %v\n", err)
				return 1
			}
		} else {
			bridge.PrintPlan(os.Stdout, plan)
		}
		if plan.Summary.Blocked > 0 {
			return 1
		}
		return 0

	case "install":
		fs := flag.NewFlagSet("install", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		write := fs.Bool("write", false, "write files into the bridge state directory")
		force := fs.Bool("force", false, "overwrite unmanaged bridge state files after backing them up")
		authorizedKeyFile := fs.String("authorized-key-file", "", "public key to install for the loopback sshd")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		rc, err := bridge.InstallTemplates(os.Stdout, m, *write, *force, *authorizedKeyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bridge install: %v\n", err)
			return 1
		}
		return rc

	case "setup":
		fs := flag.NewFlagSet("setup", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		force := fs.Bool("force", false, "back up and replace unmanaged CPA or bridge files")
		authorizedKeyFile := fs.String("authorized-key-file", "", "public key to authorize for the loopback sshd")
		noStart := fs.Bool("no-start", false, "configure files without starting the loopback sshd")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		if err := bridge.Setup(os.Stdout, m, bridge.SetupOptions{
			Force:             *force,
			AuthorizedKeyFile: *authorizedKeyFile,
			Start:             !*noStart,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "bridge setup: %v\n", err)
			return 1
		}
		return 0

	case "models":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "bridge models: expected bootstrap, refresh, list, set or policy")
			return 2
		}
		switch args[0] {
		case "bootstrap":
			fs := flag.NewFlagSet("models bootstrap", flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			write := fs.Bool("write", false, "create a missing model catalog from CPA metadata")
			jsonOut := fs.Bool("json", false, "print machine-readable JSON")
			if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
				return 2
			}
			report, err := bridge.BootstrapModelCatalog(m, *write)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bridge models bootstrap: %v\n", err)
				return 1
			}
			if *jsonOut {
				return printJSON(report)
			}
			fmt.Printf("%s model catalog %s with %d CPA models\n", report.Action, report.Path, report.CPAModels)
			return 0
		case "refresh":
			fs := flag.NewFlagSet("models refresh", flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			jsonOut := fs.Bool("json", false, "print machine-readable JSON")
			if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
				return 2
			}
			report, err := bridge.RefreshModelCatalog(m)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bridge models refresh: %v\n", err)
				return 1
			}
			if *jsonOut {
				return printJSON(report)
			}
			fmt.Printf("CPA catalog: %d models, %d added; backup: %s\n", report.CPAModels, len(report.Added), report.Backup)
			return 0
		case "policy":
			if len(args) < 2 || len(args) > 2 {
				fmt.Fprintln(os.Stderr, "bridge models policy: expected export, plan or apply")
				return 2
			}
			if args[1] == "export" {
				policy, err := bridge.ExportModelPolicy(m)
				if err != nil {
					fmt.Fprintf(os.Stderr, "bridge models policy export: %v\n", err)
					return 1
				}
				return printJSON(policy)
			}
			if args[1] != "plan" && args[1] != "apply" {
				fmt.Fprintln(os.Stderr, "bridge models policy: expected export, plan or apply")
				return 2
			}
			raw, err := io.ReadAll(io.LimitReader(os.Stdin, 10*1024*1024+1))
			if err != nil || len(raw) > 10*1024*1024 {
				fmt.Fprintln(os.Stderr, "bridge models policy: input exceeds 10 MiB or cannot be read")
				return 1
			}
			var policy bridge.VisibilityPolicy
			if err := json.Unmarshal(raw, &policy); err != nil {
				fmt.Fprintf(os.Stderr, "bridge models policy: invalid JSON: %v\n", err)
				return 1
			}
			var report bridge.ModelPolicyReport
			if args[1] == "plan" {
				report, err = bridge.PlanModelPolicy(m, policy)
			} else {
				report, err = bridge.ApplyModelPolicy(m, policy)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "bridge models policy %s: %v\n", args[1], err)
				return 1
			}
			return printJSON(report)
		case "list":
			fs := flag.NewFlagSet("models list", flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			jsonOut := fs.Bool("json", false, "print machine-readable JSON")
			if err := fs.Parse(args[1:]); err != nil {
				return 2
			}
			report, err := bridge.LoadModelCatalog(m)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bridge models list: %v\n", err)
				return 1
			}
			if *jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				enc.SetEscapeHTML(false)
				if err := enc.Encode(report); err != nil {
					fmt.Fprintf(os.Stderr, "bridge models list: %v\n", err)
					return 1
				}
			} else {
				fmt.Printf("model catalog: %s\n", report.Path)
				for _, model := range report.Models {
					fmt.Printf("  %-5s %4d  %s  %s\n", model.Visibility, model.Priority, model.Slug, model.DisplayName)
				}
			}
			return 0
		case "set":
			fs := flag.NewFlagSet("models set", flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			slug := fs.String("slug", "", "model slug")
			visibility := fs.String("visibility", "", "list or hide")
			if err := fs.Parse(args[1:]); err != nil {
				return 2
			}
			report, err := bridge.SetModelVisibility(m, *slug, *visibility)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bridge models set: %v\n", err)
				return 1
			}
			fmt.Printf("updated %s: %s -> %s\n", report.Path, *slug, *visibility)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "bridge models: unknown command: %s\n", args[0])
			return 2
		}

	case "platforms":
		if len(args) > 0 && args[0] == "init-claude" {
			fs := flag.NewFlagSet("platforms init-claude", flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			baseURL := fs.String("base-url", "", "Anthropic-compatible URL on the configured CPA endpoint")
			confirmProtocol := fs.Bool("confirm-anthropic-compatible", false, "confirm this endpoint supports Claude's Anthropic API")
			confirmAuth := fs.Bool("confirm-external-auth", false, "confirm Claude credentials are provisioned outside settings.json")
			write := fs.Bool("write", false, "create an absent Claude settings.json")
			jsonOut := fs.Bool("json", false, "print machine-readable JSON")
			if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
				return 2
			}
			report, err := bridge.InitClaudeSettings(m, *baseURL, *confirmProtocol, *confirmAuth, *write)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bridge platforms init-claude: %v\n", err)
				return 1
			}
			if *jsonOut {
				return printJSON(report)
			}
			fmt.Printf("%s Claude settings %s with %d visible models — %s\n", report.Action, report.Path, report.VisibleModels, report.Detail)
			return 0
		}
		if len(args) == 0 || (args[0] != "scan" && args[0] != "plan" && args[0] != "sync") {
			fmt.Fprintln(os.Stderr, "bridge platforms: expected scan, plan, sync or init-claude")
			return 2
		}
		fs := flag.NewFlagSet("platforms "+args[0], flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		jsonOut := fs.Bool("json", false, "print machine-readable JSON")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
			return 2
		}
		if args[0] == "scan" {
			report := bridge.ScanPlatforms(m)
			if *jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					fmt.Fprintf(os.Stderr, "bridge platforms scan: %v\n", err)
					return 1
				}
			} else {
				for _, platform := range report.Platforms {
					fmt.Printf("%-12s %-14s %s — %s\n", platform.ID, platform.State, platform.Path, platform.Detail)
				}
			}
			return 0
		}
		var report bridge.PlatformSyncReport
		var err error
		if args[0] == "plan" {
			report, err = bridge.PlanPlatformSync(m)
		} else {
			report, err = bridge.SyncPlatformConfigs(m)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "bridge platforms %s: %v\n", args[0], err)
			return 1
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				fmt.Fprintf(os.Stderr, "bridge platforms %s: %v\n", args[0], err)
				return 1
			}
		} else {
			for _, item := range report.Items {
				fmt.Printf("%-8s %-8s %s — %s\n", item.ID, item.Action, item.Path, item.Detail)
			}
		}
		if args[0] == "sync" && report.Failed > 0 {
			return 1
		}
		return 0

	case "remote":
		if len(args) == 0 || (args[0] != "scan" && args[0] != "install" && args[0] != "sync") {
			fmt.Fprintln(os.Stderr, "bridge remote: expected scan, install or sync")
			return 2
		}
		fs := flag.NewFlagSet("remote "+args[0], flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		target := fs.String("target", "", "SSH host alias or user@host")
		binary := fs.String("binary", "", "prebuilt Linux/amd64 bridge-go binary for installation")
		write := fs.Bool("write", false, "apply model visibility to the remote target")
		jsonOut := fs.Bool("json", false, "print machine-readable JSON")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
			return 2
		}
		if args[0] == "sync" {
			report, err := bridge.SyncRemote(m, *target, *write)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bridge remote sync: %v\n", err)
				return 1
			}
			if *jsonOut {
				if code := printJSON(report); code != 0 {
					return code
				}
			} else {
				fmt.Printf("%s: %s\n", report.Target, report.Detail)
			}
			if *write && (!report.RemoteReady || report.Platforms.Failed > 0) {
				return 1
			}
			return 0
		}
		if args[0] == "install" {
			report, err := bridge.InstallRemote(*target, *binary)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bridge remote install: %v\n", err)
				return 1
			}
			if *jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return 1
				}
			} else {
				fmt.Printf("%s: %s\n", report.Target, report.Detail)
			}
			if !report.DoctorReady {
				return 1
			}
			return 0
		}
		report, err := bridge.ScanRemote(*target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bridge remote scan: %v\n", err)
			return 1
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				return 1
			}
		} else {
			fmt.Printf("%s -> %s@%s:%d: %s\n", report.Target, report.ResolvedUser, report.ResolvedHost, report.ResolvedPort, report.Detail)
		}
		if !report.SSHAuthenticated {
			return 1
		}
		return 0

	case "up":
		fs := flag.NewFlagSet("up", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		foreground := fs.Bool("foreground", false, "run sshd in the foreground")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		if err := bridge.Up(os.Stdout, m, *foreground); err != nil {
			fmt.Fprintf(os.Stderr, "bridge up: %v\n", err)
			return 1
		}
		return 0

	case "down":
		return bridge.Down(os.Stdout, m)

	case "logs":
		fs := flag.NewFlagSet("logs", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		lines := fs.Int("lines", 80, "lines per log file")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		if err := bridge.ShowLogs(os.Stdout, m, *lines); err != nil {
			fmt.Fprintf(os.Stderr, "bridge logs: %v\n", err)
			return 1
		}
		return 0

	case "socks5":
		fs := flag.NewFlagSet("socks5", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		listen := fs.String("listen", "127.0.0.1:19099", "listen address for local socks5 server")
		timeout := fs.Duration("timeout", 10*time.Second, "dial timeout")
		keepalive := fs.Duration("keepalive", 30*time.Second, "tcp keepalive")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		if err := bridge.RunSocks5Server(*listen, *timeout, *keepalive); err != nil {
			fmt.Fprintf(os.Stderr, "bridge socks5: %v\n", err)
			return 1
		}
		return 0

	case "rollback":
		fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		write := fs.Bool("write", false, "perform the rollback")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		return bridge.RollbackCPAConfig(os.Stdout, m, *write)

	default:
		fmt.Fprintf(os.Stderr, "bridge: unknown command: %s\n", cmd)
		usage()
		return 2
	}
}

func printJSON(value any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		fmt.Fprintf(os.Stderr, "bridge: cannot encode JSON: %v\n", err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: bridge [--manifest bridge.toml] <command> [options]")
	fmt.Fprintln(os.Stderr, "commands: init, setup, models, platforms, remote, render, doctor, status, adopt, plan, templates, install, up, down, logs, rollback, socks5")
}
