import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { bookDupesApi, type DupBook, type DupGroup, type PastMerge } from "../lib/bookDupesApi";
import { formatBytes } from "../lib/format";
import { posterThumb } from "../lib/img";
import { Button, EmptyState, ErrorState, Modal, Skeleton, StatusChip, useConfirm, useToast } from "../ui";

// DuplicatesModal is Books → Possible duplicates: rows that may be one book, side by side,
// to merge into the one kept or to mark as different books. Nothing merges without a
// click. It shows the books and their files only — never anything about who has listened
// to them — and lists earlier merges so a book the old start-up fold took can be added
// back from Discover.

const groupKey = (g: DupGroup) => g.books.map((b) => b.id).join("-");

const SOURCE_LABEL: Record<string, string> = { openlibrary: "Open Library", hardcover: "Hardcover", google: "Google Books" };

export default function DuplicatesModal({ onClose, onChanged }: { onClose: () => void; onChanged: () => void }) {
  const confirm = useConfirm();
  const toast = useToast();
  const [groups, setGroups] = useState<DupGroup[] | null>(null);
  const [past, setPast] = useState<PastMerge[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [keep, setKeep] = useState<Record<string, number>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [left, setLeft] = useState<{ title: string; files: string[]; notes: string[] } | null>(null);

  const load = () => {
    setError(null);
    bookDupesApi.list()
      .then((r) => { setGroups(r.groups); setPast(r.past_merges); })
      .catch((e: Error) => setError(e.message));
  };
  useEffect(load, []);

  const keepOf = (g: DupGroup) => keep[groupKey(g)] ?? g.suggested_keep_id;

  const merge = async (g: DupGroup) => {
    const keepId = keepOf(g);
    const kept = g.books.find((b) => b.id === keepId);
    const drops = g.books.filter((b) => b.id !== keepId);
    if (!kept || drops.length === 0) return;
    const ok = await confirm({
      title: <>Merge into “{kept.title}”?</>,
      body: `${drops.length === 1 ? "The other row" : `The other ${drops.length} rows`} will be folded into the one you keep: editions it lacks move over, a second audiobook becomes a “Merged copy” version, and listening places, bookmarks and requests follow. No files are deleted. A merge can't be undone from here.`,
      confirmLabel: "Merge",
    });
    if (!ok) return;
    setBusy(groupKey(g));
    try {
      const r = await bookDupesApi.merge(keepId, drops.map((b) => b.id));
      const notes = r.results.map((x) => x.note).filter((n): n is string => !!n);
      if (r.left_files.length > 0 || notes.length > 0) setLeft({ title: kept.title, files: r.left_files, notes });
      toast(`Merged into “${kept.title}”.`, { tone: "good" });
      onChanged();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(null);
      load();
    }
  };

  const notSame = async (g: DupGroup) => {
    setBusy(groupKey(g));
    try {
      await bookDupesApi.ignore(g.books.map((b) => b.id));
      toast("Marked as different books — they won't be suggested again.", { tone: "good" });
      setGroups((cur) => cur?.filter((x) => groupKey(x) !== groupKey(g)) ?? null);
      onChanged();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(null);
    }
  };

  return (
    <Modal onClose={onClose} title="Possible duplicates" size="xl" variant="sheet" panelClassName="thin-scroll w-full max-w-[1100px] overflow-y-auto rounded-t-2xl p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] max-h-[calc(100dvh-2rem)] sm:rounded-2xl sm:p-5">
      <p className="mt-1.5 text-sm text-ink-dim">
        Listening places, bookmarks and requests move to the kept book. No files are deleted.
      </p>

      {left && (
        <div role="status" className="mt-3 rounded-lg px-3.5 py-2.5 text-sm" style={{ border: "1px solid var(--accent-line)", background: "var(--accent-soft)" }}>
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              {left.files.length > 0 && (
                <>
                  <b>Left on disk</b> — “{left.title}” already had this, so these files stay where they are and Arrmada no longer tracks them:
                  <ul className="mt-1 space-y-0.5">
                    {left.files.map((f) => <li key={f} className="break-all font-mono text-xs">{f}</li>)}
                  </ul>
                </>
              )}
              {left.notes.map((n) => <p key={n} className="mt-1">{n}</p>)}
            </div>
            <button onClick={() => setLeft(null)} aria-label="Dismiss" className="flex-none text-ink-faint hover:text-[var(--ink)]">✕</button>
          </div>
        </div>
      )}

      <div className="mt-4 space-y-4">
        {error && <ErrorState what="possible duplicates" message={error} onRetry={load} />}
        {!error && groups === null && <Skeleton variant="list" count={2} />}
        {!error && groups?.length === 0 && (
          <EmptyState title="No possible duplicates" body="Every book in your library looks like a different book." />
        )}
        {groups?.map((g) => {
          const k = groupKey(g);
          const keepId = keepOf(g);
          return (
            <section key={k} className="rounded-xl p-3 sm:p-4" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }} aria-label={`Possible duplicates: ${g.books[0]?.title ?? ""}`}>
              <div className="mb-2.5 flex flex-wrap gap-1.5">
                {g.reasons.map((r) => <StatusChip key={r} tone="faint">{r}</StatusChip>)}
              </div>
              <div role="radiogroup" aria-label="Book to keep" className="grid gap-2.5 sm:grid-cols-2 lg:grid-cols-3">
                {g.books.map((b) => (
                  <DupCard key={b.id} book={b} kept={b.id === keepId} suggested={b.id === g.suggested_keep_id} name={k}
                    onKeep={() => setKeep((cur) => ({ ...cur, [k]: b.id }))} disabled={busy !== null} />
                ))}
              </div>
              <div className="mt-3 flex flex-wrap justify-end gap-2">
                <Button variant="secondary" size="sm" onClick={() => notSame(g)} disabled={busy !== null}
                  title="These are different books: stop suggesting them">Not the same</Button>
                <Button variant="primary" size="sm" onClick={() => merge(g)} busy={busy === k} busyLabel="Merging…" disabled={busy !== null && busy !== k}>
                  Merge into the kept book
                </Button>
              </div>
            </section>
          );
        })}
      </div>

      <details className="mt-5 rounded-xl px-3.5 py-2.5" style={{ border: "1px solid var(--line)" }}>
        <summary className="cursor-pointer text-base font-semibold">Earlier merges ({past.length})</summary>
        {past.length === 0 ? (
          <p className="mt-2 text-sm text-ink-faint">No book has been merged into another.</p>
        ) : (
          <>
            <p className="mt-2 text-sm text-ink-faint">
              Merges Arrmada once made by itself at start-up are marked automatic. If one folded two different books, add the lost one back from Discover.
            </p>
            <ul className="mt-2 divide-y" style={{ borderColor: "var(--line)" }}>
              {past.map((m, i) => (
                <li key={`${m.book_id}-${m.created_at}-${i}`} className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1 py-2 text-sm">
                  <span className="font-mono text-xs text-ink-faint">{m.created_at.slice(0, 10)}</span>
                  {m.automatic && <StatusChip tone="avoid">Automatic</StatusChip>}
                  <span className="min-w-0 flex-1">
                    {m.lost_title ? <>“{m.lost_title}” into </> : null}
                    <Link to={`/books/${m.book_id}`} onClick={onClose} className="font-semibold hover:underline">{m.keeper_title}</Link>
                  </span>
                  {m.automatic && m.lost_title && (
                    <Link to={`/discover?tab=books&q=${encodeURIComponent(m.lost_title)}`} onClick={onClose}
                      className="text-sm font-semibold" style={{ color: "var(--accent)" }}>Find on Discover</Link>
                  )}
                </li>
              ))}
            </ul>
          </>
        )}
      </details>
    </Modal>
  );
}

function DupCard({ book, kept, suggested, name, onKeep, disabled }: {
  book: DupBook; kept: boolean; suggested: boolean; name: string; onKeep: () => void; disabled: boolean;
}) {
  const ed = (label: string, f?: DupBook["ebook"]) => f
    ? <div className="truncate" title={f.path}><b className="text-[var(--ink)]">{label}</b> {f.format} · {formatBytes(f.size_bytes)}{f.file_count > 1 ? ` · ${f.file_count} files` : ""}</div>
    : <div className="text-ink-faint">No {label.toLowerCase()}</div>;
  return (
    <label className="flex cursor-pointer gap-3 rounded-lg p-2.5 text-sm"
      style={{ background: "var(--panel)", border: `1px solid ${kept ? "var(--accent)" : "var(--line)"}`, opacity: disabled ? 0.7 : 1 }}>
      <div className="h-[84px] w-[56px] flex-none overflow-hidden rounded" style={{ background: "var(--panel-2)" }}>
        {book.cover_url && <img src={posterThumb(book.cover_url)} alt="" className="h-full w-full object-cover" loading="lazy" decoding="async" />}
      </div>
      <div className="min-w-0 flex-1 space-y-0.5 text-ink-dim">
        <div className="flex items-start gap-2">
          <input type="radio" name={`keep-${name}`} checked={kept} onChange={onKeep} disabled={disabled} className="mt-0.5" aria-label={`Keep “${book.title}”`} />
          <span className="min-w-0">
            <span className="block truncate text-base font-semibold text-[var(--ink)]" title={book.title}>{book.title}</span>
            <span className="block truncate">{book.author || "Unknown author"}{book.year ? ` · ${book.year}` : ""}</span>
          </span>
        </div>
        <div className="truncate font-mono text-xs" title={book.key}>{SOURCE_LABEL[book.source] ?? book.source} · {book.key}</div>
        {ed("Ebook", book.ebook)}
        {ed("Audiobook", book.audiobook)}
        <div>
          {book.audio_versions > 0 ? `${book.audio_versions} more audio version${book.audio_versions === 1 ? "" : "s"}` : "No other audio versions"}
          {" · "}{book.monitored ? "Monitored" : "Not monitored"}
        </div>
        <div className="flex items-center gap-1.5">
          {book.added_at && <span className="text-ink-faint">Added {book.added_at.slice(0, 10)}</span>}
          {kept ? <StatusChip tone="accent">Keep</StatusChip> : suggested ? <StatusChip tone="faint">Suggested</StatusChip> : null}
        </div>
      </div>
    </label>
  );
}
