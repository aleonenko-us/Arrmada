import { useId, useMemo, useState, type KeyboardEvent } from "react";
import { IconButton } from "../ui";
import { langName, searchLanguages, useLanguages, type Language } from "../lib/languages";

// LanguagePicker edits an ORDERED list of language codes from the shared table: type to
// search (regional variants included — "port" offers Portuguese and Portuguese (Brazil)),
// pick to add, reorder with ↑/↓, remove with ✕. Order is meaningful wherever it's used
// (for Subtitles, the first language is what an untagged sidecar counts as), so the
// picker never sorts. Reused by the Subtitles settings, and meant for language profiles
// and Convert.

export interface LanguagePickerProps {
  value: string[];
  onChange: (next: string[]) => void;
  /** The table; fetched (once per session) when not given. */
  languages?: Language[] | null;
  /** A note under the first language, e.g. what it is used for. */
  firstNote?: string;
  /** Names the search box for screen readers. */
  label?: string;
  disabled?: boolean;
}

export function LanguagePicker({ value, onChange, languages, firstNote, label = "Add a language", disabled }: LanguagePickerProps) {
  const fetched = useLanguages(languages === undefined);
  const rows = languages ?? fetched;
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const listId = useId();
  const matches = useMemo(() => (rows ? searchLanguages(rows, query, value) : []), [rows, query, value]);

  const add = (code: string) => {
    if (!value.includes(code)) onChange([...value, code]);
    setQuery("");
    setActive(0);
  };
  const remove = (code: string) => onChange(value.filter((c) => c !== code));
  const move = (i: number, by: -1 | 1) => {
    const j = i + by;
    if (j < 0 || j >= value.length) return;
    const next = [...value];
    [next[i], next[j]] = [next[j], next[i]];
    onChange(next);
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" && matches.length) { e.preventDefault(); setActive((a) => (a + 1) % matches.length); }
    else if (e.key === "ArrowUp" && matches.length) { e.preventDefault(); setActive((a) => (a - 1 + matches.length) % matches.length); }
    else if (e.key === "Enter" && matches[active]) { e.preventDefault(); add(matches[active].code); }
    else if (e.key === "Escape") { setQuery(""); setActive(0); }
  };

  const btn = "h-7 w-7 shrink-0 rounded-md text-[12px] text-ink-dim disabled:opacity-30";
  return (
    <div className="flex flex-col gap-2">
      {value.length > 0 && (
        <ol className="m-0 flex list-none flex-col gap-1 p-0">
          {value.map((code, i) => {
            const name = langName(code, rows);
            return (
              <li key={code} className="flex min-w-0 items-center gap-2 rounded-lg px-2.5 py-1.5" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
                <span className="w-4 shrink-0 text-right font-mono text-[10.5px] text-ink-faint">{i + 1}</span>
                <span className="min-w-0 flex-1">
                  <span className="text-[12.5px] font-semibold">{name}</span>
                  <span className="ml-1.5 font-mono text-[10px] text-ink-faint">{code}</span>
                  {i === 0 && firstNote && <span className="block text-[10.5px] text-ink-faint">{firstNote}</span>}
                </span>
                <IconButton label={`Move ${name} up`} className={btn} disabled={disabled || i === 0} onClick={() => move(i, -1)}>↑</IconButton>
                <IconButton label={`Move ${name} down`} className={btn} disabled={disabled || i === value.length - 1} onClick={() => move(i, 1)}>↓</IconButton>
                <IconButton label={`Remove ${name}`} className={btn} disabled={disabled} onClick={() => remove(code)}>✕</IconButton>
              </li>
            );
          })}
        </ol>
      )}
      <div className="relative">
        <input
          type="search"
          role="combobox"
          aria-label={label}
          aria-expanded={matches.length > 0}
          aria-controls={listId}
          aria-activedescendant={matches[active] ? `${listId}-${matches[active].code}` : undefined}
          aria-autocomplete="list"
          value={query}
          disabled={disabled || !rows}
          placeholder={rows ? "Add a language — type to search" : "Loading languages…"}
          onChange={(e) => { setQuery(e.target.value); setActive(0); }}
          onKeyDown={onKey}
          className="w-full rounded-lg px-3 py-2 text-[12.5px] outline-none"
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
                className="flex cursor-pointer items-center justify-between gap-2 rounded-md px-2.5 py-1.5 text-[12.5px]"
                style={{ background: i === active ? "var(--accent-soft)" : "transparent", color: i === active ? "var(--accent)" : "var(--ink)" }}
              >
                <span>{l.name}</span>
                <span className="font-mono text-[10px] text-ink-faint">{l.code}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
