import { useEffect, useState } from "react";
import { ApiError } from "../../lib/api";
import { musicNamingApi, type MusicOrganizePreview, type MusicOrganizeSummary, type MusicRenameItem } from "../../lib/musicNamingApi";
import { jobFailed, useJob } from "../../lib/useJob";
import { Button, Modal, useConfirm } from "../../ui";

const PAGE = 200;
const plural = (n: number, one: string, many = one + "s") => `${n.toLocaleString()} ${n === 1 ? one : many}`;

// The last three levels of a path: enough to see artist, album and file.
const tail = (p: string) => p.split(/[\\/]/).filter(Boolean).slice(-3).join("/");

// MusicRenameModal moves files already in the library to the names the music naming gives
// them now (MUS-34): every move is listed first, from a dry run that changes nothing, and
// only the moves shown are made. Files, cover, booklets, scans and lyrics move together;
// anything whose new name is taken is left alone, and nothing is ever overwritten.
//
// With albumId it covers one album and applies at once; without, the whole library, as a
// background job checked against the preview's token.
export function MusicRenameModal({ onClose, albumId, onRenamed }: { onClose: () => void; albumId?: number; onRenamed?: () => void }) {
  const confirm = useConfirm();
  const [items, setItems] = useState<MusicRenameItem[] | null>(null);
  const [preview, setPreview] = useState<MusicOrganizePreview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [starting, setStarting] = useState(false);
  const [jobId, setJobId] = useState<number | null>(null);
  const [summary, setSummary] = useState<{ text: string; failed: boolean } | null>(null);

  const job = useJob(jobId, {
    giveUpMs: 6 * 60 * 60 * 1000,
    onDone: (j) => {
      const s = j.result as MusicOrganizeSummary | undefined;
      const failed = jobFailed(j);
      setSummary({ text: failed ? j.error || "The rename stopped." : j.message || `Renamed ${s?.renamed ?? 0} files.`, failed });
      if (!failed) onRenamed?.();
    },
  });

  useEffect(() => {
    let alive = true;
    const load = albumId
      ? musicNamingApi.albumRenamePreview(albumId).then((r) => { if (alive) setItems(r.items ?? []); })
      : musicNamingApi.organizePreview(0, PAGE).then((p) => { if (alive) { setPreview(p); setItems(p.items ?? []); } });
    load.catch((e: Error) => { if (alive) setError(e.message); });
    return () => { alive = false; };
  }, [albumId]);

  const more = async () => {
    if (!preview || !items) return;
    setLoadingMore(true);
    try {
      const p = await musicNamingApi.organizePreview(items.length, PAGE);
      if (p.token !== preview.token) {
        setError("The library changed while you were looking — close and preview again.");
        return;
      }
      setItems([...items, ...(p.items ?? [])]);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoadingMore(false);
    }
  };

  const moves = (items ?? []).filter((it) => !it.conflict);
  const conflicts = (items ?? []).filter((it) => it.conflict);
  const toMove = albumId ? moves.length : preview?.counts.rename ?? 0;

  const apply = async () => {
    const ok = await confirm({
      title: `Rename ${plural(toMove, "file")}?`,
      body: "Each file moves to its new name, with the album's cover, booklets, scans and lyrics. Folders left empty are removed. Anything whose new name is taken is left alone, and nothing is ever overwritten.",
      confirmLabel: "Rename",
    });
    if (!ok) return;
    setStarting(true);
    setError(null);
    try {
      if (albumId) {
        const r = await musicNamingApi.albumRename(albumId, moves);
        const skipped = r.skipped?.length ?? 0;
        setSummary({ text: `Renamed ${plural(r.renamed, "file")}${skipped ? ` · ${skipped} skipped (changed since the preview, or their name is taken)` : ""}.`, failed: false });
        onRenamed?.();
      } else if (preview) {
        const r = await musicNamingApi.organizeApply(preview.token);
        if (r.job_id) setJobId(r.job_id);
        else setSummary({ text: "Renaming in the background.", failed: false });
      }
    } catch (e) {
      let msg = (e as Error).message;
      if (e instanceof ApiError && e.status === 409 && e.body?.job_id) msg = "A music rename is already running — wait for it to finish.";
      setError(msg);
    } finally {
      setStarting(false);
    }
  };

  const running = jobId !== null && !summary;
  const pct = Math.round((job.job?.progress ?? 0) * 100);

  const footer = summary || (jobId !== null && !job.running) ? (
    <Button variant="primary" onClick={onClose}>Close</Button>
  ) : running ? (
    <Button variant="ghost" onClick={onClose}>Hide — it keeps going</Button>
  ) : (
    <>
      <Button variant="ghost" onClick={onClose} disabled={starting}>Cancel</Button>
      <Button variant="primary" onClick={apply} busy={starting} busyLabel="Renaming…" disabled={items === null || toMove === 0}>
        {toMove > 0 ? `Rename ${plural(toMove, "file")}` : "Nothing to rename"}
      </Button>
    </>
  );

  return (
    <Modal onClose={onClose} size="xl" title={albumId ? "Rename this album's files" : "Rename music"} footer={footer} dismissible={!starting}>
      <p className="m-0 mb-3 text-sm text-ink-dim">
        {albumId ? "This album's files" : "Every album with files"}, checked against your music naming. This preview changes nothing.
      </p>
      {error && <div className="mb-2 text-sm" role="alert" style={{ color: "var(--reject-text)" }}>{error}</div>}

      {summary ? (
        <div className="rounded-lg p-3 text-sm" role="status" style={{ border: `1px solid ${summary.failed ? "var(--reject)" : "var(--accent-line)"}`, background: summary.failed ? "var(--reject-soft)" : "var(--accent-soft)", color: summary.failed ? "var(--reject-text)" : "var(--accent-text)" }}>
          {summary.text}
        </div>
      ) : jobId !== null ? (
        <div role="status">
          <div className="mb-1.5 text-sm text-ink-dim">{job.message || "Renaming…"}</div>
          <div className="h-1.5 overflow-hidden rounded-full" style={{ background: "var(--panel-2)" }}>
            <div className="h-full rounded-full transition-all" style={{ width: `${pct}%`, background: "var(--accent)" }} />
          </div>
          {!job.running && <div className="mt-2 text-sm text-ink-faint">Still renaming in the background — the job carries on if you close this.</div>}
        </div>
      ) : items === null ? (
        !error && <div className="p-6 text-center text-base text-ink-dim">Working out the new names…</div>
      ) : (
        <>
          <div className="mb-2 font-mono text-sm" aria-live="polite">
            <span style={{ color: "var(--ink)" }}>{plural(toMove, "file")} to rename</span>
            {preview && <span className="text-ink-faint"> · {plural(preview.counts.albums, "album")}</span>}
            {(preview ? preview.counts.conflict : conflicts.length) > 0 && (
              <span style={{ color: "var(--reject-text)" }}> · {plural(preview ? preview.counts.conflict : conflicts.length, "conflict")}</span>
            )}
          </div>
          {items.length === 0 ? (
            <div className="p-6 text-center text-base text-ink-dim">{albumId ? "This album is already named to the scheme." : "Every album is already named to the scheme."}</div>
          ) : (
            <div className="thin-scroll max-h-[50vh] overflow-y-auto">
              {items.map((it) => (
                <div key={it.from} className="py-1.5" style={{ borderBottom: "1px solid var(--line-soft)" }}>
                  {!albumId && it.album && (
                    <div className="text-sm font-semibold">
                      {it.album}
                      {it.artist && <span className="font-normal text-ink-dim"> · {it.artist}</span>}
                    </div>
                  )}
                  <div className="break-all font-mono text-xs text-ink-faint" title={it.from}>{tail(it.from)}{!it.track_id && <span className="text-ink-faint"> (extra)</span>}</div>
                  <div className="break-all font-mono text-xs" title={it.to} style={{ color: it.conflict ? "var(--reject-text)" : "var(--ink)" }}>→ {tail(it.to)}</div>
                  {it.conflict && <div className="mt-0.5 text-xs" style={{ color: "var(--reject-text)" }}>Won't be renamed: {it.conflict}</div>}
                </div>
              ))}
              {preview && items.length < preview.total && (
                <div className="py-2 text-center">
                  <Button size="sm" variant="ghost" onClick={more} busy={loadingMore} busyLabel="Loading…">
                    Show more ({(preview.total - items.length).toLocaleString()} left)
                  </Button>
                </div>
              )}
            </div>
          )}
        </>
      )}
    </Modal>
  );
}
