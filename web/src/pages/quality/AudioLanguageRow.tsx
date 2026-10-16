import { useId, useState, type KeyboardEvent, type ReactNode } from "react";
import type { TargetPref } from "../../lib/api";
import { langName, searchLanguages, useLanguages } from "../../lib/languages";

// The target's "Audio language" row: languages a file should carry, each Must / Prefer /
// Avoid like the codec and HDR options, plus "Original language" — the title's own (its
// TMDB original language), so "Original + German" is one profile for every title. Mirrors
// quality/lang.go. Setting a language back to "—" takes it off the list.

export const ORIGINAL = "original";

/** languageKeys orders the row: Original language first, then by name. */
export function languageKeys(row: Record<string, TargetPref> | undefined, name: (code: string) => string): string[] {
  return Object.keys(row ?? {})
    .filter((k) => row?.[k])
    .sort((a, b) => (a === ORIGINAL ? -1 : b === ORIGINAL ? 1 : name(a).localeCompare(name(b))));
}

/** setLanguage is the row with key set to pref ("" removes it). */
export function setLanguage(row: Record<string, TargetPref> | undefined, key: string, pref: TargetPref): Record<string, TargetPref> {
  const next = { ...(row ?? {}) };
  if (pref) next[key] = pref;
  else delete next[key];
  return next;
}

export function AudioLanguageRow({ value, onChange, picker }: {
  value: Record<string, TargetPref> | undefined;
  onChange: (next: Record<string, TargetPref>) => void;
  /** The target's Must / Prefer / — / Avoid control, so the row looks like the others. */
  picker: (value: TargetPref, onChange: (p: TargetPref) => void, label: string) => ReactNode;
}) {
  const rows = useLanguages();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const listId = useId();
  const name = (k: string) => (k === ORIGINAL ? "Original language" : langName(k, rows));
  const keys = languageKeys(value, name);
  // Base languages only: release names and track tags say "German", not a region of it.
  const matches = rows && query.trim() ? searchLanguages(rows.filter((l) => !l.regional), query, keys) : [];
  // A newly added language starts as Prefer, the mildest choice that does anything.
  const add = (key: string) => {
    if (!value?.[key]) onChange(setLanguage(value, key, "want"));
    setQuery("");
    setActive(0);
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" && matches.length) { e.preventDefault(); setActive((a) => (a + 1) % matches.length); }
    else if (e.key === "ArrowUp" && matches.length) { e.preventDefault(); setActive((a) => (a - 1 + matches.length) % matches.length); }
    else if (e.key === "Enter" && matches[active]) { e.preventDefault(); add(matches[active].code); }
    else if (e.key === "Escape") { setQuery(""); setActive(0); }
  };

  return (
    <div className="mb-4" data-testid="audio-language-row">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-sm font-semibold">Audio language</span>
      </div>
      <div className="mb-1.5 text-xs leading-[1.5] text-ink-faint">
        Languages are read from a release's name: one that names none is taken to be the original language, and MULTi or
        DL means the original plus the ones it names. Files in your library are judged by their actual audio tracks, and a
        file whose audio hasn't been read yet is left alone. Each Must is needed; Avoid only counts against a release whose
        every language is avoided.
      </div>
      <div className="flex flex-col gap-1.5">
        {keys.map((k) => (
          <div key={k} data-testid="audio-language" data-lang={k} className="flex flex-wrap items-center justify-between gap-2 rounded-lg py-1 pl-2.5 pr-1" style={{ background: "var(--panel-2)" }}>
            <span className="text-base">
              {name(k)}
              {k === ORIGINAL ? <span className="ml-1.5 text-xs text-ink-faint">each title's own</span> : <span className="ml-1.5 font-mono text-xs text-ink-faint">{k.toUpperCase()}</span>}
            </span>
            {picker(value?.[k] ?? "", (p) => onChange(setLanguage(value, k, p)), `${name(k)} audio`)}
          </div>
        ))}
      </div>
      <div className="mt-1.5 flex flex-wrap items-center gap-2">
        <div className="relative min-w-[200px] flex-1">
          <input
            type="search"
            role="combobox"
            aria-label="Add an audio language"
            aria-expanded={matches.length > 0}
            aria-controls={listId}
            aria-activedescendant={matches[active] ? `${listId}-${matches[active].code}` : undefined}
            aria-autocomplete="list"
            value={query}
            disabled={!rows}
            placeholder={rows ? "Add a language — type to search" : "Loading languages…"}
            onChange={(e) => { setQuery(e.target.value); setActive(0); }}
            onKeyDown={onKey}
            className="w-full rounded-lg px-3 py-1.5 text-base outline-none"
            style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" }}
          />
          {matches.length > 0 && (
            <ul id={listId} role="listbox" aria-label="Matching languages" className="absolute left-0 right-0 top-full z-20 m-0 mt-1 list-none overflow-hidden rounded-lg p-1" style={{ border: "1px solid var(--line)", background: "var(--panel)", boxShadow: "0 8px 24px rgba(0,0,0,.35)" }}>
              {matches.map((l, i) => (
                <li
                  key={l.code}
                  id={`${listId}-${l.code}`}
                  role="option"
                  aria-selected={i === active}
                  onMouseDown={(e) => { e.preventDefault(); add(l.code); }}
                  onMouseEnter={() => setActive(i)}
                  className="flex cursor-pointer items-center justify-between gap-2 rounded-md px-2.5 py-1.5 text-base"
                  style={{ background: i === active ? "var(--accent-soft)" : "transparent", color: i === active ? "var(--accent-text)" : "var(--ink)" }}
                >
                  <span>{l.name}</span>
                  <span className="font-mono text-xs text-ink-faint">{l.code}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
        {!value?.[ORIGINAL] && (
          <button type="button" onClick={() => add(ORIGINAL)} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>
            ＋ Original language
          </button>
        )}
      </div>
      {keys.length === 0 && <div className="mt-1 text-xs text-ink-faint">No language rules — any audio language is fine.</div>}
    </div>
  );
}
