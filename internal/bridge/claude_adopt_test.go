package bridge

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAdoptBacksUpAndReplacesRelayWithoutCopyingToken(t *testing.T) {
	m := claudeInitFixture(t)
	m.Profiles.CPA.EnvKey = "TEST_CPA_CLAUDE_KEY"
	m.Profiles.CPA.Model = "visible"
	t.Setenv(m.Profiles.CPA.EnvKey, "new-cpa-secret")
	path := m.Platforms.ClaudeSettingsJSON
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://old-relay.example","ANTHROPIC_AUTH_TOKEN":"old-secret","ANTHROPIC_MODEL":"old-model","CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY":"1","KEEP_ME":"yes"},"model":"old-model","permissions":{"defaultMode":"plan"}}`)
	if err := os.WriteFile(path, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := AdoptClaudeSettings(m, m.Profiles.CPA.Endpoint, false, false, false)
	if err != nil || preview.Action != "preview" || !preview.AuthReady || preview.SelectedModel != "visible" || preview.BaseURL != "http://127.0.0.1:8317" {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if strings.Contains(preview.Detail, "old-relay") || strings.Contains(preview.Detail, "old-secret") {
		t.Fatal("preview disclosed the previous relay")
	}
	if raw, _ := os.ReadFile(path); !bytes.Equal(raw, previous) {
		t.Fatal("preview changed Claude settings")
	}
	if _, err := AdoptClaudeSettings(m, m.Profiles.CPA.Endpoint, true, false, true); err == nil {
		t.Fatal("replacement accepted missing confirmation")
	}
	report, err := AdoptClaudeSettings(m, m.Profiles.CPA.Endpoint, true, true, true)
	if err != nil || report.Action != "adopted" || report.Backup == "" {
		t.Fatalf("adopt = %+v, %v", report, err)
	}
	backup, err := os.ReadFile(report.Backup)
	if err != nil || !bytes.Equal(backup, previous) {
		t.Fatalf("backup did not preserve original: %v", err)
	}
	for _, name := range []string{path, report.Backup} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, %v", name, info, err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("old-secret")) || bytes.Contains(raw, []byte("new-cpa-secret")) || bytes.Contains(raw, []byte("old-relay.example")) {
		t.Fatal("new settings contain a credential or old relay URL")
	}
	var settings struct {
		Env                    map[string]string `json:"env"`
		Model                  string            `json:"model"`
		APIKeyHelper           string            `json:"apiKeyHelper"`
		AvailableModels        []string          `json:"availableModels"`
		EnforceAvailableModels bool              `json:"enforceAvailableModels"`
		ModelPicker            claudeModelPicker `json:"modelPicker"`
		Permissions            map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8317" || settings.Env["KEEP_ME"] != "yes" || len(settings.Env) != 2 || settings.Model != "visible" || settings.Permissions["defaultMode"] != "plan" || !settings.EnforceAvailableModels || len(settings.AvailableModels) != 1 || settings.AvailableModels[0] != "visible" || !settings.ModelPicker.ReplaceBuiltInOptions || len(settings.ModelPicker.Options) != 1 {
		t.Fatalf("unexpected adopted settings: %+v", settings)
	}
	if settings.APIKeyHelper != "printenv TEST_CPA_CLAUDE_KEY" {
		t.Fatalf("unexpected auth helper: %q", settings.APIKeyHelper)
	}
	if scan := scanClaude(m); scan.State != "ready" || !scan.CPAEndpoint {
		t.Fatalf("adopted settings not detected: %+v", scan)
	}
	if plan, err := PlanPlatformSync(m); err != nil || syncItem(plan, "claude").Action != "noop" {
		t.Fatalf("adopted Claude needs sync: %+v, %v", plan, err)
	}
	second, err := AdoptClaudeSettings(m, m.Profiles.CPA.Endpoint, true, true, true)
	if err != nil || second.Action != "unchanged" || second.Backup != "" {
		t.Fatalf("second adopt = %+v, %v", second, err)
	}
}

func TestClaudeAdoptRejectsMissingCredentialAndSymlink(t *testing.T) {
	m := claudeInitFixture(t)
	m.Profiles.CPA.EnvKey = "TEST_CPA_CLAUDE_MISSING"
	t.Setenv(m.Profiles.CPA.EnvKey, "")
	path := m.Platforms.ClaudeSettingsJSON
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"old-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AdoptClaudeSettings(m, m.Profiles.CPA.Endpoint, true, true, true); err == nil {
		t.Fatal("adoption accepted missing CPA credential")
	}
	link := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	m.Platforms.ClaudeSettingsJSON = link
	if _, err := AdoptClaudeSettings(m, m.Profiles.CPA.Endpoint, true, true, true); err == nil {
		t.Fatal("adoption accepted symlink")
	}
}

func TestClaudeCPAAuthHelperUsesFallbackOnlyForMissingEnvironment(t *testing.T) {
	helper, err := claudeCPAAuthHelper(CPAProfile{EnvKey: "TEST_CPA_HELPER_KEY", AuthCommand: "/usr/bin/printf", AuthArgs: []string{"fallback-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ env, want string }{{"", "fallback-secret"}, {"environment-secret", "environment-secret"}} {
		cmd := exec.Command("/bin/sh", "-c", helper)
		cmd.Env = append(os.Environ(), "TEST_CPA_HELPER_KEY="+tc.env)
		out, err := cmd.Output()
		if err != nil || string(out) != tc.want {
			t.Fatalf("helper did not select the expected source: %v", err)
		}
	}
}
