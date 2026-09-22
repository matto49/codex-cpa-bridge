package main

import (
	"encoding/json"
	"time"
	"flag"
	"fmt"
	"os"

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
	m, err := bridge.LoadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bridge: %v\n", err)
		return 1
	}

	cmd := flags.Arg(0)
	args := flags.Args()[1:]
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
			fmt.Fprintln(os.Stderr, "bridge models: expected list or set")
			return 2
		}
		switch args[0] {
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

func usage() {
	fmt.Fprintln(os.Stderr, "usage: bridge [--manifest bridge.toml] <command> [options]")
	fmt.Fprintln(os.Stderr, "commands: setup, models, render, doctor, status, adopt, plan, templates, install, up, down, logs, rollback, socks5")
}
