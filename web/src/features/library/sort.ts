import type { LibraryConfig, LibraryItem, SortValue } from "./types";

export type SortDir = "asc" | "desc";
export interface SortState { key: string; dir: SortDir }

// compareValues orders two sort values: numbers by size, strings A→Z, and an empty value
// (undefined) always last whichever way the sort runs — an unknown size isn't the
// smallest. 0 means "tied", so the caller can fall back to the title.
export function compareValues(a: SortValue, b: SortValue, dir: SortDir): number {
  const aE = a === undefined, bE = b === undefined;
  if (aE && bE) return 0;
  if (aE) return 1;
  if (bE) return -1;
  const r = typeof a === "number" && typeof b === "number" ? a - b : String(a).localeCompare(String(b));
  return dir === "asc" ? r : -r;
}

// parseSortPref reads a remembered "key:dir" ("size:desc"), falling back to the first
// allowed key ascending when it names a column that no longer exists.
export function parseSortPref(pref: string, keys: readonly string[], fallback: string): SortState {
  const [k, d] = pref.split(":");
  return { key: keys.includes(k) ? k : fallback, dir: d === "desc" ? "desc" : "asc" };
}

/** One way to order a library: the Sort menu's entries and the table's sortable columns. */
export interface SortChoice<T> {
  key: string;
  label: string;
  /** undefined: the config's own order (the title). */
  value?: (item: T) => SortValue;
  /** The direction a first pick sorts in: newest, biggest and best first. */
  dir: SortDir;
}

// sortChoices merges the Sort menu with the table's sortable columns into one list keyed
// the same way, so picking "Year" in the menu and clicking the Year header are the same
// sort with the same first direction. A menu entry wins where both name a key; Title is
// always there.
export function sortChoices<T extends LibraryItem>(config: Pick<LibraryConfig<T>, "sorts" | "columns">): SortChoice<T>[] {
  const out: SortChoice<T>[] = [];
  const seen = new Set<string>();
  for (const s of config.sorts) {
    const col = config.columns.find((c) => c.key === s.key);
    out.push({ key: s.key, label: s.label, value: s.value ?? col?.sort, dir: s.dir ?? (col?.descFirst ? "desc" : "asc") });
    seen.add(s.key);
  }
  if (!seen.has("title")) out.unshift({ key: "title", label: "Title", dir: "asc" });
  for (const c of config.columns) {
    if (!c.sort || seen.has(c.key)) continue;
    out.push({ key: c.key, label: c.label, value: c.sort, dir: c.descFirst ? "desc" : "asc" });
    seen.add(c.key);
  }
  return out;
}

// nextSort is a click on a header or a pick in the menu: the same key again reverses it,
// another key starts in its own first direction.
export function nextSort(cur: SortState, key: string, firstDir: SortDir, toggle = true): SortState {
  if (toggle && cur.key === key) return { key, dir: cur.dir === "asc" ? "desc" : "asc" };
  return { key, dir: firstDir };
}

// sortItems orders a list by a choice: its value (empty last) with ties broken by the
// config's own order, or that order itself (reversed for descending).
export function sortItems<T>(items: readonly T[], choice: SortChoice<T> | undefined, dir: SortDir, compare: (a: T, b: T) => number): T[] {
  const by = choice?.value;
  const arr = [...items];
  if (!by) arr.sort((a, b) => (dir === "asc" ? 1 : -1) * compare(a, b));
  else arr.sort((a, b) => compareValues(by(a), by(b), dir) || compare(a, b));
  return arr;
}

// legacySortPref is the order someone had before the menu and the table shared one: the
// table's "key:dir" when the table was their view, else the grid menu's key in its first
// direction. Read once, so nobody's remembered order is lost by the change.
export function legacySortPref(read: (k: string) => string | null, storageKey: string, choices: readonly { key: string; dir: SortDir }[]): string | null {
  const view = read(`${storageKey}.view`);
  const table = read(`${storageKey}.table.sort`);
  const grid = read(`${storageKey}.grid.sort`);
  if (view === "table" && table) return table;
  const g = choices.find((c) => c.key === grid);
  if (g) return `${g.key}:${g.dir}`;
  return table;
}
