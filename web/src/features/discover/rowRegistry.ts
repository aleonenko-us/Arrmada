import { createContext } from "react";
import type { DiscoverCard } from "../../lib/api";

// ---- Cross-row de-duplication ----------------------------------------------
//
// TMDB's trending/popular/upcoming lists overlap heavily, so the same four titles
// used to fill every row on a tab. Each row is given an `order` (its render
// position); a row drops anything already claimed by a row ABOVE it and claims
// what it keeps.
//
// Why this can't loop: claims only ever propagate DOWNWARD. `claim(order, …)`
// notifies subscribers with a strictly greater order, so the notification chain
// is a DAG over a finite, strictly increasing ordinal — it terminates after at
// most one pass per row. A row's own state update never re-runs its own load or
// subscribe effects (their deps are `[order, registry]`, both stable for the life
// of the tab), so a re-filter can't retrigger a fetch or a sibling's effect. The
// notify is additionally skipped when a row's claim set is unchanged, so the 30s
// enrichment refresh normally causes no downstream work at all.
export const MIN_ROW_ITEMS = 4; // below this, a repeated title beats a dead row
const cardKey = (c: DiscoverCard) => `${c.media_type}:${c.tmdb_id}`;

export interface RowRegistry {
  claim: (order: number, items: DiscoverCard[]) => DiscoverCard[];
  subscribe: (order: number, fn: () => void) => () => void;
  release: (order: number) => void;
}

export function createRowRegistry(): RowRegistry {
  const claims = new Map<number, Set<string>>();
  const subs = new Map<number, () => void>();
  return {
    claim(order, items) {
      const taken = new Set<string>();
      claims.forEach((keys, o) => { if (o < order) keys.forEach((k) => taken.add(k)); });
      const filtered = items.filter((c) => !taken.has(cardKey(c)));
      const kept = filtered.length >= MIN_ROW_ITEMS ? filtered : items;
      const next = new Set(kept.map(cardKey));
      const prev = claims.get(order);
      const changed = !prev || prev.size !== next.size || [...next].some((k) => !prev.has(k));
      claims.set(order, next);
      if (changed) subs.forEach((fn, o) => { if (o > order) fn(); });
      return kept;
    },
    subscribe(order, fn) { subs.set(order, fn); return () => { subs.delete(order); }; },
    release(order) { claims.delete(order); subs.delete(order); },
  };
}

// Default is a no-op registry so a PosterRow rendered outside a provider (or with
// no `order`) behaves exactly as before.
export const RowRegistryCtx = createContext<RowRegistry>({ claim: (_o, items) => items, subscribe: () => () => {}, release: () => {} });
