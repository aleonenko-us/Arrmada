import { useEffect, useState } from "react";
import { api, ApiError, type MovieRenameItem, type OrganizePreview, type OrganizeSummary } from "../lib/api";
import { jobFailed, useJob } from "../lib/useJob";
import { Button, Modal, useConfirm } from "../ui";
import { pathTail, RenameExtras } from "./MovieRenameModal";

const PAGE = 200;
const plural = (n: number, one: string, many = one + "s") => `${n.toLocaleString()} ${n === 1 ? one : many}`;

// OrganizeModal applies the naming scheme across the movie library — or a selection, from
// the mass editor — after a naming change. It shows the counts first, then every file
// that would move (old → new) and every conflict, all from a dry run that changes nothing.
// Apply asks once more, then runs in the background with live progress; anything that
// changed since the preview, or that would overwrite another file, is skipped.
export function OrganizeModal({ ids, onClose, onDone }: { ids?: number[]; onClose: () => void; onDone?: () => void }) {
  const confirm = useConfirm();
  const [preview, setPreview] = useState<OrganizePreview | null>(null);
  const [items, setItems] = useState<MovieRenameItem[]>([]);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const [jobId, setJobId] = useState<number | null>(null);
  const [summary, setSummary] = useState<{ text: string; failed: boolean } | null>(null);
  const scoped = !!ids?.length;
  const idsKey = ids?.join(",") ?? "";

  const job = useJob(jobId, {
    onDone: (j) => {
      const s = j.result as OrganizeSummary | undefined;
      const failed = jobFailed(j);
      setSummary({ text: failed ? j.error || "The rename stopped." : j.message || `Renamed ${s?.renamed ?? 0} files.`, failed });
      onDone?.();
    },
  });

  useEffect(() => {
    let alive = true;
    const list = idsKey ? idsKey.split(",").map(Number) : undefined;
    api.organizePreview(list, 0, PAGE)
      .then((p) => { if (alive) { setPreview(p); setItems(p.items ?? []); } })
      .catch((e: Error) => { if (alive) setError(e.message); });
    return () => { alive = false; };
  }, [idsKey]);

  const more = async () => {
    if (!preview) return;
    setLoadingMore(true);
    try {
      const p = await api.organizePreview(ids, items.length, PAGE);
      if (p.token !== preview.token) {
        setError("The library changed while you were looking — close and preview again.");
        return;
      }
      setItems((cur) => [...cur, ...(p.items ?? [])]);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoadingMore(false);
    }
  };

  const apply = async () => {
    if (!preview) return;
    const ok = await confirm({
      title: `Rename ${plural(preview.counts.rename, "file")}?`,
      body: "Each file moves to its new name with its subtitles, and Plex rescans the folders. Files whose new name is taken are left alone, and nothing is ever overwritten.",
      confirmLabel: "Rename",
    });
    if (!ok) return;
    setStarting(true);
    setError(null);
    try {
      const r = await api.organizeApply(preview.token, ids);
      if (r.job_id) setJobId(r.job_id);
      else setSummary({ text: "Renaming in the background.", failed: false });
    } catch (e) {
      let msg = (e as Error).message;
      if (e instanceof ApiError && e.status === 409 && e.body?.job_id) msg = "An organize is already running — wait for it to finish.";
      setError(msg);
    } finally {
      setStarting(false);
    }
  };

  const running = jobId !== null && !summary;
  const pct = Math.round((job.job?.progress ?? 0) * 100);
  const c = preview?.counts;

  const footer = summary || (jobId !== null && !job.running) ? (
    <Button variant="primary" onClick={onClose}>Close</Button>
  ) : running ? (
    <Button variant="ghost" onClick={onClose}>Hide — it keeps going</Button>
  ) : (
    <>
      <Button variant="ghost" onClick={onClose} disabled={starting}>Cancel</Button>
      <Button variant="primary" onClick={apply} busy={starting} busyLabel="Starting…" disabled={!c || c.rename === 0}>
        {c && c.rename > 0 ? `Rename ${plural(c.rename, "file")}` : "Nothing to rename"}
      </Button>
    </>
  );

  return (
    <Modal onClose={onClose} size="xl" title={scoped ? `Rename ${plural(ids?.length ?? 0, "movie")}` : "Organize library"} footer={footer} dismissible={!starting}>
      <p className="m-0 mb-3 text-sm text-ink-dim">
        {scoped ? "The selected movies' files" : "Every movie file in your library"}, checked against your naming scheme. This preview changes nothing.
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
      ) : !preview ? (
        !error && <div className="p-6 text-center text-base text-ink-dim">Working out the new names…</div>
      ) : (
        <>
          <div className="mb-2 font-mono text-sm" aria-live="polite">
            <span style={{ color: "var(--ink)" }}>{plural(preview.counts.rename, "file")} to rename</span>
            <span className="text-ink-faint"> · {preview.counts.same.toLocaleString()} already correct</span>
            {preview.counts.conflict > 0 && <span style={{ color: "var(--reject-text)" }}> · {plural(preview.counts.conflict, "conflict")}</span>}
            {preview.counts.missing > 0 && <span className="text-ink-faint"> · {preview.counts.missing.toLocaleString()} not on disk</span>}
          </div>
          {items.length === 0 ? (
            <div className="p-6 text-center text-base text-ink-dim">Everything is already named to the scheme.</div>
          ) : (
            <div className="thin-scroll max-h-[50vh] overflow-y-auto">
              {items.map((it) => (
                <div key={`${it.movie_id}:${it.version_id}`} className="py-1.5" style={{ borderBottom: "1px solid var(--line-soft)" }}>
                  <div className="text-sm font-semibold">
                    {it.title} {it.year ? <span className="font-normal text-ink-faint">({it.year})</span> : null}
                    {it.version_id !== 0 && <span className="font-normal text-ink-dim"> · {it.label}</span>}
                  </div>
                  <div className="break-all font-mono text-xs text-ink-faint" title={it.current}>{pathTail(it.current)}</div>
                  <div className="break-all font-mono text-xs" title={it.proposed} style={{ color: it.status === "conflict" ? "var(--reject-text)" : "var(--ink)" }}>→ {pathTail(it.proposed)}</div>
                  <RenameExtras it={it} />
                  {it.status === "conflict" && <div className="mt-0.5 text-xs" style={{ color: "var(--reject-text)" }}>Won't be renamed: {it.reason}</div>}
                </div>
              ))}
              {items.length < preview.total && (
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
