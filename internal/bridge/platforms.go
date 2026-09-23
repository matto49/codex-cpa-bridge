package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// PlatformReport contains only configuration metadata. In particular, it never
// includes keys, tokens, or complete config file contents.
type PlatformReport struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	State         string `json:"state"`
	Detail        string `json:"detail"`
	CPAEndpoint   bool   `json:"cpa_endpoint"`
	Visibility    string `json:"visibility"`
	RestartNeeded bool   `json:"restart_needed"`
}

type PlatformsReport struct {
	CPAEndpoint string           `json:"cpa_endpoint"`
	Platforms   []PlatformReport `json:"platforms"`
}

func ScanPlatforms(m Manifest) PlatformsReport {
	return PlatformsReport{
		CPAEndpoint: m.Profiles.CPA.Endpoint,
		Platforms: []PlatformReport{
			scanCodex(m),
			scanClaude(m),
			scanXbot(m),
		},
	}
}

func scanCodex(m Manifest) PlatformReport {
	path := filepath.Join(m.Profiles.CPA.Home, "config.toml")
	r := PlatformReport{ID: "codex", Name: "Codex", Path: path, State: "missing", Visibility: "catalog"}
	var config struct {
		ModelProvider    string `toml:"model_provider"`
		ModelCatalogJSON string `toml:"model_catalog_json"`
		ModelProviders   map[string]struct {
			BaseURL string `toml:"base_url"`
		} `toml:"model_providers"`
	}
	if _, err := toml.DecodeFile(path, &config); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.State, r.Detail = "invalid", "Cannot read or parse Codex config; inspect the file locally"
		} else {
			r.Detail = "CPA Codex profile has not been initialized"
		}
		return r
	}
	provider, found := config.ModelProviders[config.ModelProvider]
	if !found || !sameEndpoint(provider.BaseURL, m.Profiles.CPA.Endpoint) {
		r.State, r.Detail = "unrelated", "Active Codex provider does not point to this CPA endpoint"
		return r
	}
	r.CPAEndpoint = true
	if config.ModelCatalogJSON == "" {
		r.State, r.Detail = "needs_setup", "CPA profile has no model catalog configured"
		return r
	}
	configured := expandPath(config.ModelCatalogJSON)
	if !filepath.IsAbs(config.ModelCatalogJSON) {
		configured = filepath.Join(m.Profiles.CPA.Home, config.ModelCatalogJSON)
	}
	if filepath.Clean(configured) != filepath.Clean(m.Profiles.CPA.ModelCatalogJSON) {
		r.State, r.Detail = "drift", "Codex uses a different model catalog: "+configured
		return r
	}
	if _, err := os.Stat(configured); err != nil {
		r.State, r.Detail = "needs_setup", "Configured model catalog is missing"
		return r
	}
	r.State, r.Detail, r.RestartNeeded = "ready", "Catalog visibility is configured; running sessions may need reconnect", true
	return r
}

func scanClaude(m Manifest) PlatformReport {
	path := m.Platforms.ClaudeSettingsJSON
	r := PlatformReport{ID: "claude", Name: "Claude Code", Path: path, State: "missing", Visibility: "availableModels"}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.State, r.Detail = "invalid", "Cannot read Claude settings; inspect file permissions locally"
		} else {
			r.Detail = "Claude settings have not been initialized"
		}
		return r
	}
	var settings struct {
		Env                    map[string]string `json:"env"`
		AvailableModels        []string          `json:"availableModels"`
		EnforceAvailableModels bool              `json:"enforceAvailableModels"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		r.State, r.Detail = "invalid", "Cannot parse Claude settings; inspect the file locally"
		return r
	}
	if !sameEndpoint(settings.Env["ANTHROPIC_BASE_URL"], m.Profiles.CPA.Endpoint) {
		r.State, r.Detail = "unrelated", "Claude Code points to another provider; settings will be preserved"
		return r
	}
	r.CPAEndpoint = true
	r.RestartNeeded = true
	if !settings.EnforceAvailableModels {
		r.State, r.Detail = "needs_setup", "Model restriction is not enabled"
		return r
	}
	r.State, r.Detail = "ready", fmt.Sprintf("%d models allowed by Claude settings; runtime authentication and protocol not verified", len(settings.AvailableModels))
	return r
}

func scanXbot(m Manifest) PlatformReport {
	r := PlatformReport{ID: "xbot", Name: "xbot", Path: m.Platforms.XbotDatabase, State: "missing", Visibility: "subscription database"}
	var config struct {
		LLM struct {
			BaseURL string `json:"base_url"`
		} `json:"llm"`
	}
	raw, configErr := os.ReadFile(m.Platforms.XbotConfigJSON)
	if configErr == nil {
		configErr = json.Unmarshal(raw, &config)
	}
	configCPA := configErr == nil && sameEndpoint(config.LLM.BaseURL, m.Profiles.CPA.Endpoint)
	if _, err := os.Stat(m.Platforms.XbotDatabase); err != nil {
		if configErr == nil {
			r.State, r.Detail = "needs_setup", "xbot subscription database is not present"
		} else {
			r.Detail = "xbot config and subscription database have not been initialized"
		}
		return r
	}
	snapshot, err := readXbotDB(m.Platforms.XbotDatabase, m.Profiles.CPA.Endpoint)
	if err != nil {
		r.State, r.Detail = "needs_adapter", "xbot database cannot be safely managed: "+err.Error()
		return r
	}
	if len(snapshot.subscriptions) == 0 {
		if configCPA {
			r.State, r.Detail = "needs_setup", "xbot fallback points to CPA, but no subscription does"
		} else {
			r.State, r.Detail = "unrelated", "No xbot subscription points to this CPA endpoint"
		}
		return r
	}
	r.CPAEndpoint = true
	r.RestartNeeded = true
	r.State, r.Detail = "ready", fmt.Sprintf("%d CPA subscription(s), %d model entries in xbot database; disabled models remain greyed out in xbot's picker", len(snapshot.subscriptions), len(snapshot.models))
	if !configCPA {
		r.Detail += "; fallback config is separate and will be preserved"
	}
	return r
}

func sameEndpoint(a, b string) bool {
	parse := func(value string) (string, bool) {
		u, err := url.Parse(strings.TrimRight(value, "/"))
		if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return "", false
		}
		return strings.ToLower(u.Scheme+"://"+u.Host) + strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1"), true
	}
	first, okA := parse(a)
	second, okB := parse(b)
	return okA && okB && first == second
}
