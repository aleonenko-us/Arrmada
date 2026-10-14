import { Suspense, useMemo, useState, type ReactNode } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { api, type MyBook, type MyRequest } from "../lib/api";
import { useLiveQuery } from "../lib/useLiveQuery";
import { lazyPage } from "../lib/lazyPage";
import { posterThumb } from "../lib/img";
import { formatReleaseDay } from "../lib/format";
import { stillComingLine } from "../lib/bookFormats";
import { playAudiobook } from "../lib/playerStub";
import { requestStage } from "../lib/requestStage";
import { Button, ErrorState, Skeleton, StaleBanner, StatusChip, type Tone } from "../ui";
import { PAGE, asRequest, fmtSize, newThisWeek, seriesGroups, seriesLabel } from "./myshelf/shelf";
import { Page } from "../ui/Page";

// MyBooks ("My shelf") is the requester's view of the book library: every book that has a
// file, and their own requests still on the way. Deliberately small — no editing, no
// indexer search, no library management — because its readers aren't running the
// library, they're looking for something to read.
//
// Rows, in order: New this week, Your requests (with the same stage words as the Requests
// page), each Series in reading order, then All books (search, sort, Ebooks only; paged).
// Tapping a book opens its sheet (lazy; /shelf?book=<id>, so Back closes it) with the
// blurb, Download, Send to Kindle, Listen and the rest of its series.
//
// It stays fresh from the live socket (a request of theirs moving, a Kindle result) with a
// slow fallback poll that stops while the tab is hidden — no polling in the background.

const BookSheet = lazyPage(() => import("./myshelf/BookSheet"), "BookSheet");
// "Read on your e-reader" (the OPDS catalogue) is its own chunk, shared with Audiobooks.
const EReaderPanel = lazyPage(() => import("../components/EReaderPanel"), "EReaderPanel");

// Their own requests moving (and arriving), and their inbox changing (a Kindle send).
const SHELF_TOPICS = ["user.*.request.updated", "user.*.inbox.updated"] as const;

type Sort = "newest" | "title" | "author";

export function MyBooks() {
  const { data, error, loading, refetch } = useLiveQuery("me:books", api.myBooks, { topics: SHELF_TOPICS, fallbackMs: 60_000, debounceMs: 1000, staleMs: 10_000 });
  const books = data?.books ?? null;
  const requests = data?.requests ?? [];
  const listen = !!data?.listen;
  const queueKnown = data?.queue_known ?? true;
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<Sort>("newest");
  const [onlyEbooks, setOnlyEbooks] = useState(false);
  const [limit, setLimit] = useState(PAGE);

  // The sheet lives in the address (?book=), so Back closes it and a link opens it. Closing
  // one this page opened steps back over its entry, so Back afterwards leaves the page.
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();
  const openID = Number(params.get("book") || 0);
  const open = openID && books ? books.find((b) => b.book_id === openID) : undefined;
  const openBook = (id: number) => setParams((p) => { const n = new URLSearchParams(p); n.set("book", String(id)); return n; }, { state: { bookSheet: true }, replace: !!openID });
  const closeBook = () => {
    if ((location.state as { bookSheet?: boolean } | null)?.bookSheet) navigate(-1);
    else setParams((p) => { const n = new URLSearchParams(p); n.delete("book"); return n; }, { replace: true });
  };

  const filtering = q.trim() !== "" || onlyEbooks;
  const shown = useMemo(() => {
    if (!books) return [];
    const needle = q.trim().toLowerCase();
    const list = books.filter((b) => {
      if (onlyEbooks && !b.ebook) return false;
      if (!needle) return true;
      return [b.title, b.author, b.series_name ?? b.series].some((s) => s && s.toLowerCase().includes(needle));
    });
    const by = (a: string | undefined, b: string | undefined) => (a || "").localeCompare(b || "", undefined, { sensitivity: "base" });
    if (sort === "title") list.sort((a, b) => by(a.title, b.title));
    else if (sort === "author") list.sort((a, b) => by(a.author, b.author) || by(a.title, b.title));
    // "newest" is the server's order (added_at desc)
    return list;
  }, [books, q, sort, onlyEbooks]);
  const fresh = useMemo(() => (books ? newThisWeek(books) : []), [books]);
  const groups = useMemo(() => (books ? seriesGroups(books) : []), [books]);

  const ebooks = books ? books.filter((b) => b.ebook).length : 0;

  return (
    <Page className="py-5">
      <div className="mb-4">
        {/* "My shelf", not "Books": Discover's Books tab is the catalogue to ask from. */}
        <h1 className="m-0 text-2xl font-bold">My shelf</h1>
        <p className="m-0 mt-1 text-base text-ink-dim">
          Read, download or send any book in the library, or listen to an audiobook right here.
          {books && <span className="text-ink-faint"> · {books.length} book{books.length === 1 ? "" : "s"}, {ebooks} ebook{ebooks === 1 ? "" : "s"}</span>}
        </p>
      </div>

      {/* A refresh that failed keeps the shelf it had; a first load that failed says so
          rather than showing an empty library. */}
      {error && books && <StaleBanner message={error.message} onRetry={refetch} />}

      <Link to="/audiobooks" className="mb-5 flex items-center justify-between gap-3 rounded-xl px-4 py-3 text-base" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
        <span><b>Listen</b> <span className="text-ink-dim">— in your browser or a listening app, with your place kept in sync.</span></span>
        <span className="flex-none font-semibold" style={{ color: "var(--accent-text)" }}>Audiobooks →</span>
      </Link>

      {!filtering && fresh.length > 0 && (
        <Row title="New this week">
          {fresh.map((b) => <CoverTile key={b.book_id} b={b} onOpen={openBook} />)}
        </Row>
      )}

      {requests.length > 0 && (
        <Row title="Your requests">
          {requests.map((r, i) => <RequestCard key={r.id ?? `${r.title}-${i}`} r={r} queueKnown={queueKnown} />)}
        </Row>
      )}

      {!filtering && groups.map((g) => (
        <Row key={g.name} title={g.name} sub={`Series · ${g.books.length} books`}>
          {g.books.map((b) => <CoverTile key={b.book_id} b={b} onOpen={openBook} label={b.series_position ? `#${b.series_position}` : undefined} />)}
        </Row>
      ))}

      <div className="mb-3 mt-1 flex flex-wrap items-center justify-between gap-2">
        <h2 className="m-0 text-md font-bold">All books</h2>
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <input
            value={q}
            onChange={(e) => { setQ(e.target.value); setLimit(PAGE); }}
            placeholder="Search title, author, series"
            aria-label="Search the shelf"
            className="w-[220px] min-w-0 max-w-full rounded-lg px-3 py-1.5 text-base"
            style={{ background: "var(--panel)", border: "1px solid var(--line)", color: "var(--ink)" }}
          />
          <select value={sort} onChange={(e) => setSort(e.target.value as Sort)} aria-label="Sort" className="rounded-lg px-2 py-1.5 text-sm" style={{ background: "var(--panel)", border: "1px solid var(--line)", color: "var(--ink)" }}>
            <option value="newest">Newest first</option>
            <option value="title">Title A–Z</option>
            <option value="author">Author A–Z</option>
          </select>
          <label className="flex items-center gap-1.5 text-sm text-ink-dim">
            <input type="checkbox" checked={onlyEbooks} onChange={(e) => { setOnlyEbooks(e.target.checked); setLimit(PAGE); }} />
            Ebooks only
          </label>
        </div>
      </div>

      {books === null && (error
        ? <ErrorState what="the shelf" message={error.message} onRetry={refetch} busy={loading} />
        : <Skeleton variant="grid" minWidth={140} />)}
      {books && books.length === 0 && (
        <div className="rounded-xl p-6 text-center text-md text-ink-dim" style={{ border: "1px dashed var(--line)" }}>
          The library is empty so far. Request a book from Discover and it will show up here once it arrives.
        </div>
      )}
      {books && books.length > 0 && shown.length === 0 && (
        <div className="rounded-xl p-6 text-center text-md text-ink-dim" style={{ border: "1px dashed var(--line)" }}>
          Nothing matches{q.trim() ? ` “${q.trim()}”` : ""}. Can't find it? Request it from Discover.
        </div>
      )}

      {shown.length > 0 && (
        <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(140px, 1fr))" }}>
          {shown.slice(0, limit).map((b) => <BookCard key={b.book_id} b={b} listen={listen} onOpen={openBook} />)}
        </div>
      )}
      {shown.length > limit && (
        <div className="mt-4 flex justify-center">
          <Button onClick={() => setLimit(limit + PAGE)}>
            Show more ({shown.length - limit} left)
          </Button>
        </div>
      )}

      {ebooks > 0 && <div className="mt-6"><Suspense fallback={null}><EReaderPanel /></Suspense></div>}

      {open && books && (
        <Suspense fallback={null}>
          <BookSheet key={open.book_id} book={open} shelf={books} listen={listen} onClose={closeBook} onOpen={openBook} />
        </Suspense>
      )}
    </Page>
  );
}

// Row is one horizontal strip of the shelf: a heading and covers that scroll sideways.
function Row({ title, sub, children }: { title: string; sub?: string; children: ReactNode }) {
  return (
    <section className="mb-5" aria-label={title}>
      <h2 className="m-0 mb-2 flex items-baseline gap-2 text-md font-bold">
        <span className="min-w-0 truncate">{title}</span>
        {sub && <span className="flex-none text-sm font-normal text-ink-faint">{sub}</span>}
      </h2>
      <div className="thin-scroll flex gap-3 overflow-x-auto pb-2">{children}</div>
    </section>
  );
}

function CoverBox({ url, title, children }: { url?: string; title: string; children?: ReactNode }) {
  return (
    <div className="relative" style={{ aspectRatio: "2/3", background: "var(--panel-2)" }}>
      {url ? (
        <img src={posterThumb(url)} alt={title} className="h-full w-full object-cover" loading="lazy" decoding="async" />
      ) : (
        <div className="flex h-full w-full items-center justify-center p-3 text-center" style={{ background: "linear-gradient(150deg, hsl(28 30% 26%), hsl(24 28% 16%))" }}>
          <span className="text-sm font-bold text-white">{title}</span>
        </div>
      )}
      {children}
    </div>
  );
}

// Badge is the chip over a cover's corner (a series number, "Yours"): the kit's poster chip.
function Badge({ label, tone }: { label: string; tone: Tone }) {
  return <StatusChip tone={tone} surface="poster" className="absolute right-1.5 top-1.5">{label}</StatusChip>;
}

// CoverTile is a book in a row: its cover and title, opening its sheet.
function CoverTile({ b, onOpen, label }: { b: MyBook; onOpen: (id: number) => void; label?: string }) {
  return (
    <button onClick={() => onOpen(b.book_id)} className="w-[112px] flex-none overflow-hidden rounded-xl text-left" style={{ border: "1px solid var(--line)", background: "var(--panel)" }} aria-label={`${b.title}${b.author ? ` by ${b.author}` : ""}`}>
      <CoverBox url={b.cover_url} title={b.title}>
        {label && <Badge label={label} tone="faint" />}
      </CoverBox>
      <div className="p-2">
        <div className="truncate text-sm font-semibold" title={b.title}>{b.title}</div>
        {b.author && <div className="truncate text-xs text-ink-dim">{b.author}</div>}
      </div>
    </button>
  );
}

// BookCard is a book in All books: the cover and title open its sheet; the quick actions
// (download, listen) stay on the card for the people who only ever want those.
function BookCard({ b, listen, onOpen }: { b: MyBook; listen: boolean; onOpen: (id: number) => void }) {
  const series = seriesLabel(b);
  return (
    <div className="flex flex-col overflow-hidden rounded-xl" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <button onClick={() => onOpen(b.book_id)} className="flex flex-col text-left" aria-label={`${b.title}${b.author ? ` by ${b.author}` : ""} — details`}>
        <CoverBox url={b.cover_url} title={b.title}>
          {b.mine && <Badge label="Yours" tone="accent" />}
        </CoverBox>
        <div className="flex flex-col gap-0.5 px-2.5 pt-2.5">
          <div className="truncate text-base font-semibold" title={b.title}>{b.title}</div>
          {b.author && <div className="truncate text-xs text-ink-dim" title={b.author}>{b.author}</div>}
          {series && <div className="truncate text-xs text-ink-faint" title={series}>{series}</div>}
        </div>
      </button>
      <div className="mt-auto p-2.5 pt-2">
        {b.ebook ? (
          <a
            href={api.ebookDownloadURL(b.book_id)}
            download
            className="block rounded-lg px-2 py-1.5 text-center text-sm font-semibold"
            style={{ background: "var(--accent)", color: "var(--accent-ink)" }}
            title={`Download the ${b.ebook.format.toUpperCase()}${b.ebook.size_bytes ? ` (${fmtSize(b.ebook.size_bytes)})` : ""}`}
          >
            Download {b.ebook.format ? b.ebook.format.toUpperCase() : "ebook"}
            {b.ebook.size_bytes ? <span className="font-normal"> · {fmtSize(b.ebook.size_bytes)}</span> : null}
          </a>
        ) : (
          <div className="rounded-lg px-2 py-1 text-center text-xs text-ink-faint" style={{ border: "1px solid var(--line)" }}>No ebook yet</div>
        )}
        {(b.audiobooks ?? []).map((a) => (
          <div key={a.version_id} className="mt-1 flex gap-1">
            {/* Listen plays it right here, in the mini-player; the link downloads it. */}
            {listen && a.item_key && (
              <button
                onClick={() => playAudiobook(a.item_key!, { meta: { title: b.title, author: b.author, cover: b.cover_url } })}
                className="min-w-0 flex-1 truncate rounded-lg px-2 py-1 text-center text-xs font-semibold"
                style={{ background: "var(--accent-soft)", border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}
                aria-label={`Listen to ${b.title}${a.label ? ` (${a.label})` : ""}`}
              >
                ▶ Listen{a.label ? ` · ${a.label}` : ""}
              </button>
            )}
            <a
              href={api.audiobookDownloadURL(b.book_id, a.version_id)}
              download
              className={`${listen && a.item_key ? "flex-none" : "flex-1"} block truncate rounded-lg px-2 py-1 text-center text-xs font-semibold`}
              style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}
              title={`Download the audiobook${a.label ? ` (${a.label})` : ""}${a.files > 1 ? ` — ${a.files} files as one zip` : ""}`}
            >
              {listen && a.item_key
                ? <span aria-label="Download">⬇{a.size_bytes ? <span className="font-normal"> {fmtSize(a.size_bytes)}</span> : null}</span>
                : <>Audiobook{a.label ? ` · ${a.label}` : ""}{a.size_bytes ? <span className="font-normal"> · {fmtSize(a.size_bytes)}</span> : null}</>}
            </a>
          </div>
        ))}
      </div>
    </div>
  );
}

// RequestCard is one of their requests still on the way, worded by lib/requestStage — the
// same badge and line the Requests page and Discover show ("Downloading · 40% · 2h left",
// "Searching · Not found yet — next check Tue 14 Oct"). A book that isn't out yet says so,
// with the day it is ("Not out yet · Out 12 Mar"), which is when it's first searched for.
// It opens the request.
function RequestCard({ r, queueKnown }: { r: MyRequest; queueKnown: boolean }) {
  let st = requestStage(asRequest(r), queueKnown);
  const notOut = !!r.release_date && r.status !== "declined";
  if (notOut && r.status === "approved" && !st.unknown && (r.tracking?.stage ?? "searching") === "searching") {
    st = { ...st, badge: "Not out yet", tone: "faint", detail: "" };
  }
  // Which format is still coming ("Audiobook on the way"), once it's approved.
  const coming = r.status === "approved" && !notOut ? stillComingLine(r.waiting) : "";
  const body = (
    <>
      <CoverBox url={r.cover_url} title={r.title}>
        <StatusChip tone={st.tone} surface="poster" className="absolute right-1.5 top-1.5">{st.badge}</StatusChip>
      </CoverBox>
      <div className="p-2">
        <div className="truncate text-sm font-semibold" title={r.title}>{r.title}</div>
        {r.author && <div className="truncate text-xs text-ink-dim" title={r.author}>{r.author}</div>}
        {notOut && <div className="mt-0.5 truncate text-xs" style={{ color: "var(--accent-text)" }} title="We'll grab it then">Out {formatReleaseDay(r.release_date!)}</div>}
        {st.detail && <div className="mt-0.5 line-clamp-2 text-xs" style={{ color: st.detailTone ?? "var(--ink-faint)" }} title={st.detail}>{st.detail}</div>}
        {coming && <div className="mt-0.5 truncate text-xs" style={{ color: "var(--accent-text)" }} title={coming}>{coming}</div>}
      </div>
    </>
  );
  const box = "block w-[132px] flex-none overflow-hidden rounded-xl";
  const style = { border: "1px solid var(--line)", background: "var(--panel)" };
  return r.id ? <Link to={`/requests?id=${r.id}`} className={box} style={style}>{body}</Link> : <div className={box} style={style}>{body}</div>;
}
