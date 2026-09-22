use serde_json::Value;
use std::{env, path::PathBuf, process::Command, thread, time::Duration};
use tauri::webview::PageLoadEvent;
use tauri::Manager;

const UI_PROBE: &str = r#"(() => {
    const root = document.getElementById('root');
    return {
        href: location.href,
        readyState: document.readyState,
        rootExists: Boolean(root),
        rootChildren: root?.childElementCount ?? -1,
        rootHtml: root?.innerHTML.slice(0, 500) ?? null,
        bodyText: document.body?.innerText.slice(0, 500) ?? null,
        scripts: Array.from(document.scripts).map((node) => ({ src: node.src, type: node.type })),
        styles: Array.from(document.querySelectorAll('link[rel="stylesheet"]')).map((node) => node.href),
        resources: performance.getEntriesByType('resource').map((entry) => ({ name: entry.name, duration: entry.duration }))
    };
})()"#;

const UI_MODULE_PROBE: &str = r#"(async () => {
    const root = document.getElementById('root');
    if ((root?.childElementCount ?? 0) > 0) return { skipped: 'root-rendered' };
    const src = document.querySelector('script[type="module"]')?.src;
    if (!src) return { error: 'module-script-missing' };
    try {
        await import(src);
        return { imported: true, rootChildren: root?.childElementCount ?? -1 };
    } catch (error) {
        return { imported: false, error: String(error), stack: error?.stack ?? null };
    }
})()"#;

fn bridge_binary() -> Result<PathBuf, String> {
    if let Ok(path) = env::var("CODEX_CPA_BRIDGE_BIN") {
        let candidate = PathBuf::from(path);
        if candidate.is_file() { return Ok(candidate); }
    }
    let mut candidates = Vec::new();
    if let Ok(exe) = env::current_exe() {
        if let Some(dir) = exe.parent() { candidates.push(dir.join("bridge-go")); }
    }
    if let Ok(home) = env::var("HOME") {
        candidates.push(PathBuf::from(home).join("codex-cpa-bridge/bin/bridge-go"));
    }
    candidates.into_iter().find(|path| path.is_file()).ok_or_else(|| "bridge-go binary not found; set CODEX_CPA_BRIDGE_BIN".to_string())
}

fn run_bridge(manifest: &str, args: &[&str]) -> Result<String, String> {
    if manifest.trim().is_empty() { return Err("manifest path is required".into()); }
    let output = Command::new(bridge_binary()?)
        .arg("--manifest").arg(manifest).args(args)
        .output().map_err(|error| error.to_string())?;
    let stdout = String::from_utf8_lossy(&output.stdout).trim().to_string();
    let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
    if output.status.success() { Ok(stdout) } else if stderr.is_empty() { Err(stdout) } else { Err(stderr) }
}

fn run_json(manifest: &str, args: &[&str]) -> Result<Value, String> {
    if manifest.trim().is_empty() { return Err("manifest path is required".into()); }
    let output = Command::new(bridge_binary()?)
        .arg("--manifest").arg(manifest).args(args)
        .output().map_err(|error| error.to_string())?;
    let stdout = String::from_utf8_lossy(&output.stdout).trim().to_string();
    if !stdout.is_empty() {
        return serde_json::from_str(&stdout).map_err(|error| format!("invalid bridge JSON: {error}"));
    }
    let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
    if stderr.is_empty() { Err("bridge returned no JSON output".into()) } else { Err(stderr) }
}

#[tauri::command]
fn bridge_status(manifest: String) -> Result<Value, String> { run_json(&manifest, &["status"]) }

#[tauri::command]
fn bridge_doctor(manifest: String) -> Result<Value, String> { run_json(&manifest, &["doctor", "--json"]) }

#[tauri::command]
fn bridge_models(manifest: String) -> Result<Value, String> { run_json(&manifest, &["models", "list", "--json"]) }

#[tauri::command]
fn bridge_set_model(manifest: String, slug: String, visibility: String) -> Result<String, String> {
    if visibility != "list" && visibility != "hide" { return Err("visibility must be list or hide".into()); }
    run_bridge(&manifest, &["models", "set", "--slug", &slug, "--visibility", &visibility])
}

#[tauri::command]
fn bridge_action(manifest: String, action: String) -> Result<String, String> {
    match action.as_str() {
        "setup" | "up" | "down" => run_bridge(&manifest, &[&action]),
        _ => Err("unsupported bridge action".into()),
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .setup(|app| {
            if env::var_os("CODEX_CPA_UI_DIAGNOSTICS").is_some() {
                eprintln!("ui diagnostics enabled");
                if let Some(webview) = app.get_webview_window("main") {
                    thread::spawn(move || {
                        thread::sleep(Duration::from_secs(3));
                        if let Err(error) = webview.eval_with_callback(UI_PROBE, |result| eprintln!("ui delayed probe: {result}")) {
                            eprintln!("ui delayed probe dispatch failed: {error}");
                        }
                        if let Err(error) = webview.eval_with_callback(UI_MODULE_PROBE, |result| eprintln!("ui delayed module probe: {result}")) {
                            eprintln!("ui delayed module probe dispatch failed: {error}");
                        }
                    });
                } else {
                    eprintln!("ui diagnostics: main webview missing");
                }
            }
            Ok(())
        })
        .on_page_load(|webview, payload| {
            if env::var_os("CODEX_CPA_UI_DIAGNOSTICS").is_none() {
                return;
            }
            eprintln!("ui page load: {:?} {}", payload.event(), payload.url());
            if payload.event() != PageLoadEvent::Finished {
                return;
            }
            if let Err(error) = webview.eval_with_callback(UI_PROBE, |result| eprintln!("ui probe: {result}")) {
                eprintln!("ui probe dispatch failed: {error}");
            }
            if let Err(error) = webview.eval_with_callback(UI_MODULE_PROBE, |result| eprintln!("ui module probe: {result}")) {
                eprintln!("ui module probe dispatch failed: {error}");
            }
        })
        .invoke_handler(tauri::generate_handler![bridge_status, bridge_doctor, bridge_models, bridge_set_model, bridge_action])
        .run(tauri::generate_context!())
        .expect("error while running Codex CPA Bridge");
}
