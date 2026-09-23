package bridge

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testManifest(t *testing.T) Manifest {
	t.Helper()
	root := t.TempDir()
	m := DefaultManifest()
	m.Profiles.Official = OfficialProfile{Home: filepath.Join(root, "official"), Mode: "native", Managed: false}
	m.Profiles.CPA = CPAProfile{Home: filepath.Join(root, "cpa"), Endpoint: "http://127.0.0.1:8317/v1", Model: "gpt-5.6-sol", ProviderName: "cpa-local"}
	m.SSH.CPA = SSHEndpoint{Host: "127.0.0.1", Port: 2222, User: "alice", Profile: "cpa"}
	m.Runtime = Runtime{StateDir: filepath.Join(root, "state"), CodexBinary: "codex", AppServerArgs: []string{"app-server"}}
	return m
}

func testAuthorizedKeyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(path, []byte("ssh-ed25519 AAAA test@localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRenderConfigUsesCPAHomeAuthReferenceWithoutSecret(t *testing.T) {
	m := testManifest(t)
	m.Profiles.CPA.EnvKey = "CPA_API_KEY"
	rendered := RenderCodexConfig(m)

	for _, want := range []string{
		`model_provider = "cpa-local"`,
		`base_url = "http://127.0.0.1:8317/v1"`,
		`wire_api = "responses"`,
		`requires_openai_auth = false`,
		`env_key = "CPA_API_KEY"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "auth.json") {
		t.Fatalf("rendered config unexpectedly mentions auth.json:\n%s", rendered)
	}
}

func TestWriteRenderedConfigDoesNotTouchOfficialProfile(t *testing.T) {
	m := testManifest(t)
	if err := os.MkdirAll(m.Profiles.Official.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	officialConfig := filepath.Join(m.Profiles.Official.Home, "config.toml")
	if err := os.WriteFile(officialConfig, []byte("model_provider = \"openai\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteRenderedConfig(m, false); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(officialConfig)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "model_provider = \"openai\"\n" {
		t.Fatalf("official config was changed: %q", string(raw))
	}
	cpaRaw, err := os.ReadFile(filepath.Join(m.Profiles.CPA.Home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(cpaRaw, []byte(OwnerMarker)) {
		t.Fatalf("CPA config missing owner marker:\n%s", string(cpaRaw))
	}
}

func TestInstallDryRunWritesNothing(t *testing.T) {
	m := testManifest(t)
	var out bytes.Buffer
	rc, err := InstallTemplates(&out, m, false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if _, err := os.Stat(m.Runtime.StateDir); !os.IsNotExist(err) {
		t.Fatalf("state dir exists after dry-run or unexpected stat error: %v", err)
	}
}

func TestInstallWriteCreatesOwnedStateFiles(t *testing.T) {
	m := testManifest(t)
	var out bytes.Buffer
	rc, err := InstallTemplates(&out, m, true, false, testAuthorizedKeyFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	wrapper := filepath.Join(m.Runtime.StateDir, "bin", "codex-cpa-wrapper")
	sshdConfig := filepath.Join(m.Runtime.StateDir, "ssh", "sshd_config")
	wrapperRaw, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	sshdRaw, err := os.ReadFile(sshdConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CODEX_HOME", "__codex_cpa_bridge_probe__"} {
		if !strings.Contains(string(wrapperRaw), want) {
			t.Fatalf("wrapper missing %q:\n%s", want, string(wrapperRaw))
		}
	}
	for _, want := range []string{"ListenAddress 127.0.0.1", "PasswordAuthentication no"} {
		if !strings.Contains(string(sshdRaw), want) {
			t.Fatalf("sshd_config missing %q:\n%s", want, string(sshdRaw))
		}
	}
}

func TestInstallBlocksUnmanagedFilesWithoutForce(t *testing.T) {
	m := testManifest(t)
	unmanaged := filepath.Join(m.Runtime.StateDir, "bin", "codex-cpa-wrapper")
	if err := os.MkdirAll(filepath.Dir(unmanaged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unmanaged, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	rc, err := InstallTemplates(&out, m, true, false, testAuthorizedKeyFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	raw, err := os.ReadFile(unmanaged)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(OwnerMarker)) {
		t.Fatalf("unmanaged file was overwritten:\n%s", string(raw))
	}
}

func TestPlanReportsBlockedUnmanagedFiles(t *testing.T) {
	m := testManifest(t)
	unmanaged := filepath.Join(m.Runtime.StateDir, "bin", "codex-cpa-wrapper")
	if err := os.MkdirAll(filepath.Dir(unmanaged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unmanaged, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := CollectPlan(m, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary.Blocked != 1 {
		t.Fatalf("blocked = %d, want 1", plan.Summary.Blocked)
	}
	found := false
	for _, item := range plan.StateFiles {
		if item.Path == unmanaged {
			found = true
			if !item.Blocked || item.Action != "blocked" {
				t.Fatalf("unexpected plan item: %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("plan did not include unmanaged wrapper")
	}
}

func TestDoctorReportFlagsOfficialProfileCPAEndpoint(t *testing.T) {
	m := testManifest(t)
	m.Profiles.Official.EnforceNativeLogin = true
	if err := os.MkdirAll(m.Profiles.Official.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	config := []byte(`model_provider = "cliproxy"

[model_providers.cliproxy]
base_url = "http://127.0.0.1:8317/v1"
`)
	if err := os.WriteFile(filepath.Join(m.Profiles.Official.Home, "config.toml"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	m.Profiles.CPA.ProviderName = "cliproxy"
	m.SSH.CPA.Port = 65000

	report, failures := CollectDoctorReport(m, false)
	if failures < 1 {
		t.Fatalf("failures = %d, want at least 1", failures)
	}
	if report.Result.PolicyOK {
		t.Fatalf("policy_ok = true, want false")
	}
	if !report.Official.ContainsCPAEndpoint {
		t.Fatalf("expected official profile to contain CPA endpoint")
	}
	if report.Official.ActiveModelProvider != "cliproxy" {
		t.Fatalf("active provider = %q, want cliproxy", report.Official.ActiveModelProvider)
	}
}

func TestDoctorAllowsCPAOfficialProviderWhenNativeLoginPolicyDisabled(t *testing.T) {
	m := testManifest(t)
	m.Profiles.Official.EnforceNativeLogin = false
	if err := os.MkdirAll(m.Profiles.Official.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	config := []byte(`model_provider = "cliproxy"

[model_providers.cliproxy]
base_url = "http://127.0.0.1:8317/v1"
`)
	if err := os.WriteFile(filepath.Join(m.Profiles.Official.Home, "config.toml"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	m.Profiles.CPA.ProviderName = "cliproxy"
	m.SSH.CPA.Port = 65000

	report, _ := CollectDoctorReport(m, false)
	if !report.Result.PolicyOK {
		t.Fatalf("policy_ok = false, want true; problems=%v", report.Official.Problems)
	}
	if !report.Official.ContainsCPAEndpoint {
		t.Fatalf("expected official profile to still record CPA endpoint presence")
	}
	if report.Official.ActiveModelProvider != "cliproxy" {
		t.Fatalf("active provider = %q, want cliproxy", report.Official.ActiveModelProvider)
	}
}

func TestSetupNoStartConfiguresBridgeAndPreservesOfficialProfile(t *testing.T) {
	m := testManifest(t)
	if err := os.MkdirAll(m.Profiles.Official.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	officialConfig := filepath.Join(m.Profiles.Official.Home, "config.toml")
	official := []byte("model_provider = \"openai\"\n")
	if err := os.WriteFile(officialConfig, official, 0o644); err != nil {
		t.Fatal(err)
	}
	publicKey := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest bridge@test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Setup(&out, m, SetupOptions{AuthorizedKeyFile: publicKey, Start: false}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(officialConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, official) {
		t.Fatalf("official profile changed: %q", raw)
	}
	for _, path := range []string{
		filepath.Join(m.Profiles.CPA.Home, "config.toml"),
		filepath.Join(m.Runtime.StateDir, "ssh", "authorized_keys"),
		filepath.Join(m.Runtime.StateDir, "ssh", "sshd_config"),
		filepath.Join(m.Runtime.StateDir, "bin", "codex-cpa-wrapper"),
	} {
		if !fileExists(path) {
			t.Fatalf("setup did not create %s", path)
		}
	}
}

func TestSetupRejectsInvalidAuthorizedKeyBeforeWriting(t *testing.T) {
	m := testManifest(t)
	emptyKey := filepath.Join(t.TempDir(), "empty.pub")
	if err := os.WriteFile(emptyKey, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	err := Setup(&bytes.Buffer{}, m, SetupOptions{AuthorizedKeyFile: emptyKey, Start: false})
	if err == nil || !strings.Contains(err.Error(), "invalid or empty SSH public key") {
		t.Fatalf("unexpected error: %v", err)
	}
	if fileExists(filepath.Join(m.Profiles.CPA.Home, "config.toml")) || fileExists(m.Runtime.StateDir) {
		t.Fatal("setup wrote files after rejecting the SSH key")
	}
}

func TestSetupBlocksUnmanagedConfigBeforeWritingState(t *testing.T) {
	m := testManifest(t)
	if err := os.MkdirAll(m.Profiles.CPA.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(m.Profiles.CPA.Home, "config.toml")
	if err := os.WriteFile(unmanaged, []byte("model = \"existing\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	publicKey := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest bridge@test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Setup(&bytes.Buffer{}, m, SetupOptions{AuthorizedKeyFile: publicKey, Start: false})
	if err == nil || !strings.Contains(err.Error(), "setup blocked") {
		t.Fatalf("unexpected error: %v", err)
	}
	if fileExists(m.Runtime.StateDir) {
		t.Fatal("setup wrote bridge state despite a blocked plan")
	}
}

func TestExternalCPAConfigIsPreservedDuringSetup(t *testing.T) {
	m := testManifest(t)
	m.Profiles.CPA.Management = "external"
	if err := os.MkdirAll(m.Profiles.CPA.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := []byte(`model = "gpt-5.6-sol"
model_provider = "cpa-local"

[model_providers.cpa-local]
base_url = "http://127.0.0.1:8317/v1"
wire_api = "responses"
`)
	config := filepath.Join(m.Profiles.CPA.Home, "config.toml")
	if err := os.WriteFile(config, existing, 0o644); err != nil {
		t.Fatal(err)
	}
	publicKey := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest bridge@test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := CollectPlan(m, false, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CPAConfig.Action != "external" || plan.CPAConfig.Blocked || plan.Summary.Blocked != 0 {
		t.Fatalf("unexpected external plan: %+v", plan)
	}
	if err := Setup(&bytes.Buffer{}, m, SetupOptions{AuthorizedKeyFile: publicKey, Start: false}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, existing) {
		t.Fatalf("external CPA config changed:\n%s", raw)
	}
	m.Profiles.CPA.Model = "another-model"
	if !cpaConfigMatches(m, config, raw) {
		t.Fatal("external config should stay valid when its independently managed model changes")
	}
	model, provider := externalCPAIdentity(config)
	if model != "gpt-5.6-sol" || provider != "cpa-local" {
		t.Fatalf("external identity = %q/%q", provider, model)
	}
}

func TestWrapperLoadsReferencedCredentialWithoutEmbeddingSecret(t *testing.T) {
	m := testManifest(t)
	m.Profiles.CPA.EnvKey = "CPA_API_KEY"
	m.Profiles.CPA.AuthCommand = "/usr/bin/security"
	m.Profiles.CPA.AuthArgs = []string{"find-generic-password", "-s", "codex-cpa-bridge", "-w"}
	rendered := RenderWrapper(m)
	if !strings.Contains(rendered, `export CPA_API_KEY="$(`) {
		t.Fatalf("wrapper does not export the referenced credential:\n%s", rendered)
	}
	if !strings.Contains(rendered, "/usr/bin/security") || !strings.Contains(rendered, "codex-cpa-bridge") {
		t.Fatalf("wrapper does not load the referenced credential:\n%s", rendered)
	}
	if strings.Contains(rendered, "api-keys:") {
		t.Fatalf("wrapper unexpectedly embeds a CPA secret:\n%s", rendered)
	}
}

func TestSSHProbeUsesBridgeCommandAndIgnoresStderr(t *testing.T) {
	m := testManifest(t)
	m.SSH.CPA.Management = "external"
	prepareFakeSSHProbe(t, m, `
if [ "$last" = "__codex_cpa_bridge_probe__" ]; then
  printf '%s\n' "$TEST_CPA_HOME"
  printf 'app-server warning\n' >&2
  exit 0
fi
exit 77
`)
	t.Setenv("TEST_CPA_HOME", m.Profiles.CPA.Home)
	ok, home := runSSHProbe(m, time.Second)
	if !ok || home != m.Profiles.CPA.Home {
		t.Fatalf("probe = %t, %q; want CPA home %q", ok, home, m.Profiles.CPA.Home)
	}
}

func TestSSHProbeFallsBackForOrdinaryExternalSSH(t *testing.T) {
	m := testManifest(t)
	m.SSH.CPA.Management = "external"
	prepareFakeSSHProbe(t, m, `
if [ "$last" = "__codex_cpa_bridge_probe__" ]; then
  printf 'command not found\n' >&2
  exit 127
fi
printf '%s\n' "$TEST_CPA_HOME"
`)
	t.Setenv("TEST_CPA_HOME", m.Profiles.CPA.Home)
	ok, home := runSSHProbe(m, time.Second)
	if !ok || home != m.Profiles.CPA.Home {
		t.Fatalf("probe = %t, %q; want CPA home %q", ok, home, m.Profiles.CPA.Home)
	}
}

func prepareFakeSSHProbe(t *testing.T, m Manifest, behavior string) {
	t.Helper()
	sshDir := filepath.Join(m.Runtime.StateDir, "ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "ssh_host_ed25519_key.pub"), []byte("ssh-ed25519 TESTKEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	ssh := filepath.Join(binDir, "ssh")
	script := "#!/bin/sh\nfor last do :; done\n" + behavior
	if err := os.WriteFile(ssh, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestSetModelVisibilityPreservesCatalogAndCreatesBackup(t *testing.T) {
	m := testManifest(t)
	m.Profiles.CPA.ModelCatalogJSON = filepath.Join(t.TempDir(), "models.json")
	original := map[string]any{
		"fetched_at":     "2026-09-20T00:00:00Z",
		"client_version": "test",
		"custom":         map[string]any{"keep": true},
		"models": []any{
			map[string]any{"slug": "alpha", "display_name": "Alpha", "visibility": "list", "supported_in_api": true, "priority": 2, "description": "keep me"},
			map[string]any{"slug": "beta", "display_name": "Beta", "visibility": "hide", "supported_in_api": false, "priority": 1},
		},
	}
	raw, _ := json.Marshal(original)
	if err := os.WriteFile(m.Profiles.CPA.ModelCatalogJSON, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := SetModelVisibility(m, "alpha", "hide")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Models) != 2 || report.Models[1].Slug != "alpha" || report.Models[1].Visibility != "hide" {
		t.Fatalf("unexpected report: %+v", report)
	}
	written, err := os.ReadFile(m.Profiles.CPA.ModelCatalogJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(written, []byte(`"description": "keep me"`)) || !bytes.Contains(written, []byte(`"custom"`)) {
		t.Fatalf("catalog fields were lost:\n%s", written)
	}
	backups, err := filepath.Glob(m.Profiles.CPA.ModelCatalogJSON + ".bak.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, err = %v", backups, err)
	}
}

func TestUpRejectsPortOwnedByAnotherService(t *testing.T) {
	m := testManifest(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	privateRoot, err := os.MkdirTemp(home, ".codex-cpa-port-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(privateRoot) })
	m.Runtime.StateDir = filepath.Join(privateRoot, "state")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	m.SSH.CPA.Port = listener.Addr().(*net.TCPAddr).Port
	start := filepath.Join(m.Runtime.StateDir, "bin", "start-sshd.sh")
	if err := os.MkdirAll(filepath.Dir(start), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(start, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err = Up(&bytes.Buffer{}, m, false)
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("unexpected error: %v", err)
	}
}
