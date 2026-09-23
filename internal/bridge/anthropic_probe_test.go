package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeAnthropicMessagesSuccessAndRedaction(t *testing.T) {
	const secret = "probe-secret-must-not-appear"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Method != http.MethodPost || r.Header.Get("x-api-key") != secret || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("unexpected Anthropic request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Messages  []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "claude-test" || body.MaxTokens != 16 || len(body.Messages) != 1 || body.Messages[0].Role != "user" {
			t.Errorf("unexpected probe body: %+v, err=%v", body, err)
		}
		_, _ = w.Write([]byte(`{"type":"message","model":"claude-test","content":[{"type":"text","text":"sensitive model output"}]}`))
	}))
	defer server.Close()
	m := testManifest(t)
	m.Profiles.CPA.Endpoint = server.URL + "/v1"
	m.Profiles.CPA.EnvKey = "BRIDGE_ANTHROPIC_PROBE_KEY"
	m.Profiles.CPA.AuthCommand = ""
	t.Setenv(m.Profiles.CPA.EnvKey, secret)
	probe := ProbeAnthropicMessages(m, "claude-test")
	if !probe.OK || probe.Status != "HTTP 200" {
		t.Fatalf("probe = %+v", probe)
	}
	encoded, err := json.Marshal(probe)
	if err != nil || strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "sensitive model output") {
		t.Fatalf("probe leaked credential or model output: %s, err=%v", encoded, err)
	}
}

func TestProbeAnthropicMessagesRejectsUnavailableAndBadResponses(t *testing.T) {
	m := testManifest(t)
	m.Profiles.CPA.EnvKey = "BRIDGE_MISSING_ANTHROPIC_KEY"
	m.Profiles.CPA.AuthCommand = ""
	t.Setenv(m.Profiles.CPA.EnvKey, "")
	if probe := ProbeAnthropicMessages(m, "claude-test"); probe.OK || probe.Status != "credential unavailable" {
		t.Fatalf("missing credential probe = %+v", probe)
	}
	if probe := ProbeAnthropicMessages(m, "bad\nmodel"); probe.OK || probe.Status != "invalid model" {
		t.Fatalf("invalid model probe = %+v", probe)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"sensitive upstream detail"}}`))
	}))
	defer server.Close()
	m.Profiles.CPA.Endpoint = server.URL + "/v1"
	t.Setenv(m.Profiles.CPA.EnvKey, "test-key")
	probe := ProbeAnthropicMessages(m, "claude-test")
	if probe.OK || probe.Status != "HTTP 429" || strings.Contains(probe.Detail, "sensitive") {
		t.Fatalf("error probe = %+v", probe)
	}
}

func TestProbeAnthropicMessagesDoesNotForwardCredentialsOnRedirect(t *testing.T) {
	forwarded := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = true
	}))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	m := testManifest(t)
	m.Profiles.CPA.Endpoint = server.URL + "/v1"
	m.Profiles.CPA.EnvKey = "BRIDGE_REDIRECT_ANTHROPIC_KEY"
	m.Profiles.CPA.AuthCommand = ""
	t.Setenv(m.Profiles.CPA.EnvKey, "redirect-secret")
	probe := ProbeAnthropicMessages(m, "claude-test")
	if probe.OK || probe.Status != "HTTP 307" || forwarded {
		t.Fatalf("redirect probe = %+v, forwarded=%t", probe, forwarded)
	}
}
