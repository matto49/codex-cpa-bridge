package bridge

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func syncFixture(t *testing.T, claudeBase string) Manifest {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI unavailable")
	}
	m := testManifest(t)
	m.Profiles.CPA.ModelCatalogJSON = filepath.Join(m.Profiles.CPA.Home, "catalog.json")
	m.Platforms.ClaudeSettingsJSON = filepath.Join(t.TempDir(), "claude", "settings.json")
	m.Platforms.XbotConfigJSON = filepath.Join(t.TempDir(), "xbot", "config.json")
	m.Platforms.XbotDatabase = filepath.Join(filepath.Dir(m.Platforms.XbotConfigJSON), "xbot.db")
	files := map[string]string{
		filepath.Join(m.Profiles.CPA.Home, "config.toml"): "model_provider = \"cpa-local\"\nmodel_catalog_json = \"" + m.Profiles.CPA.ModelCatalogJSON + "\"\n[model_providers.cpa-local]\nbase_url = \"http://127.0.0.1:8317/v1\"\n",
		m.Profiles.CPA.ModelCatalogJSON:                   `{"models":[{"slug":"gpt-6-sol","visibility":"list","supported_in_api":true,"priority":1},{"slug":"gpt-5.5","visibility":"hide","supported_in_api":true,"priority":2}]}`,
		m.Platforms.ClaudeSettingsJSON:                    `{"env":{"ANTHROPIC_BASE_URL":"` + claudeBase + `","ANTHROPIC_AUTH_TOKEN":"secret-test"},"availableModels":["gpt-5.5"],"enforceAvailableModels":false,"hooks":{"a":1}}`,
		m.Platforms.XbotConfigJSON:                        `{"llm":{"base_url":"http://127.0.0.1:8317/v1","api_key":"secret-xbot"}}`,
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	schema := `CREATE TABLE user_llm_subscriptions(id TEXT PRIMARY KEY,base_url TEXT NOT NULL,model TEXT NOT NULL);
CREATE TABLE subscription_models(id TEXT PRIMARY KEY,subscription_id TEXT NOT NULL,model TEXT NOT NULL,enabled INTEGER NOT NULL,updated_at TEXT NOT NULL DEFAULT (datetime('now')));
CREATE TABLE user_default_model(sender_id TEXT PRIMARY KEY,subscription_id TEXT NOT NULL,model TEXT NOT NULL,updated_at TEXT NOT NULL DEFAULT (datetime('now')));
INSERT INTO user_llm_subscriptions(id,base_url,model) VALUES('sub-cpa','http://127.0.0.1:8317/v1','gpt-5.5');
INSERT INTO user_llm_subscriptions(id,base_url,model) VALUES('sub-other','https://other.example/v1','other');
INSERT INTO user_default_model(sender_id,subscription_id,model) VALUES('cli-user','sub-cpa','gpt-5.5');
INSERT INTO subscription_models(id,subscription_id,model,enabled) VALUES('m1','sub-cpa','gpt-5.5',1);
INSERT INTO subscription_models(id,subscription_id,model,enabled) VALUES('m2','sub-other','gpt-5.5',1);`
	if output, err := exec.Command("sqlite3", m.Platforms.XbotDatabase, schema).CombinedOutput(); err != nil {
		t.Fatalf("create xbot test database: %v: %s", err, output)
	}
	return m
}

func syncItem(report PlatformSyncReport, id string) PlatformSyncItem {
	for _, item := range report.Items {
		if item.ID == id {
			return item
		}
	}
	return PlatformSyncItem{}
}

func TestPlatformSyncPreviewBackupAndIdempotence(t *testing.T) {
	m := syncFixture(t, "http://127.0.0.1:8317")
	before, err := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanPlatformSync(m)
	if err != nil {
		t.Fatal(err)
	}
	claude := syncItem(plan, "claude")
	if claude.Action != "update" || !reflect.DeepEqual(claude.Add, []string{"gpt-6-sol"}) || !reflect.DeepEqual(claude.Remove, []string{"gpt-5.5"}) {
		t.Fatalf("unexpected plan: %+v", claude)
	}
	if syncItem(plan, "xbot").Action != "update" {
		t.Fatalf("xbot model flags need update: %+v", plan)
	}
	stillBefore, _ := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if string(stillBefore) != string(before) {
		t.Fatal("preview mutated Claude settings")
	}
	result, err := SyncPlatformConfigs(m)
	if err != nil {
		t.Fatal(err)
	}
	claude = syncItem(result, "claude")
	if claude.Result != "updated" || claude.Backup == "" || result.Changed != 2 || result.Failed != 0 {
		t.Fatalf("unexpected sync result: %+v", result)
	}
	xbot := syncItem(result, "xbot")
	if xbot.Result != "updated" || xbot.Backup == "" {
		t.Fatalf("xbot was not updated and backed up: %+v", xbot)
	}
	backupInfo, err := os.Stat(xbot.Backup)
	if err != nil || backupInfo.Mode().Perm() != 0o600 {
		t.Fatalf("xbot backup must be private: %v, %v", backupInfo, err)
	}
	var xbotRows []struct {
		Model   string `json:"model"`
		Enabled int    `json:"enabled"`
	}
	if err := xbotQuery(m.Platforms.XbotDatabase, "SELECT model,enabled FROM subscription_models WHERE subscription_id='sub-cpa' ORDER BY model", &xbotRows); err != nil {
		t.Fatal(err)
	}
	if len(xbotRows) != 2 || xbotRows[0].Model != "gpt-5.5" || xbotRows[0].Enabled != 0 || xbotRows[1].Model != "gpt-6-sol" || xbotRows[1].Enabled != 1 {
		t.Fatalf("unexpected xbot rows: %+v", xbotRows)
	}
	var preferred []struct {
		Model string `json:"model"`
	}
	if err := xbotQuery(m.Platforms.XbotDatabase, "SELECT model FROM user_llm_subscriptions WHERE id='sub-cpa' UNION ALL SELECT model FROM user_default_model WHERE sender_id='cli-user'", &preferred); err != nil || len(preferred) != 2 || preferred[0].Model != "gpt-6-sol" || preferred[1].Model != "gpt-6-sol" {
		t.Fatalf("hidden preferred models were not replaced: %+v, %v", preferred, err)
	}
	var unrelated []struct {
		Enabled int `json:"enabled"`
	}
	if err := xbotQuery(m.Platforms.XbotDatabase, "SELECT enabled FROM subscription_models WHERE subscription_id='sub-other'", &unrelated); err != nil || len(unrelated) != 1 || unrelated[0].Enabled != 1 {
		t.Fatalf("unrelated subscription changed: %+v %v", unrelated, err)
	}
	backup, err := os.ReadFile(claude.Backup)
	if err != nil || string(backup) != string(before) {
		t.Fatalf("backup missing or changed: %v", err)
	}
	var settings struct {
		Env                    map[string]string `json:"env"`
		AvailableModels        []string          `json:"availableModels"`
		EnforceAvailableModels bool              `json:"enforceAvailableModels"`
		Hooks                  map[string]int    `json:"hooks"`
	}
	updated, err := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if err != nil || json.Unmarshal(updated, &settings) != nil {
		t.Fatalf("invalid updated settings: %v", err)
	}
	if settings.Env["ANTHROPIC_AUTH_TOKEN"] != "secret-test" || !settings.EnforceAvailableModels || !reflect.DeepEqual(settings.AvailableModels, []string{"gpt-6-sol"}) || settings.Hooks["a"] != 1 {
		t.Fatalf("settings not preserved: %+v", settings)
	}
	second, err := SyncPlatformConfigs(m)
	if err != nil || second.Changed != 0 || syncItem(second, "claude").Action != "noop" || syncItem(second, "xbot").Action != "noop" {
		t.Fatalf("second sync should be idempotent: %+v, %v", second, err)
	}
	serialized, _ := json.Marshal(result)
	if strings.Contains(string(serialized), "secret-") {
		t.Fatal("sync report leaked a credential")
	}
}

func TestPlatformSyncPreservesUnrelatedClaude(t *testing.T) {
	m := syncFixture(t, "https://other.example")
	before, _ := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	result, err := SyncPlatformConfigs(m)
	if err != nil {
		t.Fatal(err)
	}
	if syncItem(result, "claude").Action != "skipped" || result.Changed != 1 || syncItem(result, "xbot").Result != "updated" {
		t.Fatalf("unrelated Claude settings should be skipped: %+v", result)
	}
	after, _ := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if string(after) != string(before) {
		t.Fatal("unrelated Claude settings changed")
	}
}

func TestPlatformSyncRepairsClaudeTrailingV1WithBackup(t *testing.T) {
	m := syncFixture(t, "http://127.0.0.1:8317/v1")
	before, err := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if scanned := scanClaude(m); scanned.State != "needs_setup" || !strings.Contains(scanned.Detail, "/v1") {
		t.Fatalf("scan missed invalid Claude route: %+v", scanned)
	}
	plan, err := PlanPlatformSync(m)
	if err != nil || syncItem(plan, "claude").Action != "update" || !strings.Contains(syncItem(plan, "claude").Detail, "base URL") {
		t.Fatalf("repair not previewed: %+v, %v", plan, err)
	}
	still, _ := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if string(still) != string(before) {
		t.Fatal("preview changed Claude settings")
	}
	result, err := SyncPlatformConfigs(m)
	if err != nil {
		t.Fatal(err)
	}
	item := syncItem(result, "claude")
	if item.Result != "updated" || item.Backup == "" {
		t.Fatalf("repair failed: %+v", item)
	}
	backup, err := os.ReadFile(item.Backup)
	if err != nil || string(backup) != string(before) {
		t.Fatalf("backup not intact: %v", err)
	}
	var settings struct {
		Env                    map[string]string `json:"env"`
		AvailableModels        []string          `json:"availableModels"`
		EnforceAvailableModels bool              `json:"enforceAvailableModels"`
		Hooks                  map[string]int    `json:"hooks"`
	}
	after, err := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if err != nil || json.Unmarshal(after, &settings) != nil {
		t.Fatalf("invalid settings: %v", err)
	}
	if settings.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8317" || settings.Env["ANTHROPIC_AUTH_TOKEN"] != "secret-test" || !settings.EnforceAvailableModels || !reflect.DeepEqual(settings.AvailableModels, []string{"gpt-6-sol"}) || settings.Hooks["a"] != 1 {
		t.Fatalf("repair lost settings: %+v", settings)
	}
	if scanned := scanClaude(m); scanned.State != "ready" {
		t.Fatalf("repaired settings not ready: %+v", scanned)
	}
	second, err := SyncPlatformConfigs(m)
	if err != nil || syncItem(second, "claude").Action != "noop" {
		t.Fatalf("repair not idempotent: %+v, %v", second, err)
	}
}

func TestPlatformSyncRepairsOnlyClaudeURLWhenModelsAlreadyMatch(t *testing.T) {
	m := syncFixture(t, "http://127.0.0.1:8317/v1")
	settings := `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:8317/v1","ANTHROPIC_AUTH_TOKEN":"secret-test"},"availableModels":["gpt-6-sol"],"enforceAvailableModels":true}`
	if err := os.WriteFile(m.Platforms.ClaudeSettingsJSON, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanPlatformSync(m)
	item := syncItem(plan, "claude")
	if err != nil || item.Action != "update" || len(item.Add) != 0 || len(item.Remove) != 0 {
		t.Fatalf("URL-only repair not planned: %+v, %v", item, err)
	}
	result, err := SyncPlatformConfigs(m)
	if err != nil || syncItem(result, "claude").Result != "updated" {
		t.Fatalf("URL-only repair failed: %+v, %v", result, err)
	}
	content, err := os.ReadFile(m.Platforms.ClaudeSettingsJSON)
	if err != nil || strings.Contains(string(content), "127.0.0.1:8317/v1") {
		t.Fatalf("URL-only repair did not remove /v1: %s, %v", content, err)
	}
}

func TestPlatformSyncRejectsConcurrentConfigEdit(t *testing.T) {
	m := syncFixture(t, "http://127.0.0.1:8317")
	_, changes, err := buildPlatformSync(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Platforms.ClaudeSettingsJSON, []byte(`{"changed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := applyPlatformChange(changes["claude"]); err == nil {
		t.Fatal("concurrent edit was overwritten")
	}
}

func TestXbotSyncRejectsConcurrentModelEdit(t *testing.T) {
	m := syncFixture(t, "http://127.0.0.1:8317")
	_, changes, err := buildPlatformSync(m)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("sqlite3", m.Platforms.XbotDatabase, "UPDATE subscription_models SET enabled=0 WHERE id='m1'").CombinedOutput(); err != nil {
		t.Fatalf("mutate test database: %v: %s", err, output)
	}
	if _, err := applyXbotDBChange(*changes["xbot"].xbot); err == nil {
		t.Fatal("concurrent xbot edit was overwritten")
	}
	var rows []struct {
		Model   string `json:"model"`
		Enabled int    `json:"enabled"`
	}
	if err := xbotQuery(m.Platforms.XbotDatabase, "SELECT model, enabled FROM subscription_models WHERE subscription_id='sub-cpa'", &rows); err != nil || len(rows) != 1 || rows[0].Enabled != 0 {
		t.Fatalf("failed transaction changed xbot rows: %+v, %v", rows, err)
	}
}

func TestXbotUnsupportedSchemaBlocksSync(t *testing.T) {
	m := syncFixture(t, "http://127.0.0.1:8317")
	if output, err := exec.Command("sqlite3", m.Platforms.XbotDatabase, "DROP TABLE subscription_models").CombinedOutput(); err != nil {
		t.Fatalf("alter test database: %v: %s", err, output)
	}
	plan, err := PlanPlatformSync(m)
	if err != nil {
		t.Fatal(err)
	}
	if item := syncItem(plan, "xbot"); item.Action != "blocked" || !strings.Contains(item.Detail, "unsupported xbot schema") {
		t.Fatalf("unsupported schema should be blocked: %+v", item)
	}
}
