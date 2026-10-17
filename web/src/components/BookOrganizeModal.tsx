import { useEffect, useState } from "react";
import { ApiError } from "../lib/api";
import { bookNamingApi, type BookOrganizePreview, type BookOrganizeSummary, type BookRenameItem } from "../lib/bookNamingApi";
import { jobFailed, useJob } from "../lib/useJob";
import { Button, Modal, useConfirm } from "../ui";

const PAGE = 200;
const plural = (n: number, one: string, many = one + "s") => `${n.toLocaleString()} ${n === 1 ? one : many}`;

// The last three levels of a path: enough to see author, series and book.
const tail = (p: string) => p.split(/[\\/]/).filter(Boolean).slice(-3).join("/");

function what(it: BookRenameItem): string {
  if (it.kind === "ebook") return "Ebook";
  return it.label ? `Audiobook · ${it.label}` : "Audiobook";
}

// BookOrganizeModal applies the book naming scheme to the books already in the library:
// the counts first, then every edition that would move (old → new) and every conflict, all
// from a dry run that changes nothing. Apply asks once more, then runs in the background
// with live progress. A multi-file audiobook moves file by file in its play order, so
// listening places stay where they are; anything that would overwrite a file is skipped.
export function BookOrganizeModal({ onClose }: { onClose: () => void }) {
  const confirm = useConfirm();
  const [preview, setPreview] = useState<BookOrganizePreview | null>(null);
  const [items, setItems] = useState<BookRenameItem[]>([]);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const [jobId, setJobId] = useState<number | null>(null);
  const [summary, setSummary] = useState<{ text: string; failed: boolean } | null>(null);

  const job = useJob(jobId, {
    onDone: (j) => {
      const s = j.result as BookOrganizeSummary | undefined;
      const failed = jobFailed(j);
      setSummary({ text: failed ? j.error || "The rename stopped." : j.message || `Renamed ${s?.renamed ?? 0} books.`, failed });
    },
  });

  useEffect(() => {
    let alive = true;
    bookNamingApi.organizePreview(0, PAGE)
      .then((p) => { if (alive) { setPreview(p); setItems(p.items ?? []); } })
      .catch((e: Error) => { if (alive) setError(e.message); });
    return () => { alive = false; };
  }, []);

  const more = async () => {
    if (!preview) return;
    setLoadingMore(true);
    try {
      const p = await bookNamingApi.organizePreview(items.length, PAGE);
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
      title: `Rename ${plural(preview.counts.rename, "book edition")}?`,
      body: "Each book's files move to their new names, a multi-file audiobook file by file in its play order, so listening places stay put. Folders left empty are removed. Anything whose new name is taken is left alone, and nothing is ever overwritten.",
      confirmLabel: "Rename",
    });
    if (!ok) return;
    setStarting(true);
    setError(null);
    try {
      const r = await bookNamingApi.organizeApply(preview.token);
      if (r.job_id) setJobId(r.job_id);
      else setSummary({ text: "Renaming in the background.", failed: false });
    } catch (e) {
      let msg = (e as Error).message;
      if (e instanceof ApiError && e.status === 409 && e.body?.job_id) msg = "A book rename is already running — wait for it to finish.";
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
        {c && c.rename > 0 ? `Rename ${plural(c.rename, "edition")}` : "Nothing to rename"}
      </Button>
    </>
  );

  return (
    <Modal onClose={onClose} size="xl" title="Rename books" footer={footer} dismissible={!starting}>
      <p className="m-0 mb-3 text-sm text-ink-dim">
        Every ebook and audiobook in your library, checked against your book naming. This preview changes nothing.
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
            <span style={{ color: "var(--ink)" }}>{plural(preview.counts.rename, "edition")} to rename</span>
            <span className="text-ink-faint"> · {preview.counts.same.toLocaleString()} already correct</span>
            {preview.counts.conflict > 0 && <span style={{ color: "var(--reject-text)" }}> · {plural(preview.counts.conflict, "conflict")}</span>}
            {preview.counts.missing > 0 && <span className="text-ink-faint"> · {preview.counts.missing.toLocaleString()} not on disk</span>}
          </div>
          {items.length === 0 ? (
            <div className="p-6 text-center text-base text-ink-dim">Every book is already named to the scheme.</div>
          ) : (
            <div className="thin-scroll max-h-[50vh] overflow-y-auto">
              {items.map((it) => (
                <div key={`${it.book_id}:${it.version_id}:${it.kind}`} className="py-1.5" style={{ borderBottom: "1px solid var(--line-soft)" }}>
                  <div className="text-sm font-semibold">
                    {it.title}
                    <span className="font-normal text-ink-dim"> · {what(it)}{it.file_count > 1 ? ` · ${it.file_count} files` : ""}</span>
                  </div>
                  <div className="break-all font-mono text-xs text-ink-faint" title={it.current}>{tail(it.current)}</div>
                  <div className="break-all font-mono text-xs" title={it.proposed} style={{ color: it.status === "conflict" ? "var(--reject-text)" : "var(--ink)" }}>→ {tail(it.proposed)}</div>
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
