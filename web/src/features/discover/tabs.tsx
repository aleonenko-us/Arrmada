import { Suspense, useEffect, useMemo, useState } from "react";
import { api, type DiscoverRow } from "../../lib/api";
import { lazyPage } from "../../lib/lazyPage";
import type { RowCtx } from "./shared";
import { Hero } from "./Hero";
import { MyRequestsRow, ReadyForYouRow } from "./RequestsRow";
import { createRowRegistry, RowRegistryCtx } from "./rowRegistry";
import { browseLink, PosterRow } from "./PosterRow";

// Below-the-fold rows: their own chunk, so a requester's first load is smaller.
const CollectionRows = lazyPage(() => import("./moreRows"), "CollectionRows");
const StreamingRow = lazyPage(() => import("./moreRows"), "StreamingRow");
const GenreExplorer = lazyPage(() => import("./moreRows"), "GenreExplorer");
const WatchlistRow = lazyPage(() => import("./moreRows"), "WatchlistRow");
const HiddenTitles = lazyPage(() => import("./HiddenTitles"), "HiddenTitles");

// The Upcoming row's promise: a request waits for the real release, never a cinema copy.
const UPCOMING_NOTE = "Fetched once it’s out at home.";

// The Discover tab and the Movies / Series tabs: rows in render order, each tab with its
// own de-duplication registry.

export function DiscoverTab({ ctx }: { ctx: RowCtx }) {
  // One registry per mount of the tab — switching tabs or entering/clearing a search
  // unmounts this subtree, so the "already shown" set resets with it.
  const registry = useMemo(createRowRegistry, []);
  return (
    <RowRegistryCtx.Provider value={registry}>
      <div className="flex flex-col gap-7">
        {/* Requests first, above everything: admins see everyone's (to act on), everyone
            else their own, each with how far along it is. */}
        <MyRequestsRow />
        {/* What the viewer asked for that has arrived: the payoff, with Watch on Plex. */}
        <ReadyForYouRow />
        <Hero ctx={ctx} />
        {/* What just arrived in the library, for everyone. It doesn't claim cards from the
            rows below: those are things to request, this is what's already here. */}
        <PosterRow hideUntilLoaded hideOnError title="Recently added" load={() => api.discoverRecentlyAdded()} ctx={ctx} />
        {/* Personalized to the viewer's watch history/requests. Hidden entirely (no header,
            no skeleton) when the backend returns nothing to recommend, or on error. At
            order 0 it claims the top slot so the rows below dedupe against it. */}
        <PosterRow order={0} hideUntilLoaded hideOnError title="Recommended for you" load={() => api.discoverRecommended()} ctx={ctx} />
        <BecauseRows ctx={ctx} firstOrder={1} />
        {/* The owner's Plex watchlist (only for them), with its "request automatically". */}
        <Suspense fallback={null}><WatchlistRow ctx={ctx} /></Suspense>
        {/* Films whose first digital or disc release was in the last two weeks. */}
        <PosterRow order={4} hideUntilLoaded hideOnError title="Just out at home" load={() => api.discoverRow("just_out")} ctx={ctx} />
        <PosterRow order={5} title="Trending this week" seeAll={browseLink("trending", "all")} load={() => api.discoverTrending("all")} ctx={ctx} />
        <PosterRow order={6} title="Popular movies" seeAll={browseLink("popular", "movie")} load={() => api.discoverPopular("movie")} ctx={ctx} />
        <PosterRow order={7} title="Popular series" seeAll={browseLink("popular", "series")} load={() => api.discoverPopular("series")} ctx={ctx} />
        <PosterRow order={8} hideOnError title="In cinemas now" seeAll={browseLink("now_playing", "movie")} load={() => api.discoverRow("now_playing")} ctx={ctx} />
        <Suspense fallback={null}><StreamingRow media="movie" switchable ctx={ctx} /></Suspense>
        <Suspense fallback={null}><CollectionRows ctx={ctx} /></Suspense>
        <PosterRow order={10} hideOnError title="Hidden gems" seeAll={browseLink("hidden_gems", "movie")} load={() => api.discoverRow("hidden_gems", "movie")} ctx={ctx} />
        <PosterRow order={11} excludeOwned title="Upcoming — request ahead" note={UPCOMING_NOTE} seeAll={browseLink("upcoming", "movie")} load={() => api.discoverUpcoming()} ctx={ctx} />
        <Suspense fallback={null}><GenreExplorer media="movie" switchable ctx={ctx} /></Suspense>
        <Suspense fallback={null}><HiddenTitles ctx={ctx} /></Suspense>
      </div>
    </RowRegistryCtx.Provider>
  );
}

export function BrowseTab({ media, ctx }: { media: "movie" | "series"; ctx: RowCtx }) {
  // Keyed on media so Movies and Series each dedupe against their own rows only.
  const registry = useMemo(createRowRegistry, [media]);
  return (
    <RowRegistryCtx.Provider value={registry}>
      <div key={media} className="flex flex-col gap-7">
        <PosterRow order={0} title={`Trending ${media === "movie" ? "movies" : "series"}`} seeAll={browseLink("trending", media)} load={() => api.discoverTrending(media)} ctx={ctx} />
        <PosterRow order={1} title={`Popular ${media === "movie" ? "movies" : "series"}`} seeAll={browseLink("popular", media)} load={() => api.discoverPopular(media)} ctx={ctx} />
        <PosterRow order={2} hideOnError title={`Top rated ${media === "movie" ? "movies" : "series"}`} seeAll={browseLink("top_rated", media)} load={() => api.discoverRow("top_rated", media)} ctx={ctx} />
        {media === "movie" ? (
          <PosterRow order={3} hideOnError title="In cinemas now" seeAll={browseLink("now_playing", "movie")} load={() => api.discoverRow("now_playing")} ctx={ctx} />
        ) : (
          <PosterRow order={3} hideOnError title="Anime" load={() => api.discoverRow("anime")} ctx={ctx} />
        )}
        <Suspense fallback={null}><StreamingRow media={media} ctx={ctx} /></Suspense>
        <PosterRow order={4} hideOnError title="Hidden gems" seeAll={browseLink("hidden_gems", media)} load={() => api.discoverRow("hidden_gems", media)} ctx={ctx} />
        <PosterRow order={5} hideUntilLoaded hideOnError title="From your region" load={() => api.discoverRow("region", media)} ctx={ctx} />
        {media === "movie" ? (
          <PosterRow order={6} excludeOwned title="Upcoming — request ahead" note={UPCOMING_NOTE} seeAll={browseLink("upcoming", "movie")} load={() => api.discoverUpcoming()} ctx={ctx} />
        ) : (
          // Series upcoming ships behind a backend change; if it isn't live the row
          // hides itself rather than showing an error on an otherwise healthy tab.
          <PosterRow order={6} excludeOwned hideOnError title="Airing soon" seeAll={browseLink("upcoming", "series")} load={() => api.discoverUpcoming("series")} ctx={ctx} />
        )}
        <Suspense fallback={null}><GenreExplorer media={media} ctx={ctx} /></Suspense>
      </div>
    </RowRegistryCtx.Provider>
  );
}

// BecauseRows are the per-title strips ("Because you watched Silo"): the viewer's three
// most recent titles, each with its own recommendations. Nothing renders until the
// rows arrive, and none when there's no history to build them from.
function BecauseRows({ ctx, firstOrder }: { ctx: RowCtx; firstOrder: number }) {
  const [rows, setRows] = useState<DiscoverRow[] | null>(null);
  useEffect(() => {
    let alive = true;
    api.discoverBecause().then((r) => { if (alive) setRows(r); }).catch(() => { if (alive) setRows([]); });
    return () => { alive = false; };
  }, []);
  if (!rows || rows.length === 0) return null;
  return (
    <>
      {rows.map((r, i) => (
        <PosterRow key={r.seed} order={firstOrder + i} title={r.title} load={() => Promise.resolve(r.items)} ctx={ctx} />
      ))}
    </>
  );
}
