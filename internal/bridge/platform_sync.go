package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// PlatformSyncItem describes a proposed or completed change without exposing
// configuration contents, authentication material, or environment variables.
type PlatformSyncItem struct {
	ID            string   `json:"id"`
	Path          string   `json:"path"`
	Action        string   `json:"action"`
	Result        string   `json:"result,omitempty"`
	Detail        string   `json:"detail"`
	Add           []string `json:"add,omitempty"`
	Remove        []string `json:"remove,omitempty"`
	Unlisted      []string `json:"unlisted,omitempty"`
	Backup        string   `json:"backup,omitempty"`
	RestartNeeded bool     `json:"restart_needed"`
}

type PlatformSyncReport struct {
	CatalogPath string             `json:"catalog_path"`
	Items       []PlatformSyncItem `json:"items"`
	Changed     int                `json:"changed"`
	Failed      int                `json:"failed"`
}

type platformChange struct {
	path     string
	previous []byte
	next     []byte
	mode     os.FileMode
	xbot     *xbotDBChange
}

// PlanPlatformSync is read-only. The Codex catalog is the desired visibility
// policy; only a Claude settings file already pointed at this CPA is writable.
func PlanPlatformSync(m Manifest) (PlatformSyncReport, error) {
	report, _, err := buildPlatformSync(m)
	return report, err
}

func SyncPlatformConfigs(m Manifest) (PlatformSyncReport, error) {
	report, changes, err := buildPlatformSync(m)
	if err != nil {
		return report, err
	}
	for i := range report.Items {
		item := &report.Items[i]
		if item.Action != "update" {
			item.Result = item.Action
			if item.Action == "blocked" {
				report.Failed++
			}
			continue
		}
		change := changes[item.ID]
		var backup string
		var err error
		if change.xbot != nil {
			backup, err = applyXbotDBChange(*change.xbot)
		} else {
			backup, err = applyPlatformChange(change)
		}
		if err != nil {
			item.Result = "failed"
			item.Detail = fmt.Sprintf("Sync failed: %v", err)
			item.Backup = backup
			report.Failed++
			continue
		}
		item.Result = "updated"
		item.Backup = backup
		report.Changed++
	}
	return report, nil
}

func buildPlatformSync(m Manifest) (PlatformSyncReport, map[string]platformChange, error) {
	catalog, err := LoadModelCatalog(m)
	if err != nil {
		return PlatformSyncReport{}, nil, err
	}
	report := PlatformSyncReport{CatalogPath: catalog.Path}
	changes := make(map[string]platformChange)
	codex := scanCodex(m)
	codexItem := PlatformSyncItem{ID: "codex", Path: codex.Path, RestartNeeded: codex.RestartNeeded}
	switch codex.State {
	case "ready":
		codexItem.Action, codexItem.Detail = "noop", "Codex already uses the source catalog; reconnect running sessions after visibility changes"
	case "unrelated":
		codexItem.Action, codexItem.Detail = "skipped", codex.Detail
	default:
		codexItem.Action, codexItem.Detail = "blocked", codex.Detail
	}
	report.Items = append(report.Items, codexItem)

	claude := scanClaude(m)
	claudeItem := PlatformSyncItem{ID: "claude", Path: claude.Path}
	if claude.State == "unrelated" {
		claudeItem.Action, claudeItem.Detail = "skipped", claude.Detail
	} else if claude.State == "missing" {
		claudeItem.Action, claudeItem.Detail = "blocked", "Claude settings are absent; initialize explicitly with authentication configured"
	} else if claude.State == "invalid" {
		claudeItem.Action, claudeItem.Detail = "blocked", claude.Detail
	} else {
		item, change := planClaudeSettings(claude.Path, m.Profiles.CPA.Endpoint, catalog.Models)
		claudeItem = item
		if change != nil {
			changes["claude"] = *change
		}
	}
	report.Items = append(report.Items, claudeItem)

	xbotItem, xbotChange := planXbotDB(m, catalog.Models)
	if xbotChange != nil {
		changes["xbot"] = platformChange{xbot: xbotChange}
	}
	report.Items = append(report.Items, xbotItem)
	return report, changes, nil
}

func planClaudeSettings(path, endpoint string, models []ModelSummary) (PlatformSyncItem, *platformChange) {
	item := PlatformSyncItem{ID: "claude", Path: path, Action: "blocked"}
	desired, desiredPicker := claudeVisibleModels(models)
	if len(desired) == 0 {
		item.Detail = "Claude needs at least one visible model before its picker and default can be synchronized"
		return item, nil
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		item.Detail = "Claude settings must be an existing regular file"
		return item, nil
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		item.Detail = "Cannot read Claude settings: " + err.Error()
		return item, nil
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(previous, &settings); err != nil || settings == nil {
		item.Detail = "Claude settings are not a JSON object"
		return item, nil
	}
	var env map[string]string
	if err := json.Unmarshal(settings["env"], &env); err != nil || !sameEndpoint(env["ANTHROPIC_BASE_URL"], endpoint) {
		item.Action, item.Detail = "skipped", "Claude settings no longer point to this CPA endpoint"
		return item, nil
	}
	normalizedBaseURL := normalizeClaudeBaseURL(env["ANTHROPIC_BASE_URL"])
	fixBaseURL := normalizedBaseURL != env["ANTHROPIC_BASE_URL"]
	var current []string
	if raw, ok := settings["availableModels"]; ok {
		if err := json.Unmarshal(raw, &current); err != nil || current == nil {
			item.Detail = "Claude availableModels must be an array of model IDs"
			return item, nil
		}
	}
	var enforced bool
	if raw, ok := settings["enforceAvailableModels"]; ok {
		if err := json.Unmarshal(raw, &enforced); err != nil {
			item.Detail = "Claude enforceAvailableModels must be a boolean"
			return item, nil
		}
	}
	var currentPicker claudeModelPicker
	if raw, ok := settings["modelPicker"]; ok {
		if err := json.Unmarshal(raw, &currentPicker); err != nil {
			item.Detail = "Claude modelPicker must be a valid model lineup"
			return item, nil
		}
	}
	var currentDefault string
	if raw, ok := settings["model"]; ok {
		if err := json.Unmarshal(raw, &currentDefault); err != nil {
			item.Detail = "Claude model must be a model ID string"
			return item, nil
		}
	}
	fixDefault := currentDefault != "" && !containsModel(desired, currentDefault)
	pinnedKeys := []string{
		"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL",
	}
	fixPins := false
	for _, key := range pinnedKeys {
		if pin := env[key]; pin != "" && !containsModel(desired, pin) {
			fixPins = true
		}
	}
	for _, model := range desired {
		if !containsModel(current, model) {
			item.Add = append(item.Add, model)
		}
	}
	for _, model := range current {
		if !containsModel(desired, model) {
			item.Remove = append(item.Remove, model)
		}
	}
	item.RestartNeeded = true
	if enforced && reflect.DeepEqual(current, desired) && reflect.DeepEqual(currentPicker, desiredPicker) && !fixBaseURL && !fixDefault && !fixPins {
		item.Action, item.Detail = "noop", "Claude picker lineup and allowlist match visible CPA models; explicit --model still needs runtime verification"
		return item, nil
	}
	modelRaw, err := json.Marshal(desired)
	if err != nil {
		item.Detail = "Cannot encode visible models"
		return item, nil
	}
	settings["availableModels"] = modelRaw
	settings["enforceAvailableModels"] = json.RawMessage("true")
	pickerRaw, err := json.Marshal(desiredPicker)
	if err != nil {
		item.Detail = "Cannot encode Claude model picker"
		return item, nil
	}
	settings["modelPicker"] = pickerRaw
	if fixDefault {
		settings["model"], err = json.Marshal(desired[0])
		if err != nil {
			item.Detail = "Cannot encode Claude default model"
			return item, nil
		}
	}
	if fixBaseURL || fixPins {
		env["ANTHROPIC_BASE_URL"] = normalizedBaseURL
		for _, key := range pinnedKeys {
			if pin := env[key]; pin != "" && !containsModel(desired, pin) {
				delete(env, key)
			}
		}
		envRaw, err := json.Marshal(env)
		if err != nil {
			item.Detail = "Cannot encode Claude environment"
			return item, nil
		}
		settings["env"] = envRaw
	}
	next, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		item.Detail = "Cannot encode Claude settings"
		return item, nil
	}
	next = append(next, '\n')
	item.Action, item.Detail = "update", "Update Claude picker, allowlist and hidden model pins; preserve other settings and back up the file"
	if fixBaseURL {
		item.Detail = "Fix Claude base URL (remove trailing /v1) and update picker, allowlist and hidden model pins; preserve other settings and back up the file"
	}
	return item, &platformChange{path: path, previous: previous, next: next, mode: 0o600}
}

func containsModel(models []string, model string) bool {
	for _, candidate := range models {
		if candidate == model {
			return true
		}
	}
	return false
}

func applyPlatformChange(change platformChange) (string, error) {
	info, err := os.Lstat(change.path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("target is no longer a regular file")
	}
	current, err := os.ReadFile(change.path)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(current, change.previous) {
		return "", errors.New("target changed since the sync plan was computed; retry")
	}
	backup := fmt.Sprintf("%s.bak.%d", change.path, time.Now().UnixNano())
	if err := copyPrivatePlatformBackup(change.path, backup); err != nil {
		return "", err
	}
	if err := atomicWrite(change.path, change.next, change.mode); err != nil {
		return "", err
	}
	return filepath.Clean(backup), nil
}

func copyPrivatePlatformBackup(source, backup string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(backup)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(backup)
		return err
	}
	return nil
}
