package bridge

import (
	"strings"
	"testing"
)

func TestClassifyRemoteSetupFailureDoesNotExposeRemoteOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"strictmodes", "bridge setup: insecure SSH path /data00: directory must be owned by this user or root", "StrictModes"},
		{"unmanaged", "setup blocked by 2 unmanaged file(s): secret=do-not-print", "unmanaged files"},
		{"identity", "bridge SSH identity already exists at /private/id", "bridge SSH identity"},
		{"unknown", "unknown secret-bearing remote failure", "setup failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyRemoteSetupFailure(tt.output)
			if !strings.Contains(got, tt.want) || strings.Contains(got, "do-not-print") || strings.Contains(got, "secret-bearing") || strings.Contains(got, "/data00") || strings.Contains(got, "/private/id") {
				t.Fatalf("unsafe or wrong setup detail: %q", got)
			}
		})
	}
}
