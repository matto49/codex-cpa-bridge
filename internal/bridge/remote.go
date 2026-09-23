package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type RemoteReport struct {
	Target             string `json:"target"`
	ResolvedHost       string `json:"resolved_host"`
	ResolvedUser       string `json:"resolved_user"`
	ResolvedPort       int    `json:"resolved_port"`
	SSHAuthenticated   bool   `json:"ssh_authenticated"`
	RemoteHome         string `json:"remote_home,omitempty"`
	RemoteCodexHome    string `json:"remote_codex_home,omitempty"`
	CodexConfigPresent bool   `json:"codex_config_present"`
	BridgeInstalled    bool   `json:"bridge_installed"`
	ManifestPresent    bool   `json:"manifest_present"`
	CPAHTTPStatus      string `json:"cpa_http_status,omitempty"`
	RemoteDoctorOK     bool   `json:"remote_doctor_ok"`
	RemoteDoctorIssues int    `json:"remote_doctor_issues"`
	Ready              bool   `json:"ready"`
	Detail             string `json:"detail"`
}

const remoteProbeScript = `printf 'home=%s\n' "$HOME"
if [ -n "${CODEX_HOME:-}" ]; then printf 'codex_home=%s\n' "$CODEX_HOME"; else printf 'codex_home=%s\n' "$HOME/.codex-cpa"; fi
if [ -f "${CODEX_HOME:-$HOME/.codex-cpa}/config.toml" ]; then printf 'codex_config=present\n'; else printf 'codex_config=missing\n'; fi
if [ -x "$HOME/.local/bin/bridge-go" ] || command -v bridge-go >/dev/null 2>&1; then printf 'bridge=present\n'; else printf 'bridge=missing\n'; fi
if [ -f "$HOME/.config/codex-cpa-bridge/bridge.toml" ]; then printf 'manifest=present\n'; else printf 'manifest=missing\n'; fi
if command -v curl >/dev/null 2>&1; then curl -sS -m 3 -o /dev/null -w 'cpa_http=%{http_code}\n' http://127.0.0.1:8317/v1/models 2>/dev/null || printf 'cpa_http=unreachable\n'; else printf 'cpa_http=unavailable\n'; fi`

// ScanRemote uses existing SSH configuration and host-key trust. It never
// accepts unknown hosts, starts a service, copies credentials, or writes files.
func ScanRemote(target string) (RemoteReport, error) {
	if !validSSHTarget(target) {
		return RemoteReport{}, errors.New("remote target must be an SSH host alias or user@host without options")
	}
	report := RemoteReport{Target: target}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	config, err := exec.CommandContext(ctx, "ssh", "-G", target).Output()
	if err != nil {
		return report, fmt.Errorf("cannot resolve SSH target: %w", err)
	}
	for _, line := range strings.Split(string(config), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "hostname":
			report.ResolvedHost = fields[1]
		case "user":
			report.ResolvedUser = fields[1]
		case "port":
			report.ResolvedPort, _ = strconv.Atoi(fields[1])
		}
	}
	if report.ResolvedHost == "" || report.ResolvedUser == "" || report.ResolvedPort <= 0 {
		return report, errors.New("SSH did not resolve a host, user and port")
	}
	command := "sh -c " + shellQuote(remoteProbeScript)
	output, err := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", target, command).Output()
	if err != nil {
		report.Detail = "Authenticated SSH probe failed; check the host key, identity and network"
		return report, nil
	}
	report.SSHAuthenticated = true
	for _, line := range strings.Split(string(output), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch key {
		case "home":
			report.RemoteHome = value
		case "codex_home":
			report.RemoteCodexHome = value
		case "codex_config":
			report.CodexConfigPresent = value == "present"
		case "bridge":
			report.BridgeInstalled = value == "present"
		case "manifest":
			report.ManifestPresent = value == "present"
		case "cpa_http":
			report.CPAHTTPStatus = value
		}
	}
	if report.BridgeInstalled && report.ManifestPresent {
		if doctor, err := remoteDoctor(ctx, target); err == nil {
			report.RemoteDoctorOK = doctor.Result.BridgeReady
			report.RemoteDoctorIssues = doctor.Result.Issues
			report.CPAHTTPStatus = doctor.CPABlackbox.ModelsStatus
			report.Ready = doctor.Result.BridgeReady
		}
	}
	if report.Ready {
		report.Detail = "Remote profile, bridge, SSH and authenticated CPA endpoint are ready"
	} else if report.CPAHTTPStatus == "401" {
		report.Detail = "Remote CPA is reachable but the HTTP probe is unauthenticated; remote bridge doctor is required"
	} else {
		report.Detail = "Remote installation or authenticated CPA verification is incomplete"
	}
	return report, nil
}

func remoteDoctor(ctx context.Context, target string) (DoctorReport, error) {
	command := `"$HOME/.local/bin/bridge-go" --manifest "$HOME/.config/codex-cpa-bridge/bridge.toml" doctor --json`
	output, err := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", target, command).Output()
	if len(output) == 0 {
		return DoctorReport{}, fmt.Errorf("remote bridge doctor returned no report: %w", err)
	}
	var report DoctorReport
	if err := json.Unmarshal(output, &report); err != nil {
		return DoctorReport{}, errors.New("remote bridge doctor returned invalid JSON")
	}
	return report, nil
}

func validSSHTarget(target string) bool {
	if target == "" || strings.HasPrefix(target, "-") || strings.ContainsAny(target, " \t\n\r'\"\\/;|&$`<>!{}[]()") {
		return false
	}
	for _, r := range target {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' || r == '@' || r == ':' {
			continue
		}
		return false
	}
	return true
}
