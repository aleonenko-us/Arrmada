import { useEffect, useId, useMemo, useState } from "react";
import { IconButton, Modal } from "../../ui";
import { importListNotice, type Series as SeriesT, type SeriesImportCandidate } from "../../lib/api";
import { seriesApi } from "../../lib/seriesApi";
import { fmtSize } from "./util";

export function ManualImportModal({ series, onClose, onImported, onBackground }: { series: SeriesT; onClose: () => void; onImported: () => void; onBackground: (jobId?: number) => void }) {
  const [cands, setCands] = useState<SeriesImportCandidate[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const titleId = useId();
  const [importing, setImporting] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    seriesApi.seriesManualImportList(series.id).then((r) => { setCands(r.candidates); setNotice(importListNotice(r)); }).catch((e: Error) => setError(e.message));
  }, [series.id]);

  // Whole-folder imports. A season pack is dozens of files and clicking each one is not a
  // realistic way to import it — and importing the folder runs the full pipeline (quality
  // inherited from the release name, the upgrade gate) rather than the single-file path.
  const folders = useMemo(() => {
    const byDir = new Map<string, number>();
    for (const c of cands ?? []) {
      const cut = c.path.lastIndexOf("/");
      if (cut <= 0) continue;
      const dir = c.path.slice(0, cut);
      byDir.set(dir, (byDir.get(dir) ?? 0) + 1);
    }
    return [...byDir.entries()]
      .filter(([, n]) => n > 1) // a lone file is already one click away below
      .map(([path, count]) => ({ path, count, name: path.slice(path.lastIndexOf("/") + 1) }))
      .sort((a, b) => b.count - a.count);
  }, [cands]);

  const [background, setBackground] = useState(false);

  const doImport = async (path: string) => {
    setImporting(path);
    setError(null);
    try {
      const res = await seriesApi.seriesManualImport(series.id, path);
      // A whole folder runs detached — a season pack can take many minutes, so the
      // request returns immediately and the work continues. Say so, or the user sits on
      // a spinner wondering whether navigating away cancels it.
      if (res.background) {
        setBackground(true);
        onBackground(res.job_id);
        return;
      }
      onImported();
      // refresh the list so the imported file drops off
      const r = await seriesApi.seriesManualImportList(series.id);
      setCands(r.candidates);
      setNotice(importListNotice(r));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setImporting(null);
    }
  };

  return (
    <Modal onClose={onClose} labelledBy={titleId} size="lg">
      <div className="mb-1 flex items-center justify-between">
        <h2 id={titleId} className="m-0 text-lg font-bold">Manual import</h2>
        <IconButton label="Close" onClick={onClose} className="text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
      </div>
      <p className="mb-3 text-sm text-ink-dim">Pick episode files already on disk to import as <b>{series.title}</b>. Season/episode is detected from each filename; files are renamed to the library scheme.</p>
      {error && <div className="mb-2 text-sm" style={{ color: "var(--reject)" }}>{error}</div>}
      {background && (
        <div className="mb-3 rounded-lg p-3 text-sm" style={{ border: "1px solid var(--accent-line)", background: "var(--accent-soft)", color: "var(--accent)" }}>
          Importing in the background — this can take several minutes for a large pack.
          You can close this and keep using the page; the episode list updates as it goes, and a note appears when it's done.
        </div>
      )}
      {folders.length > 0 && (
        <div className="mb-3">
          <div className="mb-1.5 text-xs font-semibold text-ink-dim">Whole downloads</div>
          {folders.map((f) => (
            <button
              key={f.path}
              onClick={() => doImport(f.path)}
              disabled={importing !== null}
              className="mb-1.5 flex w-full items-center gap-3 rounded-lg p-2.5 text-left disabled:opacity-50"
              style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}
            >
              <div className="min-w-0 flex-1">
                <div className="truncate text-base font-medium" title={f.path}>{f.name}</div>
                <div className="mt-0.5 font-mono text-xs text-ink-faint">
                  {f.count} video files — imported together, keeping whichever copy of each episode is better
                </div>
              </div>
              <span className="flex-none text-sm font-semibold" style={{ color: "var(--accent)" }}>
                {importing === f.path ? "Importing…" : "Import all"}
              </span>
            </button>
          ))}
        </div>
      )}
      {notice && <div className="mb-2 text-sm text-ink-faint">{notice}</div>}
      <div className="thin-scroll max-h-[52vh] overflow-y-auto">
        {cands === null ? (
          <div className="p-6 text-center text-base text-ink-dim">Scanning…</div>
        ) : cands.length === 0 ? (
          <div className="p-6 text-center text-base text-ink-dim">No importable video files found in the downloads folder.</div>
        ) : (
          cands.map((c) => {
            const detected = c.season > 0 && c.episode > 0;
            return (
              <button key={c.path} onClick={() => detected && doImport(c.path)} disabled={importing !== null || !detected} className="flex w-full items-center gap-3 rounded-lg p-2.5 text-left transition-colors hover:bg-[var(--panel-2)] disabled:opacity-50">
                <div className="min-w-0 flex-1">
                  <div className="truncate text-base font-medium" title={c.filename}>{c.filename}</div>
                  <div className="mt-0.5 flex items-center gap-3 font-mono text-xs text-ink-faint">
                    {detected ? <span style={{ color: "var(--accent)" }}>S{String(c.season).padStart(2, "0")}E{String(c.episode).padStart(2, "0")}</span> : <span style={{ color: "var(--avoid)" }}>No S/E detected</span>}
                    {c.quality && <span>{c.quality}</span>}
                    <span>{fmtSize(c.size_bytes)}</span>
                  </div>
                </div>
                <span className="flex-none rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>{importing === c.path ? "Importing…" : "Import"}</span>
              </button>
            );
          })
        )}
      </div>
    </Modal>
  );
}
