import { Suspense, useCallback, useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { PageHeader } from "../../components/PageHeader";
import { MetadataMissing } from "../../components/MetadataMissing";
import { lazyPage } from "../../lib/lazyPage";
import { pickTab, withTab } from "../../lib/useTabParam";
import { TabPanel, Tabs } from "../../ui/Tabs";
import { useMe } from "../../lib/me";
import { api, type DiscoverCard } from "../../lib/api";
import { formatSeasons } from "../../lib/requestStage";
import { announceRequested } from "../../lib/pushPrompt";
import { useToast } from "../../ui";
import { discoverPlace, FOLLOWING, type ReqStatus, type RowCtx } from "./shared";
import { SearchBox } from "./SearchBox";
import { MyRequestsRow } from "./RequestsRow";
import { BrowseTab, DiscoverTab } from "./tabs";
import { Page } from "../../ui/Page";

// Discover is the requester's home: tabs (Discover, Movies, Series, Books), the omnibox,
// the rows, and whatever is open over them at its own address. Its parts live beside it in
// features/discover; pages/Discover.tsx re-exports it so the route table doesn't change.

// The Books tab is a separate Open Library experience; its code loads only when chosen.
const BooksDiscover = lazyPage(() => import("../../pages/BooksDiscover"), "BooksDiscover");
// A title's sheet is its own chunk, fetched when the first title opens.
const TitleSheet = lazyPage(() => import("./TitleSheet"), "TitleSheet");
// The browse grid behind "See all" and the paged search results: one chunk, fetched when
// either first shows.
const DiscoverBrowse = lazyPage(() => import("../../pages/DiscoverBrowse"), "DiscoverBrowse");
const SearchResults = lazyPage(() => import("../../pages/DiscoverBrowse"), "SearchResults");
// A person's page (a cast tile, a director, a name in search): its own chunk.
const PersonSheet = lazyPage(() => import("../../pages/DiscoverPerson"), "PersonSheet");
// A collection's page ("See all" on a "Complete the …" row, or a movie's sheet).
const CollectionSheet = lazyPage(() => import("../../pages/DiscoverCollection"), "CollectionSheet");

type Tab = "discover" | "movies" | "series" | "books";
const BASE_TABS: { key: Tab; label: string }[] = [
  { key: "discover", label: "Discover" },
  { key: "movies", label: "Movies" },
  { key: "series", label: "Series" },
];

export function Discover({ chrome = true }: { chrome?: boolean }) {
  const { user, booksEnabled, metadataReady } = useMe();
  // Books get their own tab at the end — a completely separate Open Library experience.
  const TABS = booksEnabled ? [...BASE_TABS, { key: "books" as Tab, label: "Books" }] : BASE_TABS;
  // What this session asked for, by card key, with the status the server answered: an
  // auto-approved request is already approved and searching, not waiting.
  const [requested, setRequested] = useState<Map<string, ReqStatus>>(new Map());
  // The tab and the committed search live in the address (?tab=, ?q=), so Back steps
  // through them, a reload keeps the results, and a notification can link straight to a
  // search. On the Books tab ?q= seeds the book search instead.
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  // Where in Discover this is: the rows or a browse grid, and what's open over it.
  const { pathname } = useLocation();
  const place = discoverPlace(pathname);
  const overlay = place.overlay && place.overlay.id > 0 ? place.overlay : null;
  const tab = pickTab(params.get("tab"), TABS.map((t) => t.key), "discover");
  const q = params.get("q") ?? "";
  // `search` is the committed query that swaps the page to the full results grid (only via
  // "See all" / Enter, not per keystroke); `searchInput` is what's typed in the omnibox.
  const search = tab === "books" ? "" : q;
  const bookSeed = tab === "books" ? q : "";
  // ?work=<key>: one book's sheet on the Books tab (a notification's link); closing it drops the key.
  const work = tab === "books" ? params.get("work") ?? "" : "";
  const closeWork = () => setParams((p) => { const next = new URLSearchParams(p); next.delete("work"); return next; }, { replace: true });
  const [searchInput, setSearchInput] = useState(search);
  // Back, Forward or a notification link changed the committed search: show it in the box.
  useEffect(() => { setSearchInput(search); }, [search]);
  const toast = useToast();
  // Readonly users can browse but never request.
  const canRequest = !!user && user.role !== "readonly";

  // Choosing a tab (even the current one, while results are showing) leaves the search.
  const setTab = (t: Tab) => {
    setSearchInput("");
    // From a browse grid, a tab goes back to the rows (the grid's filters stay behind).
    if (place.browse) { navigate(t === "discover" ? "/discover" : `/discover?tab=${t}`); return; }
    if (t === tab && !q) return;
    setParams((p) => { const next = withTab(p, "tab", t, "discover"); next.delete("q"); next.delete("work"); return next; });
  };
  // Committing a search is a new history entry; emptying the box just drops it.
  const commitSearch = (query: string) => {
    setSearchInput(query);
    if (place.browse) { navigate(`/discover?q=${encodeURIComponent(query)}`); return; }
    if (query === q) return;
    setParams((p) => { const next = new URLSearchParams(p); next.set("q", query); return next; });
  };
  const onSearchChange = (v: string) => {
    setSearchInput(v);
    if (!v.trim() && q) setParams((p) => { const next = new URLSearchParams(p); next.delete("q"); return next; }, { replace: true });
  };

  // Rethrows on failure so callers (modal, quick-request) only flip to their success
  // state on an actual success. subscribed=true → you joined an existing request.
  // seasons (a show): the season numbers asked for; absent or null is the whole show.
  // profile: staff's quality-profile pick (the title sheet's picker); absent for everyone else.
  const doRequest = useCallback(async (c: DiscoverCard, note?: string, seasons?: number[] | null, quiet = false, profile?: string): Promise<{ subscribed: boolean; status: ReqStatus }> => {
    const key = `${c.media_type}:${c.tmdb_id}`;
    try {
      const res = await api.createRequest({ media_type: c.media_type, tmdb_id: c.tmdb_id, title: c.title, year: c.year, poster_url: c.poster_url, overview: c.overview, note: note?.trim() || undefined, seasons: seasons?.length ? seasons : undefined, quality_profile: profile });
      const status = res.request.status;
      setRequested((m) => new Map(m).set(key, status));
      if (!quiet) toast(res.subscribed ? FOLLOWING : requestedMessage(c.title, status, res.request.seasons));
      announceRequested();
      return { subscribed: res.subscribed, status };
    } catch (e) {
      if (!quiet) toast((e as Error).message, { tone: "error" });
      throw e;
    }
  }, [toast]);
  const isRequested = useCallback((c: DiscoverCard) => requested.get(`${c.media_type}:${c.tmdb_id}`), [requested]);

  const hideInLibrary = params.get("hide_in_library") === "1";
  const ctx: RowCtx = { doRequest, isRequested, canRequest, hideInLibrary, flash: toast };
  const setHideInLibrary = (checked: boolean) => setParams((p) => {
    const next = new URLSearchParams(p);
    if (checked) next.set("hide_in_library", "1"); else next.delete("hide_in_library");
    return next;
  }, { replace: true });

  // A title's own address (/discover/movie/603) opens its sheet over the page. Discover
  // stays mounted underneath, so its rows, what loaded and the scroll position survive,
  // and Back from the title lands right where you were.

  return (
    <>
      {chrome && <PageHeader title="Discover" />}
      <Page width="wide" className="py-5">
        {/* Tabs + search. On a phone they stack, search on top so the tabs still sit on the
            underline, and the tabs scroll sideways rather than widening the page. Search comes
            first in the DOM as well, so focus order matches what a phone shows; sm:order-last
            puts it back after the tabs on wider screens. */}
        <div className="mb-5 flex flex-col gap-2 border-b sm:flex-row sm:flex-wrap sm:items-center sm:justify-between sm:gap-3" style={{ borderColor: "var(--line)" }}>
          {/* Books have their own search inside BooksDiscover — hide the movie/TV one there.
              The bell lives in the layout's top bar, on every page. */}
          {tab !== "books" && (
            <div className="flex w-full items-center justify-end gap-2 sm:order-last sm:w-auto sm:justify-start">
              {metadataReady ? (
                <SearchBox value={searchInput} onChange={onSearchChange} onSeeAll={commitSearch} />
              ) : (
                <input disabled placeholder="Search isn't available yet" aria-label="Search movies and TV (not available yet)" className="min-w-0 flex-1 rounded-lg px-3 py-2 text-base opacity-60 sm:w-[210px] sm:flex-initial" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
              )}
            </div>
          )}
          {/* No tab is current while search results are showing. */}
          <Tabs tabs={TABS} value={search || place.browse ? null : tab} onChange={setTab} idPrefix="discover" label="Discover sections" className="-mb-px" />
          {tab !== "books" && (
            <label className="flex min-h-[36px] items-center gap-2 text-sm font-medium text-ink-dim">
              <input type="checkbox" checked={hideInLibrary} onChange={(e) => setHideInLibrary(e.target.checked)} style={{ accentColor: "var(--accent)" }} />
              Hide in library
            </label>
          )}
        </div>

        <TabPanel idPrefix="discover" value={tab}>
          {tab === "books" ? (
            <Suspense fallback={<div className="py-10 text-center text-base text-ink-dim">Loading…</div>}>
              <BooksDiscover flash={toast} canRequest={canRequest} initialQuery={bookSeed} initialWork={work || undefined} onWorkClosed={closeWork} />
            </Suspense>
          ) : !metadataReady ? (
            // No TMDB key: every movie/TV feed would fail on its own and repeat the same error
            // row after row. Show the viewer's requests (they don't need TMDB) and one message
            // worded for their role instead. Books use Open Library and are unaffected.
            <div className="flex flex-col gap-7">
              <MyRequestsRow />
              <MetadataMissing variant="empty" />
            </div>
          ) : place.browse ? (
            <Suspense fallback={gridLoading}><DiscoverBrowse ctx={ctx} /></Suspense>
          ) : search ? (
            <Suspense fallback={gridLoading}><SearchResults query={search} ctx={ctx} /></Suspense>
          ) : (
            <>
              {tab === "discover" && <DiscoverTab ctx={ctx} />}
              {tab === "movies" && <BrowseTab media="movie" ctx={ctx} />}
              {tab === "series" && <BrowseTab media="series" ctx={ctx} />}
            </>
          )}
        </TabPanel>
      </Page>
      {(overlay?.kind === "movie" || overlay?.kind === "series") && (
        <Suspense fallback={null}><TitleSheet media={overlay.kind} tmdbId={overlay.id} ctx={ctx} /></Suspense>
      )}
      {overlay?.kind === "person" && <Suspense fallback={null}><PersonSheet id={overlay.id} ctx={ctx} /></Suspense>}
      {overlay?.kind === "collection" && <Suspense fallback={null}><CollectionSheet id={overlay.id} ctx={ctx} /></Suspense>}
      {/* The old /discover/tv/<id> address redirects from here (lib/routes). */}
      <Outlet />
    </>
  );
}

const gridLoading = <div className="py-10 text-center text-base text-ink-dim">Loading…</div>;

// requestedMessage is what asking for a title says: an auto-approved request is already
// being searched for, anything else waits for someone to approve it.
// A show asked for by season names them: Requested “Severance” S2 — waiting for approval.
function requestedMessage(title: string, status: ReqStatus, seasons?: number[]): string {
  const what = `Requested “${title}”${seasons?.length ? ` ${formatSeasons(seasons)}` : ""}`;
  return status === "approved" ? `${what} — searching now` : `${what} — waiting for approval`;
}
