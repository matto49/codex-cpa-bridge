package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanPlatformsReportsRealVisibilitySourcesWithoutSecrets(t *testing.T) {
	m := testManifest(t)
	m.Profiles.CPA.ModelCatalogJSON = filepath.Join(m.Profiles.CPA.Home, "models.json")
	m.Platforms.ClaudeSettingsJSON = filepath.Join(t.TempDir(), "claude", "settings.json")
	m.Platforms.XbotConfigJSON = filepath.Join(t.TempDir(), "xbot", "config.json")
	m.Platforms.XbotDatabase = filepath.Join(filepath.Dir(m.Platforms.XbotConfigJSON), "xbot.db")
	for path, content := range map[string]string{
		filepath.Join(m.Profiles.CPA.Home, "config.toml"): "model_provider = \"cpa-local\"\nmodel_catalog_json = \"" + m.Profiles.CPA.ModelCatalogJSON + "\"\n[model_providers.cpa-local]\nbase_url = \"http://127.0.0.1:8317/v1\"\n",
		m.Profiles.CPA.ModelCatalogJSON:                   `{"models":[]}`,
		m.Platforms.ClaudeSettingsJSON:                    `{"env":{"ANTHROPIC_BASE_URL":"https://other.example/v1","ANTHROPIC_AUTH_TOKEN":"secret-claude"},"availableModels":["other-model"],"enforceAvailableModels":true}`,
		m.Platforms.XbotConfigJSON:                        `{"llm":{"base_url":"http://127.0.0.1:8317/v1","api_key":"secret-xbot"}}`,
		m.Platforms.XbotDatabase:                          "db",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report := ScanPlatforms(m)
	if len(report.Platforms) != 3 {
		t.Fatalf("platforms = %d", len(report.Platforms))
	}
	if got := report.Platforms[0].State; got != "ready" {
		t.Errorf("codex state = %s", got)
	}
	if got := report.Platforms[1].State; got != "unrelated" {
		t.Errorf("claude state = %s", got)
	}
	if got := report.Platforms[2].State; got != "needs_adapter" {
		t.Errorf("xbot state = %s", got)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-") {
		t.Fatal("report leaked a secret")
	}
}

func TestSameEndpointNormalizesV1Only(t *testing.T) {
	if !sameEndpoint("http://127.0.0.1:8317", "http://127.0.0.1:8317/v1/") {
		t.Fatal("same CPA host should match")
	}
	if sameEndpoint("http://127.0.0.1:8317/other", "http://127.0.0.1:8317/v1") {
		t.Fatal("different path matched")
	}
	if sameEndpoint("https://127.0.0.1:8317", "http://127.0.0.1:8317") {
		t.Fatal("different scheme matched")
	}
	for _, unsafe := range []string{
		"http://user:secret@127.0.0.1:8317/v1",
		"http://127.0.0.1:8317/v1?token=secret",
		"http://127.0.0.1:8317/v1#fragment",
	} {
		if sameEndpoint(unsafe, "http://127.0.0.1:8317/v1") {
			t.Errorf("URL with extra auth or routing fields matched: %q", unsafe)
		}
	}
}
