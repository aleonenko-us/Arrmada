import { useCallback, useContext, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import type { DiscoverCard } from "../../lib/api";
import { usePoll } from "../../lib/usePoll";
import { CardSkeleton, LoadError, MediaCard, type RowCtx } from "./shared";
import { MIN_ROW_ITEMS, RowRegistryCtx } from "./rowRegistry";
import { RailArrows, RailStrip, useRail } from "./rail";

// browseLink is a row's "See all": the browse grid for its list.
export function browseLink(list: string, media: string): string {
  return `/discover/browse?list=${list}&media=${media}`;
}

export function PosterRow({ title, load, ctx, order, excludeOwned, hideOnError, hideUntilLoaded, seeAll }: {
  title: string;
  /** "See all →": where the row's whole list opens (a browse grid, a collection). */
  seeAll?: string | (() => void);
  load: () => Promise<DiscoverCard[]>;
  ctx: RowCtx;
  /** Render position on the tab — rows dedupe against every lower order. Omit to opt out. */
  order?: number;
  /** Upcoming rows: don't invite a request for something already in the library. */
  excludeOwned?: boolean;
  /** Row quietly disappears if the endpoint isn't available (e.g. not deployed yet). */
  hideOnError?: boolean;
  /** Render nothing (no header, no skeleton) until the first load resolves — then show
      the row only if it has items. Prevents a header flashing in for a row that ends up
      empty (e.g. "Recommended for you" for a user with no history). */
  hideUntilLoaded?: boolean;
}) {
  const registry = useContext(RowRegistryCtx);
  const [items, setItems] = useState<DiscoverCard[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const rail = useRail();
  // Last raw server payload, kept so the row can re-filter without re-fetching.
  const raw = useRef<DiscoverCard[] | null>(null);
  const loadRef = useRef(load); loadRef.current = load;
  const mounted = useRef(false);

  const recompute = useCallback(() => {
    const r = raw.current;
    if (!r) return;
    let pool = r;
    if (excludeOwned) {
      const fresh = r.filter((c) => !c.has_file && !c.in_library);
      pool = fresh.length >= MIN_ROW_ITEMS ? fresh : r;
    }
    setItems(order == null ? pool : registry.claim(order, pool));
  }, [excludeOwned, order, registry]);
  const recomputeRef = useRef(recompute); recomputeRef.current = recompute;

  useEffect(() => {
    if (order == null) return;
    const off = registry.subscribe(order, () => recomputeRef.current());
    return () => { off(); registry.release(order); };
  }, [order, registry]);

  // The mounted check matters: a late answer must not re-claim cards for a row that
  // has already gone.
  const pull = (first: boolean) => loadRef.current()
    .then((r) => { if (!mounted.current) return; raw.current = r; setError(null); recomputeRef.current(); })
    .catch((e) => { if (!mounted.current || !first) return; raw.current = []; setItems([]); setError((e as Error).message); });
  useEffect(() => {
    mounted.current = true;
    pull(true);
    return () => { mounted.current = false; };
  }, []);
  // Re-pull enrichment (badges, download progress) every 30s while the tab is
  // visible; refresh failures keep the last good data.
  usePoll(() => pull(false), 30000, { immediate: false });

  if (error && hideOnError) return null;
  if (hideUntilLoaded && items === null) return null;
  if (items && items.length === 0 && !error) return null;
  return (
    <div>
      <div className="mb-2.5 flex items-center justify-between">
        {title ? <h2 className="m-0 min-w-0 text-lg font-bold">{title}</h2> : <span />}
        {items && items.length > 0 && (
          <div className="flex flex-none items-center gap-2">
            {seeAll && <SeeAll to={seeAll} label={title} />}
            <RailArrows rail={rail} />
          </div>
        )}
      </div>
      {error ? (
        <LoadError message={error} />
      ) : (
        <RailStrip rail={rail}>
          {items === null
            ? Array.from({ length: 8 }).map((_, i) => <CardSkeleton key={i} />)
            : items.map((c) => <MediaCard key={`${c.media_type}:${c.tmdb_id}`} c={c} ctx={ctx} />)}
        </RailStrip>
      )}
    </div>
  );
}

// SeeAll is a row's "See all →": a link to an address, or an action (opening a collection
// over the page goes through the overlay history).
export function SeeAll({ to, label }: { to: string | (() => void); label: string }) {
  const aria = label ? `See all: ${label}` : "See all";
  const style = { color: "var(--accent-text)" };
  return typeof to === "string"
    ? <Link to={to} aria-label={aria} className="text-sm font-semibold" style={style}>See all →</Link>
    : <button onClick={to} aria-label={aria} className="min-h-[28px] text-sm font-semibold" style={style}>See all →</button>;
}
