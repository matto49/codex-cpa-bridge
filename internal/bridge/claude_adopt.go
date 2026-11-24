package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ClaudeAdoptReport excludes credential values and the previous relay URL.
type ClaudeAdoptReport struct {
	Path           string   `json:"path"`
	BaseURL        string   `json:"base_url"`
	VisibleModels  int      `json:"visible_models"`
	SelectedModel  string   `json:"selected_model"`
	AuthSource     string   `json:"auth_source"`
	AuthReady      bool     `json:"auth_ready"`
	RemovedEnvKeys []string `json:"removed_env_keys"`
	Backup         string   `json:"backup,omitempty"`
	Action         string   `json:"action"`
	Detail         string   `json:"detail"`
}

// AdoptClaudeSettings switches an existing Claude user settings file to the
// selected CPA endpoint. It preserves unrelated settings, removes credentials
// and model pins from the old relay, and backs up the complete original file.
func AdoptClaudeSettings(m Manifest, baseURL string, confirmProtocol, confirmReplace, write bool) (ClaudeAdoptReport, error) {
	path := m.Platforms.ClaudeSettingsJSON
	if path == "" {
		return ClaudeAdoptReport{}, errors.New("platforms.claude_settings_json is not configured")
	}
	baseURL, err := validateClaudeBaseURL(m, baseURL)
	if err != nil {
		return ClaudeAdoptReport{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return ClaudeAdoptReport{}, err
	}
	if !info.Mode().IsRegular() {
		return ClaudeAdoptReport{}, errors.New("Claude settings must be a regular file, not a symlink or directory")
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		return ClaudeAdoptReport{}, err
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(previous, &settings); err != nil || settings == nil {
		return ClaudeAdoptReport{}, errors.New("Claude settings must be a JSON object")
	}
	var env map[string]json.RawMessage
	if raw, found := settings["env"]; found {
		if err := json.Unmarshal(raw, &env); err != nil || env == nil {
			return ClaudeAdoptReport{}, errors.New("Claude env must be a JSON object")
		}
	} else {
		env = make(map[string]json.RawMessage)
	}
	catalog, err := LoadModelCatalog(m)
	if err != nil {
		return ClaudeAdoptReport{}, err
	}
	visible, picker := claudeVisibleModels(catalog.Models)
	if len(visible) == 0 {
		return ClaudeAdoptReport{}, errors.New("the source catalog has no visible models")
	}
	helper, err := claudeCPAAuthHelper(m.Profiles.CPA)
	if err != nil {
		return ClaudeAdoptReport{}, err
	}
	_, source := loadAuthToken(m.Profiles.CPA)
	authReady := strings.HasSuffix(source, ":present")
	selected := m.Profiles.CPA.Model
	if !containsModel(visible, selected) {
		selected = visible[0]
	}
	removed := make([]string, 0)
	for _, key := range []string{
		"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL",
		"CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_ATTRIBUTION_HEADER", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY",
	} {
		if _, found := env[key]; found {
			delete(env, key)
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	baseRaw, _ := json.Marshal(baseURL)
	env["ANTHROPIC_BASE_URL"] = baseRaw
	settings["env"], err = json.Marshal(env)
	if err != nil {
		return ClaudeAdoptReport{}, errors.New("cannot encode Claude environment")
	}
	for key, value := range map[string]any{
		"apiKeyHelper":           helper,
		"availableModels":        visible,
		"enforceAvailableModels": true,
		"modelPicker":            picker,
		"model":                  selected,
	} {
		settings[key], err = json.Marshal(value)
		if err != nil {
			return ClaudeAdoptReport{}, errors.New("cannot encode Claude model settings")
		}
	}
	next, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return ClaudeAdoptReport{}, errors.New("cannot encode Claude settings")
	}
	next = append(next, '\n')
	report := ClaudeAdoptReport{
		Path: path, BaseURL: baseURL, VisibleModels: len(visible), SelectedModel: selected,
		AuthSource: source, AuthReady: authReady, RemovedEnvKeys: removed, Action: "preview",
		Detail: "Existing settings will be backed up; old relay credentials and model pins will be removed without writing a new secret",
	}
	if !write {
		return report, nil
	}
	if !confirmProtocol || !confirmReplace {
		return report, errors.New("switching Claude requires explicit Anthropic protocol and provider-replacement confirmation")
	}
	if !authReady {
		return report, errors.New("CPA credential source is not available to the bridge process")
	}
	if bytes.Equal(previous, next) {
		report.Action = "unchanged"
		report.Detail = "Claude settings already match the selected CPA model policy"
		return report, nil
	}
	backup, err := applyPlatformChange(platformChange{path: path, previous: previous, next: next, mode: 0o600})
	if err != nil {
		return report, err
	}
	report.Backup = backup
	report.Action = "adopted"
	report.Detail = "Claude now points to CPA; reconnect Claude Code and verify an authenticated session"
	return report, nil
}

func claudeCPAAuthHelper(cpa CPAProfile) (string, error) {
	if cpa.EnvKey != "" && !isShellName(cpa.EnvKey) {
		return "", errors.New("CPA env_key is not a valid environment variable name")
	}
	command := ""
	if cpa.AuthCommand != "" {
		parts := []string{shellQuote(cpa.AuthCommand)}
		for _, arg := range cpa.AuthArgs {
			parts = append(parts, shellQuote(arg))
		}
		command = strings.Join(parts, " ")
	}
	if cpa.EnvKey != "" {
		if command != "" {
			return fmt.Sprintf(`if [ -n "${%s:-}" ]; then printf %%s "$%s"; else exec %s; fi`, cpa.EnvKey, cpa.EnvKey, command), nil
		}
		return "printenv " + cpa.EnvKey, nil
	}
	if command != "" {
		return "exec " + command, nil
	}
	return "", errors.New("CPA profile needs env_key or auth_command before Claude can use it")
}
