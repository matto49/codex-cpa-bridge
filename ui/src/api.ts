import { invoke } from "@tauri-apps/api/core";

export type Status = {
  official_home: string;
  official_managed: boolean;
  official_enforce_native_login: boolean;
  cpa_home: string;
	 cpa_management: "managed" | "external";
  cpa_config_present: boolean;
  cpa_config_managed: boolean;
  cpa_endpoint: string;
  cpa_auth_source: string;
  cpa_models_ok: boolean;
  cpa_models_status: string;
  ssh_port_open: boolean;
	 ssh_management: "managed" | "external";
};

export type Doctor = {
  official: { home: string; auth_json_present: boolean; problems: string[] };
  cpa_profile: { management: "managed" | "external"; home: string; provider: string; model: string; managed_config: boolean; render_matches: boolean };
  cpa_blackbox: { models_ok: boolean; models_status: string };
  ssh: { target: string; port_open: boolean; batch_probe_ok: boolean; remote_CODEX_HOME?: string };
  result: { ok: boolean; issues: number; policy_ok: boolean; bridge_ready: boolean };
};

export type Model = {
  slug: string;
  display_name: string;
  visibility: "list" | "hide";
  supported_in_api: boolean;
  priority: number;
};

export type ModelReport = { path: string; models: Model[] };

const previewStatus: Status = {
  official_home: "/Users/bytedance/.codex",
  official_managed: false,
  official_enforce_native_login: true,
  cpa_home: "/Users/bytedance/.codex-mac-cpa",
	 cpa_management: "external",
  cpa_config_present: true,
  cpa_config_managed: true,
  cpa_endpoint: "http://127.0.0.1:8317/v1",
  cpa_auth_source: "auth_command:present",
  cpa_models_ok: true,
  cpa_models_status: "HTTP 200",
  ssh_port_open: true,
	 ssh_management: "managed",
};

const previewDoctor: Doctor = {
  official: { home: previewStatus.official_home, auth_json_present: true, problems: [] },
  cpa_profile: { management: "external", home: previewStatus.cpa_home, provider: "cliproxy", model: "gpt-5.6-sol", managed_config: false, render_matches: true },
  cpa_blackbox: { models_ok: true, models_status: "HTTP 200" },
  ssh: { target: "bytedance@127.0.0.1:2223", port_open: true, batch_probe_ok: true, remote_CODEX_HOME: previewStatus.cpa_home },
  result: { ok: true, issues: 0, policy_ok: true, bridge_ready: true },
};

const previewModels: ModelReport = {
  path: "/Users/bytedance/.codex-mac-cpa/model-catalogs/traex.json",
  models: [
    { slug: "traex/GPT-6-Astra", display_name: "GPT 6.0 Astra", visibility: "list", supported_in_api: true, priority: 1 },
    { slug: "traex/GPT-5.6-Sol", display_name: "GPT 5.6 Sol", visibility: "list", supported_in_api: true, priority: 6 },
    { slug: "traex/GPT-5.6-Terra", display_name: "GPT 5.6 Terra", visibility: "list", supported_in_api: true, priority: 7 },
    { slug: "traex/GPT-5.6-Luna", display_name: "GPT 5.6 Luna", visibility: "hide", supported_in_api: true, priority: 8 },
    { slug: "traex/GPT-5.5", display_name: "GPT 5.5", visibility: "hide", supported_in_api: true, priority: 12 },
  ],
};

function inTauri() {
  return "__TAURI_INTERNALS__" in window;
}

export async function getStatus(manifest: string): Promise<Status> {
  return inTauri() ? invoke("bridge_status", { manifest }) : previewStatus;
}

export async function getDoctor(manifest: string): Promise<Doctor> {
  return inTauri() ? invoke("bridge_doctor", { manifest }) : previewDoctor;
}

export async function getModels(manifest: string): Promise<ModelReport> {
  return inTauri() ? invoke("bridge_models", { manifest }) : structuredClone(previewModels);
}

export async function setModelVisibility(manifest: string, slug: string, visibility: "list" | "hide") {
  if (inTauri()) return invoke<string>("bridge_set_model", { manifest, slug, visibility });
  const model = previewModels.models.find((item) => item.slug === slug);
  if (model) model.visibility = visibility;
  return `${slug} -> ${visibility}`;
}

export async function runAction(manifest: string, action: "setup" | "up" | "down") {
  return inTauri() ? invoke<string>("bridge_action", { manifest, action }) : `Preview: ${action}`;
}
