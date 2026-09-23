package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshModelCatalogAddsModelsAndPreservesVisibility(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("client_version") != "" {
			_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-6-sol","display_name":"GPT 6 Sol","context_window":250000}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.5","owned_by":"openai"},{"id":"gpt-6-sol","owned_by":"openai"}]}`))
	}))
	defer server.Close()
	m := testManifest(t)
	m.Profiles.CPA.Endpoint = server.URL + "/v1"
	m.Profiles.CPA.EnvKey = "CPA_TEST_REFRESH_KEY"
	t.Setenv("CPA_TEST_REFRESH_KEY", "test-key")
	m.Profiles.CPA.ModelCatalogJSON = filepath.Join(t.TempDir(), "models.json")
	before := `{"custom":{"keep":true},"models":[{"slug":"gpt-5.5","visibility":"hide","priority":1,"supported_in_api":true}]}`
	if err := os.WriteFile(m.Profiles.CPA.ModelCatalogJSON, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RefreshModelCatalog(m)
	if err != nil || !result.Written || len(result.Added) != 1 || result.Added[0] != "gpt-6-sol" || result.Backup == "" {
		t.Fatalf("refresh: %+v %v", result, err)
	}
	backup, _ := os.ReadFile(result.Backup)
	if string(backup) != before {
		t.Fatal("refresh backup changed")
	}
	raw, err := os.ReadFile(m.Profiles.CPA.ModelCatalogJSON)
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(raw, &catalog); err != nil || !strings.Contains(string(catalog["custom"]), "keep") {
		t.Fatalf("catalog metadata lost: %v", err)
	}
	models, err := LoadModelCatalog(m)
	if err != nil || len(models.Models) != 2 || models.Models[0].Visibility != "hide" || models.Models[1].Visibility != "list" {
		t.Fatalf("visibility not preserved: %+v %v", models, err)
	}
	second, err := RefreshModelCatalog(m)
	if err != nil || second.Written || len(second.Added) != 0 {
		t.Fatalf("refresh not idempotent: %+v %v", second, err)
	}
}

func TestBootstrapModelCatalogPreviewCreateAndRefuseOverwrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer bootstrap-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("client_version") != "" {
			_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-new","display_name":"GPT New","context_window":128000,"input_modalities":["text"],"supported_reasoning_levels":[{"effort":"medium"}],"default_reasoning_level":"medium","priority":1,"description":"keep metadata"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-new","owned_by":"openai"}]}`))
	}))
	defer server.Close()
	m := testManifest(t)
	m.Profiles.CPA.Endpoint = server.URL + "/v1"
	m.Profiles.CPA.EnvKey = "CPA_TEST_BOOTSTRAP_KEY"
	m.Profiles.CPA.ModelCatalogJSON = filepath.Join(t.TempDir(), "nested", "catalog.json")
	t.Setenv("CPA_TEST_BOOTSTRAP_KEY", "bootstrap-key")
	preview, err := BootstrapModelCatalog(m, false)
	if err != nil || preview.Action != "preview" || preview.CPAModels != 1 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if _, err := os.Lstat(m.Profiles.CPA.ModelCatalogJSON); !os.IsNotExist(err) {
		t.Fatalf("preview wrote catalog: %v", err)
	}
	created, err := BootstrapModelCatalog(m, true)
	if err != nil || created.Action != "created" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	report, err := LoadModelCatalog(m)
	if err != nil || len(report.Models) != 1 || report.Models[0].Slug != "gpt-new" || report.Models[0].Visibility != "list" {
		t.Fatalf("catalog = %+v, %v", report, err)
	}
	content, err := os.ReadFile(m.Profiles.CPA.ModelCatalogJSON)
	if err != nil || !strings.Contains(string(content), "keep metadata") {
		t.Fatalf("metadata was lost: %v", err)
	}
	if _, err := BootstrapModelCatalog(m, true); err == nil {
		t.Fatal("bootstrap overwrote an existing catalog")
	}
	still, err := os.ReadFile(m.Profiles.CPA.ModelCatalogJSON)
	if err != nil || string(still) != string(content) {
		t.Fatal("existing catalog changed")
	}
}

func TestBootstrapModelCatalogRejectsIncompleteMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("client_version") != "" {
			_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-new","display_name":"GPT New"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-new"}]}`))
	}))
	defer server.Close()
	m := testManifest(t)
	m.Profiles.CPA.Endpoint = server.URL + "/v1"
	m.Profiles.CPA.ModelCatalogJSON = filepath.Join(t.TempDir(), "catalog.json")
	if _, err := BootstrapModelCatalog(m, true); err == nil || !strings.Contains(err.Error(), "missing context_window") {
		t.Fatalf("incomplete metadata accepted: %v", err)
	}
	if _, err := os.Lstat(m.Profiles.CPA.ModelCatalogJSON); !os.IsNotExist(err) {
		t.Fatalf("bootstrap wrote incomplete catalog: %v", err)
	}
}
