package bridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type RemoteInstallReport struct {
	Target          string `json:"target"`
	BinaryInstalled bool   `json:"binary_installed"`
	BinaryBackup    string `json:"binary_backup,omitempty"`
	ManifestCreated bool   `json:"manifest_created"`
	ManifestPath    string `json:"manifest_path"`
	SetupAttempted  bool   `json:"setup_attempted"`
	SetupSucceeded  bool   `json:"setup_succeeded"`
	DoctorReady     bool   `json:"doctor_ready"`
	DoctorIssues    int    `json:"doctor_issues"`
	Detail          string `json:"detail"`
}

type RemoteInstallOptions struct {
	Setup bool
}

// InstallRemote uploads a prebuilt Linux/amd64 CLI using authenticated SSH,
// backs up an existing binary, initializes only a missing manifest, then runs
// the remote doctor. With Setup enabled, it also configures the remote
// bridge-owned loopback SSH service and generates a dedicated client key only
// if no usable key exists. It never copies local CPA credentials to the host.
func InstallRemote(target, binary string, options RemoteInstallOptions) (RemoteInstallReport, error) {
	if !validSSHTarget(target) {
		return RemoteInstallReport{}, errors.New("invalid SSH target")
	}
	if err := validateLinuxAMD64Binary(binary); err != nil {
		return RemoteInstallReport{}, err
	}
	prior, err := ScanRemote(target)
	if err != nil || !prior.SSHAuthenticated || prior.RemoteHome == "" {
		return RemoteInstallReport{}, errors.New("authenticated remote scan must pass before installation")
	}
	report := RemoteInstallReport{Target: target, ManifestPath: filepath.Join(prior.RemoteHome, ".config/codex-cpa-bridge/bridge.toml")}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	remoteOS, err := remoteShell(ctx, target, "uname -sm")
	if err != nil || strings.TrimSpace(remoteOS) != "Linux x86_64" {
		return report, errors.New("remote installer currently supports Linux x86_64 only")
	}
	if _, err := remoteShell(ctx, target, `mkdir -p "$HOME/.local/bin" "$HOME/.config/codex-cpa-bridge" && chmod 700 "$HOME/.config/codex-cpa-bridge"`); err != nil {
		return report, fmt.Errorf("cannot prepare remote directories: %w", err)
	}
	localHash, err := hashFileSHA256(binary)
	if err != nil {
		return report, err
	}
	remoteHash, err := remoteShell(ctx, target, `if [ -f "$HOME/.local/bin/bridge-go" ] && command -v sha256sum >/dev/null 2>&1; then sha256sum "$HOME/.local/bin/bridge-go" | cut -d ' ' -f1; fi`)
	if err == nil && strings.TrimSpace(remoteHash) == localHash {
		report.BinaryInstalled = true
		report.Detail = "Remote CLI is already current"
	} else {
		var nonce [12]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return report, err
		}
		staged := ".local/bin/.bridge-go.new-" + hex.EncodeToString(nonce[:])
		scpTarget := target + ":" + staged
		if _, err := exec.CommandContext(ctx, "scp", "-q", "-B", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", binary, scpTarget).CombinedOutput(); err != nil {
			return report, fmt.Errorf("cannot upload remote CLI: %w", err)
		}
		installScript := `set -eu; current="$HOME/.local/bin/bridge-go"; staged="$HOME/` + staged + `"; backup=""; if [ -f "$current" ]; then backup="$current.bak.$(date +%Y%m%d%H%M%S)"; cp -p "$current" "$backup"; fi; chmod 700 "$staged"; mv "$staged" "$current"; printf 'backup=%s\n' "$backup"`
		installed, err := remoteShell(ctx, target, installScript)
		if err != nil {
			return report, fmt.Errorf("cannot activate remote CLI: %w", err)
		}
		report.BinaryInstalled = true
		for _, line := range strings.Split(installed, "\n") {
			if value, ok := strings.CutPrefix(line, "backup="); ok {
				report.BinaryBackup = value
			}
		}
	}
	if !prior.ManifestPresent {
		initCommand := `"$HOME/.local/bin/bridge-go" --manifest "$HOME/.config/codex-cpa-bridge/bridge.toml" init --write --json`
		if _, err := remoteShell(ctx, target, initCommand); err != nil {
			report.Detail = "CLI installed; manifest discovery failed. Configure the remote CPA Codex profile and rerun init."
			return report, nil
		}
		report.ManifestCreated = true
	}
	if options.Setup {
		report.SetupAttempted = true
		setupCommand := `"$HOME/.local/bin/bridge-go" --manifest "$HOME/.config/codex-cpa-bridge/bridge.toml" setup --generate-bridge-key`
		if _, err := remoteShell(ctx, target, setupCommand); err != nil {
			report.Detail = "Remote CLI and manifest are installed, but setup failed. Inspect the remote setup plan and existing SSH identity."
			return report, nil
		}
		report.SetupSucceeded = true
	}
	doctor, err := remoteDoctor(ctx, target)
	if err != nil {
		report.Detail = "CLI and manifest installed, but remote doctor did not return a valid report"
		return report, nil
	}
	report.DoctorReady = doctor.Result.BridgeReady
	report.DoctorIssues = doctor.Result.Issues
	if report.DoctorReady {
		report.Detail = "Remote bridge is ready"
	} else {
		report.Detail = "Remote CLI and manifest are installed; inspect remote doctor issues"
	}
	return report, nil
}

func hashFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateLinuxAMD64Binary(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("remote binary must be a regular file")
	}
	image, err := elf.Open(path)
	if err != nil {
		return errors.New("remote binary must be an ELF executable")
	}
	defer image.Close()
	if image.Machine != elf.EM_X86_64 || image.Class != elf.ELFCLASS64 || image.Type != elf.ET_EXEC && image.Type != elf.ET_DYN {
		return errors.New("remote binary must target Linux x86_64")
	}
	return nil
}

func remoteShell(ctx context.Context, target, script string) (string, error) {
	command := "sh -c " + shellQuote(script)
	output, err := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", target, command).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("remote command failed: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
