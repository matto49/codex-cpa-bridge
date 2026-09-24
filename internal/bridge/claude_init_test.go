package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeInitFixture(t *testing.T) Manifest {
	t.Helper()
	m := testManifest(t)
	m.Platforms.ClaudeSettingsJSON = filepath.Join(t.TempDir(), ".claude", "settings.json")
	m.Profiles.CPA.ModelCatalogJSON = filepath.Join(t.TempDir(), "models.json")
	content := `{"models":[{"slug":"visible","visibility":"list","priority":1},{"slug":"hidden","visibility":"hide","priority":2}]}`
	if err := os.WriteFile(m.Profiles.CPA.ModelCatalogJSON, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestClaudeInitPreviewCreateAndScan(t *testing.T) {
	m := claudeInitFixture(t)
	preview, err := InitClaudeSettings(m, "http://127.0.0.1:8317", false, false, false)
	if err != nil || preview.Action != "preview" || preview.VisibleModels != 1 || preview.RuntimeVerified {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if _, err := os.Lstat(m.Platforms.ClaudeSettingsJSON); !os.IsNotExist(err) {
		t.Fatalf("preview changed the filesystem: %v", err)
	}
	if _, err := InitClaudeSettings(m, preview.BaseURL, true, false, true); err == nil {
		t.Fatal("create accepted missing authentication confirmation")
	}
	created, err := InitClaudeSettings(m, preview.BaseURL, true, true, true)
	if err != nil || created.Action != "created" || created.RuntimeVerified {
		t.Fatalf("create = %+v, %v", created, err)
	}
	info, err := os.Stat(m.Platforms.ClaudeSettingsJSON)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %v, %v", info, err)
	}
	raw, err := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Env                    map[string]string `json:"env"`
		AvailableModels        []string          `json:"availableModels"`
		EnforceAvailableModels bool              `json:"enforceAvailableModels"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Env["ANTHROPIC_BASE_URL"] != preview.BaseURL || len(settings.Env) != 1 || len(settings.AvailableModels) != 1 || settings.AvailableModels[0] != "visible" || !settings.EnforceAvailableModels {
		t.Fatalf("unexpected generated settings: %+v", settings)
	}
	if scan := scanClaude(m); scan.State != "ready" || !scan.CPAEndpoint {
		t.Fatalf("generated settings not detected: %+v", scan)
	}
	if _, err := InitClaudeSettings(m, preview.BaseURL, true, true, true); err == nil {
		t.Fatal("create overwrote existing settings")
	}
	still, err := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if err != nil || string(still) != string(raw) {
		t.Fatal("existing settings changed")
	}
}

func TestClaudeInitNormalizesTrailingV1(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		input    string
		want     string
	}{
		{"http://127.0.0.1:8317/v1", "http://127.0.0.1:8317/v1/", "http://127.0.0.1:8317"},
		{"https://proxy.example/api/v1", "https://proxy.example/api/v1", "https://proxy.example/api"},
	} {
		m := claudeInitFixture(t)
		m.Profiles.CPA.Endpoint = tc.endpoint
		preview, err := InitClaudeSettings(m, tc.input, false, false, false)
		if err != nil || preview.BaseURL != tc.want {
			t.Fatalf("preview for %q = %+v, %v", tc.input, preview, err)
		}
		created, err := InitClaudeSettings(m, tc.input, true, true, true)
		if err != nil || created.BaseURL != tc.want {
			t.Fatalf("create for %q = %+v, %v", tc.input, created, err)
		}
		var settings struct {
			Env map[string]string `json:"env"`
		}
		content, err := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
		if err != nil || json.Unmarshal(content, &settings) != nil || settings.Env["ANTHROPIC_BASE_URL"] != tc.want {
			t.Fatalf("wrong settings for %q: %s, %v", tc.input, content, err)
		}
	}
}

func TestClaudeInitProtectsUnrelatedFileAndSymlink(t *testing.T) {
	m := claudeInitFixture(t)
	if err := os.MkdirAll(filepath.Dir(m.Platforms.ClaudeSettingsJSON), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://other.example","ANTHROPIC_AUTH_TOKEN":"secret"}}`)
	if err := os.WriteFile(m.Platforms.ClaudeSettingsJSON, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InitClaudeSettings(m, m.Profiles.CPA.Endpoint, true, true, true); err == nil {
		t.Fatal("unrelated file was accepted")
	}
	still, _ := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if string(still) != string(original) {
		t.Fatal("unrelated settings changed")
	}
	link := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Symlink(m.Platforms.ClaudeSettingsJSON, link); err != nil {
		t.Fatal(err)
	}
	m.Platforms.ClaudeSettingsJSON = link
	if _, err := InitClaudeSettings(m, m.Profiles.CPA.Endpoint, true, true, true); err == nil {
		t.Fatal("symlink target was accepted")
	}
}

func TestClaudeInitRejectsUnsafeBaseURLs(t *testing.T) {
	m := claudeInitFixture(t)
	for _, base := range []string{
		"https://other.example/v1",
		"http://user:secret@127.0.0.1:8317/v1",
		"http://127.0.0.1:8317/v1?token=secret",
		"http://127.0.0.1:8317/v1#fragment",
		"http://127.0.0.1:8317/other",
	} {
		if _, err := InitClaudeSettings(m, base, false, false, false); err == nil {
			t.Errorf("unsafe base URL %q accepted", base)
		}
	}
	m.Profiles.CPA.Endpoint = "http://proxy.example/v1"
	if _, err := InitClaudeSettings(m, "http://proxy.example/v1", false, false, false); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("non-loopback plaintext URL accepted: %v", err)
	}
}
