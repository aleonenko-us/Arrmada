import { useMemo, useState } from "react";
import { usePersisted } from "../../lib/persist";
import { legacySortPref, nextSort, parseSortPref, sortChoices, type SortChoice, type SortState } from "./sort";
import type { LibraryConfig, LibraryItem } from "./types";

export interface LibraryOrder<T> {
  choices: SortChoice<T>[];
  /** The choice in use (Title when the remembered key no longer exists). */
  choice: SortChoice<T>;
  state: SortState;
  /** A header click: the sorted column again reverses it. */
  onColumn: (key: string) => void;
  /** A pick in the Sort menu: that order in its own first direction. */
  onPick: (key: string) => void;
  /** The menu's ↑/↓ button. */
  reverse: () => void;
}

function readStorage(k: string): string | null {
  try { return localStorage.getItem(k); } catch { return null; }
}

// useLibrarySort is the library's one order, shared by the grid's Sort menu and the
// table's column headers ("movies.sort" = "year:desc"). Before they shared it each had its
// own remembered sort; the first visit after the change carries the old one over.
export function useLibrarySort<T extends LibraryItem>(config: LibraryConfig<T>): LibraryOrder<T> {
  const choices = useMemo(() => sortChoices(config), [config]);
  const [initial] = useState(() => legacySortPref(readStorage, config.storageKey, choices) ?? `${choices[0]?.key ?? "title"}:${choices[0]?.dir ?? "asc"}`);
  const [pref, setPref] = usePersisted<string>(`${config.storageKey}.sort`, initial);
  const keys = choices.map((c) => c.key);
  const state = parseSortPref(pref, keys, "title");
  const choice = choices.find((c) => c.key === state.key) ?? choices[0];
  const save = (s: SortState) => setPref(`${s.key}:${s.dir}`);
  const firstDir = (key: string) => choices.find((c) => c.key === key)?.dir ?? "asc";
  return {
    choices,
    choice,
    state,
    onColumn: (key) => save(nextSort(state, key, firstDir(key))),
    onPick: (key) => save(nextSort(state, key, firstDir(key), false)),
    reverse: () => save({ key: state.key, dir: state.dir === "asc" ? "desc" : "asc" }),
  };
}
