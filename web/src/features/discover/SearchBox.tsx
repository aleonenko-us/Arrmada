import { Suspense, useEffect, useMemo, useRef, useState } from "react";
import { api, type DiscoverCard, type PersonResult } from "../../lib/api";
import { lazyPage } from "../../lib/lazyPage";
import { useOpenOverlay, useOpenTitle } from "./shared";

// The dropdown's own chunk, fetched the first time the box is focused.
const SearchDropdown = lazyPage(() => import("./SearchDropdown"), "SearchDropdown");

// ---- Instant search omnibox -------------------------------------------------

const RECENT_KEY = "arrmada.recentSearches";
function loadRecent(): string[] {
  try { const v = JSON.parse(localStorage.getItem(RECENT_KEY) || "[]"); return Array.isArray(v) ? v.filter((x) => typeof x === "string").slice(0, 6) : []; }
  catch { return []; }
}
function pushRecent(q: string): string[] {
  const next = [q, ...loadRecent().filter((x) => x.toLowerCase() !== q.toLowerCase())].slice(0, 6);
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(next)); } catch { /* ignore quota */ }
  return next;
}

export function SearchBox({ value, onChange, onSeeAll }: { value: string; onChange: (v: string) => void; onSeeAll: (q: string) => void }) {
  const [focused, setFocused] = useState(false);
  const [results, setResults] = useState<{ items: DiscoverCard[]; people?: PersonResult[] } | null>(null);
  const [loading, setLoading] = useState(false);
  const [highlight, setHighlight] = useState(-1);
  const [recent, setRecent] = useState<string[]>(loadRecent);
  const openTitle = useOpenTitle();
  const openOverlay = useOpenOverlay();
  const wrap = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const q = value.trim();

  // Debounced live lookup for the dropdown (min 2 chars) — does NOT swap the page.
  useEffect(() => {
    if (q.length < 2) { setResults(null); setLoading(false); return; }
    setLoading(true);
    let alive = true;
    const t = setTimeout(() => {
      api.discoverSearch(q)
        .then((r) => { if (alive) { setResults(r); setLoading(false); setHighlight(-1); } })
        .catch(() => { if (alive) { setResults({ items: [] }); setLoading(false); } });
    }, 250);
    return () => { alive = false; clearTimeout(t); };
  }, [q]);

  // Close the dropdown on any click outside the widget.
  useEffect(() => {
    const onDown = (e: MouseEvent) => { if (wrap.current && !wrap.current.contains(e.target as Node)) setFocused(false); };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, []);

  const movies = useMemo(() => (results?.items ?? []).filter((c) => c.media_type === "movie").slice(0, 6), [results]);
  const series = useMemo(() => (results?.items ?? []).filter((c) => c.media_type === "series").slice(0, 6), [results]);
  const people = useMemo(() => (results?.people ?? []).slice(0, 4), [results]);
  // Flat, keyboard-navigable order: movies, then series, then people. Index === total is "See all".
  const flat = useMemo(() => [...movies, ...series], [movies, series]);
  const total = flat.length + people.length;
  const showResults = q.length >= 2;
  const open = focused && (showResults || recent.length > 0);

  const commit = (query: string) => {
    const s = query.trim();
    if (!s) return;
    setRecent(pushRecent(s));
    onSeeAll(s);
    setFocused(false);
    inputRef.current?.blur();
  };
  const pick = (c: DiscoverCard) => {
    if (q) setRecent(pushRecent(q));
    setFocused(false);
    openTitle(c);
  };
  const pickPerson = (person: PersonResult) => {
    if (q) setRecent(pushRecent(q));
    setFocused(false);
    openOverlay("person", person.id);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") { setFocused(false); inputRef.current?.blur(); return; }
    if (!open) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setHighlight((h) => (showResults ? Math.min(total, h + 1) : h)); // total == See all
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setHighlight((h) => Math.max(-1, h - 1));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (showResults && highlight >= 0 && highlight < flat.length) pick(flat[highlight]);
      else if (showResults && highlight >= flat.length && highlight < total) pickPerson(people[highlight - flat.length]);
      else commit(q);
    }
  };

  return (
    <div ref={wrap} className="relative min-w-0 flex-1 sm:flex-initial">
      <svg className="pointer-events-none absolute left-2.5 top-1/2 z-10 -translate-y-1/2" width="14" height="14" viewBox="0 0 24 24" fill="none" style={{ color: "var(--ink-faint)" }}>
        <circle cx="11" cy="11" r="7" stroke="currentColor" strokeWidth="2" /><path d="M20 20l-3.5-3.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      </svg>
      <input
        ref={inputRef}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onFocus={() => setFocused(true)}
        onKeyDown={onKeyDown}
        placeholder="Search movies & TV…"
        aria-label="Search movies and TV"
        className="w-full rounded-lg py-2 pl-8 pr-7 text-[12.5px] transition-[width,box-shadow] sm:w-[210px] sm:focus:w-[300px]"
        style={{ background: "var(--panel-2)", border: `1px solid ${focused ? "var(--accent-line)" : "var(--line)"}`, color: "var(--ink)" }}
      />
      {value && (
        <button onClick={() => { onChange(""); inputRef.current?.focus(); }} className="absolute right-2 top-1/2 z-10 -translate-y-1/2 text-ink-faint hover:text-[var(--ink)]" style={{ fontSize: "13px" }} aria-label="Clear search">✕</button>
      )}

      {open && (
        <Suspense fallback={null}>
          <SearchDropdown q={q} showResults={showResults} recent={recent} loading={loading} results={results} total={total}
            movies={movies} series={series} people={people} flatLength={flat.length} highlight={highlight}
            onRecent={(rq) => { onChange(rq); inputRef.current?.focus(); }} commit={commit} pick={pick} pickPerson={pickPerson} />
        </Suspense>
      )}
    </div>
  );
}
