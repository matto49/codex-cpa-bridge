package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
)

const OwnerMarker = "# managed-by: codex-cpa-bridge"

type Manifest struct {
	Profiles struct {
		Official OfficialProfile `toml:"official"`
		CPA      CPAProfile      `toml:"cpa"`
	} `toml:"profiles"`
	SSH struct {
		CPA SSHEndpoint `toml:"cpa"`
	} `toml:"ssh"`
	Runtime   Runtime `toml:"runtime"`
	Platforms struct {
		ClaudeSettingsJSON string `toml:"claude_settings_json"`
		XbotConfigJSON     string `toml:"xbot_config_json"`
		XbotDatabase       string `toml:"xbot_database"`
	} `toml:"platforms"`
}

type OfficialProfile struct {
	Home               string `toml:"home"`
	Mode               string `toml:"mode"`
	Managed            bool   `toml:"managed"`
	EnforceNativeLogin bool   `toml:"enforce_native_login"`
}

type CPAProfile struct {
	Management       string   `toml:"management"`
	Home             string   `toml:"home"`
	Endpoint         string   `toml:"endpoint"`
	Model            string   `toml:"model"`
	ProviderName     string   `toml:"provider_name"`
	ModelCatalogJSON string   `toml:"model_catalog_json"`
	EnvKey           string   `toml:"env_key"`
	AuthCommand      string   `toml:"auth_command"`
	AuthArgs         []string `toml:"auth_args"`
}

type SSHEndpoint struct {
	Management   string `toml:"management"`
	Host         string `toml:"host"`
	Port         int    `toml:"port"`
	User         string `toml:"user"`
	Profile      string `toml:"profile"`
	IdentityFile string `toml:"identity_file"`
}

type Runtime struct {
	StateDir        string   `toml:"state_dir"`
	CodexBinary     string   `toml:"codex_binary"`
	CodexInstallDir string   `toml:"codex_install_dir"`
	AppServerArgs   []string `toml:"app_server_args"`
}

type DoctorReport struct {
	Official    OfficialReport `json:"official"`
	CPAProfile  CPAReport      `json:"cpa_profile"`
	CPABlackbox CPABlackbox    `json:"cpa_blackbox"`
	SSH         SSHReport      `json:"ssh"`
	Result      ResultReport   `json:"result"`
}

type OfficialReport struct {
	Home                 string   `json:"home"`
	Managed              bool     `json:"managed"`
	EnforceNativeLogin   bool     `json:"enforce_native_login"`
	ConfigPresent        bool     `json:"config_present"`
	AuthJSONPresent      bool     `json:"auth_json_present"`
	ContainsBridgeMarker bool     `json:"contains_bridge_marker"`
	ContainsCPAEndpoint  bool     `json:"contains_cpa_endpoint"`
	ContainsCPAProvider  bool     `json:"contains_cpa_provider"`
	ActiveModelProvider  string   `json:"active_model_provider,omitempty"`
	ActiveBaseURL        string   `json:"active_base_url,omitempty"`
	Problems             []string `json:"problems"`
}

type CPAReport struct {
	Management    string `json:"management"`
	Home          string `json:"home"`
	Endpoint      string `json:"endpoint"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	AuthSource    string `json:"auth_source"`
	ConfigPresent bool   `json:"config_present"`
	ManagedConfig bool   `json:"managed_config"`
	RenderMatches bool   `json:"render_matches"`
}

type CPABlackbox struct {
	ModelsOK       bool             `json:"models_ok"`
	ModelsStatus   string           `json:"models_status"`
	ModelSample    []string         `json:"model_sample"`
	ModelsDetail   string           `json:"models_detail,omitempty"`
	ResponsesProbe *HTTPProbeReport `json:"responses_probe"`
}

type HTTPProbeReport struct {
	OK     bool   `json:"ok"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type SSHReport struct {
	Target                string `json:"target"`
	IdentityFile          string `json:"identity_file,omitempty"`
	UserExists            bool   `json:"user_exists"`
	PortOpen              bool   `json:"port_open"`
	BatchProbeOK          bool   `json:"batch_probe_ok"`
	RemoteCodexHome       string `json:"remote_CODEX_HOME,omitempty"`
	RemoteHomeNameMatches bool   `json:"remote_home_name_matches"`
}

type ResultReport struct {
	OK          bool `json:"ok"`
	Issues      int  `json:"issues"`
	PolicyOK    bool `json:"policy_ok"`
	BridgeReady bool `json:"bridge_ready"`
}

type PlanReport struct {
	CPAConfig   FilePlan     `json:"cpa_config"`
	StateFiles  []FilePlan   `json:"state_files"`
	Summary     PlanSummary  `json:"summary"`
	NextActions []string     `json:"next_actions"`
	Profiles    PlanProfiles `json:"profiles"`
	SSH         PlanSSH      `json:"ssh"`
}

type FilePlan struct {
	Path       string `json:"path"`
	Action     string `json:"action"`
	Exists     bool   `json:"exists"`
	Managed    bool   `json:"managed"`
	Blocked    bool   `json:"blocked"`
	WillBackup bool   `json:"will_backup"`
}

type PlanSummary struct {
	Creates int `json:"creates"`
	Updates int `json:"updates"`
	Blocked int `json:"blocked"`
}

type PlanProfiles struct {
	OfficialHome string `json:"official_home"`
	CPAHome      string `json:"cpa_home"`
	Endpoint     string `json:"endpoint"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
}

type PlanSSH struct {
	Management   string `json:"management"`
	Target       string `json:"target"`
	IdentityFile string `json:"identity_file,omitempty"`
	StateDir     string `json:"state_dir"`
}

func DefaultManifest() Manifest {
	username := ""
	if current, err := user.Current(); err == nil {
		username = current.Username
	}
	var m Manifest
	m.Profiles.Official = OfficialProfile{Home: "~/.codex", Mode: "native", Managed: false, EnforceNativeLogin: true}
	m.Profiles.CPA = CPAProfile{Management: "managed", Home: "~/.codex-cpa", Endpoint: "http://127.0.0.1:8317/v1", Model: "gpt-5.6-sol", ProviderName: "cpa-local"}
	m.SSH.CPA = SSHEndpoint{Management: "managed", Host: "127.0.0.1", Port: 2222, User: username, Profile: "cpa"}
	m.Runtime = Runtime{StateDir: "~/.codex-cpa-bridge", CodexBinary: "codex", AppServerArgs: []string{"app-server"}}
	m.normalize()
	return m
}

func LoadManifest(path string) (Manifest, error) {
	m := DefaultManifest()
	if path == "" {
		path = "bridge.toml"
	}
	originalPath := path
	path = expandPath(path)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) && originalPath == "bridge.toml" {
			return m, nil
		}
		return Manifest{}, fmt.Errorf("cannot read manifest %s: %w", path, err)
	}
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return Manifest{}, err
	}
	m.normalize()
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (m *Manifest) normalize() {
	m.Profiles.Official.Home = expandPath(m.Profiles.Official.Home)
	m.Profiles.CPA.Home = expandPath(m.Profiles.CPA.Home)
	if m.Profiles.CPA.Management == "" {
		m.Profiles.CPA.Management = "managed"
	}
	m.Profiles.CPA.Endpoint = strings.TrimRight(m.Profiles.CPA.Endpoint, "/")
	if m.Profiles.Official.Mode == "" {
		m.Profiles.Official.Mode = "native"
	}
	if m.Profiles.CPA.Model == "" {
		m.Profiles.CPA.Model = "gpt-5.6-sol"
	}
	if m.Profiles.CPA.ProviderName == "" {
		m.Profiles.CPA.ProviderName = "cpa-local"
	}
	if m.Profiles.CPA.ModelCatalogJSON != "" {
		m.Profiles.CPA.ModelCatalogJSON = expandPath(m.Profiles.CPA.ModelCatalogJSON)
	}
	if m.SSH.CPA.Host == "" {
		m.SSH.CPA.Host = "127.0.0.1"
	}
	if m.SSH.CPA.Management == "" {
		m.SSH.CPA.Management = "managed"
	}
	if m.SSH.CPA.Port == 0 {
		m.SSH.CPA.Port = 2222
	}
	if m.SSH.CPA.IdentityFile != "" {
		m.SSH.CPA.IdentityFile = expandPath(m.SSH.CPA.IdentityFile)
	}
	if m.Runtime.StateDir == "" {
		m.Runtime.StateDir = "~/.codex-cpa-bridge"
	}
	m.Runtime.StateDir = expandPath(m.Runtime.StateDir)
	if m.SSH.CPA.IdentityFile == "" && m.SSH.CPA.Management != "external" {
		candidate := bridgeClientIdentityPath(*m)
		if regularFile(candidate) && regularFile(candidate+".pub") {
			m.SSH.CPA.IdentityFile = candidate
		}
	}
	if m.Platforms.ClaudeSettingsJSON == "" {
		m.Platforms.ClaudeSettingsJSON = "~/.claude/settings.json"
	}
	if m.Platforms.XbotConfigJSON == "" {
		m.Platforms.XbotConfigJSON = "~/.xbot/config.json"
	}
	if m.Platforms.XbotDatabase == "" {
		m.Platforms.XbotDatabase = "~/.xbot/xbot.db"
	}
	m.Platforms.ClaudeSettingsJSON = expandPath(m.Platforms.ClaudeSettingsJSON)
	m.Platforms.XbotConfigJSON = expandPath(m.Platforms.XbotConfigJSON)
	m.Platforms.XbotDatabase = expandPath(m.Platforms.XbotDatabase)
	if m.Runtime.CodexBinary == "" {
		m.Runtime.CodexBinary = "codex"
	}
	if len(m.Runtime.AppServerArgs) == 0 {
		m.Runtime.AppServerArgs = []string{"app-server"}
	}
}

func (m Manifest) validate() error {
	for name, mode := range map[string]string{
		"profiles.cpa.management": m.Profiles.CPA.Management,
		"ssh.cpa.management":      m.SSH.CPA.Management,
	} {
		if mode != "managed" && mode != "external" {
			return fmt.Errorf("%s must be managed or external", name)
		}
	}
	if m.Profiles.CPA.EnvKey != "" && !isShellName(m.Profiles.CPA.EnvKey) {
		return fmt.Errorf("profiles.cpa.env_key must be a valid environment variable name")
	}
	return nil
}

func expandPath(value string) string {
	value = os.ExpandEnv(value)
	if strings.HasPrefix(value, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	if abs, err := filepath.Abs(value); err == nil {
		return abs
	}
	return value
}

func RenderCodexConfig(m Manifest) string {
	cpa := m.Profiles.CPA
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", OwnerMarker)
	b.WriteString("# This file configures an isolated CPA-backed Codex profile.\n")
	b.WriteString("# Do not place secrets here. CPA credentials belong to CPA's own environment.\n\n")
	fmt.Fprintf(&b, "model = %s\n", tomlQuote(cpa.Model))
	fmt.Fprintf(&b, "model_provider = %s\n", tomlQuote(cpa.ProviderName))
	if cpa.ModelCatalogJSON != "" {
		fmt.Fprintf(&b, "model_catalog_json = %s\n", tomlQuote(cpa.ModelCatalogJSON))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "[model_providers.%s]\n", tomlKey(cpa.ProviderName))
	fmt.Fprintf(&b, "name = %s\n", tomlQuote("CPA Local"))
	fmt.Fprintf(&b, "base_url = %s\n", tomlQuote(cpa.Endpoint))
	b.WriteString("wire_api = \"responses\"\n")
	b.WriteString("requires_openai_auth = false\n")
	if cpa.EnvKey != "" {
		fmt.Fprintf(&b, "env_key = %s\n", tomlQuote(cpa.EnvKey))
	}
	if cpa.AuthCommand != "" {
		fmt.Fprintf(&b, "\n[model_providers.%s.auth]\n", tomlKey(cpa.ProviderName))
		fmt.Fprintf(&b, "command = %s\n", tomlQuote(cpa.AuthCommand))
		if len(cpa.AuthArgs) > 0 {
			fmt.Fprintf(&b, "args = %s\n", tomlArray(cpa.AuthArgs))
		}
	}
	return b.String()
}

func WriteRenderedConfig(m Manifest, force bool) (string, error) {
	target := filepath.Join(m.Profiles.CPA.Home, "config.toml")
	rendered := []byte(RenderCodexConfig(m))
	if existing, err := os.ReadFile(target); err == nil {
		if !bytes.Contains(existing, []byte(OwnerMarker)) && !force {
			return "", fmt.Errorf("refusing to overwrite unmanaged config: %s", target)
		}
		backup := fmt.Sprintf("%s.bak.%d", target, time.Now().Unix())
		if err := copyFile(target, backup); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return target, atomicWrite(target, rendered, 0o644)
}

func RenderWrapper(m Manifest) string {
	var exports []string
	exports = append(exports, "export CODEX_HOME="+shellQuote(m.Profiles.CPA.Home))
	exports = append(exports, "export CODEX_CPA_BRIDGE=1")
	if m.Profiles.CPA.EnvKey != "" && m.Profiles.CPA.AuthCommand != "" {
		command := []string{shellQuote(m.Profiles.CPA.AuthCommand)}
		for _, arg := range m.Profiles.CPA.AuthArgs {
			command = append(command, shellQuote(arg))
		}
		exports = append(exports, fmt.Sprintf("export %s=\"$(%s)\"", m.Profiles.CPA.EnvKey, strings.Join(command, " ")))
	}
	if m.Runtime.CodexInstallDir != "" {
		exports = append(exports, "export CODEX_INSTALL_DIR="+shellQuote(m.Runtime.CodexInstallDir))
	}
	args := make([]string, 0, len(m.Runtime.AppServerArgs))
	for _, arg := range m.Runtime.AppServerArgs {
		args = append(args, shellQuote(arg))
	}
	return fmt.Sprintf(`#!/bin/sh
# %s
set -eu
%s
if [ "${SSH_ORIGINAL_COMMAND:-}" = "__codex_cpa_bridge_probe__" ]; then
  printf '%%s\n' "$CODEX_HOME"
  exit 0
fi
exec %s %s "$@"
`, strings.TrimPrefix(OwnerMarker, "# "), strings.Join(exports, "\n"), shellQuote(m.Runtime.CodexBinary), strings.Join(args, " "))
}

func RenderSSHDConfig(m Manifest) string {
	sshDir := filepath.Join(m.Runtime.StateDir, "ssh")
	return fmt.Sprintf(`# %s
HostKey %s
AuthorizedKeysFile %s
ListenAddress %s
Port %d
PasswordAuthentication no
KbdInteractiveAuthentication no
ChallengeResponseAuthentication no
PubkeyAuthentication yes
PermitRootLogin no
AllowTcpForwarding no
X11Forwarding no
PermitTunnel no
AllowAgentForwarding no
PermitTTY yes
UsePAM no
AllowUsers %s
PidFile %s
ForceCommand %s
`, strings.TrimPrefix(OwnerMarker, "# "), filepath.Join(sshDir, "ssh_host_ed25519_key"), filepath.Join(sshDir, "authorized_keys"), m.SSH.CPA.Host, m.SSH.CPA.Port, m.SSH.CPA.User, filepath.Join(sshDir, "sshd.pid"), filepath.Join(m.Runtime.StateDir, "bin", "codex-cpa-wrapper"))
}

func RenderStartSSHD(m Manifest) string {
	sshDir := filepath.Join(m.Runtime.StateDir, "ssh")
	hostKey := filepath.Join(sshDir, "ssh_host_ed25519_key")
	return fmt.Sprintf(`#!/bin/sh
# %s
set -eu
mkdir -p %s
if [ ! -f %s ]; then
  ssh-keygen -t ed25519 -N '' -f %s >/dev/null
fi
exec /usr/sbin/sshd -D -e -f %s
`, strings.TrimPrefix(OwnerMarker, "# "), shellQuote(sshDir), shellQuote(hostKey), shellQuote(hostKey), shellQuote(filepath.Join(sshDir, "sshd_config")))
}

func RenderLaunchdPlist(m Manifest) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!-- %s -->
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>local.codex-cpa-bridge.sshd</string>
  <key>ProgramArguments</key>
  <array><string>%s</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, strings.TrimPrefix(OwnerMarker, "# "), filepath.Join(m.Runtime.StateDir, "bin", "start-sshd.sh"), filepath.Join(m.Runtime.StateDir, "logs", "sshd.out.log"), filepath.Join(m.Runtime.StateDir, "logs", "sshd.err.log"))
}

func RenderSystemdUnit(m Manifest) string {
	return fmt.Sprintf(`# %s
[Unit]
Description=Codex CPA bridge loopback sshd
After=network.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`, strings.TrimPrefix(OwnerMarker, "# "), filepath.Join(m.Runtime.StateDir, "bin", "start-sshd.sh"))
}

func TemplateItems(m Manifest) map[string]string {
	state := m.Runtime.StateDir
	return map[string]string{
		filepath.Join(state, "bin", "codex-cpa-wrapper"):                     RenderWrapper(m),
		filepath.Join(state, "bin", "start-sshd.sh"):                         RenderStartSSHD(m),
		filepath.Join(state, "launchd", "local.codex-cpa-bridge.sshd.plist"): RenderLaunchdPlist(m),
		filepath.Join(state, "ssh", "sshd_config"):                           RenderSSHDConfig(m),
		filepath.Join(state, "systemd", "codex-cpa-bridge-sshd.service"):     RenderSystemdUnit(m),
	}
}

func PrintTemplates(w io.Writer, m Manifest, name string) error {
	items := TemplateItems(m)
	found := false
	for _, path := range sortedKeys(items) {
		if name != "" && filepath.Base(path) != name && !strings.Contains(path, name) {
			continue
		}
		found = true
		fmt.Fprintf(w, "### %s\n%s\n", path, items[path])
	}
	if !found && name != "" {
		return fmt.Errorf("unknown template: %s", name)
	}
	return nil
}

func InstallTemplates(w io.Writer, m Manifest, write bool, force bool, authorizedKeyFile string) (int, error) {
	items := TemplateItems(m)
	authKeyTarget := filepath.Join(m.Runtime.StateDir, "ssh", "authorized_keys")
	if write || authorizedKeyFile != "" {
		resolvedKey, err := ResolveAuthorizedKeyFile(m, authorizedKeyFile)
		if err != nil {
			return 1, err
		}
		key, err := os.ReadFile(resolvedKey)
		if err != nil {
			return 1, err
		}
		items[authKeyTarget] = fmt.Sprintf("# %s\n%s\n", strings.TrimPrefix(OwnerMarker, "# "), strings.TrimSpace(string(key)))
	} else if _, ok := items[authKeyTarget]; !ok {
		items[authKeyTarget] = fmt.Sprintf("# %s\n# public key resolved when files are written\n", strings.TrimPrefix(OwnerMarker, "# "))
	}

	blocked := false
	fmt.Fprintln(w, "install plan")
	for _, path := range sortedKeys(items) {
		status := installStatus(path, force)
		fmt.Fprintf(w, "  %s: %s\n", status, path)
		if status == "blocked" {
			blocked = true
			fmt.Fprintln(w, "  problem: existing file is not managed by codex-cpa-bridge; pass --force after review")
		}
	}
	if blocked && write {
		return 1, nil
	}
	if !write {
		fmt.Fprintln(w, "dry-run: pass --write to create these files")
		return 0, nil
	}
	for _, path := range sortedKeys(items) {
		if status := installStatus(path, force); status == "blocked" {
			return 1, fmt.Errorf("refusing to overwrite unmanaged file: %s", path)
		}
		if _, err := os.Stat(path); err == nil {
			_ = copyFile(path, fmt.Sprintf("%s.bak.%d", path, time.Now().Unix()))
		}
		mode := os.FileMode(0o600)
		if strings.HasSuffix(path, "codex-cpa-wrapper") || strings.HasSuffix(path, "start-sshd.sh") {
			mode = 0o755
		}
		if err := atomicWrite(path, []byte(items[path]), mode); err != nil {
			return 1, err
		}
	}
	_ = os.MkdirAll(filepath.Join(m.Runtime.StateDir, "logs"), 0o755)
	_ = os.Chmod(filepath.Join(m.Runtime.StateDir, "ssh"), 0o700)
	fmt.Fprintf(w, "installed into %s\n", m.Runtime.StateDir)
	return 0, nil
}

type SetupOptions struct {
	Force             bool
	AuthorizedKeyFile string
	GenerateBridgeKey bool
	Start             bool
}

func Setup(w io.Writer, m Manifest, options SetupOptions) error {
	if options.GenerateBridgeKey && options.AuthorizedKeyFile != "" {
		return errors.New("--generate-bridge-key cannot be combined with --authorized-key-file")
	}
	if options.GenerateBridgeKey && m.SSH.CPA.Management == "external" {
		return errors.New("--generate-bridge-key requires a managed SSH endpoint")
	}
	if options.GenerateBridgeKey && m.SSH.CPA.Management != "external" {
		preflight, err := CollectPlan(m, options.Force, options.AuthorizedKeyFile)
		if err != nil {
			return err
		}
		if preflight.Summary.Blocked > 0 {
			PrintPlan(w, preflight)
			return fmt.Errorf("setup blocked by %d unmanaged file(s); review the plan or rerun with --force", preflight.Summary.Blocked)
		}
	}
	publicKey := ""
	if m.SSH.CPA.Management != "external" {
		var err error
		publicKey, err = ResolveAuthorizedKeyFile(m, options.AuthorizedKeyFile)
		if err != nil && options.GenerateBridgeKey && m.SSH.CPA.IdentityFile == "" {
			publicKey, err = generateBridgeClientIdentity(m)
			if err == nil {
				m.SSH.CPA.IdentityFile = strings.TrimSuffix(publicKey, ".pub")
			}
		}
		if err != nil {
			return err
		}
		if m.SSH.CPA.IdentityFile == "" {
			candidate := strings.TrimSuffix(publicKey, ".pub")
			if fileExists(candidate) {
				m.SSH.CPA.IdentityFile = candidate
			}
		}
	}
	plan, err := CollectPlan(m, options.Force, publicKey)
	if err != nil {
		return err
	}
	if plan.Summary.Blocked > 0 {
		PrintPlan(w, plan)
		return fmt.Errorf("setup blocked by %d unmanaged file(s); review the plan or rerun with --force", plan.Summary.Blocked)
	}

	fmt.Fprintln(w, "codex-cpa-bridge setup")
	configPath := filepath.Join(m.Profiles.CPA.Home, "config.toml")
	if m.Profiles.CPA.Management == "external" {
		if !fileExists(configPath) {
			return fmt.Errorf("external CPA config is missing: %s", configPath)
		}
		fmt.Fprintf(w, "using external CPA profile config without modifying it: %s\n", configPath)
	} else {
		var err error
		configPath, err = WriteRenderedConfig(m, options.Force)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "configured isolated Codex profile: %s\n", configPath)
	}

	if m.SSH.CPA.Management != "external" {
		rc, err := InstallTemplates(w, m, true, options.Force, publicKey)
		if err != nil {
			return err
		}
		if rc != 0 {
			return fmt.Errorf("bridge file installation failed with status %d", rc)
		}
	} else {
		fmt.Fprintln(w, "using external SSH endpoint without modifying its service files")
	}

	if options.Start {
		if m.SSH.CPA.Management != "external" {
			if err := Up(w, m, false); err != nil {
				return err
			}
		}
		if err := WaitForSSH(m, 5*time.Second); err != nil {
			if m.SSH.CPA.Management != "external" {
				_ = Down(io.Discard, m)
			}
			return fmt.Errorf("sshd started but readiness verification failed: %w; configuration was retained for inspection", err)
		}
		fmt.Fprintf(w, "verified SSH endpoint: %s@%s:%d -> %s\n", m.SSH.CPA.User, m.SSH.CPA.Host, m.SSH.CPA.Port, m.Profiles.CPA.Home)
	} else {
		fmt.Fprintln(w, "sshd start skipped")
	}
	return nil
}

func ResolveAuthorizedKeyFile(m Manifest, explicit string) (string, error) {
	var candidates []string
	if explicit != "" {
		candidates = append(candidates, expandPath(explicit))
	} else if m.SSH.CPA.IdentityFile != "" {
		if !regularFile(m.SSH.CPA.IdentityFile) {
			return "", fmt.Errorf("configured SSH identity is not a regular file: %s", m.SSH.CPA.IdentityFile)
		}
		candidates = append(candidates, m.SSH.CPA.IdentityFile+".pub")
	} else {
		if dedicated := bridgeClientIdentityPath(m); regularFile(dedicated) && regularFile(dedicated+".pub") {
			candidates = append(candidates, dedicated+".pub")
		}
		home, _ := os.UserHomeDir()
		for _, name := range []string{"id_ed25519.pub", "id_ed25519_byted.pub", "id_rsa.pub"} {
			candidates = append(candidates, filepath.Join(home, ".ssh", name))
		}
	}
	for _, path := range candidates {
		if explicit == "" && !regularFile(strings.TrimSuffix(path, ".pub")) {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			if explicit != "" {
				return "", fmt.Errorf("cannot read SSH public key %s: %w", path, err)
			}
			continue
		}
		fields := strings.Fields(string(raw))
		if len(fields) >= 2 && (strings.HasPrefix(fields[0], "ssh-") || strings.HasPrefix(fields[0], "ecdsa-")) {
			return path, nil
		}
		if explicit != "" {
			return "", fmt.Errorf("invalid or empty SSH public key: %s", path)
		}
	}
	return "", errors.New("no usable SSH public key found; create ~/.ssh/id_ed25519.pub or pass --authorized-key-file")
}

func WaitForSSH(m Manifest, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var detail string
	for time.Now().Before(deadline) {
		if portOpen(m.SSH.CPA.Host, m.SSH.CPA.Port, 300*time.Millisecond) {
			if ok, output := runSSHProbe(m, 2*time.Second); ok {
				if output != m.Profiles.CPA.Home {
					return fmt.Errorf("probe returned CODEX_HOME %q, want %q", output, m.Profiles.CPA.Home)
				}
				return nil
			} else {
				detail = output
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if detail != "" {
		return errors.New(detail)
	}
	return fmt.Errorf("port %s did not become ready within %s", net.JoinHostPort(m.SSH.CPA.Host, strconv.Itoa(m.SSH.CPA.Port)), timeout)
}

func CollectPlan(m Manifest, force bool, authorizedKeyFile string) (PlanReport, error) {
	items := map[string]string{}
	if m.SSH.CPA.Management != "external" {
		items = TemplateItems(m)
	}
	authKeyTarget := filepath.Join(m.Runtime.StateDir, "ssh", "authorized_keys")
	if authorizedKeyFile != "" {
		if _, err := os.ReadFile(expandPath(authorizedKeyFile)); err != nil {
			return PlanReport{}, err
		}
	}
	if m.SSH.CPA.Management != "external" {
		items[authKeyTarget] = fmt.Sprintf("# %s\n", strings.TrimPrefix(OwnerMarker, "# "))
	}
	cpaPlan := filePlan(filepath.Join(m.Profiles.CPA.Home, "config.toml"), force)
	if m.Profiles.CPA.Management == "external" {
		cpaPlan = externalFilePlan(filepath.Join(m.Profiles.CPA.Home, "config.toml"))
	}

	report := PlanReport{
		CPAConfig: cpaPlan,
		Profiles: PlanProfiles{
			OfficialHome: m.Profiles.Official.Home,
			CPAHome:      m.Profiles.CPA.Home,
			Endpoint:     m.Profiles.CPA.Endpoint,
			Provider:     m.Profiles.CPA.ProviderName,
			Model:        m.Profiles.CPA.Model,
		},
		SSH: PlanSSH{
			Management:   m.SSH.CPA.Management,
			Target:       fmt.Sprintf("%s@%s:%d", m.SSH.CPA.User, m.SSH.CPA.Host, m.SSH.CPA.Port),
			IdentityFile: m.SSH.CPA.IdentityFile,
			StateDir:     m.Runtime.StateDir,
		},
	}
	for _, path := range sortedKeys(items) {
		report.StateFiles = append(report.StateFiles, filePlan(path, force))
	}
	for _, item := range append([]FilePlan{report.CPAConfig}, report.StateFiles...) {
		if item.Blocked {
			report.Summary.Blocked++
			continue
		}
		switch item.Action {
		case "create":
			report.Summary.Creates++
		case "update", "overwrite":
			report.Summary.Updates++
		}
	}
	report.NextActions = []string{
		"doctor --json verifies policy_ok and bridge_ready separately",
	}
	if m.Profiles.CPA.Management != "external" {
		report.NextActions = append([]string{"render --write writes only the isolated CPA profile config"}, report.NextActions...)
	} else {
		report.NextActions = append([]string{"external CPA config is validated but never rewritten"}, report.NextActions...)
	}
	if m.SSH.CPA.Management != "external" {
		report.NextActions = append([]string{"install --write installs bridge-owned wrapper and sshd files", "up starts the loopback sshd for local validation"}, report.NextActions...)
	} else {
		report.NextActions = append([]string{"external SSH endpoint is validated but never started or stopped"}, report.NextActions...)
	}
	if report.Summary.Blocked > 0 {
		report.NextActions = append([]string{"review blocked unmanaged files before passing --force"}, report.NextActions...)
	}
	return report, nil
}

func PrintPlan(w io.Writer, report PlanReport) {
	fmt.Fprintln(w, "codex-cpa-bridge plan")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "profiles")
	fmt.Fprintf(w, "  official_home: %s\n", report.Profiles.OfficialHome)
	fmt.Fprintf(w, "  cpa_home: %s\n", report.Profiles.CPAHome)
	fmt.Fprintf(w, "  endpoint: %s\n", report.Profiles.Endpoint)
	fmt.Fprintf(w, "  provider: %s\n", report.Profiles.Provider)
	fmt.Fprintf(w, "  model: %s\n", report.Profiles.Model)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "cpa config")
	printFilePlan(w, report.CPAConfig)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "state files")
	fmt.Fprintf(w, "  management: %s\n", report.SSH.Management)
	for _, item := range report.StateFiles {
		printFilePlan(w, item)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "summary: %d create(s), %d update(s), %d blocked\n", report.Summary.Creates, report.Summary.Updates, report.Summary.Blocked)
	for _, action := range report.NextActions {
		fmt.Fprintf(w, "next: %s\n", action)
	}
}

func printFilePlan(w io.Writer, item FilePlan) {
	fmt.Fprintf(w, "  %s: %s", item.Action, item.Path)
	if item.WillBackup {
		fmt.Fprint(w, " (backup first)")
	}
	if item.Blocked {
		fmt.Fprint(w, " (unmanaged)")
	}
	fmt.Fprintln(w)
}

func filePlan(path string, force bool) FilePlan {
	exists := fileExists(path)
	managed := exists && pathIsManaged(path)
	action := "create"
	blocked := false
	if exists && managed {
		action = "update"
	} else if exists && force {
		action = "overwrite"
	} else if exists {
		action = "blocked"
		blocked = true
	}
	return FilePlan{Path: path, Action: action, Exists: exists, Managed: managed, Blocked: blocked, WillBackup: exists && !blocked}
}

func externalFilePlan(path string) FilePlan {
	exists := fileExists(path)
	if !exists {
		return FilePlan{Path: path, Action: "missing", Exists: false, Blocked: true}
	}
	return FilePlan{Path: path, Action: "external", Exists: true, Managed: pathIsManaged(path)}
}

func installStatus(path string, force bool) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "create"
	}
	if pathIsManaged(path) {
		return "update"
	}
	if force {
		return "overwrite"
	}
	return "blocked"
}

func pathIsManaged(path string) bool {
	content, err := os.ReadFile(path)
	return err == nil && (bytes.Contains(content, []byte(OwnerMarker)) || bytes.Contains(content, []byte(strings.TrimPrefix(OwnerMarker, "# "))))
}

func Up(w io.Writer, m Manifest, foreground bool) error {
	if m.SSH.CPA.Management == "external" {
		if err := WaitForSSH(m, 5*time.Second); err != nil {
			return fmt.Errorf("external SSH endpoint is not ready: %w", err)
		}
		fmt.Fprintf(w, "verified external SSH endpoint: %s@%s:%d\n", m.SSH.CPA.User, m.SSH.CPA.Host, m.SSH.CPA.Port)
		return nil
	}
	start := filepath.Join(m.Runtime.StateDir, "bin", "start-sshd.sh")
	if _, err := os.Stat(start); err != nil {
		return fmt.Errorf("start script is missing; run 'bridge install --write' first")
	}
	pidPath := filepath.Join(m.Runtime.StateDir, "ssh", "sshd.pid")
	if pid := readPID(pidPath); pid > 0 && processAlive(pid) {
		fmt.Fprintf(w, "already running: pid %d\n", pid)
		return nil
	}
	if portOpen(m.SSH.CPA.Host, m.SSH.CPA.Port, 300*time.Millisecond) {
		return fmt.Errorf("%s is already in use by a service not managed by this bridge", net.JoinHostPort(m.SSH.CPA.Host, strconv.Itoa(m.SSH.CPA.Port)))
	}
	if foreground {
		return syscall.Exec(start, []string{start}, os.Environ())
	}
	logDir := filepath.Join(m.Runtime.StateDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(logDir, "sshd.out.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	errFile, err := os.OpenFile(filepath.Join(logDir, "sshd.err.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer errFile.Close()
	cmd := exec.Command(start)
	cmd.Stdout = out
	cmd.Stderr = errFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Fprintf(w, "started sshd supervisor pid %d; check %s\n", cmd.Process.Pid, logDir)
	return nil
}

func Down(w io.Writer, m Manifest) int {
	if m.SSH.CPA.Management == "external" {
		fmt.Fprintln(w, "external SSH endpoint is not managed; nothing was stopped")
		return 0
	}
	pidPath := filepath.Join(m.Runtime.StateDir, "ssh", "sshd.pid")
	pid := readPID(pidPath)
	if pid <= 0 {
		fmt.Fprintln(w, "not running: pid file missing")
		return 0
	}
	if !processAlive(pid) {
		fmt.Fprintf(w, "not running: stale pid %d\n", pid)
		return 0
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if !processAlive(pid) {
			fmt.Fprintf(w, "stopped sshd pid %d\n", pid)
			return 0
		}
	}
	fmt.Fprintf(w, "sent TERM to sshd pid %d, but it is still running\n", pid)
	return 1
}

func ShowLogs(w io.Writer, m Manifest, lines int) error {
	if lines <= 0 {
		lines = 80
	}
	for _, path := range []string{filepath.Join(m.Runtime.StateDir, "logs", "sshd.err.log"), filepath.Join(m.Runtime.StateDir, "logs", "sshd.out.log")} {
		fmt.Fprintf(w, "### %s\n", path)
		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(w, "missing")
			continue
		}
		parts := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
		if len(parts) > lines {
			parts = parts[len(parts)-lines:]
		}
		for _, line := range parts {
			fmt.Fprintln(w, line)
		}
	}
	return nil
}

func RollbackCPAConfig(w io.Writer, m Manifest, write bool) int {
	pattern := filepath.Join(m.Profiles.CPA.Home, "config.toml.bak.*")
	backups, _ := filepath.Glob(pattern)
	sort.Strings(backups)
	if len(backups) == 0 {
		fmt.Fprintf(w, "no backups found under %s\n", m.Profiles.CPA.Home)
		return 1
	}
	current := filepath.Join(m.Profiles.CPA.Home, "config.toml")
	chosen := backups[len(backups)-1]
	fmt.Fprintf(w, "rollback plan: %s -> %s\n", chosen, current)
	if !write {
		fmt.Fprintln(w, "dry-run: pass --write to restore this backup")
		return 0
	}
	if _, err := os.Stat(current); err == nil {
		_ = copyFile(current, fmt.Sprintf("%s.pre-rollback.%d", current, time.Now().Unix()))
	}
	if err := copyFile(chosen, current); err != nil {
		fmt.Fprintf(w, "rollback failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(w, "restored %s\n", current)
	return 0
}

func CollectDoctorReport(m Manifest, includeResponsesProbe bool) (DoctorReport, int) {
	failures := 0
	officialConfig := filepath.Join(m.Profiles.Official.Home, "config.toml")
	officialAuth := filepath.Join(m.Profiles.Official.Home, "auth.json")
	report := DoctorReport{}
	report.Official = OfficialReport{
		Home:               m.Profiles.Official.Home,
		Managed:            m.Profiles.Official.Managed,
		EnforceNativeLogin: m.Profiles.Official.EnforceNativeLogin,
		ConfigPresent:      fileExists(officialConfig),
		AuthJSONPresent:    fileExists(officialAuth),
		Problems:           []string{},
	}
	if m.Profiles.Official.Managed {
		failures++
		report.Official.Problems = append(report.Official.Problems, "official profile must not be managed by this tool")
	}
	if content, err := os.ReadFile(officialConfig); err == nil {
		text := string(content)
		report.Official.ContainsBridgeMarker = strings.Contains(text, OwnerMarker)
		report.Official.ContainsCPAEndpoint = strings.Contains(text, m.Profiles.CPA.Endpoint)
		report.Official.ContainsCPAProvider = strings.Contains(text, m.Profiles.CPA.ProviderName)
		report.Official.ActiveModelProvider, report.Official.ActiveBaseURL = activeProviderBaseURL(officialConfig)
		if report.Official.ContainsBridgeMarker {
			failures++
			report.Official.Problems = append(report.Official.Problems, "official config contains codex-cpa-bridge ownership marker")
		}
		if m.Profiles.Official.EnforceNativeLogin && (report.Official.ActiveModelProvider == m.Profiles.CPA.ProviderName || report.Official.ActiveBaseURL == m.Profiles.CPA.Endpoint) {
			failures++
			report.Official.Problems = append(report.Official.Problems, "official active model provider points at the CPA bridge target")
		}
	}

	cpaConfig := filepath.Join(m.Profiles.CPA.Home, "config.toml")
	token, authSource := loadAuthToken(m.Profiles.CPA)
	cpaContent, _ := os.ReadFile(cpaConfig)
	cpaProvider := m.Profiles.CPA.ProviderName
	cpaModel := m.Profiles.CPA.Model
	if m.Profiles.CPA.Management == "external" {
		if actualModel, actualProvider := externalCPAIdentity(cpaConfig); actualProvider != "" {
			cpaProvider = actualProvider
			if actualModel != "" {
				cpaModel = actualModel
			}
		}
	}
	report.CPAProfile = CPAReport{
		Management:    m.Profiles.CPA.Management,
		Home:          m.Profiles.CPA.Home,
		Endpoint:      m.Profiles.CPA.Endpoint,
		Provider:      cpaProvider,
		Model:         cpaModel,
		AuthSource:    authSource,
		ConfigPresent: fileExists(cpaConfig),
		ManagedConfig: bytes.Contains(cpaContent, []byte(OwnerMarker)),
		RenderMatches: cpaConfigMatches(m, cpaConfig, cpaContent),
	}
	if !report.CPAProfile.ConfigPresent || !report.CPAProfile.RenderMatches {
		failures++
	}

	modelsOK, modelsStatus, modelsBody := httpJSON(m.Profiles.CPA.Endpoint+"/models", nil, token, 3*time.Second)
	report.CPABlackbox.ModelsOK = modelsOK
	report.CPABlackbox.ModelsStatus = modelsStatus
	if !modelsOK {
		failures++
		report.CPABlackbox.ModelsDetail = summarize(modelsBody)
	} else {
		report.CPABlackbox.ModelSample = modelSample(modelsBody)
	}
	if includeResponsesProbe {
		payload := map[string]any{"model": report.CPAProfile.Model, "input": "Reply with exactly: bridge-ok", "max_output_tokens": 16}
		ok, status, body := httpJSON(m.Profiles.CPA.Endpoint+"/responses", payload, token, 15*time.Second)
		report.CPABlackbox.ResponsesProbe = &HTTPProbeReport{OK: ok, Status: status}
		if !ok {
			failures++
			report.CPABlackbox.ResponsesProbe.Detail = summarize(body)
		}
	}

	portOpen := portOpen(m.SSH.CPA.Host, m.SSH.CPA.Port, time.Second)
	sshOK := false
	sshDetail := ""
	if portOpen {
		sshOK, sshDetail = runSSHProbe(m, 5*time.Second)
		if !sshOK {
			failures++
		}
	} else {
		failures++
	}
	report.SSH = SSHReport{
		Target:                fmt.Sprintf("%s@%s:%d", m.SSH.CPA.User, m.SSH.CPA.Host, m.SSH.CPA.Port),
		IdentityFile:          m.SSH.CPA.IdentityFile,
		UserExists:            userExists(m.SSH.CPA.User),
		PortOpen:              portOpen,
		BatchProbeOK:          sshOK,
		RemoteCodexHome:       sshDetail,
		RemoteHomeNameMatches: sshOK && sameFilesystemPath(sshDetail, m.Profiles.CPA.Home),
	}
	if sshOK && !report.SSH.RemoteHomeNameMatches {
		failures++
	}
	policyOK := len(report.Official.Problems) == 0
	configOwnedOrExternal := report.CPAProfile.ManagedConfig || report.CPAProfile.Management == "external"
	bridgeReady := report.CPAProfile.ConfigPresent && configOwnedOrExternal && report.CPAProfile.RenderMatches && report.CPABlackbox.ModelsOK && report.SSH.PortOpen && report.SSH.BatchProbeOK && report.SSH.RemoteHomeNameMatches
	report.Result = ResultReport{OK: failures == 0, Issues: failures, PolicyOK: policyOK, BridgeReady: bridgeReady}
	return report, failures
}

func sameFilesystemPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func PrintDoctorReport(w io.Writer, r DoctorReport) {
	fmt.Fprintln(w, "codex-cpa-bridge doctor")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "official profile")
	fmt.Fprintf(w, "  home: %s\n", r.Official.Home)
	fmt.Fprintf(w, "  managed: %t\n", r.Official.Managed)
	fmt.Fprintf(w, "  enforce_native_login: %t\n", r.Official.EnforceNativeLogin)
	fmt.Fprintf(w, "  config: %s\n", present(r.Official.ConfigPresent))
	fmt.Fprintf(w, "  auth.json: %s\n", present(r.Official.AuthJSONPresent))
	fmt.Fprintf(w, "  contains_bridge_marker: %t\n", r.Official.ContainsBridgeMarker)
	fmt.Fprintf(w, "  contains_cpa_endpoint: %t\n", r.Official.ContainsCPAEndpoint)
	fmt.Fprintf(w, "  contains_cpa_provider: %t\n", r.Official.ContainsCPAProvider)
	fmt.Fprintf(w, "  active_model_provider: %s\n", fallback(r.Official.ActiveModelProvider, "unknown"))
	fmt.Fprintf(w, "  active_base_url: %s\n", fallback(r.Official.ActiveBaseURL, "unknown"))
	for _, p := range r.Official.Problems {
		fmt.Fprintf(w, "  problem: %s\n", p)
	}
	fmt.Fprintln(w, "\ncpa profile")
	fmt.Fprintf(w, "  management: %s\n", r.CPAProfile.Management)
	fmt.Fprintf(w, "  home: %s\n", r.CPAProfile.Home)
	fmt.Fprintf(w, "  endpoint: %s\n", r.CPAProfile.Endpoint)
	fmt.Fprintf(w, "  provider: %s\n", r.CPAProfile.Provider)
	fmt.Fprintf(w, "  model: %s\n", r.CPAProfile.Model)
	fmt.Fprintf(w, "  auth_source: %s\n", r.CPAProfile.AuthSource)
	fmt.Fprintf(w, "  config: %s\n", present(r.CPAProfile.ConfigPresent))
	fmt.Fprintf(w, "  managed_config: %t\n", r.CPAProfile.ManagedConfig)
	fmt.Fprintf(w, "  render_matches: %t\n", r.CPAProfile.RenderMatches)
	fmt.Fprintln(w, "\ncpa blackbox")
	fmt.Fprintf(w, "  /models: %s (%s)\n", okFailed(r.CPABlackbox.ModelsOK), r.CPABlackbox.ModelsStatus)
	if len(r.CPABlackbox.ModelSample) > 0 {
		fmt.Fprintf(w, "  model_sample: %s\n", strings.Join(r.CPABlackbox.ModelSample, ", "))
	}
	if r.CPABlackbox.ModelsDetail != "" {
		fmt.Fprintf(w, "  detail: %s\n", r.CPABlackbox.ModelsDetail)
	}
	if r.CPABlackbox.ResponsesProbe != nil {
		fmt.Fprintf(w, "  /responses: %s (%s)\n", okFailed(r.CPABlackbox.ResponsesProbe.OK), r.CPABlackbox.ResponsesProbe.Status)
		if r.CPABlackbox.ResponsesProbe.Detail != "" {
			fmt.Fprintf(w, "  detail: %s\n", r.CPABlackbox.ResponsesProbe.Detail)
		}
	}
	fmt.Fprintln(w, "\nssh endpoint")
	fmt.Fprintf(w, "  target: %s\n", r.SSH.Target)
	fmt.Fprintf(w, "  user_exists: %t\n", r.SSH.UserExists)
	fmt.Fprintf(w, "  port_open: %t\n", r.SSH.PortOpen)
	if r.SSH.PortOpen {
		fmt.Fprintf(w, "  batch_probe: %s\n", okFailed(r.SSH.BatchProbeOK))
		fmt.Fprintf(w, "  remote_CODEX_HOME: %s\n", fallback(r.SSH.RemoteCodexHome, "empty"))
	}
	fmt.Fprintf(w, "\n  policy_ok: %t\n", r.Result.PolicyOK)
	fmt.Fprintf(w, "  bridge_ready: %t\n", r.Result.BridgeReady)
	if r.Result.OK {
		fmt.Fprintln(w, "result: ok")
	} else {
		fmt.Fprintf(w, "result: %d issue(s)\n", r.Result.Issues)
	}
}

func PrintStatus(w io.Writer, m Manifest) error {
	cpaConfig := filepath.Join(m.Profiles.CPA.Home, "config.toml")
	token, authSource := loadAuthToken(m.Profiles.CPA)
	modelsOK, modelsStatus, _ := httpJSON(m.Profiles.CPA.Endpoint+"/models", nil, token, 3*time.Second)
	payload := map[string]any{
		"official_home":                 m.Profiles.Official.Home,
		"official_managed":              m.Profiles.Official.Managed,
		"official_enforce_native_login": m.Profiles.Official.EnforceNativeLogin,
		"cpa_home":                      m.Profiles.CPA.Home,
		"cpa_management":                m.Profiles.CPA.Management,
		"cpa_config_present":            fileExists(cpaConfig),
		"cpa_config_managed":            pathIsManaged(cpaConfig),
		"cpa_endpoint":                  m.Profiles.CPA.Endpoint,
		"cpa_auth_source":               authSource,
		"cpa_models_ok":                 modelsOK,
		"cpa_models_status":             modelsStatus,
		"ssh_port_open":                 portOpen(m.SSH.CPA.Host, m.SSH.CPA.Port, time.Second),
		"ssh_management":                m.SSH.CPA.Management,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(payload)
}

func PrintAdopt(w io.Writer, m Manifest) error {
	officialConfig := filepath.Join(m.Profiles.Official.Home, "config.toml")
	cpaConfig := filepath.Join(m.Profiles.CPA.Home, "config.toml")
	officialHits := fileContains(officialConfig, []string{m.Profiles.CPA.Endpoint, m.Profiles.CPA.ProviderName, OwnerMarker})
	cpaHits := fileContains(cpaConfig, []string{m.Profiles.CPA.Endpoint, m.Profiles.CPA.ProviderName, OwnerMarker})

	fmt.Fprintln(w, "# Suggested bridge.toml")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[profiles.official]")
	fmt.Fprintf(w, "home = %s\n", tomlQuote(m.Profiles.Official.Home))
	fmt.Fprintln(w, "mode = \"native\"")
	fmt.Fprintln(w, "managed = false")
	fmt.Fprintf(w, "enforce_native_login = %t\n", m.Profiles.Official.EnforceNativeLogin)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[profiles.cpa]")
	fmt.Fprintf(w, "management = %s\n", tomlQuote(m.Profiles.CPA.Management))
	fmt.Fprintf(w, "home = %s\n", tomlQuote(m.Profiles.CPA.Home))
	fmt.Fprintf(w, "endpoint = %s\n", tomlQuote(m.Profiles.CPA.Endpoint))
	fmt.Fprintf(w, "model = %s\n", tomlQuote(m.Profiles.CPA.Model))
	fmt.Fprintf(w, "provider_name = %s\n", tomlQuote(m.Profiles.CPA.ProviderName))
	if m.Profiles.CPA.EnvKey != "" {
		fmt.Fprintf(w, "env_key = %s\n", tomlQuote(m.Profiles.CPA.EnvKey))
	}
	if m.Profiles.CPA.AuthCommand != "" {
		fmt.Fprintf(w, "auth_command = %s\n", tomlQuote(m.Profiles.CPA.AuthCommand))
		if len(m.Profiles.CPA.AuthArgs) > 0 {
			fmt.Fprintf(w, "auth_args = %s\n", tomlArray(m.Profiles.CPA.AuthArgs))
		}
	}
	if m.Profiles.CPA.ModelCatalogJSON != "" {
		fmt.Fprintf(w, "model_catalog_json = %s\n", tomlQuote(m.Profiles.CPA.ModelCatalogJSON))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[ssh.cpa]")
	fmt.Fprintf(w, "management = %s\n", tomlQuote(m.SSH.CPA.Management))
	fmt.Fprintf(w, "host = %s\n", tomlQuote(m.SSH.CPA.Host))
	fmt.Fprintf(w, "port = %d\n", m.SSH.CPA.Port)
	fmt.Fprintf(w, "user = %s\n", tomlQuote(m.SSH.CPA.User))
	fmt.Fprintf(w, "profile = %s\n", tomlQuote(m.SSH.CPA.Profile))
	if m.SSH.CPA.IdentityFile != "" {
		fmt.Fprintf(w, "identity_file = %s\n", tomlQuote(m.SSH.CPA.IdentityFile))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# Inspection")
	fmt.Fprintf(w, "# official_config = %s\n", officialConfig)
	fmt.Fprintf(w, "# official_contains_cpa_endpoint = %t\n", officialHits[m.Profiles.CPA.Endpoint])
	fmt.Fprintf(w, "# official_contains_bridge_marker = %t\n", officialHits[OwnerMarker])
	fmt.Fprintf(w, "# cpa_config = %s\n", cpaConfig)
	fmt.Fprintf(w, "# cpa_contains_endpoint = %t\n", cpaHits[m.Profiles.CPA.Endpoint])
	fmt.Fprintf(w, "# cpa_contains_bridge_marker = %t\n", cpaHits[OwnerMarker])
	return nil
}

func tomlQuote(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "\"\""
	}
	return string(raw)
}

func tomlKey(value string) string {
	if value == "" || !isASCIIAlpha(value[0]) {
		return tomlQuote(value)
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !isASCIIAlpha(c) && !isASCIIDigit(c) && c != '_' && c != '-' {
			return tomlQuote(value)
		}
	}
	return value
}

func tomlArray(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, tomlQuote(value))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func sortedKeys(items map[string]string) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func atomicWrite(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if mode != 0 {
		if err := os.Chmod(tmpName, mode); err != nil {
			return err
		}
	}
	return os.Rename(tmpName, path)
}

func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func readPID(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	return pid
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadAuthToken(cpa CPAProfile) (string, string) {
	if cpa.EnvKey != "" {
		token := os.Getenv(cpa.EnvKey)
		if token != "" {
			return token, "env:" + cpa.EnvKey + ":present"
		}
	}
	if cpa.AuthCommand != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cpa.AuthCommand, cpa.AuthArgs...)
		out, err := cmd.Output()
		if ctx.Err() == context.DeadlineExceeded {
			return "", "auth_command:timeout"
		}
		if errors.Is(err, os.ErrNotExist) {
			return "", "auth_command:not_found"
		}
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return "", fmt.Sprintf("auth_command:exit_%d", exitErr.ExitCode())
			}
			return "", "auth_command:error"
		}
		token := strings.TrimSpace(string(out))
		if token == "" {
			return "", "auth_command:empty"
		}
		return token, "auth_command:present"
	}
	if cpa.EnvKey != "" {
		return "", "env:" + cpa.EnvKey + ":missing"
	}
	return "", "none"
}

func httpJSON(url string, payload any, bearerToken string, timeout time.Duration) (bool, string, any) {
	var body io.Reader
	method := http.MethodGet
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return false, "json_marshal_error", err.Error()
		}
		body = bytes.NewReader(raw)
		method = http.MethodPost
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return false, "request_error", err.Error()
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, shortErrorName(err), err.Error()
	}
	defer resp.Body.Close()
	limit := int64(1024 * 1024)
	if resp.StatusCode >= 400 {
		limit = 8192
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	status := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, status, string(raw)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return true, status, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false, status + ", non-json response", string(raw)
	}
	return true, status, decoded
}

func shortErrorName(err error) string {
	name := fmt.Sprintf("%T", err)
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.TrimPrefix(name, "*")
	if name == "" {
		return "error"
	}
	return name
}

func summarize(value any) string {
	if value == nil {
		return "empty"
	}
	var text string
	if s, ok := value.(string); ok {
		text = s
	} else if raw, err := json.Marshal(value); err == nil {
		text = string(raw)
	} else {
		text = fmt.Sprint(value)
	}
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 500 {
		return text[:500]
	}
	return text
}

func modelSample(value any) []string {
	root, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	items, ok := root["data"].([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, ok := obj["id"].(string)
		if ok && id != "" {
			out = append(out, id)
		}
		if len(out) == 5 {
			break
		}
	}
	return out
}

func portOpen(host string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func runSSHProbe(m Manifest, timeout time.Duration) (bool, string) {
	if m.SSH.CPA.User == "" {
		return false, "ssh user is empty"
	}
	knownHosts, err := ensureKnownHost(m)
	if err != nil {
		return false, err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=3",
		"-o", "UserKnownHostsFile=" + knownHosts,
		"-o", "StrictHostKeyChecking=yes",
		"-p", strconv.Itoa(m.SSH.CPA.Port),
	}
	if m.SSH.CPA.IdentityFile != "" {
		args = append(args, "-i", m.SSH.CPA.IdentityFile)
	}
	args = append(args, fmt.Sprintf("%s@%s", m.SSH.CPA.User, m.SSH.CPA.Host))
	commands := []string{"__codex_cpa_bridge_probe__"}
	if m.SSH.CPA.Management == "external" {
		// An external endpoint may be an ordinary SSH shell without our ForceCommand.
		commands = append(commands, `printf '%s\n' "$CODEX_HOME"`)
	}
	lastDetail := "SSH probe returned no CODEX_HOME"
	for _, probeCommand := range commands {
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "ssh", append(args, probeCommand)...)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if ctx.Err() == context.DeadlineExceeded {
			return false, "ssh probe timed out"
		}
		if errors.Is(err, os.ErrNotExist) {
			return false, "ssh binary not found"
		}
		output := strings.TrimSpace(string(out))
		if err == nil && filepath.IsAbs(output) && !strings.ContainsAny(output, "\r\n") {
			return true, output
		}
		if err != nil {
			lastDetail = strings.TrimSpace(stderr.String())
			if lastDetail == "" {
				lastDetail = err.Error()
			}
		} else {
			lastDetail = "SSH probe returned no valid CODEX_HOME"
		}
	}
	return false, lastDetail
}

func ensureKnownHost(m Manifest) (string, error) {
	knownHosts := filepath.Join(m.Runtime.StateDir, "ssh", "known_hosts")
	hostKey := filepath.Join(m.Runtime.StateDir, "ssh", "ssh_host_ed25519_key.pub")
	public, err := os.ReadFile(hostKey)
	if err == nil {
		hostPort := fmt.Sprintf("[%s]:%d", m.SSH.CPA.Host, m.SSH.CPA.Port)
		if err := atomicWrite(knownHosts, []byte(hostPort+" "+strings.TrimSpace(string(public))+"\n"), 0o600); err != nil {
			return knownHosts, err
		}
		return knownHosts, nil
	}
	if fileExists(knownHosts) {
		if info, statErr := os.Stat(knownHosts); statErr == nil && info.Size() > 0 {
			return knownHosts, nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh-keyscan", "-p", strconv.Itoa(m.SSH.CPA.Port), m.SSH.CPA.Host)
	output, scanErr := cmd.Output()
	if scanErr != nil || len(bytes.TrimSpace(output)) == 0 {
		if ctx.Err() == context.DeadlineExceeded {
			return knownHosts, errors.New("ssh-keyscan timed out")
		}
		return knownHosts, fmt.Errorf("cannot collect SSH host key for %s:%d", m.SSH.CPA.Host, m.SSH.CPA.Port)
	}
	if err := atomicWrite(knownHosts, output, 0o600); err != nil {
		return knownHosts, err
	}
	return knownHosts, nil
}

func cpaConfigMatches(m Manifest, path string, content []byte) bool {
	if m.Profiles.CPA.Management != "external" {
		return string(content) == RenderCodexConfig(m)
	}
	provider, baseURL := activeProviderBaseURL(path)
	return provider == m.Profiles.CPA.ProviderName && strings.TrimRight(baseURL, "/") == m.Profiles.CPA.Endpoint
}

func externalCPAIdentity(path string) (string, string) {
	var data map[string]any
	if _, err := toml.DecodeFile(path, &data); err != nil {
		return "", ""
	}
	model, _ := data["model"].(string)
	provider, _ := data["model_provider"].(string)
	return model, provider
}

func isShellName(value string) bool {
	if value == "" || (!isASCIIAlpha(value[0]) && value[0] != '_') {
		return false
	}
	for i := 1; i < len(value); i++ {
		if !isASCIIAlpha(value[i]) && !isASCIIDigit(value[i]) && value[i] != '_' {
			return false
		}
	}
	return true
}

func userExists(name string) bool {
	if name == "" {
		return false
	}
	_, err := user.Lookup(name)
	return err == nil
}

func activeProviderBaseURL(configPath string) (string, string) {
	var data map[string]any
	if _, err := toml.DecodeFile(configPath, &data); err != nil {
		return "", ""
	}
	providerName, _ := data["model_provider"].(string)
	if providerName == "" {
		return "", ""
	}
	providers, _ := data["model_providers"].(map[string]any)
	if providers == nil {
		return providerName, ""
	}
	provider, _ := providers[providerName].(map[string]any)
	if provider == nil {
		return providerName, ""
	}
	baseURL, _ := provider["base_url"].(string)
	return providerName, baseURL
}

func fileContains(path string, needles []string) map[string]bool {
	result := map[string]bool{}
	for _, needle := range needles {
		result[needle] = false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	text := string(content)
	for _, needle := range needles {
		result[needle] = strings.Contains(text, needle)
	}
	return result
}

func present(ok bool) string {
	if ok {
		return "present"
	}
	return "missing"
}

func fallback(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func okFailed(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

func isASCIIAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
