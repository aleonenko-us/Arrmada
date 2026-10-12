import { useState } from "react";
import { type DownloadClient, type NewDownloadClient, type PathMap } from "../../lib/api";
import { downloadClientApi, type PathReport } from "../../lib/downloadClientApi";
import { Button } from "../../ui";

// PathMappings sits in a download client's editor: the folders it sees under another path
// than Arrmada does (a seedbox, or qBittorrent on another machine), and a check of which of
// its paths Arrmada can actually see. Adding or removing a mapping saves at once; there is
// no Save button. The bundled qBittorrent shares Arrmada's folders, so it only gets the check.

const field = "w-full min-w-0 rounded-lg px-3 py-2 font-mono text-[12px]";
const fieldStyle = { background: "var(--panel)", border: "1px solid var(--line)", color: "var(--ink)" } as const;

const WHAT: Record<string, string> = { save_folder: "Save folder", newest_download: "Newest finished download" };

export function PathMappings({ client, body, onChanged }: {
  client: DownloadClient;
  /** The client's saved settings as a PUT body with these mappings (password blank = kept). */
  body: (maps: PathMap[]) => NewDownloadClient;
  onChanged?: () => void;
}) {
  const [maps, setMaps] = useState<PathMap[]>(client.path_maps ?? []);
  const [remote, setRemote] = useState("");
  const [local, setLocal] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);
  const [report, setReport] = useState<PathReport | null>(null);
  const [checkError, setCheckError] = useState<string | null>(null);

  const save = async (next: PathMap[]) => {
    setSaving(true);
    setError(null);
    try {
      const c = await downloadClientApi.updateDownloadClient(client.id, body(next));
      setMaps(c.path_maps ?? []);
      setReport(null); // it answered for the old mappings
      onChanged?.();
      return true;
    } catch (e) {
      setError((e as Error).message);
      return false;
    } finally {
      setSaving(false);
    }
  };

  const add = async (pm: PathMap) => {
    // A mapping for the same remote folder is replaced, not doubled.
    const next = [...maps.filter((m) => m.remote.replace(/[\\/]+$/, "") !== pm.remote.replace(/[\\/]+$/, "")), pm];
    if (await save(next)) {
      setRemote("");
      setLocal("");
    }
  };

  const check = async () => {
    setChecking(true);
    setCheckError(null);
    setReport(null);
    try {
      const r = await downloadClientApi.testDownloadClient(client.id);
      if (!r.ok) setCheckError(r.error ?? "It didn't answer.");
      else if (r.paths) setReport(r.paths);
      else setCheckError(r.paths_error ? `Connected, but it couldn't say where it saves: ${r.paths_error}` : "Connected; this client can't say where it saves.");
    } catch (e) {
      setCheckError((e as Error).message);
    } finally {
      setChecking(false);
    }
  };

  return (
    <section aria-label="Path mappings" className="mt-2 rounded-lg p-3.5" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-[12.5px] font-semibold">{client.bundled ? "Paths" : "Path mappings"}</div>
        <Button size="sm" onClick={check} busy={checking} busyLabel="Checking…">Check paths</Button>
      </div>
      {!client.bundled && (
        <>
          <p className="mb-0 mt-1 text-[11px] text-ink-faint">
            Only for a qBittorrent that sees its downloads under another path than Arrmada, such as a seedbox or another machine. Left, the folder as qBittorrent names it; right, the same folder inside the Arrmada container.
          </p>
          {maps.length > 0 && (
            <ul className="m-0 mt-2.5 flex list-none flex-col gap-1.5 p-0">
              {maps.map((m) => (
                <li key={m.remote} className="flex min-w-0 items-center gap-2 text-[12px]">
                  <span className="min-w-0 flex-1 break-all font-mono">
                    {m.remote} <span className="text-ink-faint">→</span> {m.local}
                  </span>
                  <Button size="sm" variant="ghost" disabled={saving} aria-label={`Remove the mapping for ${m.remote}`} onClick={() => void save(maps.filter((x) => x !== m))}>
                    Remove
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <form
            aria-label="Add a path mapping"
            className="mt-2.5 grid grid-cols-1 items-end gap-2 sm:grid-cols-[1fr_1fr_auto]"
            onSubmit={(e) => { e.preventDefault(); if (remote.trim() && local.trim()) void add({ remote: remote.trim(), local: local.trim() }); }}
          >
            <label className="flex min-w-0 flex-col gap-1">
              <span className="font-mono text-[9.5px] font-bold uppercase tracking-[0.1em] text-ink-faint">qBittorrent's path</span>
              <input className={field} style={fieldStyle} value={remote} onChange={(e) => setRemote(e.target.value)} placeholder="/downloads" autoComplete="off" />
            </label>
            <label className="flex min-w-0 flex-col gap-1">
              <span className="font-mono text-[9.5px] font-bold uppercase tracking-[0.1em] text-ink-faint">Arrmada's path</span>
              <input className={field} style={fieldStyle} value={local} onChange={(e) => setLocal(e.target.value)} placeholder="/media/downloads" autoComplete="off" />
            </label>
            <Button type="submit" size="md" disabled={saving || !remote.trim() || !local.trim()} busy={saving} busyLabel="Saving…">Add mapping</Button>
          </form>
          {error && <p role="alert" className="mb-0 mt-2 text-[12px]" style={{ color: "var(--reject)" }}>{error}</p>}
        </>
      )}

      {checkError && <p role="alert" className="mb-0 mt-2.5 break-words text-[12px]" style={{ color: "var(--reject)" }}>{checkError}</p>}
      {report && (
        <div className="mt-2.5 flex flex-col gap-1.5" aria-live="polite">
          {report.checks.map((c) => (
            <div key={c.what} className="break-all text-[11.5px]" style={{ color: c.visible ? "var(--good)" : c.gone ? "var(--avoid)" : "var(--reject)" }}>
              {c.visible ? "✓" : c.gone ? "–" : "✕"} <span className="text-ink-dim">{WHAT[c.what] ?? c.what}:</span>{" "}
              <span className="font-mono">{c.remote}</span>
              {c.local !== c.remote && <> <span className="text-ink-faint">→</span> <span className="font-mono">{c.local}</span></>}
              {!c.visible && <span className="text-ink-dim">{c.gone ? " (not there any more: moved or deleted)" : " (Arrmada can't see it)"}</span>}
            </div>
          ))}
          <p className="m-0 break-words text-[11.5px] text-ink-dim">{report.summary}</p>
          {report.suggestion && !client.bundled && (
            <div className="flex flex-wrap items-center gap-2">
              <span className="break-all font-mono text-[11.5px]">{report.suggestion.remote} → {report.suggestion.local}</span>
              <Button size="sm" variant="primary" disabled={saving} onClick={() => void add(report.suggestion as PathMap)}>Use this mapping</Button>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
