import { Suspense, useCallback, useEffect, useId, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { PageHeader } from "../components/PageHeader";
import { DeleteBookDialog } from "../components/DeleteBookDialog";
import { disposalLine, useRecycleMode } from "../lib/disposal";
import { BookReleaseModal } from "../components/BookReleaseModal";
import { UploadTorrentModal } from "../components/UploadTorrentModal";
import { FileDetailsModal } from "../components/FileDetailsModal";
import { AddAudioVersion, AudioVersionPanel } from "../components/AudioVersions";
import { api, importListNotice, type BookSeriesEntry, type BookSource, type Book, type BookFile, type BookFileEntry, type BookImportCandidate, type BookLookup, type BookSeries, type MovieEvent } from "../lib/api";
import { qualityApi } from "../lib/qualityApi";
import { bookEditApi } from "../lib/bookEditApi";
import { langName, useLanguages } from "../lib/languages";
import { useCanHover } from "../lib/useCanHover";
import { usePoll } from "../lib/usePoll";
import { jobFailed, useJob } from "../lib/useJob";
import { searchJobLine } from "../lib/searchOutcome";
import { LastSearches, refreshSearches } from "../components/LastSearch";
import { wantedCopy } from "../lib/bookSearch";
import { formatBytes, formatReleaseDay, isUpcoming } from "../lib/format";
import { libraryStatus } from "../lib/status";
import { Button, ConfirmDialog, IconButton, Menu, Modal, StatusChip, useToast, type MenuItem, type ToastFn } from "../ui";
import { Switch } from "../ui/Switch";
import { Page } from "../ui/Page";
import { lazyPage } from "../lib/lazyPage";

// Send to Kindle for the ebook, to the signed-in person's own Kindle — the same lazy
// module My shelf uses (the first-time sheet, the address kept on the Me page).
const KindleButton = lazyPage(() => import("../components/Kindle"), "KindleButton");
// Edit details and the per-book rename preview load when opened.
const EditBookDetails = lazyPage(() => import("../components/EditBookDetails"), "EditBookDetails");
const BookOrganizeModal = lazyPage(() => import("../components/BookOrganizeModal"), "BookOrganizeModal");

// ALIAS_SOURCE names where a former catalogue key came from, for the "Also catalogued as" line.
const ALIAS_SOURCE: Record<string, string> = {
  openlibrary: "Open Library",
  hardcover: "Hardcover",
  google: "Google Books",
  request: "from a request",
};

export function BookDetail() {
  const { id } = useParams();
  const bid = Number(id);
  const [b, setB] = useState<Book | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();
  const load = useCallback(() => {
    api.bookDetail(bid).then(setB).catch((e: Error) => {
      if (e.message.toLowerCase().includes("not found")) setNotFound(true);
      else setError(e.message);
    });
  }, [bid]);
  useEffect(() => { load(); }, [load]);
  // The language table names a language edited onto the book; fetched only then.
  const languages = useLanguages(!!b?.language);
  // "Search now" beside a wanted edition runs a books.search job; when it ends the page
  // says what it found (a toast, and a line under the toolbar) and reloads its search state.
  const [searchJob, setSearchJob] = useState<number | null>(null);
  const [searchResult, setSearchResult] = useState<{ text: string; failed: boolean } | null>(null);
  useJob(searchJob, {
    onDone: (j) => {
      setSearchJob(null);
      const text = searchJobLine(j);
      setSearchResult({ text, failed: jobFailed(j) });
      toast(text, { tone: jobFailed(j) ? "error" : "info" });
      refreshSearches("book", bid);
      load();
    },
  });

  if (notFound) return <Shell><div className="py-10 text-center text-md text-ink-dim">That book isn't in your library. <Link to="/books" className="underline" style={{ color: "var(--accent-text)" }}>Back to Books</Link></div></Shell>;
  if (!b) return <Shell><p className="text-base text-ink-dim">{error ?? "Loading…"}</p></Shell>;

  const searchNow = async () => {
    try {
      setSearchResult(null);
      const r = await api.searchBook(b.id);
      if (r.job_id) setSearchJob(r.job_id);
      toast(`Searching for “${b.title}”…`);
    } catch (e) { toast((e as Error).message, { tone: "error" }); }
  };
  const wantedLine = { book: b, searching: searchJob !== null, onSearch: searchNow };

  const st = libraryStatus({ hasFile: b.has_file, monitored: b.monitored });

  return (
    <>
      <PageHeader title={b.title} />
      <div className="relative">
        {b.cover_url && (
          <>
            <div className="pointer-events-none absolute inset-0 bg-cover bg-center opacity-[0.08] blur-2xl" style={{ backgroundImage: `url(${b.cover_url})` }} />
            {/* Scrim pulls every cover toward the neutral bg so a bright cover (e.g. an orange
                one) doesn't wash the whole hero — keeps all book pages equally clean. */}
            <div className="pointer-events-none absolute inset-0" style={{ background: "linear-gradient(180deg, transparent 0%, var(--bg) 65%)" }} />
          </>
        )}
        <Page className="relative py-6">
          <Link to="/books" className="mb-4 inline-flex items-center gap-1 text-sm text-ink-dim hover:text-[var(--ink)]">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M15 19l-7-7 7-7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>
            All books
          </Link>
          <div className="flex flex-col gap-6 sm:flex-row">
            <Poster book={b} onChange={load} toast={toast} />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2.5">
                <StatusChip tone={st.tone} size="md">{st.label}</StatusChip>
                {b.year > 0 && <span className="font-mono text-xs text-ink-faint">{b.year}</span>}
                {/* Not out yet: searches start on this day. */}
                {b.release_date && isUpcoming(b.release_date) && (
                  <span className="rounded-full px-2.5 py-1 font-mono text-xs font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>Out {formatReleaseDay(b.release_date)}</span>
                )}
                {/* The catalogue the book actually came from. Open Library keeps its navy
                    chip; the others use the panel style so the palette doesn't grow. */}
                {b.catalogue && (
                  <a href={b.catalogue.url} target="_blank" rel="noreferrer" className="rounded px-1.5 py-0.5 font-mono text-xs font-bold" style={b.catalogue.name === "Open Library" ? { background: "#0a3d62", color: "#fff" } : { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>{b.catalogue.name}</a>
                )}
              </div>
              {b.subtitle && <div className="mt-1.5 text-md text-ink">{b.subtitle}</div>}
              <div className="mt-1.5 text-md font-semibold text-ink-dim">
                {b.author_id ? <Link to={`/books/authors/${b.author_id}`} className="hover:underline hover:text-[var(--accent)]">{b.author}</Link> : (b.author || "Unknown author")}
              </div>
              {/* Details only a hand edit fills, and a marker when any detail was edited
                  (Fix ▾ Edit details shows which, with what the catalogue says). */}
              {(b.publisher || b.language || b.isbn || Object.keys(b.edited ?? {}).length > 0) && (
                <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-ink-faint">
                  {(b.publisher || b.language || b.isbn) && (
                    <span>{[b.publisher, b.language ? langName(b.language, languages) : "", b.isbn ? `ISBN ${b.isbn}` : ""].filter(Boolean).join(" · ")}</span>
                  )}
                  {Object.keys(b.edited ?? {}).length > 0 && (
                    <span data-testid="book-edited" className="rounded px-1.5 py-px font-mono text-2xs" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink-dim)" }}
                      title="Some details were edited by hand. Fix ▾ Edit details shows which, and what the catalogue says.">
                      Edited by hand
                    </span>
                  )}
                </div>
              )}
              {/* Keys the book had before (a Change match, the Hardcover upgrade): cards
                  still carrying one of them show this book as in the library. */}
              {b.aliases && b.aliases.length > 0 && (
                <div className="mt-1 text-xs text-ink-faint">
                  Also catalogued as{" "}
                  {b.aliases.map((k, i) => (
                    <span key={k.key}>{i > 0 && " · "}<span className="font-mono">{k.key}</span> ({ALIAS_SOURCE[k.source] ?? k.source})</span>
                  ))}
                </div>
              )}
              {b.description && <p className="mt-3 max-h-[180px] overflow-y-auto text-md leading-relaxed text-ink-dim">{b.description}</p>}
              {b.subjects && b.subjects.length > 0 && (
                <div className="mt-3 flex flex-wrap gap-1.5">{b.subjects.slice(0, 8).map((s) => <span key={s} className="rounded-full px-2 py-0.5 text-xs" style={{ background: "var(--panel-2)", color: "var(--ink-dim)" }}>{s}</span>)}</div>
              )}
              <div className="mt-4 flex flex-wrap items-center gap-4">
                <ProfileSelector book={b} onChange={load} />
              </div>
              <Toolbar book={b} onChange={load} toast={toast} onSearch={searchNow} searching={searchJob !== null} />
              {(searchJob !== null || searchResult) && (
                <div className="mt-2 text-sm" role="status" style={{ color: searchJob !== null ? "var(--ink-dim)" : searchResult?.failed ? "var(--reject-text)" : "var(--ink)" }}>
                  {searchJob !== null ? "Searching…" : searchResult?.text}
                </div>
              )}
              <LastSearches kind="book" id={b.id} />
            </div>
          </div>
        </Page>
      </div>

      <Page className="pb-10">
        <h2 className="m-0 mb-3 text-md font-bold">Editions</h2>
        <div className="flex flex-col gap-2.5">
          <EditionPanel label="Ebook" file={b.ebook} wanted={b.want_ebook} bookId={b.id} kind="ebook" onChange={load} toast={toast} search={wantedLine} />
          <EditionPanel label={b.audio_versions && b.audio_versions.length > 0 ? "Audiobook · Standard" : "Audiobook"} file={b.audiobook} wanted={b.want_audiobook} bookId={b.id} kind="audiobook" onChange={load} toast={toast} search={wantedLine} />
          {(b.audio_versions ?? []).map((v) => <AudioVersionPanel key={v.id} book={b} v={v} onChange={load} />)}
          <AddAudioVersion book={b} onAdded={load} />
        </div>
        <HistoryPanel bookId={b.id} refreshKey={b} />
      </Page>
    </>
  );
}

// Poster shows the cover with a hover "Edit cover" affordance that opens the picker.
// Touch can't hover, so there it's a plain button under the cover instead.
function Poster({ book, onChange, toast }: { book: Book; onChange: () => void; toast: ToastFn }) {
  const [picking, setPicking] = useState(false);
  const canHover = useCanHover();
  const pencil = <svg width="13" height="13" viewBox="0 0 24 24" fill="none"><path d="M4 20h4l10-10-4-4L4 16v4z" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>;
  return (
    <div className="flex-none">
      <div className="group relative w-[180px] overflow-hidden rounded-xl" style={{ border: "1px solid var(--line)", aspectRatio: "2/3", background: "var(--panel-2)" }}>
        {book.cover_url ? (
          <img src={book.cover_url} alt={book.title} className="h-full w-full object-cover" />
        ) : (
          <div className="flex h-full items-center justify-center p-3 text-center text-md font-bold text-white" style={{ background: "linear-gradient(150deg, hsl(28 30% 26%), hsl(24 28% 16%))" }}>{book.title}</div>
        )}
        {canHover && (
          <button onClick={() => setPicking(true)} className="pointer-events-none absolute inset-0 flex items-end justify-center opacity-0 transition-opacity duration-150 focus-visible:pointer-events-auto focus-visible:opacity-100 group-hover:pointer-events-auto group-hover:opacity-100" style={{ background: "linear-gradient(180deg, transparent 45%, rgba(0,0,0,.68))" }}>
            <span className="mb-3 inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-semibold text-white" style={{ background: "rgba(0,0,0,.55)", border: "1px solid rgba(255,255,255,.28)" }}>
              {pencil}
              Edit cover
            </span>
          </button>
        )}
      </div>
      {!canHover && (
        <button onClick={() => setPicking(true)} className="mt-2 inline-flex min-h-[32px] w-[180px] items-center justify-center gap-1.5 rounded-lg px-3 text-sm font-semibold" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink-dim)" }}>
          {pencil}
          Edit cover
        </button>
      )}
      {picking && <CoverPickerModal book={book} onClose={() => setPicking(false)} onChange={onChange} toast={toast} />}
    </div>
  );
}

// CoverPickerModal lets the user pick from searched covers (the book's own catalogue:
// Hardcover editions, or Open Library editions + Google Books) or upload a custom image.
function CoverPickerModal({ book, onClose, onChange, toast }: { book: Book; onClose: () => void; onChange: () => void; toast: ToastFn }) {
  const [covers, setCovers] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    api.bookCovers(book.id).then(setCovers).catch((e: Error) => { setError(e.message); setCovers([]); });
  }, [book.id]);

  const choose = async (url: string) => {
    setSaving(url); setError(null);
    try { await api.setBookCover(book.id, url); onChange(); onClose(); toast("Cover updated."); }
    catch (e) { setError((e as Error).message); setSaving(null); }
  };
  const upload = async (file: File) => {
    setSaving("upload"); setError(null);
    try { await api.uploadBookCover(book.id, file); onChange(); onClose(); toast("Custom cover uploaded."); }
    catch (e) { setError((e as Error).message); setSaving(null); }
  };

  return (
    <Modal onClose={onClose} labelledBy="cover-picker-title" size="lg" dismissible={saving === null}>
        <div className="mb-1 flex items-center justify-between">
          <h2 id="cover-picker-title" className="m-0 text-lg font-bold">Choose a cover</h2>
          <IconButton label="Close" onClick={onClose} className="h-7 w-7 rounded-md text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
        </div>
        <p className="mb-3 text-sm text-ink-dim">{book.catalogue?.name === "Hardcover" ? "Covers from Hardcover editions" : "Covers from Open Library editions and Google Books"} — or upload your own.</p>
        {error && <div className="mb-2 text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}
        <div className="mb-3">
          <input ref={fileRef} type="file" accept="image/jpeg,image/png,image/webp,image/gif" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = ""; }} />
          <Button onClick={() => fileRef.current?.click()} disabled={saving !== null} busy={saving === "upload"} busyLabel="Uploading…" className="disabled:opacity-50" style={{ border: "1px solid var(--accent-line)", background: "transparent", color: "var(--accent-text)" }}>
            ⭱ Upload custom cover
          </Button>
        </div>
        <div className="thin-scroll max-h-[58vh] overflow-y-auto">
          {covers === null ? (
            <div className="p-8 text-center text-base text-ink-dim">Finding covers…</div>
          ) : covers.length === 0 ? (
            <div className="p-8 text-center text-base text-ink-dim">No alternate covers found — you can still upload your own above.</div>
          ) : (
            <div className="grid grid-cols-3 gap-3 sm:grid-cols-4 md:grid-cols-5">
              {covers.map((url) => {
                const current = url === book.cover_url;
                return (
                  <button key={url} onClick={() => choose(url)} disabled={saving !== null} aria-label={current ? "The current cover" : "Use this cover"} className="relative overflow-hidden rounded-lg disabled:opacity-60" style={{ border: current ? "2px solid var(--accent)" : "1px solid var(--line)", aspectRatio: "2/3", background: "var(--panel-2)" }}>
                    <img src={url} alt="" loading="lazy" className="h-full w-full object-cover" onError={(e) => { const btn = e.currentTarget.closest("button"); if (btn) (btn as HTMLElement).style.display = "none"; }} />
                    {saving === url && <div className="absolute inset-0 grid place-items-center text-xs font-semibold text-white" style={{ background: "rgba(0,0,0,.5)" }}>Saving…</div>}
                    {current && <span className="absolute right-1 top-1 rounded px-1.5 py-0.5 text-xs font-bold text-accent-ink" style={{ background: "var(--accent)" }}>Current</span>}
                  </button>
                );
              })}
            </div>
          )}
        </div>
    </Modal>
  );
}

interface WantedSearch { book: Book; searching: boolean; onSearch: () => void }

function EditionPanel({ label, file, wanted, bookId, kind, onChange, toast, search }: { label: string; file?: BookFile; wanted: boolean; bookId: number; kind: "ebook" | "audiobook"; onChange: () => void; toast: ToastFn; search: WantedSearch }) {
  const [showFile, setShowFile] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const mode = useRecycleMode();
  if (!file && !wanted) {
    return (
      <div className="flex items-center gap-3 rounded-xl px-4 py-3 opacity-60" style={{ border: "1px dashed var(--line)" }}>
        <span className="font-mono text-2xs uppercase text-ink-faint">{label}</span>
        <span className="text-sm text-ink-faint">Not included in this book's format profile.</span>
      </div>
    );
  }
  if (!file) {
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-xl px-4 py-3" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
        <span className="rounded px-1.5 py-0.5 font-mono text-2xs uppercase" style={{ background: "var(--avoid-soft)", color: "var(--avoid-text)" }}>{label}</span>
        <span className="min-w-0 flex-1 text-sm" style={{ color: "var(--avoid-text)" }}>{wantedCopy(search.book, label)}</span>
        <button onClick={search.onSearch} disabled={search.searching} title="Search your indexers for this book's missing editions now" className="flex-none whitespace-nowrap rounded-lg px-3 py-1.5 text-sm font-semibold disabled:opacity-50" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" }}>{search.searching ? "Searching…" : "Search now"}</button>
      </div>
    );
  }
  // A refusal (the recycle bin couldn't take a file) stays in the dialog rather than closing it.
  const del = async () => {
    setBusy(true); setErr(null);
    try { await api.deleteBookFile(bookId, kind); setConfirming(false); onChange(); } catch (e) { setErr((e as Error).message); } finally { setBusy(false); }
  };
  const multi = file.file_count > 1;
  return (
    <div className="rounded-xl p-4" style={{ border: "1px solid var(--good)", background: "var(--panel)" }}>
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="rounded px-1.5 py-0.5 font-mono text-2xs uppercase" style={{ background: "var(--good-soft)", color: "var(--good-text)" }}>{label}</span>
            <span className="rounded px-2 py-0.5 text-xs font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>{file.format}</span>
            {multi && <span className="rounded px-2 py-0.5 text-xs font-semibold" style={{ background: "var(--panel-2)", color: "var(--ink-dim)" }}>{file.file_count} files</span>}
            <span className="font-mono text-sm text-ink-dim">{formatBytes(file.size_bytes)}</span>
          </div>
          <button
            onClick={() => setShowFile(true)}
            title="Show this file's details — format, size and the release it came from"
            className="mt-1.5 block break-all text-left font-mono text-sm text-ink-faint hover:underline"
          >
            {file.path}
          </button>
          {multi && <FileList bookId={bookId} kind={kind} count={file.file_count} />}
          {kind === "audiobook" && search.book.audiobook_narrator && (
            <div className="mt-1.5 text-sm text-ink-dim">Narrator: {search.book.audiobook_narrator}</div>
          )}
        </div>
        <div className="flex flex-none flex-col items-end gap-1.5">
          {kind === "ebook" && (
            <div className="flex max-w-[240px] flex-wrap justify-end gap-1.5 text-right">
              <Suspense fallback={null}>
                <KindleButton book={{ book_id: bookId, title: search.book.title, ebook: { format: file.format, size_bytes: file.size_bytes, kindle: file.kindle } }} />
              </Suspense>
              {file.kepub && (
                <a href={`${api.ebookDownloadURL(bookId)}?format=kepub`} download className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }} title="An EPUB made for Kobo's own reader: copy it onto the Kobo over USB">
                  Download for Kobo (KEPUB)
                </a>
              )}
            </div>
          )}
          {multi && kind === "audiobook" && <MergeButton bookId={bookId} fileCount={file.file_count} onDone={onChange} toast={toast} />}
          <button onClick={() => { setErr(null); setConfirming(true); }} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>{multi ? "Delete all" : "Delete file"}</button>
        </div>
      </div>
      {confirming && (
        <ConfirmDialog
          title={`Delete the ${label.toLowerCase()}${multi ? `'s ${file.file_count} files` : " file"}?`}
          body={
            <>
              <div className="mt-1 break-all font-mono text-sm" style={{ color: "var(--ink)" }}>{file.path}</div>
              <p className="mb-0 mt-1.5">{disposalLine(file.size_bytes, mode)}. The book stays, so Arrmada looks for the {label.toLowerCase()} again while it's monitored.</p>
            </>
          }
          confirmLabel={multi ? "Delete all" : "Delete file"}
          busyLabel="Deleting…"
          busy={busy}
          error={err}
          onConfirm={del}
          onCancel={() => setConfirming(false)}
        />
      )}
      {showFile && (
        <FileDetailsModal
          path={file.path}
          title={`${label} file`}
          subtitle={file.format}
          onClose={() => setShowFile(false)}
        />
      )}
    </div>
  );
}

function FileList({ bookId, kind, count }: { bookId: number; kind: "ebook" | "audiobook"; count: number }) {
  const [open, setOpen] = useState(false);
  const [files, setFiles] = useState<BookFileEntry[] | null>(null);
  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next && files === null) api.bookEditionFiles(bookId, kind).then(setFiles).catch(() => setFiles([]));
  };
  return (
    <div className="mt-2">
      <button onClick={toggle} className="flex items-center gap-1.5 text-sm font-semibold" style={{ color: "var(--accent-text)" }}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" style={{ transform: open ? "rotate(90deg)" : "none", transition: "transform .15s" }}><path d="M9 6l6 6-6 6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>
        {open ? "Hide files" : `Show all ${count} files`}
      </button>
      {open && (
        <div className="thin-scroll mt-2 max-h-[240px] overflow-y-auto rounded-lg" style={{ border: "1px solid var(--line)" }}>
          {files === null ? (
            <div className="p-3 text-sm text-ink-dim">Loading…</div>
          ) : (
            files.map((f, i) => (
              <div key={i} className="flex items-center justify-between gap-3 px-3 py-1.5 text-sm" style={{ borderBottom: i < files.length - 1 ? "1px solid var(--line-soft)" : "none" }}>
                <span className="min-w-0 flex-1 truncate font-mono text-ink-dim" title={f.name}>{f.name}</span>
                <span className="flex-none font-mono text-xs text-ink-faint">{formatBytes(f.size_bytes)}</span>
              </div>
            ))
          )}
        </div>
      )}
    </div>
  );
}

// The newest merge outcome in a book's history ('merged' or 'merge-failed'), if any.
function lastMergeEvent(events: MovieEvent[]): MovieEvent | undefined {
  return events.find((e) => e.event === "merged" || e.event === "merge-failed");
}
const eventKey = (e?: MovieEvent) => (e ? `${e.event}|${e.created_at}|${e.detail ?? ""}` : "");

// MergeButton asks before combining, then watches the book's history for the outcome the
// server records ('merged' or 'merge-failed') instead of guessing from a timer. The wording
// is the same for everyone and never says who is listening.
function MergeButton({ bookId, fileCount, onDone, toast }: { bookId: number; fileCount: number; onDone: () => void; toast: ToastFn }) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  // While a merge runs: the history entry it started from, and when to give up.
  const [watch, setWatch] = useState<{ baseline: string; until: number } | null>(null);
  const running = watch !== null;
  const [err, setErr] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  // The server gives a merge 30 minutes; stop watching a little after that (by the
  // clock, so the time a hidden tab spends paused still counts).
  usePoll(async () => {
    if (!watch) return;
    const ev = lastMergeEvent(await api.bookHistory(bookId).catch(() => []));
    const changed = !!ev && eventKey(ev) !== watch.baseline;
    if (!changed && Date.now() < watch.until) return;
    setWatch(null);
    onDone();
    if (changed && ev?.event === "merge-failed") {
      setFailure(ev.detail ?? "unknown reason");
      toast(`Merge failed: ${ev.detail ?? "unknown reason"} — the original files are untouched.`, { tone: "error" });
    } else if (changed && ev?.event === "merged") {
      toast("Combined into one .m4b.");
    } else {
      // Only a server restart mid-merge leaves no outcome; say so rather than nothing.
      toast("The merge didn't report back — check the book's history before trying again.");
    }
  }, watch ? 5000 : null, { immediate: false });

  const merge = async () => {
    setBusy(true); setErr(null); setFailure(null);
    try {
      const baseline = eventKey(lastMergeEvent(await api.bookHistory(bookId).catch(() => [])));
      await api.mergeAudiobook(bookId);
      setAsking(false);
      setWatch({ baseline, until: Date.now() + 400 * 5000 });
      toast("Combining into a single chapterized .m4b — this runs in the background and may take a while.");
    } catch (e) { setErr((e as Error).message); } finally { setBusy(false); }
  };

  return (
    <>
      <button onClick={() => { setErr(null); setAsking(true); }} disabled={busy || running} title="Merge all chapter files into one chapterized .m4b (needs ffmpeg)" className="rounded-lg px-3 py-1.5 text-sm font-semibold disabled:opacity-60" style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}>
        {running ? "Merging…" : "⧉ Combine into one file"}
      </button>
      {failure && <span className="max-w-[260px] text-right text-xs" style={{ color: "var(--reject-text)" }}>Merge failed: {failure}</span>}
      {asking && (
        <ConfirmDialog
          title="Combine into one file?"
          body={`Combine ${fileCount} files into one .m4b? The originals go to the recycle bin (or are kept for 14 days). Apps that downloaded this audiobook will need to download it again, and anyone listening right now will need to reopen it.`}
          confirmLabel="Combine"
          busyLabel="Starting…"
          tone="accent"
          busy={busy}
          error={err}
          onConfirm={merge}
          onCancel={() => setAsking(false)}
        />
      )}
    </>
  );
}

// Toolbar groups the book's controls instead of nine equal buttons: the Monitor switch,
// Search ▾, Add files ▾, Fix ▾ and a separate Delete. Each menu item does what its old
// button did; the menus are the kit's (keyboard, Escape, focus back to the trigger).
function Toolbar({ book, onChange, toast, onSearch, searching }: { book: Book; onChange: () => void; toast: ToastFn; onSearch: () => Promise<void>; searching: boolean }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [showSearch, setShowSearch] = useState(false);
  const [showImport, setShowImport] = useState(false);
  const [showPaste, setShowPaste] = useState(false);
  const [showEdit, setShowEdit] = useState(false);
  const [showMatch, setShowMatch] = useState(false);
  // An edit changed what the book's files would be called: offer the rename preview.
  // Files are never renamed by an edit itself.
  const [renameOffer, setRenameOffer] = useState(false);
  const [showRename, setShowRename] = useState(false);

  const run = async (key: string, fn: () => Promise<void>) => { setBusy(key); try { await fn(); } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(null); } };
  const trigger = "inline-flex min-h-[36px] items-center gap-1.5 whitespace-nowrap rounded-lg px-3 py-2 text-base font-semibold";
  const ghost = { border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" } as const;
  const caret = <span aria-hidden="true" className="text-xs opacity-70">▾</span>;
  const missing = (book.want_ebook && !book.ebook) || (book.want_audiobook && !book.audiobook);

  const rename = async () => {
    const res = await api.renameBook(book.id);
    const skipped = res.skipped ?? [];
    if (skipped.length > 0) toast(`${res.renamed > 0 ? "Renamed some files, but not all" : "Not renamed"}: ${skipped[0].reason ?? "see the book's history"}.`, { tone: "error" });
    else toast(res.renamed > 0 ? "Renamed to the book naming scheme." : "Files already named correctly.");
    onChange();
  };
  const group = (label: string, items: MenuItem[]) => (
    <Menu label={label} trigger={<>{label} {caret}</>} items={items} align="left" portal triggerClassName={trigger} triggerStyle={ghost} itemClassName="min-h-[40px]" />
  );

  return (
    <>
      <div className="mt-4 flex flex-wrap items-center gap-2.5">
        <Switch size="sm" className="mr-1 min-h-[36px]" checked={book.monitored} disabled={busy !== null}
          onChange={() => run("monitor", async () => { await api.setBookMonitored(book.id, !book.monitored); onChange(); })}>
          {book.monitored ? "Monitored" : "Monitor"}
        </Switch>
        {group("Search", [
          {
            label: searching ? "Searching…" : "Automatic search",
            disabled: !missing || searching,
            title: missing ? "Search your indexers for this book's missing editions and grab the best match" : "Nothing to search for: every edition the format profile wants is here",
            onSelect: onSearch,
          },
          { label: "Interactive search…", title: "Browse every release your indexers have for this book and pick one", onSelect: () => setShowSearch(true) },
        ])}
        {group("Add files", [
          { label: "Upload .torrent or .nzb…", title: "Found the release yourself? Drop the .torrent or .nzb here and Arrmada will grab and import it", onSelect: () => setShowPaste(true) },
          { label: "Manual import…", title: "Import a book file that's already in the downloads folder", onSelect: () => setShowImport(true) },
        ])}
        {group("Fix", [
          { label: "Edit details…", title: "Fix the title, author, series and other details the catalogue got wrong — what it matched stays underneath", onSelect: () => setShowEdit(true) },
          { label: "Change match…", title: "This is the wrong book — search the catalogue and re-link it to the right one, keeping your files", onSelect: () => setShowMatch(true) },
          { label: "Rename files", busyLabel: "Renaming…", disabled: busy !== null, title: "Rename the files on disk to the library's naming", onSelect: () => run("rename", rename) },
          { label: "Refresh & rescan", busyLabel: "Refreshing…", disabled: busy !== null, title: "Fetch the metadata again and rescan the files on disk", onSelect: () => run("refresh", async () => { await api.refreshBook(book.id); onChange(); toast("Refreshed metadata and rescanned disk."); }) },
        ])}
        <DeleteButton book={book} />
      </div>
      {renameOffer && (
        <div className="mt-2 flex flex-wrap items-center gap-2 text-sm" role="status" style={{ color: "var(--ink-dim)" }}>
          Files can be renamed to match:
          <button type="button" onClick={() => setShowRename(true)} className="font-semibold underline-offset-2 hover:underline" style={{ color: "var(--accent-text)" }}>Preview rename</button>
          <IconButton label="Dismiss" onClick={() => setRenameOffer(false)} className="h-6 w-6 rounded-md text-xs text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
        </div>
      )}
      <Suspense fallback={null}>
        {showEdit && (
          <EditBookDetails book={book} onClose={() => setShowEdit(false)} onSaved={(r) => {
            onChange();
            setRenameOffer(r.rename_available);
            toast(r.changed.length > 0 ? "Details saved." : "Nothing changed.");
          }} />
        )}
        {showRename && <BookOrganizeModal bookId={book.id} onClose={() => setShowRename(false)} onRenamed={() => { setRenameOffer(false); onChange(); }} />}
      </Suspense>
      {showMatch && <RematchModal book={book} onClose={() => setShowMatch(false)} onMatched={() => { onChange(); toast("Re-matched to the chosen work."); }} />}
      {showSearch && (
        <BookReleaseModal
          title={`Search indexers — ${book.title}`}
          fetchReleases={() => api.bookReleases(book.id)}
          targets={(book.audio_versions ?? []).map((v) => ({ id: v.id, label: v.label }))}
          onGrab={async (rel, versionId) => { await api.grabBook(book.id, { token: rel.token ?? "", version_id: versionId }); onChange(); }}
          onClose={() => setShowSearch(false)}
        />
      )}
      <SeriesPanel bookID={book.id} />
      {showPaste && (
        <UploadTorrentModal
          what={book.title}
          onPreview={(torrent) => api.previewTorrent(torrent)}
          targets={(book.audio_versions ?? []).map((v) => ({ id: v.id, label: v.label }))}
          onGrab={async (torrent, filename, title, versionId) => { await api.grabBookTorrent(book.id, torrent, filename, title, versionId); onChange(); }}
          onClose={() => setShowPaste(false)}
        />
      )}
      {showImport && <ManualImportModal book={book} onClose={() => setShowImport(false)} onImported={() => { onChange(); toast("Imported."); }} />}
    </>
  );
}

function ManualImportModal({ book, onClose, onImported }: { book: Book; onClose: () => void; onImported: () => void }) {
  const [cands, setCands] = useState<BookImportCandidate[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [importing, setImporting] = useState<string | null>(null);
  const versions = book.audio_versions ?? [];
  const [target, setTarget] = useState(0);
  const [notice, setNotice] = useState<string | null>(null);

  const load = () => api.bookManualImportList(book.id).then((r) => { setCands(r.candidates); setNotice(importListNotice(r)); }).catch((e: Error) => setError(e.message));
  useEffect(() => { load(); }, [book.id]);

  const doImport = async (path: string, edition: string) => {
    setImporting(path); setError(null);
    try { await api.bookManualImport(book.id, path, edition === "audiobook" ? target : 0); onImported(); load(); } catch (e) { setError((e as Error).message); } finally { setImporting(null); }
  };

  return (
    <Modal onClose={onClose} labelledBy="book-import-title" size="lg" dismissible={importing === null}>
        <div className="mb-1 flex items-center justify-between">
          <h2 id="book-import-title" className="m-0 text-lg font-bold">Manual import</h2>
          <IconButton label="Close" onClick={onClose} className="h-7 w-7 rounded-md text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
        </div>
        <p className="mb-3 text-sm text-ink-dim">Pick a book file on disk to import as <b>{book.title}</b>. Ebook and audiobook files are assigned to the right edition automatically.</p>
        {versions.length > 0 && (
          <label className="mb-3 flex items-center gap-2 text-sm text-ink-dim">
            Import audiobook files as
            <select value={target} onChange={(e) => setTarget(Number(e.target.value))} className="rounded-md px-2 py-1 text-sm" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>
              <option value={0}>Standard</option>
              {versions.map((v) => <option key={v.id} value={v.id}>{v.label}</option>)}
            </select>
          </label>
        )}
        {error && <div className="mb-2 text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}
        {notice && <div className="mb-2 text-sm text-ink-faint">{notice}</div>}
        <div className="thin-scroll max-h-[52vh] overflow-y-auto">
          {cands === null ? (
            <div className="p-6 text-center text-base text-ink-dim">Scanning…</div>
          ) : cands.length === 0 ? (
            <div className="p-6 text-center text-base text-ink-dim">No ebook or audiobook files found in the downloads folder.</div>
          ) : (
            cands.map((c) => (
              <button key={c.path} onClick={() => doImport(c.path, c.edition)} disabled={importing !== null} className="flex w-full items-center gap-3 rounded-lg p-2.5 text-left transition-colors hover:bg-[var(--panel-2)]">
                <div className="min-w-0 flex-1">
                  <div className="truncate text-base font-medium" title={c.filename}>{c.filename}</div>
                  <div className="mt-0.5 flex items-center gap-3 font-mono text-xs text-ink-faint">
                    <span className="uppercase" style={{ color: c.edition === "audiobook" ? "var(--accent-text)" : "var(--good-text)" }}>{c.edition}</span>
                    <span>{c.format}</span>
                    <span>{formatBytes(c.size_bytes)}</span>
                  </div>
                </div>
                <span className="flex-none rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>{importing === c.path ? "Importing…" : "Import"}</span>
              </button>
            ))
          )}
        </div>
    </Modal>
  );
}

function ProfileSelector({ book, onChange }: { book: Book; onChange: () => void }) {
  const profileId = useId();
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  useEffect(() => { qualityApi.qualityProfiles("book").then((r) => setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })))).catch(() => {}); }, []);
  const change = async (p: string) => { if (p === book.quality_profile) return; await api.setBookProfile(book.id, p); onChange(); };
  const opts = [...profiles];
  if (book.quality_profile && !opts.some((o) => o.key === book.quality_profile)) opts.unshift({ key: book.quality_profile, name: book.quality_profile === "n/a" ? "Not set" : book.quality_profile });
  return (
    <div className="flex items-center gap-2">
      <label htmlFor={profileId} className="font-mono text-2xs uppercase text-ink-faint">Format profile</label>
      <select id={profileId} value={book.quality_profile} onChange={(e) => change(e.target.value)} className="rounded-lg px-2.5 py-1.5 text-sm font-medium" style={{ background: "var(--accent-soft)", border: "1px solid var(--line)", color: "var(--accent-text)" }}>
        {opts.map((p) => <option key={p.key} value={p.key} style={{ background: "var(--panel)", color: "var(--ink)" }}>{p.name}</option>)}
      </select>
    </div>
  );
}

function DeleteButton({ book }: { book: Book }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button onClick={() => setOpen(true)} className="rounded-lg px-3 py-2 text-base font-semibold" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>Delete</button>
      {open && <DeleteBookDialog book={book} onClose={() => setOpen(false)} onDeleted={() => { window.location.href = "/books"; }} />}
    </>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return <><PageHeader title="Books" /><Page>{children}</Page></>;
}

// Short names for events whose stored name won't fit the History column. The stored
// name stays as it is: the duplicate review reads it.
const EVENT_LABELS: Record<string, string> = {
  possible_duplicate: "dup?",
};

const EVENT_TONES: Record<string, string> = {
  added: "var(--ink-faint)",
  grabbed: "var(--accent-text)",
  imported: "var(--good-text)",
  merged: "var(--good-text)",
  possible_duplicate: "var(--accent-text)",
  matched: "var(--accent-text)",
  edited: "var(--ink-dim)",
  renamed: "var(--ink-dim)",
  deleted: "var(--reject-text)",
  failed: "var(--reject-text)",
};

function fmtEventTime(s: string): string {
  const d = new Date(s.includes("T") ? s : s.replace(" ", "T") + "Z");
  if (isNaN(d.getTime())) return s;
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

// HistoryPanel is the book's activity trail — why it was grabbed, what release it came
// from, when it failed. Books were the only media module without one.
function HistoryPanel({ bookId, refreshKey }: { bookId: number; refreshKey: unknown }) {
  const [events, setEvents] = useState<MovieEvent[] | null>(null);

  useEffect(() => {
    api.bookHistory(bookId).then(setEvents).catch(() => setEvents([]));
  }, [bookId, refreshKey]);

  return (
    <div className="mt-8">
      <h2 className="m-0 mb-3 text-md font-bold">History</h2>
      {events === null ? (
        <div className="text-base text-ink-dim">Loading…</div>
      ) : events.length === 0 ? (
        <div className="rounded-xl p-6 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>No activity yet.</div>
      ) : (
        <div className="flex flex-col">
          {events.map((e, i) => (
            <div key={i} className="flex items-center gap-3 border-b py-2 text-sm" style={{ borderColor: "var(--line)" }}>
              <span className="w-[74px] flex-none truncate font-mono text-2xs font-bold uppercase" style={{ color: EVENT_TONES[e.event] ?? "var(--ink-dim)" }}>{EVENT_LABELS[e.event] ?? e.event}</span>
              <span className="min-w-0 flex-1 truncate text-ink-dim" title={e.detail}>{e.detail || "—"}</span>
              <span className="flex-none font-mono text-xs text-ink-faint">{fmtEventTime(e.created_at)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// RematchModal re-points the book at a different catalogue entry. This is the fix for a
// book identified as the wrong title — most often one catalogued by the library scan, which
// takes the first search hit. Files on disk, monitoring and the quality profile are kept.
function RematchModal({ book, onClose, onMatched }: { book: Book; onClose: () => void; onMatched: () => void }) {
  const [query, setQuery] = useState(`${book.title} ${book.author}`.trim());
  const [results, setResults] = useState<BookLookup[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);
  const [saving, setSaving] = useState<string | null>(null);
  const [source, setSource] = useState<BookSource>("openlibrary");
  const [olResults, setOlResults] = useState<BookLookup[] | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  // Details edited by hand: kept through the re-match by default (the new entry's go
  // underneath), or cleared when they were about the book it was wrongly matched to.
  const editedCount = Object.keys(book.edited ?? {}).length;
  const [edits, setEdits] = useState<"keep" | "clear">("keep");

  const search = useCallback(async (q: string) => {
    if (!q.trim()) return;
    setSearching(true); setError(null); setOlResults(null);
    try { const r = await api.lookupBooks(q.trim()); setResults(r.results); setSource(r.source); }
    catch (e) { setError((e as Error).message); setResults([]); }
    finally { setSearching(false); }
  }, []);
  const searchOpenLibrary = async () => {
    try {
      const r = await api.lookupBooks(query.trim(), "openlibrary");
      const seen = new Set((results ?? []).map((x) => x.key));
      setOlResults(r.results.filter((x) => !seen.has(x.key)));
    } catch (e) { setError((e as Error).message); }
  };

  // Seed with the book's current title so the right work is usually one click away.
  useEffect(() => { void search(`${book.title} ${book.author}`.trim()); }, [book.id]); // eslint-disable-line react-hooks/exhaustive-deps

  const choose = async (r: BookLookup) => {
    setSaving(r.key); setError(null);
    try {
      await bookEditApi.rematch(book.id, { ol_key: r.key, title: r.title, author: r.author, year: r.year, cover_url: r.cover_url ?? "", edits });
      onMatched();
      onClose();
    } catch (e) { setError((e as Error).message); setSaving(null); }
  };

  return (
    <Modal onClose={onClose} labelledBy="book-rematch-title" size="lg" dismissible={saving === null} initialFocus={inputRef}>
        <div className="mb-1 flex items-center justify-between gap-3">
          <h2 id="book-rematch-title" className="m-0 min-w-0 truncate text-lg font-bold">Change match — {book.title}</h2>
          <IconButton label="Close" onClick={onClose} className="h-7 w-7 flex-none rounded-md text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
        </div>
        <p className="mb-3 text-sm text-ink-dim">Pick the correct catalogue entry. Your files, monitoring and quality profile are kept — only the metadata changes.</p>
        {editedCount > 0 && (
          <fieldset className="mb-3 rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--line)" }}>
            <legend className="px-1 text-ink-dim">You edited {editedCount === 1 ? "1 detail" : `${editedCount} details`} of this book by hand</legend>
            <label className="flex items-center gap-2 py-0.5">
              <input type="radio" name="rematch-edits" checked={edits === "keep"} onChange={() => setEdits("keep")} />
              Keep my edits <span className="text-ink-faint">— the new entry's details go underneath them</span>
            </label>
            <label className="flex items-center gap-2 py-0.5">
              <input type="radio" name="rematch-edits" checked={edits === "clear"} onChange={() => setEdits("clear")} />
              Clear my edits <span className="text-ink-faint">— they were for the wrong book</span>
            </label>
          </fieldset>
        )}

        <form className="mb-3 flex gap-2" onSubmit={(e) => { e.preventDefault(); void search(query); }}>
          <input ref={inputRef} value={query} onChange={(e) => setQuery(e.target.value)} aria-label="Search the catalogue" placeholder={source === "hardcover" ? "Search Hardcover…" : "Search Open Library…"} className="min-w-0 flex-1 rounded-lg px-3 py-2 text-md" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
          <Button type="submit" variant="primary" busy={searching} busyLabel="Searching…" className="disabled:opacity-50">Search</Button>
        </form>

        {error && <div className="mb-3 rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>{error}</div>}

        {results === null ? (
          <div className="p-4 text-center text-base text-ink-dim">Searching…</div>
        ) : results.length === 0 ? (
          <div className="rounded-xl p-6 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>No works found. Try a different search.</div>
        ) : (
          <div className="thin-scroll flex max-h-[52vh] flex-col overflow-y-auto">
            {source === "hardcover" && olResults === null && (
              <button type="button" onClick={searchOpenLibrary} className="mb-1 self-end text-sm font-semibold underline-offset-2 hover:underline" style={{ color: "var(--ink-dim)" }}>
                Show Open Library results too
              </button>
            )}
            {[...results, ...(olResults ?? [])].map((r) => {
              const current = r.key === book.ol_key;
              return (
                <div key={r.key} className="flex items-center gap-3 border-b py-2.5" style={{ borderColor: "var(--line)" }}>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-md font-medium">{r.title} {r.year > 0 && <span className="font-mono text-xs text-ink-faint">{r.year}</span>}</div>
                    <div className="truncate text-sm text-ink-dim">{r.author || "Unknown author"} <span className="font-mono text-ink-faint">· {r.key}</span></div>
                  </div>
                  {current ? (
                    <span className="flex-none rounded-lg px-2.5 py-1 text-xs font-semibold" style={{ background: "var(--panel-2)", color: "var(--ink-faint)" }}>Current</span>
                  ) : (
                    <button disabled={saving !== null} onClick={() => choose(r)} className="flex-none rounded-lg px-3 py-1.5 text-sm font-semibold disabled:opacity-50" style={{ border: "1px solid var(--accent)", color: "var(--accent-text)" }}>
                      {saving === r.key ? "Matching…" : "Use this"}
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        )}
    </Modal>
  );
}

// SeriesPanel shows the series a book belongs to and what's missing from it.
//
// The series is learned from the release that matched the book — book trackers state it
// ("Coven of Bones #1") and Arrmada used to discard it — so it appears once something has
// been grabbed, and the panel simply doesn't render before then.
//
// Gaps are only shown BETWEEN entries you own. Nothing here knows how long a series really
// runs, so a hole at #2 when you have #1 and #3 is a fact, while "#6 is missing" past your
// highest entry would be a guess.
function SeriesPanel({ bookID }: { bookID: number }) {
  const [series, setSeries] = useState<BookSeries | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const load = useCallback(() => api.bookSeries(bookID).then(setSeries).catch(() => {}), [bookID]);
  useEffect(() => {
    let live = true;
    api.bookSeries(bookID).then((s) => { if (live) setSeries(s); }).catch(() => {});
    return () => { live = false; };
  }, [bookID]);

  if (!series?.name || series.entries.length === 0) return null;
  const catalogue = series.source === "catalogue";

  // With the catalogue's listing, a missing entry is a real book with a key — add it,
  // or add all of them.
  const addOne = async (e: BookSeriesEntry) => {
    if (!e.key) return;
    setBusy(e.key); setNote(null);
    try { await api.addBook({ ol_key: e.key, monitored: true, title: e.title, author: e.author, year: e.year, cover_url: e.cover_url }); await load(); }
    catch (err) { setNote((err as Error).message); } finally { setBusy(null); }
  };
  const addAll = async () => {
    setBusy("all"); setNote(null);
    try { const r = await api.addMissingInSeries(bookID); setNote(`Added ${r.added}${r.skipped ? `, ${r.skipped} already in library` : ""}.`); await load(); }
    catch (err) { setNote((err as Error).message); } finally { setBusy(null); }
  };

  return (
    <section className="mt-4 rounded-xl p-4" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="m-0 text-lg font-bold">{series.name}</h2>
        <div className="flex items-center gap-3">
          <span className="font-mono text-xs text-ink-faint">
            {catalogue && series.total ? `${series.total} books · ` : ""}{series.gaps > 0 ? `${series.gaps} missing from your library` : "complete"}
          </span>
          {catalogue && series.gaps > 0 && (
            <button onClick={addAll} disabled={busy !== null} className="rounded-lg px-2.5 py-1 text-sm font-semibold disabled:opacity-50" style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}>
              {busy === "all" ? "Adding…" : `Add all ${series.gaps} missing`}
            </button>
          )}
        </div>
      </div>
      {note && <div className="mt-2 text-sm text-ink-dim">{note}</div>}
      <ol className="mt-3 flex list-none flex-col gap-1 p-0">
        {series.entries.map((e, i) => (
          <li key={e.book_id ?? `gap-${e.position}-${i}`} className="flex items-center gap-2.5 text-base">
            <span className="w-8 flex-none text-right font-mono text-xs text-ink-faint">
              {e.position ? `#${e.position}` : "—"}
            </span>
            {e.missing && e.key ? (
              <>
                <span className="min-w-0 truncate" style={{ color: "var(--ink-dim)" }}>{e.title}{e.year ? <span className="font-mono text-xs text-ink-faint"> {e.year}</span> : null}</span>
                <button onClick={() => addOne(e)} disabled={busy !== null} className="ml-auto flex-none rounded-md px-2 py-0.5 text-xs font-semibold disabled:opacity-50" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>
                  {busy === e.key ? "Adding…" : "Add"}
                </button>
              </>
            ) : e.missing ? (
              <span style={{ color: "var(--avoid-text)" }}>Not in your library</span>
            ) : (
              <>
                <Link to={`/books/${e.book_id}`} style={{ color: e.book_id === bookID ? "var(--accent-text)" : "var(--ink)" }}>
                  {e.title}
                </Link>
                <span className="font-mono text-xs" style={{ color: e.has_file ? "var(--good-text)" : "var(--ink-faint)" }}>
                  {e.has_file ? "downloaded" : "wanted"}
                </span>
              </>
            )}
          </li>
        ))}
      </ol>
    </section>
  );
}
