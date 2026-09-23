package bridge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitManifestDiscoversProfileWithoutCopyingSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	catalog := filepath.Join(home, "models.json")
	if err := os.WriteFile(catalog, []byte(`{"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config := `model = "gpt-6-sol"
model_provider = "cliproxy"
model_catalog_json = "models.json"
[model_providers.cliproxy]
base_url = "http://127.0.0.1:8317/v1"
env_key = "CPA_KEY"
[model_providers.cliproxy.auth]
command = "/bin/echo"
args = ["secret-do-not-copy"]
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "bridge.toml")
	preview, err := InitManifestFromHost(target, false)
	if err != nil || preview.Action != "preview" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("preview created manifest: %v", err)
	}
	written, err := InitManifestFromHost(target, true)
	if err != nil || written.Action != "created" {
		t.Fatalf("write: %+v %v", written, err)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-do-not-copy") || strings.Contains(string(raw), "auth_command") {
		t.Fatal("manifest copied authentication arguments")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest is not private: %v %v", info, err)
	}
	m, err := LoadManifest(target)
	if err != nil || m.Profiles.CPA.Management != "external" || m.Profiles.CPA.ModelCatalogJSON != catalog || m.Profiles.CPA.EnvKey != "CPA_KEY" {
		t.Fatalf("discovered manifest invalid: %+v %v", m.Profiles.CPA, err)
	}
	if _, err := InitManifestFromHost(target, true); err == nil {
		t.Fatal("init overwrote an existing manifest")
	}
}

func TestLoadManifestRejectsMissingExplicitPath(t *testing.T) {
	_, err := LoadManifest(filepath.Join(t.TempDir(), "missing.toml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing manifest error: %v", err)
	}
}

func TestDiscoverLocalSSHFromExistingState(t *testing.T) {
	m := DefaultManifest()
	m.Runtime.StateDir = t.TempDir()
	path := filepath.Join(m.Runtime.StateDir, "ssh", "sshd_config")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# managed-by: codex-cpa-bridge\nListenAddress 127.0.0.1\nPort 2223\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	discoverLocalSSH(&m)
	if m.SSH.CPA.Port != 2223 || m.SSH.CPA.Management != "managed" || m.SSH.CPA.Host != "127.0.0.1" {
		t.Fatalf("existing SSH configuration not detected: %+v", m.SSH.CPA)
	}
}

func TestSharedOfficialCatalogRequiresMatchingEndpoint(t *testing.T) {
	home := t.TempDir()
	catalog := filepath.Join(home, "models.json")
	if err := os.WriteFile(catalog, []byte(`{"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config := `model_provider = "cliproxy"
model_catalog_json = "models.json"
[model_providers.cliproxy]
base_url = "http://127.0.0.1:8317/v1"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sharedOfficialCatalog(home, "http://127.0.0.1:8317/v1"); got != catalog {
		t.Fatalf("matching catalog = %q", got)
	}
	if got := sharedOfficialCatalog(home, "https://other.example/v1"); got != "" {
		t.Fatalf("unrelated catalog accepted: %q", got)
	}
}
