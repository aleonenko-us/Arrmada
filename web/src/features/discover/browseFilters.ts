import type { DiscoverCard } from "../../lib/api";
import type { ReqStatus } from "./shared";

// The browse grid's filters as plain data: what each one is called, the chips for the ones
// in use (each naming the address keys that clear it), and the client-side "Hide
// requested". The grid page renders them; tests read them.

export const SORTS: { value: string; label: string; movieOnly?: boolean }[] = [
  { value: "popularity.desc", label: "Most popular" },
  { value: "vote_average.desc", label: "Highest rated" },
  { value: "primary_release_date.desc", label: "Newest" },
  { value: "revenue.desc", label: "Highest grossing", movieOnly: true },
];

export const RATINGS = ["", "5", "6", "7", "8"];
// A floor on how many people rated it, so a 10/10 from three votes doesn't top "Highest rated".
export const VOTES: [string, string][] = [["", "Any number of votes"], ["100", "100+ votes"], ["1000", "1,000+ votes"], ["10000", "10,000+ votes"]];
// Runtime presets: [label, min, max] in minutes ("" = no bound).
export const RUNTIMES: [string, string, string][] = [["Any length", "", ""], ["Under 90 min", "", "89"], ["90 min – 2 h", "90", "120"], ["Over 2 h", "121", ""]];
export const LANGUAGES: [string, string][] = [["", "Any language"], ["en", "English"], ["ja", "Japanese"], ["ko", "Korean"], ["fr", "French"], ["es", "Spanish"], ["de", "German"], ["it", "Italian"], ["hi", "Hindi"], ["zh", "Chinese"]];

// The address's filter keys the server reads, passed through to /api/v1/discover/browse.
export const FILTER_KEYS = ["list", "media", "genre", "year_from", "year_to", "rating", "votes", "runtime_min", "runtime_max", "provider", "lang", "cert", "sort"];
// Filters the page applies itself (the server never sees them).
export const LOCAL_KEYS = ["hide_requested"];

/** A filter in use, as a removable chip: its label and the address keys that clear it. */
export interface FilterChip { id: string; label: string; clear: Record<string, string | null> }

/** Names for ids the address carries (genres, streaming services). */
export interface FilterNames { genre: (id: number) => string | undefined; provider: (id: number) => string | undefined }

export function runtimeIndex(p: URLSearchParams): number {
  return RUNTIMES.findIndex(([, lo, hi]) => lo === (p.get("runtime_min") ?? "") && hi === (p.get("runtime_max") ?? ""));
}

// activeFilters is the chips for what's in use, in the order the panel shows them.
export function activeFilters(p: URLSearchParams, names: FilterNames): FilterChip[] {
  const out: FilterChip[] = [];
  const sort = p.get("sort");
  if (sort) out.push({ id: "sort", label: SORTS.find((s) => s.value === sort)?.label ?? "Sorted", clear: { sort: null } });
  const genres = (p.get("genre") ?? "").split(",").filter(Boolean).map(Number);
  for (const g of genres) {
    const rest = genres.filter((x) => x !== g).join(",");
    out.push({ id: `genre:${g}`, label: names.genre(g) ?? "Genre", clear: { genre: rest || null } });
  }
  const rating = p.get("rating");
  if (rating) out.push({ id: "rating", label: `★ ${rating}+`, clear: { rating: null } });
  const votes = p.get("votes");
  if (votes) out.push({ id: "votes", label: VOTES.find(([v]) => v === votes)?.[1] ?? `${votes}+ votes`, clear: { votes: null } });
  const rt = runtimeIndex(p);
  if (p.get("runtime_min") || p.get("runtime_max")) {
    out.push({ id: "runtime", label: rt > 0 ? RUNTIMES[rt][0] : "Length", clear: { runtime_min: null, runtime_max: null } });
  }
  const lang = p.get("lang");
  if (lang) out.push({ id: "lang", label: LANGUAGES.find(([v]) => v === lang)?.[1] ?? lang.toUpperCase(), clear: { lang: null } });
  const from = p.get("year_from"), to = p.get("year_to");
  if (from || to) out.push({ id: "years", label: from && to ? (from === to ? from : `${from}–${to}`) : from ? `From ${from}` : `Up to ${to}`, clear: { year_from: null, year_to: null } });
  const cert = p.get("cert");
  if (cert) out.push({ id: "cert", label: `Rated up to ${cert}`, clear: { cert: null } });
  const provider = Number(p.get("provider") ?? 0);
  if (provider > 0) out.push({ id: "provider", label: names.provider(provider) ?? "Streaming service", clear: { provider: null } });
  if (p.get("hide_requested") === "1") out.push({ id: "hide_requested", label: "Not requested yet", clear: { hide_requested: null } });
  return out;
}

// hideRequested drops what someone has already asked for (pending or approved), including
// what this visit asked for. Declined titles stay: they can be asked for again.
export function hideRequested(cards: DiscoverCard[], isRequested: (c: DiscoverCard) => ReqStatus | undefined): DiscoverCard[] {
  return cards.filter((c) => {
    const s = isRequested(c) ?? c.request_status;
    return s !== "pending" && s !== "approved";
  });
}
