package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

type RemoteSyncReport struct {
	Target         string                `json:"target"`
	Action         string                `json:"action"`
	ModelPolicy    ModelPolicyReport     `json:"model_policy"`
	CatalogRefresh *CatalogRefreshReport `json:"catalog_refresh,omitempty"`
	Platforms      PlatformSyncReport    `json:"platforms"`
	RemoteReady    bool                  `json:"remote_ready"`
	Detail         string                `json:"detail"`
}

func SyncRemote(m Manifest, target string, write bool) (RemoteSyncReport, error) {
	if !validSSHTarget(target) {
		return RemoteSyncReport{}, errors.New("invalid SSH target")
	}
	status, err := ScanRemote(target)
	if err != nil || !status.Ready {
		return RemoteSyncReport{}, errors.New("remote bridge is not ready; run remote scan and doctor first")
	}
	policy, err := ExportModelPolicy(m)
	if err != nil {
		return RemoteSyncReport{}, err
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return RemoteSyncReport{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	report := RemoteSyncReport{Target: target, Action: "preview", RemoteReady: status.Ready}
	if err := remoteModelPolicy(ctx, target, "plan", encoded, &report.ModelPolicy); err != nil {
		return report, err
	}
	if !write {
		if len(report.ModelPolicy.Missing) > 0 {
			refreshCommand := `"$HOME/.local/bin/bridge-go" --manifest "$HOME/.config/codex-cpa-bridge/bridge.toml" models refresh --dry-run --json`
			if output, refreshErr := remoteCommand(ctx, target, refreshCommand, nil); refreshErr == nil {
				var refresh CatalogRefreshReport
				if json.Unmarshal(output, &refresh) == nil {
					report.CatalogRefresh = &refresh
				}
			}
		}
		command := `"$HOME/.local/bin/bridge-go" --manifest "$HOME/.config/codex-cpa-bridge/bridge.toml" platforms plan --json`
		output, err := remoteCommand(ctx, target, command, nil)
		if err != nil {
			return report, fmt.Errorf("remote platform plan failed: %w", err)
		}
		if err := json.Unmarshal(output, &report.Platforms); err != nil {
			return report, errors.New("remote platform plan returned invalid JSON")
		}
		report.Detail = remotePreviewDetail(report.ModelPolicy, report.CatalogRefresh)
		return report, nil
	}
	if len(report.ModelPolicy.Missing) > 0 {
		refreshCommand := `"$HOME/.local/bin/bridge-go" --manifest "$HOME/.config/codex-cpa-bridge/bridge.toml" models refresh --json`
		output, err := remoteCommand(ctx, target, refreshCommand, nil)
		if err != nil {
			return report, fmt.Errorf("remote catalog refresh failed: %w", err)
		}
		var refresh CatalogRefreshReport
		if err := json.Unmarshal(output, &refresh); err != nil {
			return report, errors.New("remote catalog refresh returned invalid JSON")
		}
		report.CatalogRefresh = &refresh
		report.Action = "catalog_refreshed"
		if err := remoteModelPolicy(ctx, target, "plan", encoded, &report.ModelPolicy); err != nil {
			return report, err
		}
	}
	if err := remoteModelPolicy(ctx, target, "apply", encoded, &report.ModelPolicy); err != nil {
		return report, err
	}
	report.Action = "applied"
	if len(report.ModelPolicy.Missing) > 0 {
		report.Action = "applied_partial"
	}
	platformCommand := `"$HOME/.local/bin/bridge-go" --manifest "$HOME/.config/codex-cpa-bridge/bridge.toml" platforms sync --json`
	output, err := remoteCommand(ctx, target, platformCommand, nil)
	if len(output) > 0 {
		if parseErr := json.Unmarshal(output, &report.Platforms); parseErr != nil {
			return report, errors.New("remote platform sync returned invalid JSON")
		}
	}
	if err != nil {
		report.Action = "needs_attention"
		report.Detail = "Remote platform sync failed after model policy processing; inspect the per-platform report"
		return report, nil
	}
	verified, err := ScanRemote(target)
	if err == nil {
		report.RemoteReady = verified.Ready
	}
	finishRemoteSyncReport(&report)
	return report, nil
}

func remotePreviewDetail(policy ModelPolicyReport, refresh *CatalogRefreshReport) string {
	var detail string
	if len(policy.Missing) == 0 {
		if len(policy.Changes) == 0 {
			detail = "Shared model visibility already matches; no write needed"
		} else {
			detail = fmt.Sprintf("Remote catalog would change %d shared model visibility flags; pass --write to apply", len(policy.Changes))
		}
		return appendExtraVisibleDetail(detail, policy.ExtraVisible, policy.ExtraVisibleChecked)
	}
	modelWord := "models"
	if len(policy.Missing) == 1 {
		modelWord = "model"
	}
	if refresh == nil {
		detail = fmt.Sprintf("Remote catalog is missing %d source %s; live CPA availability was not verified in this preview", len(policy.Missing), modelWord)
	} else {
		advertised := make(map[string]bool, len(refresh.Added))
		for _, slug := range refresh.Added {
			advertised[slug] = true
		}
		refreshable := 0
		for _, slug := range policy.Missing {
			if advertised[slug] {
				refreshable++
			}
		}
		switch {
		case refreshable == len(policy.Missing):
			advertised := "all of them"
			if len(policy.Missing) == 1 {
				advertised = "it"
			}
			detail = fmt.Sprintf("Remote catalog is missing %d source %s, but authenticated CPA advertises %s; write can refresh the catalog", len(policy.Missing), modelWord, advertised)
		case refreshable == 0:
			pronoun := "them"
			if len(policy.Missing) == 1 {
				pronoun = "it"
			}
			detail = fmt.Sprintf("Remote CPA does not advertise %d source %s; catalog refresh cannot add %s", len(policy.Missing), modelWord, pronoun)
		default:
			detail = fmt.Sprintf("Remote catalog is missing %d source %s; CPA advertises %d for refresh and does not advertise %d", len(policy.Missing), modelWord, refreshable, len(policy.Missing)-refreshable)
		}
	}
	if len(policy.Changes) > 0 {
		changeWord := "changes"
		if len(policy.Changes) == 1 {
			changeWord = "change"
		}
		detail += fmt.Sprintf("; write can also apply %d shared visibility %s", len(policy.Changes), changeWord)
	} else {
		detail = "Shared model visibility already matches. " + detail
	}
	return appendExtraVisibleDetail(detail, policy.ExtraVisible, policy.ExtraVisibleChecked)
}

func appendExtraVisibleDetail(detail string, extra []string, checked bool) string {
	if !checked {
		return detail + "; remote-only visible models were not checked; update the remote bridge CLI"
	}
	if len(extra) == 0 {
		return detail
	}
	modelWord := "models"
	if len(extra) == 1 {
		modelWord = "model"
	}
	return fmt.Sprintf("%s; %d remote-only visible %s remain preserved, so the full catalogs do not match", detail, len(extra), modelWord)
}

func finishRemoteSyncReport(report *RemoteSyncReport) {
	if !report.RemoteReady || report.Platforms.Failed > 0 {
		report.Action = "needs_attention"
		report.Detail = "Remote readiness or platform propagation needs attention; inspect the per-platform report"
		return
	}
	for _, item := range report.Platforms.Items {
		if item.ID == "xbot" && len(item.Unlisted) > 0 {
			report.Action = "needs_attention"
			report.Detail = "Remote xbot has enabled models outside the source catalog; inspect the per-platform report before claiming full sync"
			return
		}
	}
	finishRemoteSharedVisibilityReport(report)
	if !report.ModelPolicy.ExtraVisibleChecked || len(report.ModelPolicy.ExtraVisible) > 0 {
		report.Action = "needs_attention"
		report.Detail = appendExtraVisibleDetail(report.Detail, report.ModelPolicy.ExtraVisible, report.ModelPolicy.ExtraVisibleChecked)
	}
}

func finishRemoteSharedVisibilityReport(report *RemoteSyncReport) {
	changed := report.ModelPolicy.Applied || report.CatalogRefresh != nil && report.CatalogRefresh.Written || report.Platforms.Changed > 0
	if len(report.ModelPolicy.Missing) == 0 {
		if changed {
			report.Detail = "Remote model visibility synchronized; reconnect running Codex sessions to refresh the picker"
		} else {
			report.Action = "unchanged"
			report.Detail = "Remote model visibility already matches; no changes applied"
		}
		return
	}
	if !changed {
		report.Action = "unchanged_partial"
		modelWord, verb := "models", "remain"
		if len(report.ModelPolicy.Missing) == 1 {
			modelWord, verb = "model", "remains"
		}
		report.Detail = fmt.Sprintf("Shared model visibility already matches; %d source %s %s unavailable from remote CPA.", len(report.ModelPolicy.Missing), modelWord, verb)
		return
	}
	modelWord, verb := "models", "are"
	if len(report.ModelPolicy.Missing) == 1 {
		modelWord, verb = "model", "is"
	}
	report.Detail = fmt.Sprintf("Matching models synchronized; %d source %s %s unavailable from remote CPA. Reconnect running Codex sessions.", len(report.ModelPolicy.Missing), modelWord, verb)
}

func remoteModelPolicy(ctx context.Context, target, action string, policy []byte, report *ModelPolicyReport) error {
	command := `"$HOME/.local/bin/bridge-go" --manifest "$HOME/.config/codex-cpa-bridge/bridge.toml" models policy ` + action
	output, err := remoteCommand(ctx, target, command, policy)
	if err != nil {
		return fmt.Errorf("remote model policy %s failed: %w", action, err)
	}
	if err := json.Unmarshal(output, report); err != nil {
		return errors.New("remote model policy returned invalid JSON")
	}
	return nil
}

func remoteCommand(ctx context.Context, target, command string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", target, command)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	output, err := cmd.Output()
	if err != nil {
		return output, fmt.Errorf("SSH command failed: %w", err)
	}
	return output, nil
}
