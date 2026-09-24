package bridge

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The transport fixture models an SSH host with a CPA Codex profile but no
// bridge binary or manifest. It tests installer orchestration, not Linux
// execution, real SSH trust, or a fresh host's CPA credentials.
func TestInstallRemoteFirstRunAndIdempotence(t *testing.T) {
	root := t.TempDir()
	remoteDir := filepath.Join(root, "remote")
	toolsDir := filepath.Join(root, "tools")
	for _, dir := range []string{remoteDir, toolsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(remoteDir, "codex-profile"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"ssh": fakeRemoteSSH, "scp": fakeRemoteSCP} {
		if err := os.WriteFile(filepath.Join(toolsDir, name), []byte(source), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BRIDGE_TEST_REMOTE_DIR", remoteDir)
	t.Setenv("PATH", toolsDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A minimal ELF header exercises architecture validation. The fake
	// transport records upload and simulates remote CLI command results.
	binaryPath := filepath.Join(root, "bridge-go-linux-amd64")
	image := make([]byte, 64)
	copy(image[:4], []byte{0x7f, 'E', 'L', 'F'})
	image[4], image[5], image[6] = 2, 1, 1
	binary.LittleEndian.PutUint16(image[16:], 2)
	binary.LittleEndian.PutUint16(image[18:], 62)
	binary.LittleEndian.PutUint32(image[20:], 1)
	binary.LittleEndian.PutUint16(image[52:], 64)
	if err := os.WriteFile(binaryPath, image, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := InstallRemote("fresh-host", binaryPath, RemoteInstallOptions{Setup: true})
	if err != nil {
		t.Fatal(err)
	}
	if !first.BinaryInstalled || first.BinaryBackup != "" || !first.ManifestCreated || !first.SetupAttempted || !first.SetupSucceeded || !first.DoctorChecked || !first.DoctorReady || first.DoctorIssues != 0 {
		t.Fatalf("fresh install did not become ready: %+v", first)
	}
	if _, err := os.Stat(filepath.Join(remoteDir, "config", "bridge.toml")); err != nil {
		t.Fatalf("remote manifest missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(remoteDir, "bin", "bridge-go")); err != nil {
		t.Fatalf("remote CLI missing: %v", err)
	}
	second, err := InstallRemote("fresh-host", binaryPath, RemoteInstallOptions{Setup: true})
	if err != nil {
		t.Fatal(err)
	}
	if !second.BinaryInstalled || second.BinaryBackup != "" || second.ManifestCreated || !second.SetupSucceeded || !second.DoctorReady {
		t.Fatalf("rerun was not idempotent: %+v", second)
	}
	updatedImage := append([]byte(nil), image...)
	updatedImage[63] = 1
	if err := os.WriteFile(binaryPath, updatedImage, 0o700); err != nil {
		t.Fatal(err)
	}
	third, err := InstallRemote("fresh-host", binaryPath, RemoteInstallOptions{Setup: true})
	if err != nil {
		t.Fatal(err)
	}
	if !third.BinaryInstalled || third.BinaryBackup == "" || third.ManifestCreated || !third.DoctorReady {
		t.Fatalf("binary update did not preserve a backup: %+v", third)
	}
	backup, err := os.ReadFile(third.BinaryBackup)
	if err != nil || string(backup) != string(image) {
		t.Fatalf("previous remote binary was not backed up: %v", err)
	}
	active, err := os.ReadFile(filepath.Join(remoteDir, "bin", "bridge-go"))
	if err != nil || string(active) != string(updatedImage) {
		t.Fatalf("new remote binary was not activated: %v", err)
	}
	corruptCandidate := append([]byte(nil), updatedImage...)
	corruptCandidate[62] = 2
	if err := os.WriteFile(binaryPath, corruptCandidate, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRIDGE_TEST_CORRUPT_UPLOAD", "1")
	failed, err := InstallRemote("fresh-host", binaryPath, RemoteInstallOptions{Setup: true})
	if err == nil || !strings.Contains(err.Error(), "checksum") || failed.BinaryInstalled {
		t.Fatalf("corrupt staged upload was accepted: %+v, %v", failed, err)
	}
	stillActive, err := os.ReadFile(filepath.Join(remoteDir, "bin", "bridge-go"))
	if err != nil || string(stillActive) != string(updatedImage) {
		t.Fatalf("corrupt upload replaced working binary: %v", err)
	}
	staged, err := filepath.Glob(filepath.Join(remoteDir, "bin", ".bridge-go.new-*"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("corrupt staged upload was not cleaned up: %v, %v", staged, err)
	}
	uploads, err := os.ReadFile(filepath.Join(remoteDir, "scp-calls"))
	if err != nil || strings.Count(string(uploads), "uploaded\n") != 3 {
		t.Fatalf("expected three uploads across first install, rerun, update, and corrupt attempt: %q, %v", uploads, err)
	}
}

const fakeRemoteSSH = `#!/bin/sh
set -eu
remote_dir=$BRIDGE_TEST_REMOTE_DIR
if [ "$1" = "-G" ]; then
  printf 'hostname mock.example\nuser mock\nport 22\n'
  exit 0
fi
remote_command=
for arg in "$@"; do remote_command=$arg; done
case "$remote_command" in
  *"cpa_http="*)
    printf 'home=%s\ncodex_home=%s/codex\n' "$remote_dir" "$remote_dir"
    if [ -f "$remote_dir/codex-profile" ]; then printf 'codex_config=present\n'; else printf 'codex_config=missing\n'; fi
    if [ -f "$remote_dir/bin/bridge-go" ]; then printf 'bridge=present\n'; else printf 'bridge=missing\n'; fi
    if [ -f "$remote_dir/config/bridge.toml" ]; then printf 'manifest=present\n'; else printf 'manifest=missing\n'; fi
    printf 'cpa_http=HTTP 200\n'
    ;;
  *"uname -sm"*) printf 'Linux x86_64\n' ;;
  *"mkdir -p "*) mkdir -p "$remote_dir/bin" "$remote_dir/config" ;;
  *"sha256sum"*)
    case "$remote_command" in
      *".bridge-go.new-"*)
        set -- "$remote_dir"/bin/.bridge-go.new-*
        [ -f "$1" ] || exit 1
        shasum -a 256 "$1" | cut -d ' ' -f1
        ;;
      *)
        if [ -f "$remote_dir/bin/bridge-go" ]; then shasum -a 256 "$remote_dir/bin/bridge-go" | cut -d ' ' -f1; fi
        ;;
    esac
    ;;
  *"rm -f "*)
    set -- "$remote_dir"/bin/.bridge-go.new-*
    [ -f "$1" ] && rm -f "$1"
    ;;
  *'current="$HOME/.local/bin/bridge-go"'*)
    set -- "$remote_dir"/bin/.bridge-go.new-*
    [ -f "$1" ] || exit 1
    backup=
    if [ -f "$remote_dir/bin/bridge-go" ]; then
      backup="$remote_dir/bin/bridge-go.bak.1"
      cp "$remote_dir/bin/bridge-go" "$backup"
    fi
    mv "$1" "$remote_dir/bin/bridge-go"
    printf 'backup=%s\n' "$backup"
    ;;
  *" init --write --json"*)
    [ -f "$remote_dir/codex-profile" ] || exit 1
    (umask 077; : > "$remote_dir/config/bridge.toml")
    printf '{"action":"created"}\n'
    ;;
  *" setup --generate-bridge-key"*) (umask 077; : > "$remote_dir/setup-ok") ;;
  *" doctor --json"*)
    if [ -f "$remote_dir/config/bridge.toml" ] && [ -f "$remote_dir/setup-ok" ]; then
      printf '{"result":{"bridge_ready":true,"issues":0},"cpa_blackbox":{"models_status":"HTTP 200"}}\n'
    else
      printf '{"result":{"bridge_ready":false,"issues":1},"cpa_blackbox":{"models_status":"HTTP 200"}}\n'
    fi
    ;;
  *) printf 'unexpected fake SSH command: %s\n' "$remote_command" >&2; exit 2 ;;
esac
`

const fakeRemoteSCP = `#!/bin/sh
set -eu
remote_dir=$BRIDGE_TEST_REMOTE_DIR
previous=
current=
for arg in "$@"; do previous=$current; current=$arg; done
destination="$remote_dir/bin/$(basename "$current")"
cp "$previous" "$destination"
if [ "${BRIDGE_TEST_CORRUPT_UPLOAD:-}" = "1" ]; then printf 'corrupt' >> "$destination"; fi
printf 'uploaded\n' >> "$remote_dir/scp-calls"
`
