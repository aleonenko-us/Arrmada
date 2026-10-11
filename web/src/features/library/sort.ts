import type { SortValue } from "./types";

// compareValues orders two sort values: numbers by size, strings A→Z, and an empty value
// (undefined) always last whichever way the sort runs — an unknown size isn't the
// smallest. 0 means "tied", so the caller can fall back to the title.
export function compareValues(a: SortValue, b: SortValue, dir: "asc" | "desc"): number {
  const aE = a === undefined, bE = b === undefined;
  if (aE && bE) return 0;
  if (aE) return 1;
  if (bE) return -1;
  const r = typeof a === "number" && typeof b === "number" ? a - b : String(a).localeCompare(String(b));
  return dir === "asc" ? r : -r;
}

// parseSortPref reads a remembered "key:dir" ("size:desc"), falling back to the first
// allowed key ascending when it names a column that no longer exists.
export function parseSortPref(pref: string, keys: readonly string[], fallback: string): { key: string; dir: "asc" | "desc" } {
  const [k, d] = pref.split(":");
  return { key: keys.includes(k) ? k : fallback, dir: d === "desc" ? "desc" : "asc" };
}
