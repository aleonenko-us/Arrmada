import { useEffect, useState } from "react";
import { req } from "./api";

// The shared language table (internal/langs on the server): every language the app
// matches, with its English name and the regional variants (pt-br, zh-tw, es-419…).
// Fetched once per session and kept in memory — it's static data. It lives here rather
// than in api.ts so it only downloads with the staff pages that name languages.

export interface Language {
  code: string; // canonical: "fr", "pt-br"
  name: string; // "French", "Portuguese (Brazil)"
  regional: boolean;
}

let table: Language[] | null = null;
let pending: Promise<Language[]> | null = null;

/** loadLanguages fetches the table once; later calls share the answer. A failed fetch is
 *  forgotten so the next caller tries again. */
export function loadLanguages(): Promise<Language[]> {
  if (table) return Promise.resolve(table);
  if (!pending) {
    pending = req<Language[]>("/api/v1/languages").then(
      (rows) => { table = rows; pending = null; return rows; },
      (e: unknown) => { pending = null; throw e; },
    );
  }
  return pending;
}

/** useLanguages is the table, or null until it has loaded (or when it can't be).
 *  enabled=false skips the fetch, for a caller that was handed the table. */
export function useLanguages(enabled = true): Language[] | null {
  const [rows, setRows] = useState<Language[] | null>(table);
  useEffect(() => {
    if (rows || !enabled) return;
    let live = true;
    loadLanguages().then((r) => { if (live) setRows(r); }, () => {});
    return () => { live = false; };
  }, [rows, enabled]);
  return rows;
}

/** langName names a language code from the table; an unknown code (or a table that
 *  hasn't loaded) shows as the code in capitals, as it always has. */
export function langName(code: string, rows: Language[] | null | undefined): string {
  const c = code.trim().toLowerCase();
  return rows?.find((l) => l.code === c)?.name ?? code.toUpperCase();
}

// fold lower-cases and strips accents, so "francais" finds "Français" and "bokmal" finds
// "Norwegian Bokmål".
const fold = (s: string) => s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();

/** searchLanguages ranks the table for a typed query: an exact code first, then names
 *  that start with it, then names with a word that starts with it, then any match. A
 *  base language comes before its regional variants at the same rank. */
export function searchLanguages(rows: Language[], query: string, exclude: string[] = [], limit = 8): Language[] {
  const q = fold(query.trim());
  if (!q) return [];
  const skip = new Set(exclude);
  const ranked: { l: Language; r: number }[] = [];
  for (const l of rows) {
    if (skip.has(l.code)) continue;
    const name = fold(l.name);
    let r = -1;
    if (l.code === q) r = 0;
    else if (name.startsWith(q)) r = 1;
    else if (name.split(/[\s()]+/).some((w) => w.startsWith(q))) r = 2;
    else if (name.includes(q) || l.code.startsWith(q)) r = 3;
    if (r >= 0) ranked.push({ l, r });
  }
  ranked.sort((a, b) => a.r - b.r || Number(a.l.regional) - Number(b.l.regional) || a.l.name.localeCompare(b.l.name));
  return ranked.slice(0, limit).map((x) => x.l);
}
