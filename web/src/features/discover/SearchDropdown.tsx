import type { DiscoverCard, PersonResult } from "../../lib/api";
import { posterThumb } from "../../lib/img";
import { StatusChip } from "../../ui";
import { badgeFor } from "./shared";

// SearchDropdown is the omnibox's panel: recent searches, then quick matches (movies,
// series, people) and "See all". It loads the first time the box takes focus, so a
// requester's first load carries only the box itself.
export interface SearchDropdownProps {
  q: string;
  showResults: boolean;
  recent: string[];
  loading: boolean;
  results: unknown;
  total: number;
  movies: DiscoverCard[];
  series: DiscoverCard[];
  people: PersonResult[];
  flatLength: number;
  highlight: number;
  onRecent: (q: string) => void;
  commit: (q: string) => void;
  pick: (c: DiscoverCard) => void;
  pickPerson: (p: PersonResult) => void;
}

export function SearchDropdown({ q, showResults, recent, loading, results, total, movies, series, people, flatLength, highlight, onRecent, commit, pick, pickPerson }: SearchDropdownProps) {
  return (
    /* Phone: pinned to the viewport edges (fixed, auto top), the same fix as the
       notification bell, since a 340px panel right-anchored to the box ran off the left
       edge. sm+ keeps the box-anchored dropdown. */
    <div
      className="thin-scroll fixed inset-x-3 z-50 mt-2 max-h-[70vh] overflow-y-auto rounded-xl py-1.5 sm:absolute sm:inset-x-auto sm:right-0 sm:w-[340px]"
      style={{ background: "var(--panel)", border: "1px solid var(--line)", boxShadow: "0 16px 40px rgba(0,0,0,.45)" }}
    >
      {!showResults ? (
        <div className="px-3 py-2">
          <div className="mb-2 font-mono text-[9.5px] uppercase tracking-wide" style={{ color: "var(--ink-faint)" }}>Recent searches</div>
          <div className="flex flex-wrap gap-1.5">
            {recent.map((rq) => (
              <button
                key={rq}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => onRecent(rq)}
                className="rounded-full px-2.5 py-1 text-[11.5px] font-medium"
                style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink-dim)" }}
              >
                {rq}
              </button>
            ))}
          </div>
        </div>
      ) : loading && !results ? (
        <div className="px-3 py-6 text-center text-[12px]" style={{ color: "var(--ink-faint)" }}>Searching…</div>
      ) : total === 0 ? (
        <button onMouseDown={(e) => e.preventDefault()} onClick={() => commit(q)} className="block w-full px-3 py-6 text-center text-[12px]" style={{ color: "var(--ink-faint)" }}>
          No quick matches — see all results for “{q}”
        </button>
      ) : (
        <>
          {movies.length > 0 && <SearchGroup label="Movies" items={movies} flatBase={0} highlight={highlight} onPick={pick} />}
          {series.length > 0 && <SearchGroup label="Series" items={series} flatBase={movies.length} highlight={highlight} onPick={pick} />}
          {people.length > 0 && <PeopleGroup people={people} flatBase={flatLength} highlight={highlight} onPick={pickPerson} />}
          <button
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => commit(q)}
            className="mt-1 flex w-full items-center gap-2 border-t px-3 py-2.5 text-left text-[12px] font-semibold"
            style={{ borderColor: "var(--line)", background: highlight === total ? "var(--accent-soft)" : "transparent", color: "var(--accent)" }}
          >
            See all results for “{q}”
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" className="ml-auto"><path d="M9 6l6 6-6 6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>
          </button>
        </>
      )}
    </div>
  );
}

function SearchGroup({ label, items, flatBase, highlight, onPick }: { label: string; items: DiscoverCard[]; flatBase: number; highlight: number; onPick: (c: DiscoverCard) => void }) {
  return (
    <div className="py-1">
      <div className="px-3 py-1 font-mono text-[9.5px] uppercase tracking-wide" style={{ color: "var(--ink-faint)" }}>{label}</div>
      {items.map((c, i) => {
        const idx = flatBase + i;
        const on = highlight === idx;
        const badge = badgeFor(c);
        return (
          <button
            key={`${c.media_type}:${c.tmdb_id}`}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => onPick(c)}
            className="flex w-full items-center gap-2.5 px-2.5 py-1.5 text-left"
            style={{ background: on ? "var(--accent-soft)" : "transparent" }}
          >
            <div className="h-[54px] w-[36px] flex-none overflow-hidden rounded" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
              {c.poster_url ? <img src={posterThumb(c.poster_url)} alt="" className="h-full w-full object-cover" loading="lazy" /> : (
                <div className="grid h-full w-full place-items-center" style={{ color: "var(--ink-faint)" }}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none"><rect x="3" y="4" width="18" height="16" rx="2" stroke="currentColor" strokeWidth="1.6" /></svg>
                </div>
              )}
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-[12.5px] font-semibold" style={{ color: "var(--ink)" }}>{c.title}</div>
              <div className="flex items-center gap-1.5 text-[10.5px]" style={{ color: "var(--ink-faint)" }}>
                <span>{c.year || "—"}</span>
                <span>·</span>
                <span>{c.media_type === "series" ? "TV" : "Movie"}</span>
                {c.vote_average > 0 && <span style={{ color: "var(--accent)" }}>★ {c.vote_average.toFixed(1)}</span>}
              </div>
            </div>
            {badge && <StatusChip tone={badge.tone} surface="poster" size="xs" className="flex-none">{badge.label}</StatusChip>}
          </button>
        );
      })}
    </div>
  );
}

// PeopleGroup is the dropdown's people: photo, name, and what they're known for.
function PeopleGroup({ people, flatBase, highlight, onPick }: { people: PersonResult[]; flatBase: number; highlight: number; onPick: (p: PersonResult) => void }) {
  return (
    <div className="py-1">
      <div className="px-3 py-1 font-mono text-[9.5px] uppercase tracking-wide" style={{ color: "var(--ink-faint)" }}>People</div>
      {people.map((p, i) => (
        <button
          key={p.id}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => onPick(p)}
          className="flex w-full items-center gap-2.5 px-2.5 py-1.5 text-left"
          style={{ background: highlight === flatBase + i ? "var(--accent-soft)" : "transparent" }}
        >
          <span className="h-[40px] w-[40px] flex-none overflow-hidden rounded-full" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
            {p.profile_url ? <img src={p.profile_url} alt="" className="h-full w-full object-cover" loading="lazy" /> : null}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-[12.5px] font-semibold" style={{ color: "var(--ink)" }}>{p.name}</span>
            <span className="block truncate text-[10.5px]" style={{ color: "var(--ink-faint)" }}>{[p.known_for_department, ...(p.known_for ?? [])].filter(Boolean).join(" · ")}</span>
          </span>
        </button>
      ))}
    </div>
  );
}
