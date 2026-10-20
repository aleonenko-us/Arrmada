import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { api, type DiscoverCard, type DiscoverPage, type Genre, type PersonResult, type WatchProvider } from "../lib/api";
import { CardSkeleton, GRID, LoadError, MediaCard, useOpenOverlay, visibleCards, type RowCtx } from "../features/discover/shared";
import { activeFilters, FILTER_KEYS, hideRequested, LANGUAGES, LOCAL_KEYS, RATINGS, runtimeIndex, RUNTIMES, SORTS, VOTES } from "../features/discover/browseFilters";
import { discoverApi, type Certification } from "../features/discover/discoverApi";

// The deeper Discover grids (REQ-19): a filterable browse grid behind every row's
// "See all" (/discover/browse?list=popular&media=movie), and search results past the first
// page. Both load one page of 20 at a time as the end of the grid scrolls into view; the
// server whitelists every filter, so the address is the whole state and a reload or a
// shared link reproduces the grid. Its own chunk: Discover's first load doesn't carry it.

// The fixed lists a row's "See all" opens, and what each is called.
const LISTS: Record<string, { movie: string; series: string }> = {
  trending: { movie: "Trending movies", series: "Trending series" },
  popular: { movie: "Popular movies", series: "Popular series" },
  top_rated: { movie: "Top rated movies", series: "Top rated series" },
  now_playing: { movie: "In cinemas now", series: "In cinemas now" },
  upcoming: { movie: "Upcoming — request ahead", series: "Airing soon" },
  hidden_gems: { movie: "Hidden gems", series: "Hidden gems" },
};

const cardKey = (c: DiscoverCard) => `${c.media_type}:${c.tmdb_id}`;

// useInfinite pages through a list: page 1 when key changes, then the next page each time
// the sentinel at the end of the grid comes near the screen. Nothing is fetched ahead.
function useInfinite<P extends DiscoverPage>(key: string, fetchPage: (page: number) => Promise<P>, onFirst?: (p: P) => void) {
  const [items, setItems] = useState<DiscoverCard[] | null>(null);
  const [page, setPage] = useState(0);
  const [total, setTotal] = useState(1);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const fetchRef = useRef(fetchPage); fetchRef.current = fetchPage;
  const firstRef = useRef(onFirst); firstRef.current = onFirst;
  // Bumped on every new key, so an answer for the old one is dropped.
  const gen = useRef(0);
  const sentinel = useRef<HTMLDivElement>(null);
  const shown = useRef<DiscoverCard[]>([]);
  // Pages in a row that added nothing (everything on them filtered out or already shown).
  // After a few the grid stops loading by itself and offers a button, so a filter that
  // TMDB pads with unshowable rows can't walk all 500 pages unattended.
  const [dry, setDry] = useState(0);

  const load = useCallback((p: number, manual = false) => {
    const my = gen.current;
    setLoading(true); setError(null);
    fetchRef.current(p).then((r) => {
      if (gen.current !== my) return;
      // TMDB's pages shift as popularity moves, so a title can turn up twice: keep the first.
      const base = p === 1 ? [] : shown.current;
      const seen = new Set(base.map(cardKey));
      const fresh = r.items.filter((c) => !seen.has(cardKey(c)));
      shown.current = [...base, ...fresh];
      setItems(shown.current);
      setDry((d) => (fresh.length > 0 || manual ? 0 : d + 1));
      setPage(p); setTotal(r.total_pages); setLoading(false);
      if (p === 1) firstRef.current?.(r);
    }).catch((e) => {
      if (gen.current !== my) return;
      setLoading(false); setError((e as Error).message);
      if (p === 1) { shown.current = []; setItems([]); }
    });
  }, []);

  useEffect(() => {
    gen.current++;
    shown.current = [];
    setItems(null); setPage(0); setTotal(1); setDry(0);
    load(1);
  }, [key, load]);

  const stalled = dry >= 3;
  const more = page > 0 && page < total && !loading && !error && !stalled;
  useEffect(() => {
    const el = sentinel.current;
    if (!el || !more || typeof IntersectionObserver === "undefined") return;
    // The page scrolls inside the layout's <main>, not the window: watching from the
    // scroller itself lets the margin reach below its visible edge.
    const io = new IntersectionObserver((es) => { if (es.some((e) => e.isIntersecting)) load(page + 1); }, { root: scrollParent(el), rootMargin: "0px 0px 400px 0px" });
    io.observe(el);
    return () => io.disconnect();
  }, [more, page, load]);

  return { items, loading, error, sentinel, retry: () => load(Math.max(1, page + 1), true), hasMore: page < total, stalled };
}

// scrollParent is the nearest ancestor that scrolls vertically, or null for the window.
function scrollParent(el: HTMLElement): HTMLElement | null {
  for (let n = el.parentElement; n; n = n.parentElement) {
    const oy = getComputedStyle(n).overflowY;
    if (oy === "auto" || oy === "scroll") return n;
  }
  return null;
}

// Grid is the infinite poster grid: skeletons first, the error line on a failure (with a
// retry for a later page), and the sentinel that loads the next page.
function Grid({ state, ctx, empty, skipRequested }: { state: ReturnType<typeof useInfinite>; ctx: RowCtx; empty: string; skipRequested?: boolean }) {
  const { items, loading, error, sentinel, retry, hasMore, stalled } = state;
  const shown = items ? visibleCards(items, ctx) : null;
  const visible = shown && skipRequested ? hideRequested(shown, ctx.isRequested) : shown;
  const thinned = ctx.hideInLibrary || skipRequested;
  if (items === null) {
    return <div className="grid gap-x-3 gap-y-5" style={GRID}>{Array.from({ length: 12 }).map((_, i) => <CardSkeleton key={i} full />)}</div>;
  }
  if (visible?.length === 0) {
    return error ? <LoadError message={error} /> : (
      <div className="rounded-xl p-8 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>
        {hasMore && thinned ? "Nothing on this page is left once those are hidden." : empty}
        {hasMore && thinned && <div><button onClick={retry} className="mt-3 min-h-[32px] font-semibold" style={{ color: "var(--accent-text)" }}>Load more</button></div>}
      </div>
    );
  }
  return (
    <>
      <div className="grid gap-x-3 gap-y-5" style={GRID}>
        {visible!.map((c) => <MediaCard key={cardKey(c)} c={c} ctx={ctx} full />)}
        {loading && Array.from({ length: 6 }).map((_, i) => <CardSkeleton key={`s${i}`} full />)}
      </div>
      {error && (
        <div className="mt-4 flex flex-wrap items-center gap-2">
          <LoadError message={error} />
          <button onClick={retry} className="min-h-[32px] text-sm font-semibold" style={{ color: "var(--accent-text)" }}>Try again</button>
        </div>
      )}
      {stalled && hasMore && !loading && !error && (
        <div className="mt-4 text-center"><button onClick={retry} className="min-h-[32px] text-sm font-semibold" style={{ color: "var(--accent-text)" }}>Load more</button></div>
      )}
      {!hasMore && items.length > 20 && <div className="mt-6 text-center text-sm text-ink-faint">That’s everything.</div>}
      <div ref={sentinel} aria-hidden className="h-px" />
    </>
  );
}

// SearchResults is a committed search (?q=), every page of it.
// The people on its first page come first, as a strip.
export function SearchResults({ query, ctx }: { query: string; ctx: RowCtx }) {
  const [people, setPeople] = useState<PersonResult[]>([]);
  const openOverlay = useOpenOverlay();
  useEffect(() => { setPeople([]); }, [query]);
  const state = useInfinite(query, (page) => api.discoverSearchPage(query, page), (p) => setPeople(p.people ?? []));
  return (
    <div>
      <h2 className="m-0 mb-3 text-lg font-bold">Results for “{query}”</h2>
      {people.length > 0 && (
        <div className="mb-5">
          <h3 className="m-0 mb-2 text-sm font-bold uppercase tracking-wide text-ink-faint">People</h3>
          <div className="thin-scroll flex gap-3 overflow-x-auto pb-1">
            {people.map((p) => (
              <button key={p.id} onClick={() => openOverlay("person", p.id)} aria-label={`Open ${p.name}`} className="w-[96px] flex-none text-center">
                <span className="mx-auto mb-1 block h-[84px] w-[84px] overflow-hidden rounded-full" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
                  {p.profile_url ? <img src={p.profile_url} alt="" className="h-full w-full object-cover" loading="lazy" /> : null}
                </span>
                <span className="line-clamp-2 block text-xs font-semibold leading-tight">{p.name}</span>
                {p.known_for_department && <span className="mt-0.5 block text-xs text-ink-faint">{p.known_for_department}</span>}
              </button>
            ))}
          </div>
        </div>
      )}
      <Grid state={state} ctx={ctx} empty={people.length > 0 ? `No movies or shows match “${query}”.` : `No movies, shows or people match “${query}”.`} />
    </div>
  );
}

const chip = (on: boolean) => ({ border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`, background: on ? "var(--accent-soft)" : "var(--panel)", color: on ? "var(--accent-text)" : "var(--ink-faint)" });
const field = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };
const selectCls = "min-h-[36px] w-full min-w-0 rounded-lg px-2 text-sm sm:w-auto";

// DiscoverBrowse is /discover/browse: a filter panel over an infinite grid, all its state
// in the address. The filters in use show as chips that each take one off; on a phone the
// panel folds away behind "Filters" so the grid starts near the top.
export function DiscoverBrowse({ ctx }: { ctx: RowCtx }) {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const location = useLocation();
  const list = params.get("list") ?? "";
  const media = params.get("media") === "series" ? "series" : params.get("media") === "all" && list === "trending" ? "all" : "movie";
  const genres = (params.get("genre") ?? "").split(",").filter(Boolean).map(Number);
  const provider = Number(params.get("provider") ?? 0);
  const sort = params.get("sort") ?? "";
  const skipRequested = params.get("hide_requested") === "1";
  const [panelOpen, setPanelOpen] = useState(false);

  const [genreList, setGenreList] = useState<Genre[]>([]);
  const [providers, setProviders] = useState<WatchProvider[]>([]);
  const [certs, setCerts] = useState<Certification[]>([]);
  useEffect(() => {
    if (media === "all") { setGenreList([]); setProviders([]); setCerts([]); return; }
    let alive = true;
    api.discoverGenres(media).then((g) => { if (alive) setGenreList(g); }).catch(() => { if (alive) setGenreList([]); });
    api.discoverProviders(media).then((p) => { if (alive) setProviders(p); }).catch(() => { if (alive) setProviders([]); });
    discoverApi.certifications(media).then((c) => { if (alive) setCerts(c.certifications ?? []); }).catch(() => { if (alive) setCerts([]); });
    return () => { alive = false; };
  }, [media]);

  // The query the server sees: only the filter keys, in a stable order (the grid's key).
  const qs = new URLSearchParams();
  for (const k of FILTER_KEYS) { const v = params.get(k); if (v) qs.set(k, v); }
  const key = qs.toString();
  const state = useInfinite(key, (page) => api.discoverBrowse(key, page));

  // Any filter turns a fixed list into a filtered browse of the same media. Replacing the
  // entry keeps Back for leaving the grid, not undoing each filter. "Hide requested" is
  // the page's own: it narrows any list without leaving it.
  const set = (changes: Record<string, string | null>) => setParams((p) => {
    const next = new URLSearchParams(p);
    if (Object.keys(changes).some((k) => !LOCAL_KEYS.includes(k))) next.delete("list");
    for (const [k, v] of Object.entries(changes)) { if (v) next.set(k, v); else next.delete(k); }
    if (!next.get("media") || (next.get("media") === "all" && !next.get("list"))) next.set("media", "movie");
    return next;
  }, { replace: true });
  // Switching media keeps a fixed list (Popular movies → Popular series); genre ids and
  // age ratings differ between movies and series, so those are dropped, and series have
  // no box office.
  const setMedia = (m: "movie" | "series") => setParams((p) => {
    const next = new URLSearchParams(p);
    next.set("media", m);
    next.delete("genre");
    next.delete("cert");
    if (m === "series" && next.get("sort") === "revenue.desc") next.delete("sort");
    return next;
  }, { replace: true });
  const toggleGenre = (id: number) => {
    const next = genres.includes(id) ? genres.filter((g) => g !== id) : [...genres, id];
    set({ genre: next.join(",") || null });
  };
  const runtime = runtimeIndex(params);
  const chips = activeFilters(params, {
    genre: (id) => genreList.find((g) => g.id === id)?.name,
    provider: (id) => providers.find((p) => p.id === id)?.name,
  });
  const clearAll = () => setParams((p) => {
    const next = new URLSearchParams();
    next.set("media", media === "all" ? "movie" : media);
    if (p.get("hide_in_library")) next.set("hide_in_library", "1");
    return next;
  }, { replace: true });
  const providerName = providers.find((p) => p.id === provider)?.name;
  const noun = media === "series" ? "series" : media === "all" ? "titles" : "movies";
  const title = list && LISTS[list] ? (media === "all" ? "Trending this week" : LISTS[list][media]) : providerName ? `${providerName} ${noun}` : `Browse ${noun}`;
  // Back leaves the grid for wherever it was opened from; a grid opened cold goes to Discover.
  const back = () => (location.key !== "default" ? navigate(-1) : navigate("/discover", { replace: true }));
  const panelId = "discover-filters";

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-2">
        <button onClick={back} className="min-h-[32px] text-sm font-semibold" style={{ color: "var(--accent-text)" }}>← Discover</button>
        <h2 className="m-0 min-w-0 flex-1 text-xl font-bold">{title}</h2>
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-2">
        {(["movie", "series"] as const).map((k) => (
          <button key={k} onClick={() => setMedia(k)} aria-pressed={media === k} className="min-h-[36px] rounded-full px-3 text-sm font-semibold" style={chip(media === k)}>
            {k === "movie" ? "Movies" : "Series"}
          </button>
        ))}
        {/* On a phone the panel folds away; from sm up it's always open. */}
        <button onClick={() => setPanelOpen((o) => !o)} aria-expanded={panelOpen} aria-controls={panelId} className="ml-auto inline-flex min-h-[36px] items-center gap-1.5 rounded-full px-3 text-sm font-semibold sm:hidden" style={chip(panelOpen)}>
          Filters{chips.length > 0 ? ` (${chips.length})` : ""}
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true" style={{ transform: panelOpen ? "rotate(180deg)" : undefined }}><path d="M6 9l6 6 6-6" /></svg>
        </button>
      </div>

      <div id={panelId} className={`mb-3 min-w-0 flex-col gap-2.5 rounded-xl p-3 sm:flex ${panelOpen ? "flex" : "hidden"}`} style={{ background: "var(--panel)", border: "1px solid var(--line)" }} role="group" aria-label="Filters">
        <div className="grid grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:items-center">
          <select aria-label="Sort by" value={sort} onChange={(e) => set({ sort: e.target.value || null })} className={selectCls} style={field}>
            <option value="">Sort: default</option>
            {SORTS.filter((s) => !s.movieOnly || media !== "series").map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
          </select>
          <select aria-label="Minimum rating" value={params.get("rating") ?? ""} onChange={(e) => set({ rating: e.target.value || null })} className={selectCls} style={field}>
            {RATINGS.map((r) => <option key={r} value={r}>{r ? `★ ${r}+` : "Any rating"}</option>)}
          </select>
          <select aria-label="Minimum number of votes" value={params.get("votes") ?? ""} onChange={(e) => set({ votes: e.target.value || null })} className={selectCls} style={field}>
            {VOTES.map(([v, label]) => <option key={v} value={v}>{label}</option>)}
          </select>
          <select aria-label="Length" value={Math.max(0, runtime)} onChange={(e) => { const [, lo, hi] = RUNTIMES[Number(e.target.value)]; set({ runtime_min: lo || null, runtime_max: hi || null }); }} className={selectCls} style={field}>
            {RUNTIMES.map(([label], i) => <option key={label} value={i}>{label}</option>)}
          </select>
          <select aria-label="Original language" value={params.get("lang") ?? ""} onChange={(e) => set({ lang: e.target.value || null })} className={selectCls} style={field}>
            {LANGUAGES.map(([v, label]) => <option key={v} value={v}>{label}</option>)}
          </select>
          {certs.length > 0 && (
            <select aria-label="Age rating" value={params.get("cert") ?? ""} onChange={(e) => set({ cert: e.target.value || null })} className={selectCls} style={field}>
              <option value="">Any age rating</option>
              {certs.map((c) => <option key={c.code} value={c.code} title={c.meaning}>Up to {c.code}</option>)}
            </select>
          )}
          <YearRange from={params.get("year_from") ?? ""} to={params.get("year_to") ?? ""} onChange={(from, to) => set({ year_from: from || null, year_to: to || null })} />
          <label className="col-span-2 flex min-h-[36px] items-center gap-2 text-sm font-medium text-ink-dim sm:col-span-1">
            <input type="checkbox" checked={skipRequested} onChange={(e) => set({ hide_requested: e.target.checked ? "1" : null })} style={{ accentColor: "var(--accent)" }} />
            Hide requested
          </label>
        </div>
        {genreList.length > 0 && (
          <div className="thin-scroll -mx-1 flex gap-1.5 overflow-x-auto px-1 pb-1" role="group" aria-label="Genres">
            {genreList.map((g) => (
              <button key={g.id} onClick={() => toggleGenre(g.id)} aria-pressed={genres.includes(g.id)} className="min-h-[32px] flex-none rounded-full px-3 text-sm font-semibold" style={chip(genres.includes(g.id))}>{g.name}</button>
            ))}
          </div>
        )}
        {providers.length > 0 && (
          <div className="thin-scroll -mx-1 flex gap-1.5 overflow-x-auto px-1 pb-1" role="group" aria-label="Streaming services">
            {providers.map((p) => (
              <button key={p.id} onClick={() => set({ provider: provider === p.id ? null : String(p.id) })} aria-pressed={provider === p.id} title={p.name} className="flex min-h-[32px] flex-none items-center gap-1.5 rounded-full py-0.5 pl-1 pr-2.5 text-sm font-semibold" style={chip(provider === p.id)}>
                {p.logo_url ? <img src={p.logo_url} alt="" className="h-5 w-5 rounded-md" /> : null}
                <span>{p.name}</span>
              </button>
            ))}
          </div>
        )}
      </div>

      {chips.length > 0 && (
        <div className="mb-4 flex flex-wrap items-center gap-1.5" role="group" aria-label="Filters in use">
          {chips.map((c) => (
            <button key={c.id} onClick={() => set(c.clear)} aria-label={`Remove filter: ${c.label}`} className="inline-flex min-h-[32px] max-w-full items-center gap-1.5 rounded-full px-3 text-sm font-semibold" style={chip(true)}>
              <span className="truncate">{c.label}</span>
              <svg width="10" height="10" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8" /></svg>
            </button>
          ))}
          <button onClick={clearAll} className="min-h-[32px] px-1 text-sm font-semibold" style={{ color: "var(--accent-text)" }}>Clear filters</button>
        </div>
      )}

      <Grid state={state} ctx={ctx} skipRequested={skipRequested} empty="Nothing matches these filters." />
    </div>
  );
}

// YearRange is two year boxes, applied when one is left or Enter is pressed (not on every
// keystroke, which would fetch a grid per digit).
function YearRange({ from, to, onChange }: { from: string; to: string; onChange: (from: string, to: string) => void }) {
  const [f, setF] = useState(from);
  const [t, setT] = useState(to);
  // Back, Forward or Clear changed the address: show it. Each box follows only its own
  // value, so applying one never wipes what's being typed in the other.
  useEffect(() => { setF(from); }, [from]);
  useEffect(() => { setT(to); }, [to]);
  const clean = (v: string) => (/^\d{4}$/.test(v.trim()) ? v.trim() : "");
  const apply = () => { if (clean(f) !== from || clean(t) !== to) onChange(clean(f), clean(t)); };
  const box = (label: string, v: string, setV: (v: string) => void) => (
    <input
      aria-label={label}
      inputMode="numeric"
      placeholder={label === "From year" ? "From" : "To"}
      value={v}
      maxLength={4}
      onChange={(e) => setV(e.target.value.replace(/\D/g, ""))}
      onBlur={apply}
      onKeyDown={(e) => { if (e.key === "Enter") apply(); }}
      className="min-h-[36px] w-0 min-w-0 flex-1 rounded-lg px-2 text-sm sm:w-[64px] sm:flex-none"
      style={field}
    />
  );
  return (
    <span className="col-span-2 flex min-w-0 items-center gap-1 text-sm text-ink-faint sm:col-span-1">
      {box("From year", f, setF)}–{box("To year", t, setT)}
    </span>
  );
}
