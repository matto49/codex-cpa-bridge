package bridge

import (
	"strings"
	"testing"
)

func TestRemoteClaudeCommandRequiresExplicitWriteAndUsesRemoteEndpoint(t *testing.T) {
	for _, tc := range []struct {
		mode, action, confirmation string
	}{
		{"init", "init-claude", "--confirm-external-auth"},
		{"adopt", "adopt-claude", "--confirm-replace-provider"},
	} {
		preview := remoteClaudeCommand(tc.mode, "http://127.0.0.1:8317", false)
		if !strings.Contains(preview, "platforms "+tc.action) || !strings.Contains(preview, "--base-url 'http://127.0.0.1:8317'") || strings.Contains(preview, "--write") || strings.Contains(preview, tc.confirmation) {
			t.Fatalf("unsafe or incorrect %s preview: %q", tc.mode, preview)
		}
		write := remoteClaudeCommand(tc.mode, "http://127.0.0.1:8317", true)
		if !strings.Contains(write, "--confirm-anthropic-compatible") || !strings.Contains(write, tc.confirmation) || !strings.Contains(write, "--write") {
			t.Fatalf("missing %s write confirmations: %q", tc.mode, write)
		}
	}
}

func TestRemoteClaudeRejectsUnsafeOrUnconfirmedOperationBeforeSSH(t *testing.T) {
	for _, tc := range []struct {
		target, mode            string
		write, protocol, second bool
	}{
		{"devbox;touch /tmp/unsafe", "adopt", false, false, false},
		{"devbox", "unknown", false, false, false},
		{"devbox", "adopt", true, false, true},
		{"devbox", "adopt", true, true, false},
	} {
		if _, err := RemoteClaudeSettings(tc.target, tc.mode, tc.write, tc.protocol, tc.second); err == nil {
			t.Fatalf("invalid remote Claude operation was accepted: %+v", tc)
		}
	}
}
