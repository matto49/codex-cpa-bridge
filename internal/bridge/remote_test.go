package bridge

import "testing"

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
