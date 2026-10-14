import { useEffect, useId, useMemo, useState } from "react";
import { IconButton, Modal } from "../../ui";
import { type RenameSkip, type SeriesRenameItem } from "../../lib/api";
import { seriesApi } from "../../lib/seriesApi";

const base = (p: string) => p.slice(Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\")) + 1);
const seasonName = (n: number) => (n === 0 ? "Specials" : `Season ${n}`);

// RenameModal shows every file a rename would move before anything moves. The button used
// to fetch this list and then rename straight away, so a renumber's surprises were only
// discovered on disk. Confirm sends back exactly the previewed moves; anything that changed
// in between is skipped by the server and listed here, never applied blind.
export function RenameModal({ seriesId, title, onClose, onRenamed }: { seriesId: number; title: string; onClose: () => void; onRenamed: () => void }) {
  const [items, setItems] = useState<SeriesRenameItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const titleId = useId();
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ renamed: number; skipped: RenameSkip[] } | null>(null);

  useEffect(() => {
    let alive = true;
    seriesApi.seriesRenamePreview(seriesId)
      .then((r) => { if (alive) setItems(r.items ?? []); })
      .catch((e: Error) => { if (alive) setError(e.message); });
    return () => { alive = false; };
  }, [seriesId]);

  const moves = useMemo(() => (items ?? []).filter((it) => !it.conflict), [items]);
  const conflicts = (items ?? []).length - moves.length;
  const bySeason = useMemo(() => {
    const m = new Map<number, SeriesRenameItem[]>();
    for (const it of items ?? []) m.set(it.season, [...(m.get(it.season) ?? []), it]);
    // Specials last, as on the series page.
    return [...m.entries()].sort(([a], [b]) => (a === 0 ? 1 : b === 0 ? -1 : a - b));
  }, [items]);

  const confirm = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await seriesApi.renameSeries(seriesId, moves);
      setResult({ renamed: res.renamed, skipped: res.skipped ?? [] });
      onRenamed();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal onClose={onClose} labelledBy={titleId} size="xl">
      <div className="mb-1 flex items-center justify-between">
        <h2 id={titleId} className="m-0 text-lg font-bold">Rename files</h2>
        <IconButton label="Close" onClick={onClose} className="text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
      </div>
      <p className="mb-3 text-sm text-ink-dim">
        Episode files of <b>{title}</b> that aren't named to the library scheme yet. Nothing moves until you confirm, and no existing file is ever replaced.
      </p>
      {error && <div className="mb-2 text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}

      {result ? (
        <div>
          <div className="mb-3 rounded-lg p-3 text-sm" style={{ border: "1px solid var(--accent-line)", background: "var(--accent-soft)", color: "var(--accent-text)" }}>
            Renamed {result.renamed} file{result.renamed === 1 ? "" : "s"}{result.skipped.length > 0 ? ` · skipped ${result.skipped.length}` : ""}.
          </div>
          {result.skipped.length > 0 && (
            <div className="thin-scroll max-h-[40vh] overflow-y-auto">
              {result.skipped.map((sk) => (
                <div key={sk.from} className="py-1.5" style={{ borderBottom: "1px solid var(--line-soft)" }}>
                  <div className="truncate font-mono text-xs" title={sk.from}>{base(sk.from)}</div>
                  <div className="mt-0.5 text-xs" style={{ color: "var(--reject-text)" }}>{sk.reason}</div>
                </div>
              ))}
            </div>
          )}
          <div className="mt-4 flex justify-end">
            <button onClick={onClose} className="rounded-lg px-3 py-2 text-base font-semibold" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" }}>Close</button>
          </div>
        </div>
      ) : items === null ? (
        !error && <div className="p-6 text-center text-base text-ink-dim">Working out the new names…</div>
      ) : items.length === 0 ? (
        <div className="p-6 text-center text-base text-ink-dim">Already named correctly.</div>
      ) : (
        <>
          <div className="mb-2 font-mono text-xs text-ink-faint">
            {moves.length} file{moves.length === 1 ? "" : "s"} to rename
            {conflicts > 0 && <span style={{ color: "var(--reject-text)" }}> · {conflicts} can't be renamed</span>}
          </div>
          <div className="thin-scroll max-h-[52vh] overflow-y-auto">
            {bySeason.map(([season, list]) => (
              <div key={season} className="mb-3">
                <div className="mb-1 text-xs font-semibold text-ink-dim">{seasonName(season)}</div>
                {list.map((it) => (
                  <div key={it.from} className="py-1.5" style={{ borderBottom: "1px solid var(--line-soft)" }}>
                    <div className="truncate font-mono text-xs text-ink-faint" title={it.from}>{base(it.from)}</div>
                    <div className="truncate font-mono text-xs" title={it.to} style={{ color: it.conflict ? "var(--reject-text)" : "var(--ink)" }}>→ {base(it.to)}</div>
                    {it.conflict && <div className="mt-0.5 text-xs" style={{ color: "var(--reject-text)" }}>Not renamed: {it.conflict}</div>}
                  </div>
                ))}
              </div>
            ))}
          </div>
          <div className="mt-4 flex justify-end gap-2">
            <button onClick={onClose} className="rounded-lg px-3 py-2 text-base font-semibold" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" }}>Cancel</button>
            <button
              onClick={confirm}
              disabled={busy || moves.length === 0}
              className="rounded-lg px-3 py-2 text-base font-semibold disabled:opacity-50"
              style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}
            >
              {busy ? "Renaming…" : `Rename ${moves.length} file${moves.length === 1 ? "" : "s"}`}
            </button>
          </div>
        </>
      )}
    </Modal>
  );
}
