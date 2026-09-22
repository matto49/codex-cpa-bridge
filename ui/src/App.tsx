import { useCallback, useEffect, useMemo, useState } from "react";
import { Check, ChevronRight, CircleAlert, Eye, EyeOff, Power, RefreshCw, Server, Settings2, SlidersHorizontal, TerminalSquare } from "lucide-react";
import { Doctor, Model, ModelReport, Status, getDoctor, getModels, getStatus, runAction, setModelVisibility } from "./api";

type View = "overview" | "models" | "settings";
const DEFAULT_MANIFEST = "/Users/bytedance/codex-cpa-bridge/examples/mac.toml";

function StateDot({ ok }: { ok: boolean }) {
  return <span className={ok ? "state-dot ok" : "state-dot bad"} aria-hidden="true" />;
}

function Toggle({ checked, onChange, label }: { checked: boolean; onChange: () => void; label: string }) {
  return (
    <button className={`toggle ${checked ? "checked" : ""}`} role="switch" aria-checked={checked} aria-label={label} onClick={onChange}>
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
      <div className="brand"><span className="brand-mark">C</span><span>Codex CPA</span></div>
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

function Models({ report, query, setQuery, onToggle }: { report?: ModelReport; query: string; setQuery: (value: string) => void; onToggle: (model: Model) => void }) {
  const filtered = useMemo(() => report?.models.filter((model) => `${model.display_name} ${model.slug}`.toLowerCase().includes(query.toLowerCase())) ?? [], [report, query]);
  const visible = report?.models.filter((model) => model.visibility === "list").length ?? 0;
  return (
    <div className="view">
      <header className="page-head"><div><h1>Models</h1><p>{visible} visible of {report?.models.length ?? 0}</p></div></header>
      <div className="model-toolbar"><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter models" aria-label="Filter models" /><code>{report?.path ?? "No catalog configured"}</code></div>
      <section className="model-table">
        <div className="model-table-head"><span>Model</span><span>Slug</span><span>API</span><span>Visible</span></div>
        {filtered.map((model) => (
          <div className="model-row" key={model.slug}>
            <div><strong>{model.display_name || model.slug}</strong><small>Priority {model.priority}</small></div>
            <code>{model.slug}</code>
            <span className={model.supported_in_api ? "api yes" : "api no"}>{model.supported_in_api ? "Ready" : "No"}</span>
            <Toggle checked={model.visibility === "list"} onChange={() => onToggle(model)} label={`Show ${model.display_name || model.slug}`} />
          </div>
        ))}
        {filtered.length === 0 && <div className="empty">No matching models</div>}
      </section>
    </div>
  );
}

function Settings({ manifest, setManifest, status, onAction, busy }: { manifest: string; setManifest: (value: string) => void; status?: Status; onAction: (action: "setup" | "up" | "down") => void; busy: boolean }) {
  return (
    <div className="view narrow">
      <header className="page-head"><div><h1>Settings</h1><p>Bridge-owned configuration</p></div></header>
      <section className="settings-section"><h2>Manifest</h2><label><span>Path</span><input value={manifest} onChange={(event) => setManifest(event.target.value)} /></label></section>
      <section className="settings-section"><h2>Profile paths</h2><dl><div><dt>Native Codex</dt><dd>{status?.official_home ?? "-"}</dd></div><div><dt>CPA Codex</dt><dd>{status?.cpa_home ?? "-"}</dd></div><div><dt>CPA endpoint</dt><dd>{status?.cpa_endpoint ?? "-"}</dd></div></dl></section>
      <section className="settings-section action-section"><div><h2>Local SSH service</h2><p>{status?.ssh_port_open ? "Running" : "Stopped"}</p></div><div><button className="secondary" onClick={() => onAction("down")} disabled={busy || !status?.ssh_port_open}>Stop</button><button className="primary" onClick={() => onAction("setup")} disabled={busy}>Run setup</button></div></section>
    </div>
  );
}

export default function App() {
  const [view, setView] = useState<View>("overview");
  const [manifest, setManifest] = useState(() => localStorage.getItem("bridge-manifest") || DEFAULT_MANIFEST);
  const [status, setStatus] = useState<Status>();
  const [doctor, setDoctor] = useState<Doctor>();
  const [models, setModels] = useState<ModelReport>();
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string>();

  const refresh = useCallback(async () => {
    setBusy(true);
    setNotice(undefined);
    try {
      const [statusResult, doctorResult, modelsResult] = await Promise.allSettled([getStatus(manifest), getDoctor(manifest), getModels(manifest)]);
      if (statusResult.status === "fulfilled") setStatus(statusResult.value);
      if (doctorResult.status === "fulfilled") setDoctor(doctorResult.value);
      if (modelsResult.status === "fulfilled") setModels(modelsResult.value);
      localStorage.setItem("bridge-manifest", manifest);
      const failures = [statusResult, doctorResult, modelsResult]
        .filter((result): result is PromiseRejectedResult => result.status === "rejected")
        .map((result) => String(result.reason));
      if (failures.length > 0) setNotice(failures.join(" · "));
    } catch (error) {
      setNotice(String(error));
    } finally { setBusy(false); }
  }, [manifest]);

  useEffect(() => { void refresh(); }, [refresh]);

  async function action(name: "setup" | "up" | "down") {
    setBusy(true);
    try { const result = await runAction(manifest, name); await refresh(); setNotice(result); }
    catch (error) { setNotice(String(error)); setBusy(false); }
  }

  async function toggleModel(model: Model) {
    const visibility = model.visibility === "list" ? "hide" : "list";
    setModels((current) => current && ({ ...current, models: current.models.map((item) => item.slug === model.slug ? { ...item, visibility } : item) }));
    try { await setModelVisibility(manifest, model.slug, visibility); }
    catch (error) { setNotice(String(error)); await refresh(); }
  }

  return (
    <main className="app-shell">
      <Sidebar view={view} onView={setView} />
      <div className="content">
        {view === "overview" && <Overview status={status} doctor={doctor} busy={busy} onRefresh={refresh} onAction={action} />}
        {view === "models" && <Models report={models} query={query} setQuery={setQuery} onToggle={toggleModel} />}
        {view === "settings" && <Settings manifest={manifest} setManifest={setManifest} status={status} onAction={action} busy={busy} />}
        {notice && <div className="toast"><span>{notice}</span><button aria-label="Dismiss" onClick={() => setNotice(undefined)}>×</button></div>}
      </div>
    </main>
  );
}
