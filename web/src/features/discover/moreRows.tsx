import { useEffect, useRef, useState } from "react";
import { api, type CollectionRow, type DiscoverCard, type Genre, type WatchProvider } from "../../lib/api";
import { CardSkeleton, GRID, LoadError, MediaCard, useOpenOverlay, visibleCards, type RowCtx } from "./shared";
import { PosterRow, SeeAll } from "./PosterRow";

// The rows further down Discover that build themselves from a list: the collections to
// complete, new on streaming, and the genre explorer. None of them dedupes against the
// rows above, so they load as their own chunk after the page and slot in when they arrive.

// CollectionRows are "Complete the Alien Collection" and the like: one row per movie
// collection the library has started, each the released films it lacks, with "See all"
// opening the collection's page. Nothing renders until they arrive, and none when there
// are none. They sit below the deduped rows and don't claim cards from them.
export function CollectionRows({ ctx }: { ctx: RowCtx }) {
  const [rows, setRows] = useState<CollectionRow[] | null>(null);
  const openOverlay = useOpenOverlay();
  useEffect(() => {
    let alive = true;
    api.discoverCollections().then((r) => { if (alive) setRows(r); }).catch(() => { if (alive) setRows([]); });
    return () => { alive = false; };
  }, []);
  if (!rows || rows.length === 0) return null;
  return (
    <>
      {rows.map((r) => (
        <PosterRow key={r.collection_id} excludeOwned title={r.title} seeAll={() => openOverlay("collection", r.collection_id)} load={() => Promise.resolve(r.items)} ctx={ctx} />
      ))}
    </>
  );
}

// StreamingRow: "New on <service>", one strip with a chip per streaming service in the
// region (from TMDB's provider list). One request per chip, cached like every row.
export function StreamingRow({ media, switchable, ctx }: { media: "movie" | "series"; switchable?: boolean; ctx: RowCtx }) {
  const [m, setM] = useState<"movie" | "series">(media);
  const [providers, setProviders] = useState<WatchProvider[] | null>(null);
  const [active, setActive] = useState<WatchProvider | null>(null);
  useEffect(() => { setM(media); }, [media]);
  useEffect(() => {
    let alive = true;
    setProviders(null); setActive(null);
    api.discoverProviders(m).then((p) => { if (alive) { setProviders(p); setActive(p[0] ?? null); } }).catch(() => { if (alive) setProviders([]); });
    return () => { alive = false; };
  }, [m]);
  if (providers && providers.length === 0) return null;
  return (
    <div>
      <div className="mb-2.5 flex flex-wrap items-center justify-between gap-2">
        <h2 className="m-0 text-lg font-bold">New on streaming</h2>
        <div className="flex flex-wrap items-center gap-1.5">
          {switchable && (
            <div className="mr-2 flex gap-1">
              {(["movie", "series"] as const).map((k) => {
                const on = m === k;
                return (
                  <button key={k} onClick={() => setM(k)} className="rounded-full px-3 py-1 text-sm font-semibold" style={{ border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`, background: on ? "var(--accent-soft)" : "var(--panel)", color: on ? "var(--accent-text)" : "var(--ink-faint)" }}>
                    {k === "movie" ? "Movies" : "Series"}
                  </button>
                );
              })}
            </div>
          )}
          {(providers ?? []).map((p) => {
            const on = active?.id === p.id;
            return (
              <button key={p.id} onClick={() => setActive(p)} title={p.name} className="flex items-center gap-1.5 rounded-full py-1 pl-1 pr-2.5 text-sm font-semibold" style={{ border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`, background: on ? "var(--accent-soft)" : "var(--panel)", color: on ? "var(--accent-text)" : "var(--ink-faint)" }}>
                {p.logo_url ? <img src={p.logo_url} alt="" className="h-5 w-5 rounded-md" /> : null}
                <span>{p.name}</span>
              </button>
            );
          })}
        </div>
      </div>
      {active ? (
        <PosterRow key={`${m}:${active.id}`} hideOnError title="" seeAll={`/discover/browse?media=${m}&provider=${active.id}`} load={() => api.discoverProviderNew(m, active.id)} ctx={ctx} />
      ) : (
        <div className="flex gap-3 overflow-hidden pb-2">{Array.from({ length: 8 }).map((_, i) => <CardSkeleton key={i} />)}</div>
      )}
    </div>
  );
}

export function GenreExplorer({ media, switchable, ctx }: { media: "movie" | "series"; switchable?: boolean; ctx: RowCtx }) {
  // On the main Discover tab (switchable) the explorer can flip between movies and
  // series; on the per-media tabs it stays locked to that tab's media.
  const [m, setM] = useState<"movie" | "series">(media);
  const [genres, setGenres] = useState<Genre[]>([]);
  const [genresError, setGenresError] = useState<string | null>(null);
  const [active, setActive] = useState<Genre | null>(null);
  const [items, setItems] = useState<DiscoverCard[] | null>(null);
  const [itemsError, setItemsError] = useState<string | null>(null);
  // Monotonic sequence so a slow, earlier genre response can never overwrite the
  // results of a later selection.
  const seq = useRef(0);

  useEffect(() => { setM(media); }, [media]);

  useEffect(() => {
    let alive = true;
    seq.current++; // invalidate any in-flight genre-item load
    setActive(null); setItems(null); setItemsError(null); setGenresError(null);
    api.discoverGenres(m)
      .then((g) => { if (alive) setGenres(g); })
      .catch((e) => { if (alive) { setGenres([]); setGenresError((e as Error).message); } });
    return () => { alive = false; };
  }, [m]);

  const pick = (g: Genre) => {
    const my = ++seq.current;
    setActive(g); setItems(null); setItemsError(null);
    api.discoverByGenre(m, g.id)
      .then((r) => { if (seq.current === my) setItems(r); })
      .catch((e) => { if (seq.current === my) { setItems([]); setItemsError((e as Error).message); } });
  };

  if (!switchable && genres.length === 0 && !genresError) return null;
  return (
    <div>
      <div className="mb-2.5 flex items-center justify-between">
        <h2 className="m-0 text-lg font-bold">Browse by genre</h2>
        {switchable && (
          <div className="flex gap-1">
            {(["movie", "series"] as const).map((k) => {
              const on = m === k;
              return (
                <button key={k} onClick={() => setM(k)} className="rounded-full px-3 py-1 text-sm font-semibold" style={{ border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`, background: on ? "var(--accent-soft)" : "var(--panel)", color: on ? "var(--accent-text)" : "var(--ink-faint)" }}>
                  {k === "movie" ? "Movies" : "Series"}
                </button>
              );
            })}
          </div>
        )}
      </div>
      {genresError ? (
        <LoadError message={genresError} />
      ) : (
        <>
          <div className="mb-4 flex flex-wrap gap-2">
            {genres.map((g) => {
              const on = active?.id === g.id;
              return (
                <button key={g.id} onClick={() => pick(g)} className="rounded-full px-3 py-1 text-sm font-semibold" style={{ border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`, background: on ? "var(--accent-soft)" : "var(--panel)", color: on ? "var(--accent-text)" : "var(--ink-faint)" }}>
                  {g.name}
                </button>
              );
            })}
          </div>
          {active && !itemsError && items && items.length > 0 && (
            <div className="-mt-2 mb-3 flex justify-end"><SeeAll to={`/discover/browse?media=${m}&genre=${active.id}`} label={active.name} /></div>
          )}
          {active && (
            itemsError ? (
              <LoadError message={itemsError} />
            ) : (
              <div className="grid gap-x-3 gap-y-5" style={GRID}>
                {items === null
                  ? Array.from({ length: 10 }).map((_, i) => <CardSkeleton key={i} full />)
                  : visibleCards(items, ctx).map((c) => <MediaCard key={`${c.media_type}:${c.tmdb_id}`} c={c} ctx={ctx} full />)}
              </div>
            )
          )}
        </>
      )}
    </div>
  );
}
