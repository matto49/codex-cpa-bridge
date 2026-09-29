package bridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidSSHTargetRejectsOptionsAndShellSyntax(t *testing.T) {
	for _, target := range []string{"devbox", "user@example.com", "host-1.example"} {
		if !validSSHTarget(target) {
			t.Errorf("valid target %q was rejected", target)
		}
	}
	for _, target := range []string{"", "-oProxyCommand=bad", "host;echo bad", "host with spaces", "host/path", "host$(bad)"} {
		if validSSHTarget(target) {
			t.Errorf("invalid target %q was accepted", target)
		}
	}
}

func TestRemoteProbeDiscoversExistingCodexProfile(t *testing.T) {
	tests := []struct {
		name     string
		profiles []string
		explicit string
		want     string
		present  bool
	}{
		{name: "mac CPA profile", profiles: []string{".codex-mac-cpa", ".codex-cpa"}, want: ".codex-mac-cpa", present: true},
		{name: "CPA profile", profiles: []string{".codex-cpa", ".codex"}, want: ".codex-cpa", present: true},
		{name: "headless default profile", profiles: []string{".codex"}, want: ".codex", present: true},
		{name: "explicit profile", profiles: []string{".codex-cpa", "custom"}, explicit: "custom", want: "custom", present: true},
		{name: "missing explicit profile", profiles: []string{".codex"}, explicit: "custom", want: "custom"},
		{name: "no profile", want: ".codex-cpa"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			for _, profile := range test.profiles {
				path := filepath.Join(home, profile, "config.toml")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("model = \"test\"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("/bin/sh", "-c", remoteProbeScript)
			cmd.Env = []string{"HOME=" + home, "PATH=" + t.TempDir()}
			if test.explicit != "" {
				cmd.Env = append(cmd.Env, "CODEX_HOME="+filepath.Join(home, test.explicit))
			}
			output, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			wantHome := "codex_home=" + filepath.Join(home, test.want) + "\n"
			if !strings.Contains(string(output), wantHome) {
				t.Errorf("probe output %q does not contain %q", output, wantHome)
			}
			wantPresence := "codex_config=missing\n"
			if test.present {
				wantPresence = "codex_config=present\n"
			}
			if !strings.Contains(string(output), wantPresence) {
				t.Errorf("probe output %q does not contain %q", output, wantPresence)
			}
		})
	}
}

func TestRemoteDoctorReplacesGuessedProfile(t *testing.T) {
	report := RemoteReport{RemoteCodexHome: "/home/user/.codex-mac-cpa", CodexConfigPresent: true}
	report.applyDoctor(DoctorReport{
		CPAProfile:  CPAReport{Home: "/home/user/.codex-cpa", ConfigPresent: false},
		CPABlackbox: CPABlackbox{ModelsStatus: "HTTP 200"},
		Result:      ResultReport{BridgeReady: false, Issues: 1},
	})
	if report.RemoteCodexHome != "/home/user/.codex-cpa" || report.CodexConfigPresent || report.RemoteDoctorOK || report.Ready || report.RemoteDoctorIssues != 1 || report.CPAHTTPStatus != "HTTP 200" {
		t.Fatalf("doctor did not replace the guessed profile and status: %+v", report)
	}
}
