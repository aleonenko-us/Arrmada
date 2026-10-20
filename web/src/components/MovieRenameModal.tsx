import { useEffect, useMemo, useState } from "react";
import { api, type MovieRenameItem, type MovieRenameReply } from "../lib/api";
import { Button, Modal } from "../ui";

// tail shows the last two parts of a path ("Heat (1995)/Heat (1995) - 1080p.mkv"): the
// folder matters, because a rename can move a film to a new one.
export function pathTail(p: string): string {
  const parts = p.split(/[\\/]/).filter(Boolean);
  return parts.slice(-2).join("/");
}

// RenameExtras lists what moves along with a planned file, and why the old folder's other
// files stay: the rename moves exactly what the preview shows.
export function RenameExtras({ it }: { it: MovieRenameItem }) {
  if (it.status !== "ok" || (!it.extras?.length && !it.kept)) return null;
  return (
    <span className="mt-0.5 block text-xs text-ink-dim">
      {it.extras?.map((x) => (
        <span key={x.from} className="block break-all font-mono" title={`${x.from} → ${x.to}`}>+ {pathTail(x.from)} → {pathTail(x.to)}</span>
      ))}
      {it.kept && <span className="block">Other files in the old folder stay: {it.kept}</span>}
    </span>
  );
}

const STATUS_NOTE: Record<MovieRenameItem["status"], string> = {
  ok: "",
  same: "Already named correctly",
  conflict: "Can't be renamed",
  missing: "Not on disk",
};

// MovieRenameModal shows old → new for every track of a movie before anything moves. Each
// clean rename has a tick (on by default); conflicts and missing files say why and can't
// be ticked. Apply sends exactly the previewed items, and the server skips any that
// changed in between — the results list says what happened to each.
export function MovieRenameModal({ movieId, title, onClose, onRenamed }: { movieId: number; title: string; onClose: () => void; onRenamed: () => void }) {
  const [items, setItems] = useState<MovieRenameItem[] | null>(null);
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<MovieRenameReply | null>(null);

  useEffect(() => {
    let alive = true;
    api.renamePreview(movieId)
      .then((r) => {
        if (!alive) return;
        const list = r.items ?? [];
        setItems(list);
        setPicked(new Set(list.filter((it) => it.status === "ok").map((it) => it.version_id)));
      })
      .catch((e: Error) => { if (alive) setError(e.message); });
    return () => { alive = false; };
  }, [movieId]);

  const chosen = useMemo(() => (items ?? []).filter((it) => it.status === "ok" && picked.has(it.version_id)), [items, picked]);
  const toggle = (vid: number) => setPicked((s) => { const n = new Set(s); if (n.has(vid)) n.delete(vid); else n.add(vid); return n; });

  const apply = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await api.renameMovie(movieId, chosen);
      setResult(r);
      if (r.renamed > 0) onRenamed();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const footer = result ? (
    <Button variant="primary" onClick={onClose}>Close</Button>
  ) : (
    <>
      <Button variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
      <Button variant="primary" onClick={apply} busy={busy} busyLabel="Renaming…" disabled={chosen.length === 0}>
        {`Rename ${chosen.length} file${chosen.length === 1 ? "" : "s"}`}
      </Button>
    </>
  );

  return (
    <Modal onClose={onClose} size="lg" title="Rename files" footer={footer} dismissible={!busy}>
      <p className="m-0 mb-3 text-sm text-ink-dim">
        Every file of <b>{title}</b> and the name your naming scheme gives it. Nothing moves until you confirm, and no existing file is ever replaced.
      </p>
      {error && <div className="mb-2 whitespace-pre-line text-sm" role="alert" style={{ color: "var(--reject-text)" }}>{error}</div>}
      {result ? (
        <div>
          <div className="mb-3 rounded-lg p-3 text-sm" role="status" style={{ border: "1px solid var(--accent-line)", background: "var(--accent-soft)", color: "var(--accent-text)" }}>
            Renamed {result.renamed} file{result.renamed === 1 ? "" : "s"}{result.skipped > 0 ? ` · ${result.skipped} left as they were` : ""}.
          </div>
          {result.results.map((r) => (
            <div key={`${r.version_id}:${r.from}`} className="py-1.5" style={{ borderBottom: "1px solid var(--line-soft)" }}>
              <div className="text-xs font-semibold text-ink-dim">{r.label}</div>
              <div className="break-all font-mono text-xs" style={{ color: r.renamed ? "var(--ink)" : "var(--ink-faint)" }}>{pathTail(r.renamed ? r.to : r.from)}</div>
              {!r.renamed && <div className="mt-0.5 text-xs" style={{ color: "var(--reject-text)" }}>Not renamed: {r.reason}</div>}
            </div>
          ))}
        </div>
      ) : items === null ? (
        !error && <div className="p-6 text-center text-base text-ink-dim">Working out the new names…</div>
      ) : items.length === 0 ? (
        <div className="p-6 text-center text-base text-ink-dim">This movie has no files to rename.</div>
      ) : (
        <div className="thin-scroll max-h-[52vh] overflow-y-auto">
          {items.map((it) => {
            const ok = it.status === "ok";
            return (
              <label key={it.version_id} className="flex items-start gap-2.5 py-2" style={{ borderBottom: "1px solid var(--line-soft)", cursor: ok ? "pointer" : "default" }}>
                <input type="checkbox" className="mt-0.5" disabled={!ok || busy} checked={ok && picked.has(it.version_id)} onChange={() => toggle(it.version_id)} aria-label={`Rename the ${it.label} file`} />
                <span className="min-w-0 flex-1">
                  <span className="block text-xs font-semibold text-ink-dim">{it.label}</span>
                  <span className="block break-all font-mono text-xs text-ink-faint" title={it.current}>{pathTail(it.current)}</span>
                  {it.status !== "same" && it.status !== "missing" && (
                    <span className="block break-all font-mono text-xs" title={it.proposed} style={{ color: ok ? "var(--ink)" : "var(--reject-text)" }}>→ {pathTail(it.proposed)}</span>
                  )}
                  <RenameExtras it={it} />
                  {!ok && (
                    <span className="mt-0.5 block text-xs" style={{ color: it.status === "same" ? "var(--good-text)" : "var(--reject-text)" }}>
                      {STATUS_NOTE[it.status]}{it.reason ? `: ${it.reason}` : ""}
                    </span>
                  )}
                </span>
              </label>
            );
          })}
        </div>
      )}
    </Modal>
  );
}
