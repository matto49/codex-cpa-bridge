package bridge

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupCanBootstrapDedicatedSSHIdentity(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	m := testManifest(t)
	var output bytes.Buffer
	if err := Setup(&output, m, SetupOptions{GenerateBridgeKey: true, Start: false}); err != nil {
		t.Fatal(err)
	}
	private := bridgeClientIdentityPath(m)
	public := private + ".pub"
	if !regularFile(private) || !regularFile(public) {
		t.Fatal("dedicated SSH identity was not created")
	}
	info, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %04o, want 0600", info.Mode().Perm())
	}
	installed, err := os.ReadFile(filepath.Join(m.Runtime.StateDir, "ssh", "authorized_keys"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(public)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(installed, bytes.TrimSpace(key)) {
		t.Fatal("generated public key was not authorized")
	}
	m.useBridgeIdentity()
	if m.SSH.CPA.IdentityFile != private {
		t.Fatalf("normalized identity = %q, want %q", m.SSH.CPA.IdentityFile, private)
	}
}

func TestLoadManifestUsesIdentityFromFinalStateDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defaultKey := filepath.Join(home, ".codex-cpa-bridge", "ssh", "client_ed25519")
	if err := os.MkdirAll(filepath.Dir(defaultKey), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{defaultKey, defaultKey + ".pub"} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	customState := filepath.Join(t.TempDir(), "custom-state")
	manifest := filepath.Join(t.TempDir(), "bridge.toml")
	content := "[runtime]\nstate_dir = \"" + customState + "\"\n"
	if err := os.WriteFile(manifest, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.SSH.CPA.IdentityFile != "" {
		t.Fatalf("custom state inherited default identity %q", m.SSH.CPA.IdentityFile)
	}
	customKey := filepath.Join(customState, "ssh", "client_ed25519")
	if err := os.MkdirAll(filepath.Dir(customKey), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{customKey, customKey + ".pub"} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, err = LoadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.SSH.CPA.IdentityFile != customKey {
		t.Fatalf("identity = %q, want %q", m.SSH.CPA.IdentityFile, customKey)
	}
}

func TestBridgeIdentityDoesNotReplacePartialPair(t *testing.T) {
	m := testManifest(t)
	private := bridgeClientIdentityPath(m)
	if err := os.MkdirAll(filepath.Dir(private), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := generateBridgeClientIdentity(m)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
	content, err := os.ReadFile(private)
	if err != nil || string(content) != "keep me" {
		t.Fatalf("existing private file changed: %q, %v", content, err)
	}
}

func TestSetupRejectsBridgeKeyGenerationForExternalSSH(t *testing.T) {
	m := testManifest(t)
	m.SSH.CPA.Management = "external"
	if err := Setup(&bytes.Buffer{}, m, SetupOptions{GenerateBridgeKey: true}); err == nil {
		t.Fatal("expected external SSH endpoint to be rejected")
	}
	if fileExists(m.Runtime.StateDir) {
		t.Fatal("external SSH setup wrote bridge state")
	}
}

func TestSetupDoesNotGenerateKeyWhenPlanIsBlocked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := testManifest(t)
	if err := os.MkdirAll(m.Profiles.CPA.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(m.Profiles.CPA.Home, "config.toml")
	if err := os.WriteFile(config, []byte("model = \"unmanaged\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Setup(&bytes.Buffer{}, m, SetupOptions{GenerateBridgeKey: true, Start: false})
	if err == nil || !strings.Contains(err.Error(), "setup blocked") {
		t.Fatalf("unexpected error: %v", err)
	}
	if fileExists(bridgeClientIdentityPath(m)) {
		t.Fatal("key was generated despite a blocked plan")
	}
}

func TestSetupDoesNotReplaceMissingConfiguredIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := testManifest(t)
	m.SSH.CPA.IdentityFile = filepath.Join(t.TempDir(), "missing")
	err := Setup(&bytes.Buffer{}, m, SetupOptions{GenerateBridgeKey: true, Start: false})
	if err == nil || !strings.Contains(err.Error(), "configured SSH identity") {
		t.Fatalf("unexpected error: %v", err)
	}
	if fileExists(bridgeClientIdentityPath(m)) {
		t.Fatal("dedicated key replaced a configured identity")
	}
}

func TestOrphanDefaultPublicKeyDoesNotPreventBootstrap(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(sshDir, "id_ed25519.pub")
	if err := os.WriteFile(orphan, []byte("ssh-ed25519 AAAA orphan@test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := testManifest(t)
	if err := Setup(&bytes.Buffer{}, m, SetupOptions{GenerateBridgeKey: true, Start: false}); err != nil {
		t.Fatal(err)
	}
	if !regularFile(bridgeClientIdentityPath(m)) {
		t.Fatal("dedicated key was not generated")
	}
	if content, err := os.ReadFile(orphan); err != nil || string(content) != "ssh-ed25519 AAAA orphan@test\n" {
		t.Fatalf("orphan public key changed: %q, %v", content, err)
	}
}
