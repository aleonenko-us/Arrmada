import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { PageHeader } from "../components/PageHeader";
import { DeleteBookDialog } from "../components/DeleteBookDialog";
import { api, type BookSource, type BookUpgradeStatus, type BookSweepStatus, type Book, type BookLookup, type BookAuthor, type BookDiscoverCard } from "../lib/api";
import { qualityApi } from "../lib/qualityApi";
import { usePersisted } from "../lib/persist";
import { useSearchParamState } from "../lib/useSearchState";
import { PHONE, useMediaQuery } from "../lib/useMediaQuery";
import { isWanted, libraryStatus } from "../lib/status";
import { jobFailed, jobToast, useJob } from "../lib/useJob";
import { posterThumb } from "../lib/img";
import { editionsHeld, wantedTitle } from "../lib/bookSearch";
import { LINKS } from "../lib/links";
import { useMe, isAdmin } from "../lib/me";
import { usePoll, usePollBurst } from "../lib/usePoll";
import { useQuery } from "../lib/query";
import { Button, ErrorState, FilterBar, IconButton, LibraryToolbar, Modal, Skeleton, StaleBanner, useToast, type LibraryTool, type MenuItem } from "../ui";
import { LibraryCard } from "../features/library/LibraryCard";
import { LibraryEmpty } from "../features/library/LibraryEmpty";
import { useProgressive } from "../features/library/progressive";
import type { LibraryConfig } from "../features/library/types";
import { AUTHOR_SORTS, BOOK_SORTS, DEFAULT_SORT, SORT_KEYS, sortAuthors, sortBooks, sortFor, type BookSort } from "./books/sort";
import { Page } from "../ui/Page";

const FILTERS = [
  { key: "all", label: "All" },
  { key: "monitored", label: "Monitored" },
  { key: "wanted", label: "Wanted" },
  { key: "downloaded", label: "Downloaded" },
  { key: "no_audiobook", label: "No audiobook" },
  { key: "no_ebook", label: "No ebook" },
] as const;
type FilterKey = (typeof FILTERS)[number]["key"];
const FILTER_KEYS = FILTERS.map((f) => f.key);

function matches(b: Book, f: FilterKey): boolean {
  switch (f) {
    case "monitored": return b.monitored;
    case "wanted": return isWanted({ hasFile: b.has_file, monitored: b.monitored });
    case "downloaded": return b.has_file;
    // "Missing an edition" means the profile WANTS it and it isn't there. Listing every
    // book without an audiobook would include the ones whose profile only ever asked for
    // an ebook — nothing outstanding about those, and they'd bury the ones that are.
    case "no_audiobook": return b.want_audiobook === true && !b.audiobook;
    case "no_ebook": return b.want_ebook === true && !b.ebook;
    default: return true;
  }
}

const NO_BOOKS: Book[] = [];

// The duplicates review loads only when opened.
const DuplicatesModal = lazy(() => import("../components/DuplicatesModal"));

function statusOf(b: Book): { label: string; color: string; title?: string } {
  const st = libraryStatus({ hasFile: b.has_file, monitored: b.monitored });
  if (st.label === "Downloaded") {
    // Say which editions are present, so the grid tells ebook from audiobook.
    const held = editionsHeld(b);
    return { label: held ? `${held} ✓` : st.label, color: st.color };
  }
  // The tooltip says whether it has been looked for and missed, and when it is looked for next.
  return { label: st.label, color: st.color, title: st.label === "Wanted" ? wantedTitle(b) : undefined };
}

export function Books() {
  const admin = isAdmin(useMe().user); // only admins can open Settings → System
  // Cached across visits, so Back from a book renders at once, then revalidates.
  const booksQ = useQuery("books", () => api.books(), { staleMs: 0 });
  const list = booksQ.data?.books ?? NO_BOOKS;
  const metaOK = booksQ.data?.metadata_available ?? true;
  const error = booksQ.error?.message ?? null;
  const [filter, setFilter] = useSearchParamState<FilterKey>("filter", "all", { allowed: FILTER_KEYS });
  const [query, setQuery] = useSearchParamState<string>("q", "", { debounceMs: 300 });
  // The order lives in the address too (?sort=added), so it survives a reload and Back.
  const [sortKey, setSortKey] = useSearchParamState<BookSort>("sort", DEFAULT_SORT, { allowed: SORT_KEYS });
  const [adding, setAdding] = useState(false);
  const [addingAuthor, setAddingAuthor] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<Book | null>(null);
  const [savedView, setView] = usePersisted("books.view", "grid", ["grid", "table"] as const);
  // A phone always gets the grid (the table doesn't fit); the saved choice is kept for the desktop.
  const phone = useMediaQuery(PHONE);
  const view = phone ? "grid" : savedView;
  const [mode, setMode] = usePersisted("books.mode", "author", ["author", "book"] as const);
  const [sweep, setSweep] = useState<BookSweepStatus | null>(null);
  const [scanning, setScanning] = useState(false);
  const [backfilling, setBackfilling] = useState(false);
  const [multiSelect, setMultiSelect] = useState(false);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  // Which catalogue Books use, and how many rows still carry Open Library keys — the
  // "Upgrade to Hardcover" button shows while there are any.
  const source: BookSource = booksQ.data?.metadata_source ?? "openlibrary";
  const upgradable = booksQ.data?.upgradable ?? 0;
  const [upgrade, setUpgrade] = useState<BookUpgradeStatus | null>(null);
  // Author photos from the catalogue. The server resolves a few unknown authors per
  // call, so keep asking (spaced out) until none are pending.
  const [authorImages, setAuthorImages] = useState<Record<string, string>>({});
  useEffect(() => {
    let alive = true;
    let timer: number | undefined;
    const tick = () => api.bookAuthorImages().then((r) => {
      if (!alive) return;
      setAuthorImages(r.images);
      if (r.pending > 0) timer = window.setTimeout(tick, 4000);
    }).catch(() => {});
    tick();
    return () => { alive = false; window.clearTimeout(timer); };
  }, [list.length]);

  // Possible duplicates: how many groups the review lists, for the header. The calls
  // live in a lazy module, loaded only here and by the review itself.
  const [dupesOpen, setDupesOpen] = useState(false);
  const [dupeCount, setDupeCount] = useState(0);
  const countDupes = () => {
    import("../lib/bookDupesApi")
      .then((m) => m.bookDupesApi.list())
      .then((r) => setDupeCount(r.groups.length))
      .catch(() => {});
  };
  const loaded = !!booksQ.data;
  useEffect(() => { if (loaded) countDupes(); }, [loaded, list.length]);

  const toast = useToast();
  // err shows the toast in the error colour: a search or scan that failed says so plainly.
  const say = (m: string, err = false) => toast(m, { tone: err ? "error" : "info", ms: 3500 });
  const refresh = booksQ.refetch;
  // Poll the upgrade while it runs, then reload the library once it lands.
  usePoll(() => api.bookUpgradeStatus().then((st) => {
    setUpgrade(st);
    if (!st.running) {
      refresh();
      say(st.error ? `Upgrade stopped: ${st.error}` : `Upgraded ${st.upgraded}, flagged ${st.flagged} as possible duplicates, no match for ${st.unmatched}.${st.notes?.length ? ` E.g. ${st.notes[0]}` : ""}`);
    }
  }).catch(() => {}), upgrade?.running ? 2000 : null, { immediate: false });
  // Poll the missing-editions sweep while it runs, then reload once it lands.
  usePoll(() => api.bookSweepStatus().then((st) => {
    setSweep(st);
    if (!st.running) {
      refresh();
      say(`Searched ${st.done} book${st.done === 1 ? "" : "s"}: ${st.grabbed} edition${st.grabbed === 1 ? "" : "s"} grabbed${st.skipped ? `, ${st.skipped} already downloading` : ""}.`);
    }
  }).catch(() => {}), sweep?.running ? 2000 : null, { immediate: false });
  const startSweep = async () => {
    try {
      const r = await api.startBookSweep();
      setSweep(r.status);
      say(r.started ? "Searching for every missing edition — this takes a while, one book at a time." : "A sweep is already running.");
    } catch (e) { say((e as Error).message, true); }
  };
  // Books the sweep would search: any lacking an edition its profile wants. Monitoring
  // only governs the automatic sweep; a library catalogued from disk is mostly unmonitored.
  const missingEditions = list.filter((b) => (b.want_ebook && !b.ebook) || (b.want_audiobook && !b.audiobook)).length;
  // Dismissing the re-match report: keep a book on its current entry (it drops out of
  // the count and the next run), or just hide the panel until the next run.
  // Hiding the panel sticks for this run (keyed by when it started), so it doesn't
  // reappear on every visit; a new run brings it back once.
  const [hideUpgrade, setHideUpgradeState] = useState(false);
  useEffect(() => {
    try { setHideUpgradeState(!!upgrade?.started_at && localStorage.getItem("books.upgradeHidden") === String(upgrade.started_at)); } catch { /* storage blocked */ }
  }, [upgrade?.started_at]);
  const setHideUpgrade = (on: boolean) => {
    setHideUpgradeState(on);
    try { if (on && upgrade?.started_at) localStorage.setItem("books.upgradeHidden", String(upgrade.started_at)); } catch { /* ignore */ }
  };
  const [kept, setKept] = useState<Set<number>>(new Set());
  const leftVisible = (upgrade?.left ?? []).filter((l) => !kept.has(l.id));
  const keepOne = async (id: number) => {
    try {
      await api.keepBookCatalogue(id, true);
      setKept((s) => new Set(s).add(id));
      const cur = booksQ.data;
      if (cur) booksQ.mutate((r) => ({ ...(r ?? cur), upgradable: Math.max(0, ((r ?? cur).upgradable ?? 0) - 1) }));
    } catch (e) { say((e as Error).message, true); }
  };
  const keepAll = async () => {
    const ids = (upgrade?.left ?? []).map((l) => l.id).filter((id) => !kept.has(id));
    for (const id of ids) await keepOne(id);
    say(`Ignoring ${ids.length} book${ids.length === 1 ? "" : "s"} — the re-match leaves them alone from now on.`);
  };
  const startUpgrade = async () => {
    try {
      const r = await api.startBookUpgrade();
      setUpgrade(r.status);
      say(r.started ? "Re-matching your books to Hardcover…" : "An upgrade is already running.");
    } catch (e) { say((e as Error).message, true); }
  };
  useEffect(() => {
    qualityApi.qualityProfiles("book").then((r) => setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })))).catch(() => {});
    api.bookUpgradeStatus().then((st) => { if (st.started_at) setUpgrade(st); }).catch(() => {});
    api.bookSweepStatus().then((st) => { if (st.running) setSweep(st); }).catch(() => {});
  }, []);

  const toggleSelect = (id: number) => setSelected((s) => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n; });
  const clearSelect = () => setSelected(new Set());
  const exitMultiSelect = () => { setMultiSelect(false); clearSelect(); };
  const enterSelect = () => { setMode("book"); setMultiSelect(true); }; // selection is per-book
  const bulkMonitor = async (mon: boolean) => {
    setBulkBusy(true);
    try {
      await Promise.all([...selected].map((id) => api.setBookMonitored(id, mon)));
      say(`${selected.size} ${mon ? "monitored" : "unmonitored"}.`);
      clearSelect();
      refresh();
    } finally { setBulkBusy(false); }
  };
  const bulkProfile = async (profile: string) => {
    setBulkBusy(true);
    try {
      await Promise.all([...selected].map((id) => api.setBookProfile(id, profile)));
      say(`Quality profile set on ${selected.size} books.`);
      clearSelect();
      refresh();
    } finally { setBulkBusy(false); }
  };

  const q = query.trim().toLowerCase();
  const sort = sortFor(mode, sortKey);
  const filtered = useMemo(
    () =>
      sortBooks(
        list
          .filter((b) => matches(b, filter))
          .filter((b) => !q || b.title.toLowerCase().includes(q) || (b.author ?? "").toLowerCase().includes(q)),
        sort,
      ),
    [list, filter, q, sort],
  );
  const authors = useMemo(() => sortAuthors(groupByAuthor(filtered, authorImages), sort), [filtered, authorImages, sort]);
  // The lists render a few screens and grow as they scroll (a big library was all of it
  // at once); a new filter, search or sort starts them from the top again.
  const listKey = `${filter}|${q}|${sort}`;
  const authorList = useProgressive(authors, "authors", listKey);
  const bookList = useProgressive(filtered, "books", listKey);

  // A search runs as a job; when the latest one ends the toast says what it found.
  const [searchJob, setSearchJob] = useState<{ id: number; title: string } | null>(null);
  useJob(searchJob?.id, {
    onDone: (j) => {
      const title = searchJob?.title;
      setSearchJob(null);
      say(title ? `“${title}”: ${jobToast(j, "search finished.")}` : jobToast(j, "Search finished."), jobFailed(j));
      refresh();
    },
  });
  const search = async (b: Book) => {
    try {
      const r = await api.searchBook(b.id);
      if (r.job_id) setSearchJob({ id: r.job_id, title: b.title });
      say(`Searching for “${b.title}”…`);
    } catch (e) { say((e as Error).message, true); }
  };
  // One-off: a series is normally learned from the release that matched a book, so a
  // library assembled before that existed knows nothing — and a book you already own is
  // never grabbed again to teach it. This reads the series off a search per unlabelled
  // book. It grabs nothing.
  const backfillSeries = async () => {
    setBackfilling(true);
    try {
      await api.backfillBookSeries();
      say("Looking up series for your books — this takes a few minutes.");
    } catch (e) { say((e as Error).message, true); }
    finally { setBackfilling(false); }
  };
  // After a scan starts, refresh the grid a dozen times as books land.
  const watchScan = usePollBurst(() => refresh(), 2500, 12, () => setScanning(false));
  // The scan runs as a job; when it ends the toast says what it added.
  const [scanJob, setScanJob] = useState<number | null>(null);
  const scan = useJob(scanJob, {
    onDone: (j) => { setScanJob(null); refresh(); say(jobToast(j, "Scan finished."), jobFailed(j)); },
  });
  const scanLibrary = async () => {
    setScanning(true);
    try {
      const r = await api.scanBooks();
      if (r.job_id) setScanJob(r.job_id);
      say("Scanning your library — books will appear shortly.");
      watchScan();
    } catch (e) { say((e as Error).message, true); setScanning(false); }
  };

  // The header keeps the everyday actions in sight (+ Add book, + Add author, Search
  // missing) and the occasional jobs in the Library tools menu. On a phone the two
  // secondary buttons fold into the menu too, so + Add book always fits on the line.
  const sweepProgress = sweep?.running ? `Searching… ${sweep.done}/${sweep.total}${sweep.grabbed ? ` · ${sweep.grabbed} grabbed` : ""}` : undefined;
  const showSweep = missingEditions > 0 || !!sweep?.running;
  const sweepTitle = `Search your indexers for every book that lacks an edition its profile wants (ebook or audiobook), monitored or not: ${missingEditions} book${missingEditions === 1 ? "" : "s"}. Fills gaps only — editions you already have are never replaced.${sweep?.notes?.length ? `\n\nLast run, nothing found for: ${sweep.notes.slice(0, 4).join("; ")}` : ""}`;
  const addAuthor = { label: "+ Add author", onClick: () => setAddingAuthor(true), disabled: !metaOK, title: "Add an author's entire catalogue of official books" };
  const tools: LibraryTool[] = [
    { label: "Scan library", busyLabel: "Scanning…", busy: scanning || scan.running, onClick: scanLibrary, title: "Find books already in your library folder and catalog them" },
    {
      label: `Re-match ${upgradable} to Hardcover`,
      hidden: !(source === "hardcover" && (upgradable > 0 || upgrade?.running)),
      busy: !!upgrade?.running,
      progress: upgrade?.running ? `Re-matching… ${upgrade.done}/${upgrade.total}` : undefined,
      onClick: startUpgrade,
      title: `${upgradable} book${upgradable === 1 ? " is" : "s are"} still on Open Library keys. Re-matching runs by itself when the key is saved and at startup; this retries whatever didn't match. Files, monitoring and profiles are kept.${upgrade?.notes?.length ? `\n\nLast run: ${upgrade.notes.slice(0, 4).join("\n")}` : ""}`,
    },
    { label: dupeCount > 0 ? `Possible duplicates (${dupeCount})` : "Possible duplicates", onClick: () => setDupesOpen(true), title: "Rows that may be the same book, side by side to merge or mark as different — and earlier merges" },
    // Hardcover hands over the series with each book (the refresh-books job fills it
    // in), so this indexer-search fallback is for Open Library setups only.
    { label: "Find series", hidden: source === "hardcover", busyLabel: "Looking up…", busy: backfilling, onClick: backfillSeries, title: "Open Library doesn't say which series a book is in, so this searches your indexers once per book to read the series off release names. Downloads nothing." },
    ...(phone ? [
      { label: `Search missing (${missingEditions})`, hidden: !showSweep, busy: !!sweep?.running, progress: sweepProgress, onClick: startSweep, title: sweepTitle },
      addAuthor,
    ] : []),
  ];
  // One View control for both choices: authors or books, as tiles or a table (a phone
  // always gets tiles).
  const viewOptions = phone
    ? [{ value: "author-grid", label: "Authors" }, { value: "book-grid", label: "Books" }]
    : [{ value: "author-grid", label: "Authors · grid" }, { value: "author-table", label: "Authors · table" }, { value: "book-grid", label: "Books · grid" }, { value: "book-table", label: "Books · table" }];

  // The grid's cards are the shared library card: its ⋯ menu (Search now, Remove) shows
  // on hover with a mouse and always on a touch screen. Only the card reads this config.
  const cardConfig: LibraryConfig<BookItem> = {
    noun: { one: "book", many: "books" },
    storageKey: "books",
    detailPath: (b) => `/books/${b.id}`,
    filters: [],
    searchText: (b) => `${b.title} ${b.author}`,
    compare: (a, b) => a.title.localeCompare(b.title),
    columns: [],
    tableMinWidth: 780,
    sorts: [],
    card: { meta: (b) => b.author || "—", status: statusOf },
    actions: (b): MenuItem[] => [
      ...(b.monitored && !b.has_file ? [{ label: "Search now", busyLabel: "Searching…", onSelect: () => search(b) }] : []),
      { label: "Remove…", tone: "danger" as const, onSelect: () => setConfirmDelete(b) },
    ],
  };

  return (
    <>
      <PageHeader title="Books" />
      <Page width="wide">
        <LibraryToolbar
          count={booksQ.data ? list.length : null}
          pickers={multiSelect ? [] : [
            { label: "View", value: `${mode}-${view}`, options: viewOptions, onChange: (v) => { const [m, vw] = v.split("-"); setMode(m as "author" | "book"); if (!phone) setView(vw as "grid" | "table"); } },
          ]}
          sort={{ label: "Sort", value: sort, options: mode === "author" ? AUTHOR_SORTS : BOOK_SORTS, onChange: (v) => setSortKey(v as BookSort) }}
          select={{ active: multiSelect, onToggle: () => (multiSelect ? exitMultiSelect() : enterSelect()) }}
          tools={tools}
          toolsBadge={dupeCount}
          actions={!phone && showSweep ? [{ label: `Search missing (${missingEditions})`, busy: !!sweep?.running, busyLabel: sweepProgress, onClick: startSweep, title: sweepTitle }] : []}
          secondary={phone ? undefined : addAuthor}
          primary={{ label: "+ Add book", onClick: () => setAdding(true), disabled: !metaOK }}
        />
        <FilterBar
          filters={FILTERS.map((f) => ({ key: f.key, label: f.label, count: f.key === "all" ? list.length : list.filter((b) => matches(b, f.key)).length }))}
          active={filter}
          onFilter={(k) => setFilter(k as FilterKey)}
          query={query}
          onQuery={setQuery}
          noun="books"
        />

        {multiSelect && (
          <div className="mb-4 flex flex-wrap items-center gap-2.5 rounded-xl p-3" style={{ background: "var(--panel)", border: "1px solid var(--accent-line)" }}>
            <span className="font-mono text-sm font-semibold" style={{ color: "var(--accent)" }}>{selected.size} selected</span>
            <button onClick={() => setSelected(new Set(filtered.map((b) => b.id)))} className="rounded-lg px-2.5 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink)" }}>Select all ({filtered.length})</button>
            <button onClick={clearSelect} disabled={selected.size === 0} className="rounded-lg px-2.5 py-1.5 text-sm" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Clear</button>
            <span className="mx-1 h-5 w-px" style={{ background: "var(--line)" }} />
            <button onClick={() => bulkMonitor(true)} disabled={selected.size === 0 || bulkBusy} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>Monitor</button>
            <button onClick={() => bulkMonitor(false)} disabled={selected.size === 0 || bulkBusy} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Unmonitor</button>
            <span className="mx-1 h-5 w-px" style={{ background: "var(--line)" }} />
            <select
              aria-label="Set the quality profile of the selected books"
              defaultValue=""
              disabled={selected.size === 0 || bulkBusy}
              onChange={(e) => e.target.value && (bulkProfile(e.target.value), (e.target.value = ""))}
              className="rounded-lg px-2.5 py-1.5 text-sm font-medium"
              style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
            >
              <option value="">Set quality profile…</option>
              {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
            </select>
          </div>
        )}

        {booksQ.data && error && <StaleBanner message={error} onRetry={refresh} />}
        {upgrade && !upgrade.running && !hideUpgrade && (upgrade.error || leftVisible.length > 0 || ((upgrade.left?.length ?? 0) === 0 && (upgrade.notes?.length ?? 0) > 0)) && (
          <div className="mb-4 rounded-lg px-3.5 py-2.5 text-sm" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink-dim)" }}>
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <b>Hardcover re-match:</b> {upgrade.upgraded} upgraded, {upgrade.flagged} flagged as possible duplicates, {upgrade.unmatched} left as they were{upgrade.error ? ` — stopped: ${upgrade.error}` : "."}
                {leftVisible.length > 0 && <span className="ml-1 text-ink-faint">Ignore a book and the re-match leaves it alone for good.</span>}
              </div>
              <div className="flex flex-none items-center gap-2">
                {leftVisible.length > 1 && (
                  <button onClick={() => keepAll()} className="rounded-md px-2 py-1 text-xs font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Ignore all</button>
                )}
                <IconButton label="Hide this until the next run" onClick={() => setHideUpgrade(true)} className="text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
              </div>
            </div>
            {leftVisible.length > 0 ? (
              <ul className="m-0 mt-1.5 list-none space-y-1 p-0 text-sm text-ink-faint">
                {leftVisible.map((l) => (
                  <li key={l.id} className="flex items-center gap-2">
                    <span className="min-w-0 flex-1"><Link to={`/books/${l.id}`} className="font-semibold hover:text-[var(--accent)]" style={{ color: "var(--ink-dim)" }}>{l.title}</Link>{l.author ? ` — ${l.author}` : ""}: {l.reason}</span>
                    <button onClick={() => keepOne(l.id)} title="Stop reporting this book: it stays on its current catalogue entry and the re-match skips it from now on" className="flex-none rounded-md px-2 py-0.5 text-xs font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Ignore</button>
                  </li>
                ))}
              </ul>
            ) : (upgrade.notes?.length ?? 0) > 0 && (
              <ul className="m-0 mt-1.5 list-disc pl-5 text-sm text-ink-faint">
                {upgrade.notes!.map((n, i) => <li key={i}>{n}</li>)}
              </ul>
            )}
          </div>
        )}
        {/* Open Library works with no key; Hardcover is the better catalogue. Say so once,
            where the books are, rather than hoping the Settings page gets read. */}
        {source === "openlibrary" && metaOK && (
          <div className="mb-4 rounded-lg px-3.5 py-2.5 text-sm" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink-dim)" }}>
            Books are using <b>Open Library</b>, which needs no key. For the preferred catalogue — one entry per book, series and author pages, cleaner covers —{" "}
            {admin
              ? <>add a free <b>Hardcover</b> API key in <Link to={LINKS.apiKeys} style={{ color: "var(--accent)" }}>Settings → Connections</Link>.</>
              : <>ask an admin to add a free <b>Hardcover</b> API key (Settings → Connections).</>}
            {" "}Everything already here keeps working either way.
          </div>
        )}

        {!booksQ.data ? (
          error ? <ErrorState what="your books" message={error} onRetry={refresh} busy={booksQ.loading} /> : <Skeleton variant={view === "table" ? "table" : "grid"} />
        ) : list.length === 0 ? (
          <LibraryEmpty noun="books" folder="ebook and audiobook folders" addLabel="Add book" onScan={scanLibrary} scanning={scanning || scan.running} />
        ) : filtered.length === 0 ? (
          <div className="rounded-xl p-12 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>{q ? <>No books match “<b>{query.trim()}</b>”.</> : <>No books match the <b>{FILTERS.find((f) => f.key === filter)?.label}</b> filter.</>}</div>
        ) : mode === "author" ? (
          <>
            {view === "table" ? <AuthorTable authors={authorList.shown} /> : (
              <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))" }}>
                {authorList.shown.map((a) => <AuthorCard key={a.key} group={a} />)}
              </div>
            )}
            {authorList.sentinel}
          </>
        ) : view === "table" ? (
          <>
            <BooksTable list={bookList.shown} onSearch={search} />
            {bookList.sentinel}
          </>
        ) : (
          <>
            <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(140px, 1fr))" }}>
              {bookList.shown.map((b) => (
                <LibraryCard
                  key={b.id}
                  item={{ ...b, poster_url: b.cover_url }}
                  config={cardConfig}
                  selectable={multiSelect}
                  selected={selected.has(b.id)}
                  onToggle={() => toggleSelect(b.id)}
                />
              ))}
            </div>
            {bookList.sentinel}
          </>
        )}

      </Page>
      {adding && <AddBookModal onClose={() => setAdding(false)} onAdded={() => { setAdding(false); refresh(); }} />}
      {addingAuthor && <AddAuthorModal onClose={() => setAddingAuthor(false)} onAdded={(msg) => { setAddingAuthor(false); refresh(); say(msg); }} />}
      {dupesOpen && (
        <Suspense fallback={null}>
          <DuplicatesModal onClose={() => setDupesOpen(false)} onChanged={() => { refresh(); countDupes(); }} />
        </Suspense>
      )}
      {confirmDelete && <DeleteBookDialog book={confirmDelete} onClose={() => setConfirmDelete(null)} onDeleted={() => { setConfirmDelete(null); refresh(); }} />}
    </>
  );
}

function gb(bytes?: number): string {
  if (!bytes) return "—";
  const mb = bytes / 1024 ** 2;
  return mb >= 1000 ? `${(mb / 1024).toFixed(1)} GB` : `${mb.toFixed(0)} MB`;
}

function editionTags(b: Book): string {
  const t: string[] = [];
  if (b.ebook) t.push(b.ebook.format);
  if (b.audiobook) t.push(b.audiobook.file_count > 1 ? `${b.audiobook.format}×${b.audiobook.file_count}` : b.audiobook.format);
  return t.join(" + ") || "—";
}

function BooksTable({ list, onSearch }: { list: Book[]; onSearch: (b: Book) => void }) {
  const th = "px-2.5 py-2 text-left font-mono text-2xs font-bold uppercase tracking-[0.06em] text-ink-faint";
  const td = "px-2.5 py-2 align-middle";
  return (
    <div className="thin-scroll overflow-x-auto rounded-xl" style={{ border: "1px solid var(--line)" }}>
      <table className="w-full border-collapse text-sm" style={{ minWidth: "780px" }}>
        <thead>
          <tr style={{ background: "var(--panel)", borderBottom: "1px solid var(--line)" }}>
            <th className={th}>Title</th>
            <th className={th}>Author</th>
            <th className={`${th} text-right`}>Year</th>
            <th className={th}>Status</th>
            <th className={th}>Editions</th>
            <th className={`${th} text-right`}>Size</th>
            <th className={th}>Monitored</th>
            <th className={th}><span className="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {list.map((b) => {
            const st = statusOf(b);
            const size = (b.ebook?.size_bytes ?? 0) + (b.audiobook?.size_bytes ?? 0);
            const missing = (b.want_ebook && !b.ebook) || (b.want_audiobook && !b.audiobook);
            return (
              <tr key={b.id} className="transition-colors hover:bg-[var(--panel-2)]" style={{ background: "var(--panel)", borderBottom: "1px solid var(--line-soft)" }}>
                <td className={`${td} min-w-[200px]`}><Link to={`/books/${b.id}`} className="font-semibold hover:text-[var(--accent)]">{b.title}</Link></td>
                <td className={td}>{b.author || "—"}</td>
                <td className={`${td} text-right font-mono text-xs text-ink-dim`}>{b.year || "—"}</td>
                <td className={td}><span className="font-mono text-2xs uppercase" style={{ color: st.color }} title={st.title}>{st.label}</span></td>
                <td className={td}><span className="font-mono text-xs text-ink-dim">{editionTags(b)}</span></td>
                <td className={`${td} text-right font-mono text-xs text-ink-dim`}>{size > 0 ? gb(size) : "—"}</td>
                <td className={td}><span className="font-mono text-2xs uppercase" style={{ color: b.monitored ? "var(--accent)" : "var(--ink-faint)" }}>{b.monitored ? "Yes" : "No"}</span></td>
                <td className={`${td} text-right`}>
                  {missing && (
                    <button onClick={() => onSearch(b)} title="Search your indexers for the edition this book is missing" className="whitespace-nowrap rounded-md px-2 py-1 text-xs font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Search</button>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// A library row as the shared card wants it: the cover is its poster.
type BookItem = Book & { poster_url?: string };

function AddBookModal({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<BookLookup[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [profile, setProfile] = useState("");
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  const [addingKey, setAddingKey] = useState<string | null>(null);
  // Which catalogue answered; when it's Hardcover, Open Library is one click away.
  const [source, setSource] = useState<BookSource>("openlibrary");
  const [olResults, setOlResults] = useState<BookLookup[] | null>(null);
  const [olLoading, setOlLoading] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    qualityApi.qualityProfiles("book").then((r) => {
      setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })));
      const def = r.profiles.find((p) => p.is_default) ?? r.profiles[0];
      if (def) setProfile(def.key);
    }).catch(() => {});
  }, []);

  const search = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!q.trim()) return;
    setLoading(true); setError(null); setOlResults(null);
    try { const r = await api.lookupBooks(q.trim()); setResults(r.results); setSource(r.source); }
    catch (e) { setError((e as Error).message); } finally { setLoading(false); }
  };
  const searchOpenLibrary = async () => {
    setOlLoading(true);
    try {
      const r = await api.lookupBooks(q.trim(), "openlibrary");
      const seen = new Set(results.map((x) => x.key));
      setOlResults(r.results.filter((x) => !seen.has(x.key)));
    } catch (e) { setError((e as Error).message); } finally { setOlLoading(false); }
  };
  const row = (r: BookLookup) => (
    <button key={r.key} onClick={() => add(r)} disabled={addingKey !== null} className="flex w-full items-center gap-3 rounded-lg p-2 text-left transition-colors hover:bg-[var(--panel-2)]">
      <div className="h-[66px] w-[44px] flex-none overflow-hidden rounded" style={{ background: "var(--panel-2)" }}>
        {r.cover_url && <img src={r.cover_url} alt="" className="h-full w-full object-cover" loading="lazy" />}
      </div>
      <div className="min-w-0 flex-1">
        <div className="truncate text-md font-semibold">{r.title} <span className="font-normal text-ink-faint">{r.year ? `(${r.year})` : ""}</span></div>
        <div className="truncate text-sm text-ink-dim">{r.author || "Unknown author"}</div>
      </div>
      <span className="flex-none rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>{addingKey === r.key ? "Adding…" : "Add"}</span>
    </button>
  );

  const add = async (r: BookLookup) => {
    setAddingKey(r.key); setError(null);
    try { await api.addBook({ ol_key: r.key, quality_profile: profile, monitored: true, title: r.title, author: r.author, year: r.year, cover_url: r.cover_url }); onAdded(); }
    catch (e) { setError((e as Error).message); setAddingKey(null); }
  };

  return (
    <Modal onClose={onClose} labelledBy="add-book-title" size="lg" initialFocus={inputRef}>
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h2 id="add-book-title" className="m-0 text-lg font-bold">Add a book</h2>
          <label className="flex items-center gap-2">
            <span className="font-mono text-2xs uppercase text-ink-faint">Format</span>
            <select value={profile} onChange={(e) => setProfile(e.target.value)} className="rounded-lg px-2 py-1.5 text-sm" style={inputStyle}>
              {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
            </select>
          </label>
        </div>
        <form onSubmit={search} className="mb-3 flex gap-2">
          <input ref={inputRef} value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search by title or author" placeholder="Search by title or author — e.g. Dune Herbert" className="min-w-0 flex-1 rounded-lg px-3 py-2 text-md" style={inputStyle} />
          <Button type="submit" variant="primary" busy={loading} busyLabel="…">Search</Button>
        </form>
        {error && <div className="mb-3 text-sm" style={{ color: "var(--reject)" }}>{error}</div>}
        <div className="thin-scroll max-h-[56vh] overflow-y-auto">
          {results.map(row)}
          {source === "hardcover" && results.length > 0 && olResults === null && (
            <button type="button" onClick={searchOpenLibrary} disabled={olLoading} className="mt-1 w-full rounded-lg p-2 text-center text-sm font-semibold hover:bg-[var(--panel-2)]" style={{ color: "var(--ink-dim)" }}>
              {olLoading ? "Searching Open Library…" : "Not there? Show Open Library results too"}
            </button>
          )}
          {olResults !== null && (
            <>
              <div className="mt-2 px-2 font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">Open Library</div>
              {olResults.length === 0 ? <div className="p-2 text-sm text-ink-dim">Open Library has nothing more for this search.</div> : olResults.map(row)}
            </>
          )}
        </div>
    </Modal>
  );
}

// AuthorGroup is one author tile: the books linked to one author row (author_id), or, for
// books not linked to anyone yet, the books sharing an author string. Spelling variants of
// one person ("J.K. Rowling" / "J. K. Rowling") are one row, so one tile.
interface AuthorGroup {
  key: string;
  id?: number;
  name: string;
  books: Book[];
  image?: string;
}

function groupByAuthor(books: Book[], images: Record<string, string>): AuthorGroup[] {
  const map = new Map<string, Book[]>();
  for (const b of books) {
    const key = b.author_id ? `id:${b.author_id}` : `name:${b.author || "Unknown author"}`;
    const arr = map.get(key);
    if (arr) arr.push(b); else map.set(key, [b]);
  }
  return [...map.entries()]
    .map(([key, group]) => {
      // The tile shows the spelling most of the author's books carry.
      const counts = new Map<string, number>();
      for (const b of group) counts.set(b.author || "Unknown author", (counts.get(b.author || "Unknown author") ?? 0) + 1);
      const names = [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([n]) => n);
      return {
        key,
        id: group[0].author_id,
        name: names[0],
        books: group.slice().sort((a, b) => a.title.localeCompare(b.title)),
        image: names.map((n) => images[n]).find(Boolean),
      };
    })
    .sort((a, b) => a.name.localeCompare(b.name)); // authors A→Z, books A→Z within each
}

// An author's page: by id when the books are linked to an author row; an unlinked name
// goes through the old by-name route, which resolves it or asks which author is meant.
function authorPath(a: { id?: number; name: string }): string {
  return a.id ? `/books/authors/${a.id}` : `/books/author/${encodeURIComponent(a.name)}`;
}

function authorStats(books: Book[]): { downloaded: number; wanted: number; cover?: string } {
  let downloaded = 0, wanted = 0;
  for (const b of books) {
    if (b.has_file) downloaded++;
    else if (b.monitored) wanted++;
  }
  return { downloaded, wanted, cover: books.find((b) => b.cover_url)?.cover_url };
}

function AuthorCard({ group }: { group: AuthorGroup }) {
  const { name, books, image } = group;
  const { downloaded, wanted, cover } = authorStats(books);
  return (
    <Link to={authorPath(group)} className="group relative block overflow-hidden rounded-xl" style={{ aspectRatio: "2/3", border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      {image ? (
        <img src={image} alt={name} className="h-full w-full object-cover transition-transform group-hover:scale-[1.03]" loading="lazy" decoding="async" />
      ) : cover ? (
        <img src={posterThumb(cover)} alt={name} className="h-full w-full object-cover transition-transform group-hover:scale-[1.03]" loading="lazy" decoding="async" />
      ) : (
        <div className="flex h-full w-full items-center justify-center text-display-lg font-bold text-white" style={{ background: "linear-gradient(150deg, hsl(28 30% 26%), hsl(24 28% 16%))" }}>{name.charAt(0).toUpperCase()}</div>
      )}
      <div className="absolute inset-x-0 bottom-0 p-2.5" style={{ background: "linear-gradient(to top, rgba(0,0,0,.92), rgba(0,0,0,.35) 60%, transparent)" }}>
        <div className="truncate text-base font-bold text-white" title={name}>{name}</div>
        <div className="mt-0.5 flex items-center gap-2 text-xs" style={{ color: "rgba(255,255,255,.8)" }}>
          <span>{books.length} book{books.length === 1 ? "" : "s"}</span>
          {downloaded > 0 && <span style={{ color: "var(--good)" }}>{downloaded} ✓</span>}
          {wanted > 0 && <span style={{ color: "var(--avoid)" }}>{wanted} wanted</span>}
        </div>
      </div>
    </Link>
  );
}

function AuthorTable({ authors }: { authors: AuthorGroup[] }) {
  return (
    <div className="overflow-hidden rounded-xl" style={{ border: "1px solid var(--line)" }}>
      <table className="w-full border-collapse text-base">
        <thead>
          <tr style={{ background: "var(--panel-2)" }}>
            <th className="px-3 py-2 text-left font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">Author</th>
            <th className="px-3 py-2 text-right font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">Books</th>
            <th className="px-3 py-2 text-right font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">Downloaded</th>
            <th className="px-3 py-2 text-right font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">Wanted</th>
          </tr>
        </thead>
        <tbody>
          {authors.map((a, i) => {
            const { downloaded, wanted } = authorStats(a.books);
            return (
              <tr key={a.key} style={{ borderTop: i === 0 ? "none" : "1px solid var(--line-soft)" }}>
                <td className="px-3 py-2">
                  <Link to={authorPath(a)} className="font-semibold hover:underline" style={{ color: "var(--accent)" }}>{a.name}</Link>
                </td>
                <td className="px-3 py-2 text-right font-mono text-ink-dim">{a.books.length}</td>
                <td className="px-3 py-2 text-right font-mono" style={{ color: downloaded > 0 ? "var(--good)" : "var(--ink-faint)" }}>{downloaded}</td>
                <td className="px-3 py-2 text-right font-mono" style={{ color: wanted > 0 ? "var(--avoid)" : "var(--ink-faint)" }}>{wanted}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function AddAuthorModal({ onClose, onAdded }: { onClose: () => void; onAdded: (msg: string) => void }) {
  const [q, setQ] = useState("");
  const [authors, setAuthors] = useState<BookAuthor[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<BookAuthor | null>(null);
  const [works, setWorks] = useState<BookDiscoverCard[] | null>(null);
  const [profile, setProfile] = useState("");
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    qualityApi.qualityProfiles("book").then((r) => {
      setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })));
      const def = r.profiles.find((p) => p.is_default) ?? r.profiles[0];
      if (def) setProfile(def.key);
    }).catch(() => {});
  }, []);

  const search = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!q.trim()) return;
    setLoading(true); setError(null);
    try { setAuthors(await api.searchBookAuthors(q.trim())); } catch (e) { setError((e as Error).message); } finally { setLoading(false); }
  };

  const pick = (a: BookAuthor) => {
    setSelected(a); setWorks(null); setError(null);
    api.bookAuthorWorks(a.key).then(setWorks).catch(() => setWorks([]));
  };

  const add = async () => {
    if (!selected) return;
    setBusy(true); setError(null);
    try {
      const r = await api.addAuthor({ author_key: selected.key, name: selected.name, quality_profile: profile });
      const parts = [`Added ${r.added} book${r.added === 1 ? "" : "s"} by ${selected.name}`];
      if (r.skipped > 0) parts.push(`${r.skipped} already in library`);
      onAdded(parts.join(" · "));
    } catch (e) { setError((e as Error).message); setBusy(false); }
  };

  return (
    <Modal onClose={onClose} labelledBy="add-author-title" size="lg" initialFocus={inputRef}>
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h2 id="add-author-title" className="m-0 text-lg font-bold">Add an author</h2>
          <label className="flex items-center gap-2">
            <span className="font-mono text-2xs uppercase text-ink-faint">Format</span>
            <select value={profile} onChange={(e) => setProfile(e.target.value)} className="rounded-lg px-2 py-1.5 text-sm" style={inputStyle}>
              {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
            </select>
          </label>
        </div>

        {error && <div className="mb-3 text-sm" style={{ color: "var(--reject)" }}>{error}</div>}

        {!selected ? (
          <>
            <p className="mb-3 text-sm text-ink-dim">Search for an author and add their entire catalogue of official individual books (box sets and collections are excluded).</p>
            <form onSubmit={search} className="mb-3 flex gap-2">
              <input ref={inputRef} value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search by author" placeholder="Search by author — e.g. Brandon Sanderson" className="min-w-0 flex-1 rounded-lg px-3 py-2 text-md" style={inputStyle} />
              <Button type="submit" variant="primary" busy={loading} busyLabel="…">Search</Button>
            </form>
            <div className="thin-scroll max-h-[52vh] overflow-y-auto">
              {authors === null ? null : authors.length === 0 ? (
                <div className="p-6 text-center text-base text-ink-dim">No authors found.</div>
              ) : authors.map((a) => (
                <button key={a.key} onClick={() => pick(a)} className="flex w-full items-center gap-3 rounded-lg p-2.5 text-left transition-colors hover:bg-[var(--panel-2)]">
                  <span className="grid h-10 w-10 flex-none place-items-center rounded-full text-md font-bold text-white" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))" }}>{a.name.charAt(0).toUpperCase()}</span>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-md font-semibold">{a.name}</div>
                    <div className="truncate text-sm text-ink-faint">{a.work_count > 0 ? `${a.work_count.toLocaleString()} works` : "Author"}{a.top_work ? ` · ${a.top_work}` : ""}</div>
                  </div>
                  <span className="flex-none text-ink-faint">›</span>
                </button>
              ))}
            </div>
          </>
        ) : (
          <>
            <button onClick={() => { setSelected(null); setWorks(null); }} className="mb-3 inline-flex items-center gap-1 text-sm text-ink-dim hover:text-[var(--ink)]">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none"><path d="M15 19l-7-7 7-7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>
              Choose a different author
            </button>
            <div className="mb-3 flex items-center justify-between gap-3">
              <div className="min-w-0">
                <div className="text-lg font-bold">{selected.name}</div>
                <div className="text-sm text-ink-faint">{works === null ? "Loading catalogue…" : `${works.length} official book${works.length === 1 ? "" : "s"}`}</div>
              </div>
              <Button variant="primary" onClick={add} disabled={works === null || works.length === 0} busy={busy} busyLabel="Adding…" className="flex-none disabled:opacity-50">
                {works ? `Add all ${works.length} books` : "Add all"}
              </Button>
            </div>
            <div className="thin-scroll max-h-[48vh] overflow-y-auto">
              {works === null ? (
                <div className="grid gap-2.5" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(90px, 1fr))" }}>
                  {Array.from({ length: 12 }).map((_, i) => <div key={i} className="rounded-lg" style={{ aspectRatio: "2/3", background: "var(--panel-2)" }} />)}
                </div>
              ) : (
                <div className="grid gap-2.5" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(90px, 1fr))" }}>
                  {works.map((b) => (
                    <div key={b.key} className="overflow-hidden rounded-lg" style={{ aspectRatio: "2/3", border: "1px solid var(--line)", background: "var(--panel-2)" }} title={`${b.title}${b.year ? ` (${b.year})` : ""}`}>
                      {b.cover_url ? <img src={b.cover_url} alt={b.title} className="h-full w-full object-cover" loading="lazy" /> : <div className="flex h-full items-center justify-center p-1.5 text-center text-xs font-semibold text-white" style={{ background: "linear-gradient(150deg, hsl(28 30% 26%), hsl(24 28% 16%))" }}>{b.title}</div>}
                    </div>
                  ))}
                </div>
              )}
            </div>
          </>
        )}
    </Modal>
  );
}

const inputStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;
