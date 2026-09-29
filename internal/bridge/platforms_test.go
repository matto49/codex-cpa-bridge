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

func TestScanClaudeDescribesPickerBoundary(t *testing.T) {
	m := claudeInitFixture(t)
	if _, err := InitClaudeSettings(m, m.Profiles.CPA.Endpoint, true, true, true); err != nil {
		t.Fatal(err)
	}
	report := scanClaude(m)
	if report.State != "ready" || !strings.Contains(report.Detail, "picker allowlist") || !strings.Contains(report.Detail, "--model") {
		t.Fatalf("Claude scan should not imply hard model access control: %+v", report)
	}
}

func TestScanClaudeMatchesSyncPlanForModelDrift(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"allowlist", func(settings map[string]any) { settings["availableModels"] = []string{"hidden"} }},
		{"picker", func(settings map[string]any) {
			settings["modelPicker"] = map[string]any{"options": []any{}, "replaceBuiltInOptions": false}
		}},
		{"default", func(settings map[string]any) { settings["model"] = "hidden" }},
		{"environment pin", func(settings map[string]any) { settings["env"].(map[string]any)["ANTHROPIC_MODEL"] = "hidden" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := claudeInitFixture(t)
			if _, err := InitClaudeSettings(m, m.Profiles.CPA.Endpoint, true, true, true); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
			if err != nil {
				t.Fatal(err)
			}
			var settings map[string]any
			if err := json.Unmarshal(raw, &settings); err != nil {
				t.Fatal(err)
			}
			tt.change(settings)
			raw, err = json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(m.Platforms.ClaudeSettingsJSON, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if scan := scanClaude(m); scan.State != "drift" || !scan.CPAEndpoint {
				t.Fatalf("Claude drift not detected: %+v", scan)
			}
			plan, err := PlanPlatformSync(m)
			if err != nil || syncItem(plan, "claude").Action != "update" {
				t.Fatalf("scan and plan disagree: %+v, %v", plan, err)
			}
			if result, err := SyncPlatformConfigs(m); err != nil || syncItem(result, "claude").Result != "updated" {
				t.Fatalf("sync failed to repair drift: %+v, %v", result, err)
			}
			if scan := scanClaude(m); scan.State != "ready" {
				t.Fatalf("repaired Claude settings are not ready: %+v", scan)
			}
		})
	}
}

func TestScanClaudeDoesNotReportReadyWithoutValidCatalog(t *testing.T) {
	m := claudeInitFixture(t)
	if _, err := InitClaudeSettings(m, m.Profiles.CPA.Endpoint, true, true, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Profiles.CPA.ModelCatalogJSON, []byte(`{"models":"invalid"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if scan := scanClaude(m); scan.State != "needs_setup" {
		t.Fatalf("invalid source catalog reported ready: %+v", scan)
	}
}

func TestScanClaudeDetectsCatalogVisibilityChange(t *testing.T) {
	m := claudeInitFixture(t)
	if _, err := SetModelVisibility(m, "hidden", "list"); err != nil {
		t.Fatal(err)
	}
	if _, err := InitClaudeSettings(m, m.Profiles.CPA.Endpoint, true, true, true); err != nil {
		t.Fatal(err)
	}
	if scan := scanClaude(m); scan.State != "ready" {
		t.Fatalf("initialized Claude settings are not ready: %+v", scan)
	}
	if _, err := SetModelVisibility(m, "hidden", "hide"); err != nil {
		t.Fatal(err)
	}
	if scan := scanClaude(m); scan.State != "drift" {
		t.Fatalf("model visibility change was not detected: %+v", scan)
	}
	if result, err := SyncPlatformConfigs(m); err != nil || syncItem(result, "claude").Result != "updated" {
		t.Fatalf("visibility change was not synchronized: %+v, %v", result, err)
	}
	if scan := scanClaude(m); scan.State != "ready" {
		t.Fatalf("synchronized Claude settings are not ready: %+v", scan)
	}
}

func TestScanXbotReportsLimitedPicker(t *testing.T) {
	m := syncFixture(t, "http://127.0.0.1:8317")
	report := scanXbot(m)
	if report.State != "limited" || !report.CPAEndpoint || !strings.Contains(report.Detail, "greyed out") {
		t.Fatalf("xbot adapter readiness must not imply hidden models disappear: %+v", report)
	}
}
