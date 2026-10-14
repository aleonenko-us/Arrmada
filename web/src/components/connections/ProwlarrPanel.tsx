import { useEffect, useState } from "react";

import { indexerApi } from "../../lib/indexerApi";
import { prowlarrLastSyncLine, prowlarrSyncMessage, prowlarrUILink } from "../../lib/indexerStatus";
import { useQuery } from "../../lib/query";

// ProwlarrPanel opens inside the Prowlarr card: pull Prowlarr's indexers in as Torznab
// feeds (and from then on every hour). Re-syncing keeps what was set here; indexers
// disabled or removed in Prowlarr are turned off. A blank key reuses the saved one.

// isBundledProwlarr: the Prowlarr that ships with Arrmada, by its compose service name.
function isBundledProwlarr(u: string): boolean {
  try {
    return new URL(u).hostname.toLowerCase() === "arrmada-prowlarr";
  } catch {
    return false;
  }
}

const field = "w-full rounded-lg px-3 py-2 text-md";
const fieldStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;

export function ProwlarrPanel({ onSynced }: { onSynced: () => void }) {
  const [url, setUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [hasKey, setHasKey] = useState(false);
  const [busy, setBusy] = useState(false);
  const [addProxy, setAddProxy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; msg: string } | null>(null);
  const [uiUrl, setUiUrl] = useState("");
  const [uiMsg, setUiMsg] = useState<{ ok: boolean; msg: string } | null>(null);
  // The bundled Prowlarr is Arrmada's own and always gets Arrmada's FlareSolverr; any other
  // Prowlarr is someone's own setup and is only changed when this box is ticked.
  const bundled = isBundledProwlarr(url);
  // Re-read after a sync (clicked or the hourly one) so "Last synced" stays current.
  const info = useQuery("prowlarr-info", () => indexerApi.prowlarrInfo(), { staleMs: 60_000 });

  useEffect(() => {
    const i = info.data;
    if (!i) return;
    setUrl((u) => u || i.url);
    setHasKey(i.has_key);
    setUiUrl((u) => u || i.ui_url || "");
  }, [info.data]);

  const sync = async () => {
    setBusy(true);
    setResult(null);
    try {
      const r = await indexerApi.syncProwlarr({ url, api_key: apiKey, add_flaresolverr_proxy: !bundled && addProxy });
      setResult({ ok: true, msg: prowlarrSyncMessage(r) });
      setHasKey(true);
      setApiKey("");
      onSynced();
    } catch (e) {
      setResult({ ok: false, msg: (e as Error).message });
    } finally {
      setBusy(false);
      void info.refetch();
    }
  };

  const saveUiUrl = async () => {
    setUiMsg(null);
    try {
      const r = await indexerApi.saveProwlarrUIURL(uiUrl.trim());
      setUiUrl(r.ui_url);
      setUiMsg({ ok: true, msg: r.ui_url ? "Saved" : "Cleared — the link uses this host again" });
      void info.refetch();
    } catch (e) {
      setUiMsg({ ok: false, msg: (e as Error).message });
    }
  };

  // Prowlarr's own UI as the user's browser reaches it (this host on the port Prowlarr is
  // published on, or a saved address), not the internal name Arrmada uses to talk to it.
  const prowlarrUI = prowlarrUILink(info.data, window.location);
  const port = info.data?.ui_port || "9696";
  const last = prowlarrLastSyncLine(info.data?.last_sync);

  return (
    <div className="mt-2 rounded-lg p-3.5" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      <p className="m-0 mb-3 text-sm text-ink-dim">
        Pull your Prowlarr indexers in as Torznab feeds: API search, no scraping. Once synced it re-syncs every hour: what you set here is kept; indexers disabled or removed in Prowlarr are turned off. Add trackers in Prowlarr first.
      </p>
      {last && <p className="m-0 mb-3 break-words text-sm" style={{ color: last.color }}>{last.text}</p>}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1.5">
          <span className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Prowlarr URL</span>
          <input className={field} style={fieldStyle} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="http://arrmada-prowlarr:9696" />
        </label>
        <label className="flex flex-col gap-1.5">
          <span className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">API key {hasKey && <span className="text-xs normal-case" style={{ color: "var(--good-text)" }}>· saved</span>}</span>
          <input
            type="password"
            className={field}
            style={fieldStyle}
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder={hasKey ? "•••••••• (leave blank to reuse)" : `${bundled ? `Bundled Prowlarr (port ${port})` : "Prowlarr"} → Settings → General`}
            autoComplete="off"
          />
        </label>
      </div>
      <p className="mt-2.5 text-xs text-ink-faint">
        The URL must be reachable from the Arrmada server. With the bundled Prowlarr (<code className="font-mono">docker compose --profile prowlarr up -d</code>) the default above works.
      </p>

      <div className="mt-3 flex flex-col gap-1.5 rounded-lg p-3 text-sm" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
        <div className="break-words">
          <span className="text-ink-faint">Open Prowlarr to add trackers:</span>{" "}
          <a href={prowlarrUI} target="_blank" rel="noreferrer" className="break-all font-mono font-semibold" style={{ color: "var(--accent-text)" }}>{prowlarrUI} ↗</a>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <input
            aria-label="Prowlarr web address"
            className="min-w-0 flex-1 basis-[200px] rounded-lg px-2.5 py-1.5 text-sm"
            style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
            value={uiUrl}
            onChange={(e) => setUiUrl(e.target.value)}
            placeholder="Prowlarr on another machine? Its address, e.g. http://192.168.1.10:9696"
          />
          <button type="button" onClick={saveUiUrl} className="rounded-lg px-3 py-1.5 text-sm" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>
            Save address
          </button>
          {uiMsg && <span className="text-xs" style={{ color: uiMsg.ok ? "var(--good-text)" : "var(--reject-text)" }}>{uiMsg.msg}</span>}
        </div>
        <div className="text-ink-dim">
          For a Cloudflare-protected tracker, add the{" "}
          <code className="rounded px-1 py-0.5 font-mono text-xs" style={{ background: "var(--panel-2)", color: "var(--accent-text)" }}>flaresolverr</code>{" "}
          tag to that tracker in Prowlarr; syncing the bundled Prowlarr sets up the FlareSolverr proxy it uses. Public indexers don't need it.
        </div>
      </div>

      {!bundled && (
        <label className="mt-2.5 flex items-center gap-2 text-sm text-ink-dim">
          <input type="checkbox" checked={addProxy} onChange={(e) => setAddProxy(e.target.checked)} />
          Add Arrmada's FlareSolverr to this Prowlarr
        </label>
      )}

      {result && (
        <div role="status" className="mt-2.5 break-words text-sm" style={{ color: result.ok ? "var(--good-text)" : "var(--reject-text)" }}>
          {result.ok ? "✓ " : "✕ "}{result.msg}
        </div>
      )}
      <button onClick={sync} disabled={busy} className="mt-3 rounded-lg px-4 py-2 text-base font-semibold" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)", opacity: busy ? 0.6 : 1 }}>
        {busy ? "Syncing…" : "Sync now"}
      </button>
    </div>
  );
}
