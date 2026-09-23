import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Check, ChevronRight, CircleAlert, Power, RefreshCw, Server, Settings2, SlidersHorizontal, TerminalSquare } from "lucide-react";
import { CatalogBootstrapReport, ClaudeInitReport, Doctor, Model, ModelReport, PlatformSyncReport, PlatformsReport, RemoteReport, RemoteSyncReport, Status, bootstrapCatalog, getDoctor, getModels, getPlatformPlan, getPlatforms, getStatus, initClaude, initManifest, previewRemoteSync, runAction, scanRemote, setModelVisibility, syncPlatforms, syncRemote } from "./api";
import bridgeIcon from "../src-tauri/icons/bridge.svg";

type View = "overview" | "models" | "settings";
const DEFAULT_MANIFEST = "~/.config/codex-cpa-bridge/bridge.toml";

function StateDot({ ok }: { ok: boolean }) {
  return <span className={ok ? "state-dot ok" : "state-dot bad"} aria-hidden="true" />;
}

function Toggle({ checked, onChange, label, disabled }: { checked: boolean; onChange: () => void; label: string; disabled?: boolean }) {
  return (
    <button className={`toggle ${checked ? "checked" : ""}`} role="switch" aria-checked={checked} aria-label={label} onClick={onChange} disabled={disabled}>
      <span />
    </button>
  );
}

function Sidebar({ view, onView }: { view: View; onView: (view: View) => void }) {
  const items = [
    { id: "overview" as const, label: "Overview", icon: Server },
    { id: "models" as const, label: "Models", icon: SlidersHorizontal },
    { id: "settings" as const, label: "Settings", icon: Settings2 },
  ];
  return (
    <aside className="sidebar">
      <div className="brand"><img className="brand-mark" src={bridgeIcon} alt="" /><span>Codex CPA</span></div>
      <nav>
        {items.map(({ id, label, icon: Icon }) => (
          <button key={id} className={view === id ? "active" : ""} onClick={() => onView(id)}>
            <Icon size={17} strokeWidth={1.8} /><span>{label}</span>
          </button>
        ))}
      </nav>
      <div className="sidebar-foot"><TerminalSquare size={15} /><span>bridge-go</span><code>v0.1</code></div>
    </aside>
  );
}

function Overview({ status, doctor, busy, onRefresh, onAction }: { status?: Status; doctor?: Doctor; busy: boolean; onRefresh: () => void; onAction: (action: "setup" | "up" | "down") => void }) {
  const ready = doctor?.result.bridge_ready ?? false;
	const externalSSH = status?.ssh_management === "external";
  return (
    <div className="view">
      <header className="page-head">
        <div><h1>Overview</h1><p>Local Codex profile and SSH bridge</p></div>
        <button className="icon-button" title="Refresh status" onClick={onRefresh} disabled={busy}><RefreshCw size={17} className={busy ? "spin" : ""} /></button>
      </header>

      <section className="health-band">
        <div className={`health-icon ${ready ? "ready" : "warning"}`}>{ready ? <Check size={20} /> : <CircleAlert size={20} />}</div>
        <div><strong>{ready ? "Bridge is ready" : "Bridge needs attention"}</strong><span>{doctor?.ssh.target ?? "Waiting for status"}</span></div>
        <div className="health-actions">
          <button className="secondary" onClick={() => onAction("down")} disabled={busy || !status?.ssh_port_open || externalSSH}><Power size={15} />Stop</button>
          <button className="primary" onClick={() => onAction(externalSSH ? "setup" : status?.ssh_port_open ? "up" : "setup")} disabled={busy}><Power size={15} />{externalSSH ? "Verify" : status?.ssh_port_open ? "Restart" : "Set up"}</button>
        </div>
      </section>

      <section className="status-grid">
        <article>
          <div className="section-label"><StateDot ok={doctor?.result.policy_ok ?? false} />Native Codex</div>
          <h2>OpenAI login</h2>
          <dl><div><dt>Home</dt><dd>{status?.official_home ?? "-"}</dd></div><div><dt>Auth state</dt><dd>{doctor?.official.auth_json_present ? "Available" : "Missing"}</dd></div><div><dt>Managed by bridge</dt><dd>{status?.official_managed ? "Yes" : "No"}</dd></div></dl>
        </article>
        <article>
          <div className="section-label"><StateDot ok={doctor?.cpa_profile.render_matches ?? false} />CPA profile</div>
          <h2>{doctor?.cpa_profile.model ?? "No model"}</h2>
          <dl><div><dt>Home</dt><dd>{status?.cpa_home ?? "-"}</dd></div><div><dt>Provider</dt><dd>{doctor?.cpa_profile.provider ?? "-"}</dd></div><div><dt>Ownership</dt><dd>{status?.cpa_management ?? "-"}</dd></div><div><dt>Endpoint</dt><dd>{status?.cpa_endpoint ?? "-"}</dd></div></dl>
        </article>
      </section>

      <section className="checks">
        <div className="checks-head"><h2>Runtime checks</h2><span>{doctor?.result.issues ?? 0} issues</span></div>
        {[
          ["CPA endpoint", doctor?.cpa_blackbox.models_ok ?? false, doctor?.cpa_blackbox.models_status ?? "Unknown"],
          ["Loopback sshd", doctor?.ssh.port_open ?? false, doctor?.ssh.target ?? "Unknown"],
          ["Authenticated probe", doctor?.ssh.batch_probe_ok ?? false, doctor?.ssh.remote_CODEX_HOME ?? "Not verified"],
        ].map(([label, ok, detail]) => <div className="check-row" key={String(label)}><StateDot ok={Boolean(ok)} /><strong>{label}</strong><span>{detail}</span><ChevronRight size={16} /></div>)}
      </section>
    </div>
  );
}

function Models({ report, plan, syncResult, remoteTarget, query, setQuery, onToggle, onSync, busy }: { report?: ModelReport; plan?: PlatformSyncReport; syncResult?: PlatformSyncReport; remoteTarget: string; query: string; setQuery: (value: string) => void; onToggle: (model: Model) => void; onSync: () => void; busy: boolean }) {
  const filtered = useMemo(() => report?.models.filter((model) => `${model.display_name} ${model.slug}`.toLowerCase().includes(query.toLowerCase())) ?? [], [report, query]);
  const visible = report?.models.filter((model) => model.visibility === "list").length ?? 0;
  return (
    <div className="view">
      <header className="page-head"><div><h1>Models</h1><p>{visible} visible of {report?.models.length ?? 0} · Toggles sync CPA-backed clients{remoteTarget ? ` and ${remoteTarget}` : ""}; reconnect active sessions after changes</p></div></header>
      <section className="sync-panel">
        <div className="sync-panel-head"><div><h2>Platform sync</h2><p>Preview changes before writing. Unrelated configurations are preserved. xbot disables models but still lists them greyed out.</p></div><button className="primary" onClick={onSync} disabled={busy || !plan}>{remoteTarget ? "Sync local + remote" : "Sync local platforms"}</button></div>
        {plan?.items.map((item) => <div className="sync-row" key={item.id}><strong>{item.id}</strong><span className={`platform-state ${item.action}`}>{item.action}</span><span>{item.detail}{item.add?.length ? ` · ${item.id === "xbot" ? "Enable" : "Add"}: ${item.add.join(", ")}` : ""}{item.remove?.length ? ` · ${item.id === "xbot" ? "Disable" : "Remove"}: ${item.remove.join(", ")}` : ""}</span></div>) ?? <p className="section-help">Sync preview unavailable. Check the desktop app and manifest.</p>}
        {syncResult && <p className="section-help">Last sync: {syncResult.changed} updated, {syncResult.failed} blocked or failed. {syncResult.items.filter((item) => item.result === "updated").map((item) => `${item.id} backed up to ${item.backup}`).join(" · ")}</p>}
      </section>
      <div className="model-toolbar"><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter models" aria-label="Filter models" /><code>{report?.path ?? "No catalog configured"}</code></div>
      <section className="model-table">
        <div className="model-table-head"><span>Model</span><span>Slug</span><span>API</span><span>Visible</span></div>
        {filtered.map((model) => (
          <div className="model-row" key={model.slug}>
            <div><strong>{model.display_name || model.slug}</strong><small>Priority {model.priority}</small></div>
            <code>{model.slug}</code>
            <span className={model.supported_in_api ? "api yes" : "api no"}>{model.supported_in_api ? "Ready" : "No"}</span>
            <Toggle checked={model.visibility === "list"} onChange={() => onToggle(model)} label={`Show ${model.display_name || model.slug}`} disabled={busy} />
          </div>
        ))}
        {filtered.length === 0 && <div className="empty">No matching models</div>}
      </section>
    </div>
  );
}

function ClaudeInitSection({ manifest, status, onCreated, onNotice, onBeginWork, onEndWork, busy }: { manifest: string; status?: Status; onCreated: () => Promise<void>; onNotice: (message: string) => void; onBeginWork: () => void; onEndWork: () => void; busy: boolean }) {
  const [baseURL, setBaseURL] = useState("");
  const [preview, setPreview] = useState<ClaudeInitReport>();
  const [confirmProtocol, setConfirmProtocol] = useState(false);
  const [confirmAuth, setConfirmAuth] = useState(false);
  const [working, setWorking] = useState(false);
  useEffect(() => { setPreview(undefined); setConfirmProtocol(false); setConfirmAuth(false); }, [manifest]);

  async function previewInit() {
    setWorking(true);
    onBeginWork();
    try { setPreview(await initClaude(manifest, baseURL, false, false, false)); }
    catch (error) { setPreview(undefined); onNotice(String(error)); }
    finally { setWorking(false); onEndWork(); }
  }

  async function createSettings() {
    setWorking(true);
    onBeginWork();
    try {
      const result = await initClaude(manifest, baseURL, confirmProtocol, confirmAuth, true);
      await onCreated();
      onNotice(`${result.action} ${result.path}. Claude authentication and runtime model picker still need verification.`);
    } catch (error) { onNotice(String(error)); }
    finally { setWorking(false); onEndWork(); }
  }

  return <section className="settings-section">
    <h2>Initialize Claude Code</h2>
    <p className="section-help">Only for a missing settings file. Use an Anthropic-compatible base URL on this CPA endpoint; no token is stored or copied.</p>
    <label><span>Anthropic base URL</span><input value={baseURL} onChange={(event) => { setBaseURL(event.target.value); setPreview(undefined); }} placeholder={status?.cpa_endpoint ?? "http://127.0.0.1:8317"} disabled={busy || working} /></label>
    <div className="remote-actions"><button className="secondary" onClick={previewInit} disabled={busy || working || !baseURL.trim()}>Preview Claude settings</button><button className="primary" onClick={createSettings} disabled={busy || working || !preview || !confirmProtocol || !confirmAuth}>Create settings</button></div>
    {preview && <div className="remote-summary"><strong>Preview: {preview.visible_models} visible models</strong><p>{preview.path}</p><p>{preview.detail}</p></div>}
    {preview && <div className="confirmations">
      <label><input type="checkbox" checked={confirmProtocol} onChange={(event) => setConfirmProtocol(event.target.checked)} /><span>I confirmed this CPA URL supports Claude's Anthropic API.</span></label>
      <label><input type="checkbox" checked={confirmAuth} onChange={(event) => setConfirmAuth(event.target.checked)} /><span>I have provisioned Claude credentials outside settings.json.</span></label>
    </div>}
  </section>;
}

function Settings({ manifest, manifestDraft, setManifestDraft, onLoadManifest, status, catalogReady, catalogPreview, platforms, remoteTarget, setRemoteTarget, remoteStatus, remotePlan, remoteResult, onRemoteCheck, onRemoteSync, onCatalogPreview, onCatalogCreate, onClaudeCreated, onNotice, onBeginWork, onEndWork, onAction, onInit, busy }: { manifest: string; manifestDraft: string; setManifestDraft: (value: string) => void; onLoadManifest: () => void; status?: Status; catalogReady: boolean; catalogPreview?: CatalogBootstrapReport; platforms?: PlatformsReport; remoteTarget: string; setRemoteTarget: (value: string) => void; remoteStatus?: RemoteReport; remotePlan?: RemoteSyncReport; remoteResult?: RemoteSyncReport; onRemoteCheck: () => void; onRemoteSync: () => void; onCatalogPreview: () => void; onCatalogCreate: () => void; onClaudeCreated: () => Promise<void>; onNotice: (message: string) => void; onBeginWork: () => void; onEndWork: () => void; onAction: (action: "setup" | "up" | "down") => void; onInit: () => void; busy: boolean }) {
  const claudeMissing = platforms?.platforms.some((platform) => platform.id === "claude" && platform.state === "missing");
  return (
    <div className="view narrow">
      <header className="page-head"><div><h1>Settings</h1><p>Bridge-owned configuration</p></div></header>
      <section className="settings-section"><h2>Manifest</h2><label><span>Path</span><input value={manifestDraft} onChange={(event) => setManifestDraft(event.target.value)} /></label><p className="section-help">Loaded: {manifest}. Apply a new path before initializing or syncing it. Existing files are never overwritten.</p><div className="remote-actions"><button className="secondary" onClick={onLoadManifest} disabled={busy || !manifestDraft.trim() || manifestDraft.trim() === manifest}>Load manifest</button><button className="secondary" onClick={onInit} disabled={busy || manifestDraft.trim() !== manifest}>Initialize from this Mac</button></div></section>
      <section className="settings-section"><h2>Profile paths</h2><dl><div><dt>Native Codex</dt><dd>{status?.official_home ?? "-"}</dd></div><div><dt>CPA Codex</dt><dd>{status?.cpa_home ?? "-"}</dd></div><div><dt>CPA endpoint</dt><dd>{status?.cpa_endpoint ?? "-"}</dd></div></dl></section>
      {!catalogReady && status && <section className="settings-section"><h2>Model catalog setup</h2><p className="section-help">If this is a new CPA profile, preview a catalog generated from CPA's full Codex metadata. Existing catalogs are never overwritten.</p><div className="remote-actions"><button className="secondary" onClick={onCatalogPreview} disabled={busy}>Preview catalog</button><button className="primary" onClick={onCatalogCreate} disabled={busy || !catalogPreview}>Create catalog</button></div>{catalogPreview && <p className="section-help">{catalogPreview.cpa_models} models can be created at {catalogPreview.path}.</p>}</section>}
      {claudeMissing && catalogReady && <ClaudeInitSection manifest={manifest} status={status} onCreated={onClaudeCreated} onNotice={onNotice} onBeginWork={onBeginWork} onEndWork={onEndWork} busy={busy} />}
      <section className="settings-section"><h2>Platform configuration</h2><p className="section-help">Detected paths and actual model visibility sources. Scan does not change configuration.</p>
        <div className="platform-list">{platforms?.platforms.map((platform) => (
          <div className="platform-item" key={platform.id}>
            <div className="platform-title"><strong>{platform.name}</strong><span className={`platform-state ${platform.state}`}>{platform.state.replaceAll("_", " ")}</span></div>
            <code title={platform.path}>{platform.path}</code>
            <p>{platform.detail}</p>
            <small>Visibility source: {platform.visibility}{platform.restart_needed ? " · reconnect may be required" : ""}</small>
          </div>
        )) ?? <p className="section-help">Scanning platform configuration…</p>}</div>
      </section>
      <section className="settings-section"><h2>Remote CPA over SSH</h2><p className="section-help">Use an existing SSH host alias with key and host-key trust. Check is read-only; sync can refresh the remote catalog and applies matching visibility with backups.</p>
        <label><span>SSH target</span><input value={remoteTarget} onChange={(event) => setRemoteTarget(event.target.value)} placeholder="devbox" disabled={busy} /></label>
        <div className="remote-actions"><button className="secondary" onClick={onRemoteCheck} disabled={busy || !remoteTarget.trim()}>Check &amp; preview</button><button className="primary" onClick={onRemoteSync} disabled={busy || !remoteStatus?.ready || !remotePlan}>Sync remote</button></div>
        {remoteStatus && <div className="remote-summary"><strong>{remoteStatus.ready ? "Remote ready" : "Remote needs attention"}</strong><span>{remoteStatus.resolved_user}@{remoteStatus.resolved_host}:{remoteStatus.resolved_port} · CPA {remoteStatus.cpa_http_status ?? "unknown"}</span><p>{remoteStatus.detail}</p></div>}
        {remotePlan && <div className="remote-summary"><strong>Preview</strong><p>{remotePlan.model_policy.changes.length} visibility changes · {remotePlan.model_policy.missing?.length ?? 0} models not yet in remote catalog</p>{remotePlan.model_policy.changes.length > 0 && <small>Changes: {remotePlan.model_policy.changes.map((change) => `${change.slug}: ${change.from} → ${change.to}`).join(", ")}</small>}{(remotePlan.model_policy.missing?.length ?? 0) > 0 && <small>Missing: {remotePlan.model_policy.missing?.join(", ")}. Sync will refresh from remote CPA first; models it does not provide remain unavailable there.</small>}</div>}
        {remoteResult && <div className="remote-summary"><strong>Last sync: {remoteResult.action}</strong><p>{remoteResult.detail}</p>{remoteResult.model_policy.backup && <small>Catalog backup: {remoteResult.model_policy.backup}</small>}{remoteResult.catalog_refresh?.backup && <small>Refresh backup: {remoteResult.catalog_refresh.backup}</small>}</div>}
      </section>
      <section className="settings-section action-section"><div><h2>Local SSH service</h2><p>{status?.ssh_port_open ? "Running" : "Stopped"}</p></div><div><button className="secondary" onClick={() => onAction("down")} disabled={busy || !status?.ssh_port_open || status?.ssh_management === "external"}>Stop</button><button className="primary" onClick={() => onAction("setup")} disabled={busy}>Run setup</button></div></section>
    </div>
  );
}

export default function App() {
  const [view, setView] = useState<View>("overview");
  const [manifest, setManifest] = useState(() => localStorage.getItem("bridge-manifest") || DEFAULT_MANIFEST);
  const [manifestDraft, setManifestDraft] = useState(manifest);
  const [status, setStatus] = useState<Status>();
  const [doctor, setDoctor] = useState<Doctor>();
  const [models, setModels] = useState<ModelReport>();
  const [platforms, setPlatforms] = useState<PlatformsReport>();
  const [platformPlan, setPlatformPlan] = useState<PlatformSyncReport>();
  const [catalogPreview, setCatalogPreview] = useState<CatalogBootstrapReport>();
  const [syncResult, setSyncResult] = useState<PlatformSyncReport>();
  const [remoteTarget, updateRemoteTarget] = useState(() => localStorage.getItem("bridge-remote-target") || "");
  const [remoteStatus, setRemoteStatus] = useState<RemoteReport>();
  const [remotePlan, setRemotePlan] = useState<RemoteSyncReport>();
  const [remoteResult, setRemoteResult] = useState<RemoteSyncReport>();
  const [query, setQuery] = useState("");
  const [pendingWork, setPendingWork] = useState(0);
  const [notice, setNotice] = useState<string>();
  const [loadedManifest, setLoadedManifest] = useState<string>();
  const refreshId = useRef(0);
  const currentManifest = useRef(manifest);
  currentManifest.current = manifest;
  const busy = pendingWork > 0;
  const current = loadedManifest === manifest;

  const beginWork = useCallback(() => setPendingWork((count) => count + 1), []);
  const endWork = useCallback(() => setPendingWork((count) => count - 1), []);

  const refresh = useCallback(async () => {
    const requestId = ++refreshId.current;
    beginWork();
    setNotice(undefined);
    try {
      const [statusResult, doctorResult, modelsResult, platformsResult, planResult] = await Promise.allSettled([getStatus(manifest), getDoctor(manifest), getModels(manifest), getPlatforms(manifest), getPlatformPlan(manifest)]);
      if (requestId !== refreshId.current || manifest !== currentManifest.current) return;
      setStatus(statusResult.status === "fulfilled" ? statusResult.value : undefined);
      setDoctor(doctorResult.status === "fulfilled" ? doctorResult.value : undefined);
      setModels(modelsResult.status === "fulfilled" ? modelsResult.value : undefined);
      setPlatforms(platformsResult.status === "fulfilled" ? platformsResult.value : undefined);
      setPlatformPlan(planResult.status === "fulfilled" ? planResult.value : undefined);
      setLoadedManifest(manifest);
      localStorage.setItem("bridge-manifest", manifest);
      const failures = [statusResult, doctorResult, modelsResult, platformsResult, planResult]
        .filter((result): result is PromiseRejectedResult => result.status === "rejected")
        .map((result) => String(result.reason));
      if (failures.length > 0) setNotice(failures.join(" · "));
    } catch (error) {
      if (requestId === refreshId.current && manifest === currentManifest.current) setNotice(String(error));
    } finally { endWork(); }
  }, [beginWork, endWork, manifest]);

  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => { setRemoteStatus(undefined); setRemotePlan(undefined); setRemoteResult(undefined); setCatalogPreview(undefined); setSyncResult(undefined); }, [manifest]);

  function loadManifest() {
    const next = manifestDraft.trim();
    if (next && next !== manifest) {
      setLoadedManifest(undefined);
      setNotice(undefined);
      setManifest(next);
    }
  }

  async function action(name: "setup" | "up" | "down") {
    beginWork();
    try { const result = await runAction(manifest, name); await refresh(); setNotice(result); }
    catch (error) { setNotice(String(error)); }
    finally { endWork(); }
  }

  async function toggleModel(model: Model) {
    const visibility = model.visibility === "list" ? "hide" : "list";
    beginWork();
    setSyncResult(undefined);
    try { await setModelVisibility(manifest, model.slug, visibility); await performSync(); }
    catch (error) { await refresh(); setNotice(String(error)); }
    finally { endWork(); }
  }

  async function performSync() {
    const local = await syncPlatforms(manifest);
    let remote: RemoteSyncReport | undefined;
    let remoteError: string | undefined;
    if (remoteTarget.trim()) {
      try { remote = await syncRemote(manifest, remoteTarget.trim()); }
      catch (error) { remoteError = String(error); }
    }
    await refresh();
    setSyncResult(local);
    if (remote) setRemoteResult(remote);
    setNotice(`Local: ${local.changed} updated, ${local.failed} blocked or failed.${remote ? ` Remote: ${remote.detail}` : remoteError ? ` Remote failed: ${remoteError}` : ""}`);
  }

  async function syncModels() {
    beginWork();
    try { await performSync(); }
    catch (error) { setNotice(String(error)); }
    finally { endWork(); }
  }

  async function initializeManifest() {
    beginWork();
    try { const result = await initManifest(manifest); await refresh(); setNotice(`Created ${result.manifest_path} from ${result.profile_path}`); }
    catch (error) { setNotice(String(error)); }
    finally { endWork(); }
  }

  async function previewCatalogSetup() {
    beginWork();
    try { const result = await bootstrapCatalog(manifest, false); setCatalogPreview(result); setNotice(`${result.cpa_models} CPA models ready to create at ${result.path}`); }
    catch (error) { setCatalogPreview(undefined); setNotice(String(error)); }
    finally { endWork(); }
  }

  async function createCatalog() {
    beginWork();
    try { const result = await bootstrapCatalog(manifest, true); setCatalogPreview(undefined); await refresh(); setNotice(`Created ${result.cpa_models} models at ${result.path}. Configure the CPA Codex profile to use this catalog if needed.`); }
    catch (error) { setNotice(String(error)); }
    finally { endWork(); }
  }

  function setRemoteTarget(value: string) {
    updateRemoteTarget(value);
    setRemoteStatus(undefined);
    setRemotePlan(undefined);
    setRemoteResult(undefined);
  }

  async function checkRemote() {
    beginWork();
    setRemotePlan(undefined);
    try {
      const target = remoteTarget.trim();
      const status = await scanRemote(manifest, target);
      setRemoteStatus(status);
      localStorage.setItem("bridge-remote-target", target);
      if (status.ready) setRemotePlan(await previewRemoteSync(manifest, target));
      setNotice(status.detail);
    } catch (error) { setNotice(String(error)); }
    finally { endWork(); }
  }

  async function syncRemoteModels() {
    beginWork();
    try {
      const result = await syncRemote(manifest, remoteTarget.trim());
      setRemoteResult(result);
      setRemoteStatus(await scanRemote(manifest, remoteTarget.trim()));
      setRemotePlan(await previewRemoteSync(manifest, remoteTarget.trim()));
      setNotice(result.detail);
    } catch (error) { setNotice(String(error)); }
    finally { endWork(); }
  }

  return (
    <main className="app-shell">
      <Sidebar view={view} onView={setView} />
      <div className="content">
        {view === "overview" && <Overview status={current ? status : undefined} doctor={current ? doctor : undefined} busy={busy || !current} onRefresh={refresh} onAction={action} />}
        {view === "models" && <Models report={current ? models : undefined} plan={current ? platformPlan : undefined} syncResult={current ? syncResult : undefined} remoteTarget={remoteTarget} query={query} setQuery={setQuery} onToggle={toggleModel} onSync={syncModels} busy={busy || !current} />}
        {view === "settings" && <Settings manifest={manifest} manifestDraft={manifestDraft} setManifestDraft={setManifestDraft} onLoadManifest={loadManifest} status={current ? status : undefined} catalogReady={current && Boolean(models)} catalogPreview={current ? catalogPreview : undefined} platforms={current ? platforms : undefined} remoteTarget={remoteTarget} setRemoteTarget={setRemoteTarget} remoteStatus={current ? remoteStatus : undefined} remotePlan={current ? remotePlan : undefined} remoteResult={current ? remoteResult : undefined} onRemoteCheck={checkRemote} onRemoteSync={syncRemoteModels} onCatalogPreview={previewCatalogSetup} onCatalogCreate={createCatalog} onClaudeCreated={refresh} onNotice={setNotice} onBeginWork={beginWork} onEndWork={endWork} onAction={action} onInit={initializeManifest} busy={busy || !current} />}
        {notice && <div className="toast"><span>{notice}</span><button aria-label="Dismiss" onClick={() => setNotice(undefined)}>×</button></div>}
      </div>
    </main>
  );
}
