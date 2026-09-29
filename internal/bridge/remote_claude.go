package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RemoteClaudeReport contains no credential values or previous provider URL.
type RemoteClaudeReport struct {
	Target           string   `json:"target"`
	Mode             string   `json:"mode"`
	Path             string   `json:"path"`
	BaseURL          string   `json:"base_url"`
	VisibleModels    int      `json:"visible_models"`
	SelectedModel    string   `json:"selected_model"`
	AuthSource       string   `json:"auth_source"`
	AuthReady        bool     `json:"auth_ready"`
	HelperConfigured bool     `json:"helper_configured,omitempty"`
	RemovedEnvKeys   []string `json:"removed_env_keys,omitempty"`
	Backup           string   `json:"backup,omitempty"`
	Action           string   `json:"action"`
	Detail           string   `json:"detail"`
}

// RemoteClaudeSettings previews or explicitly applies the remote bridge's
// existing Claude initialization/adoption workflow. The remote host supplies
// its own CPA endpoint, catalog and credential source; no local secret moves.
func RemoteClaudeSettings(target, mode string, write, confirmProtocol, confirmAuthOrReplace bool) (RemoteClaudeReport, error) {
	if !validSSHTarget(target) {
		return RemoteClaudeReport{}, errors.New("invalid SSH target")
	}
	if mode != "init" && mode != "adopt" {
		return RemoteClaudeReport{}, errors.New("remote Claude mode must be init or adopt")
	}
	if write && !confirmProtocol {
		return RemoteClaudeReport{}, errors.New("remote Claude write requires Anthropic protocol confirmation")
	}
	if write && !confirmAuthOrReplace {
		if mode == "init" {
			return RemoteClaudeReport{}, errors.New("remote Claude initialization requires external authentication confirmation")
		}
		return RemoteClaudeReport{}, errors.New("remote Claude adoption requires provider-replacement confirmation")
	}
	status, err := ScanRemote(target)
	if err != nil || !status.Ready {
		return RemoteClaudeReport{}, errors.New("remote bridge is not ready; run remote scan and doctor first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	doctor, err := remoteDoctor(ctx, target)
	if err != nil || doctor.CPAProfile.Endpoint == "" {
		return RemoteClaudeReport{}, errors.New("remote doctor did not provide a CPA endpoint")
	}
	command := remoteClaudeCommand(mode, normalizeClaudeBaseURL(doctor.CPAProfile.Endpoint), write)
	output, err := remoteCommand(ctx, target, command, nil)
	if err != nil {
		return RemoteClaudeReport{}, fmt.Errorf("remote Claude %s failed: %w", mode, err)
	}
	var report RemoteClaudeReport
	if err := json.Unmarshal(output, &report); err != nil || report.Action == "" {
		return RemoteClaudeReport{}, errors.New("remote Claude command returned invalid JSON")
	}
	report.Target = target
	report.Mode = mode
	return report, nil
}

func remoteClaudeCommand(mode, baseURL string, write bool) string {
	action := "init-claude"
	confirmation := "--confirm-external-auth"
	if mode == "adopt" {
		action = "adopt-claude"
		confirmation = "--confirm-replace-provider"
	}
	command := `"$HOME/.local/bin/bridge-go" --manifest "$HOME/.config/codex-cpa-bridge/bridge.toml" platforms ` + action + ` --base-url ` + shellQuote(baseURL) + ` --json`
	if write {
		command += " --confirm-anthropic-compatible " + confirmation + " --write"
	}
	return command
}
