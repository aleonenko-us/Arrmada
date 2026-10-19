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

// parseSortPref reads a remembered "key:dir" ("size:desc"). One naming a column that no
// longer exists falls back to the fallback key in its own direction: a remembered
// "descending" was about the old column, not the title.
export function parseSortPref(pref: string, keys: readonly string[], fallback: string, fallbackDir: SortDir = "asc"): SortState {
  const [k, d] = pref.split(":");
  if (!keys.includes(k)) return { key: fallback, dir: fallbackDir };
  return { key: k, dir: d === "desc" ? "desc" : "asc" };
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

// LEGACY_SORT_KEYS are the two remembered sorts the menu and the table had before they
// shared one; carried over once, then removed.
export const LEGACY_SORT_KEYS = (storageKey: string) => [`${storageKey}.grid.sort`, `${storageKey}.table.sort`];

// legacySortPref is the order someone had before the menu and the table shared one: the
// table's "key:dir" when the table was their view, else the grid menu's key in its first
// direction — the order they were actually looking at. A grid user never inherits a
// table sort they no longer saw; with nothing to carry over it's null (Title A→Z).
export function legacySortPref(read: (k: string) => string | null, storageKey: string, choices: readonly { key: string; dir: SortDir }[]): string | null {
  const view = read(`${storageKey}.view`);
  const [gridKey, tableKey] = LEGACY_SORT_KEYS(storageKey);
  const table = read(tableKey);
  if (view === "table") return table && choices.some((c) => c.key === table.split(":")[0]) ? table : null;
  const g = choices.find((c) => c.key === read(gridKey));
  return g ? `${g.key}:${g.dir}` : null;
}

export interface PrefStore {
  get(k: string): string | null;
  set(k: string, v: string): void;
  remove(k: string): void;
}

// migrateSortPref moves the old remembered sorts onto the shared one ("movies.sort") once:
// the carried-over order is saved there and the old keys are removed, so a later visit
// reads the same order rather than working it out again from what's left.
export function migrateSortPref(store: PrefStore, storageKey: string, choices: readonly { key: string; dir: SortDir }[]): void {
  const key = `${storageKey}.sort`;
  if (store.get(key) === null) {
    const legacy = legacySortPref((k) => store.get(k), storageKey, choices);
    if (legacy) store.set(key, legacy);
  }
  for (const k of LEGACY_SORT_KEYS(storageKey)) store.remove(k);
}
