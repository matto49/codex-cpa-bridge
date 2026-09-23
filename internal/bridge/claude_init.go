package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

type ClaudeInitReport struct {
	Path              string `json:"path"`
	BaseURL           string `json:"base_url"`
	VisibleModels     int    `json:"visible_models"`
	ProtocolConfirmed bool   `json:"protocol_confirmed"`
	AuthConfirmed     bool   `json:"auth_confirmed"`
	RuntimeVerified   bool   `json:"runtime_verified"`
	Action            string `json:"action"`
	Detail            string `json:"detail"`
}

// InitClaudeSettings only creates an absent file. An operator must separately
// confirm Anthropic protocol support and provision Claude Code credentials;
// neither can be inferred from CPA's OpenAI-compatible /models response.
func InitClaudeSettings(m Manifest, baseURL string, confirmProtocol, confirmAuth, write bool) (ClaudeInitReport, error) {
	path := m.Platforms.ClaudeSettingsJSON
	if path == "" {
		return ClaudeInitReport{}, errors.New("platforms.claude_settings_json is not configured")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !sameEndpoint(baseURL, m.Profiles.CPA.Endpoint) {
		return ClaudeInitReport{}, errors.New("Claude base URL must be a credential-free URL on the configured CPA endpoint")
	}
	if parsed.Scheme == "http" && !claudeLoopbackHost(parsed.Hostname()) {
		return ClaudeInitReport{}, errors.New("non-loopback Claude base URL must use HTTPS")
	}
	if _, err := os.Lstat(path); err == nil {
		return ClaudeInitReport{}, fmt.Errorf("Claude settings already exist at %s; refusing to replace them", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ClaudeInitReport{}, err
	}
	catalog, err := LoadModelCatalog(m)
	if err != nil {
		return ClaudeInitReport{}, err
	}
	visible := make([]string, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		if model.Visibility == "list" && model.Slug != "" {
			visible = append(visible, model.Slug)
		}
	}
	if len(visible) == 0 {
		return ClaudeInitReport{}, errors.New("the source catalog has no visible models")
	}
	report := ClaudeInitReport{
		Path:              path,
		BaseURL:           baseURL,
		VisibleModels:     len(visible),
		ProtocolConfirmed: confirmProtocol,
		AuthConfirmed:     confirmAuth,
		RuntimeVerified:   false,
		Action:            "preview",
		Detail:            "No credential will be written; Claude Code needs external authentication and an Anthropic-compatible CPA endpoint",
	}
	if !write {
		return report, nil
	}
	if !confirmProtocol || !confirmAuth {
		return report, errors.New("creation requires explicit confirmation of Anthropic protocol compatibility and external Claude authentication")
	}
	settings := map[string]any{
		"env":                    map[string]string{"ANTHROPIC_BASE_URL": baseURL},
		"availableModels":        visible,
		"enforceAvailableModels": true,
	}
	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return report, err
	}
	content = append(content, '\n')
	if err := createPrivateFile(path, content); err != nil {
		return report, err
	}
	report.Action = "created"
	report.Detail = "Claude settings created; reconnect Claude Code and verify authentication and model picker behavior"
	return report, nil
}

func claudeLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
