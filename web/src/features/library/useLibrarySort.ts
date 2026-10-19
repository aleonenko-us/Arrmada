import { useMemo, useState } from "react";
import { usePersisted } from "../../lib/persist";
import { migrateSortPref, nextSort, parseSortPref, sortChoices, type PrefStore, type SortChoice, type SortState } from "./sort";
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

// Storage can be blocked (private windows, strict settings): then nothing is carried over
// or remembered, and the order starts at Title.
const local: PrefStore = {
  get: (k) => { try { return localStorage.getItem(k); } catch { return null; } },
  set: (k, v) => { try { localStorage.setItem(k, v); } catch { /* blocked */ } },
  remove: (k) => { try { localStorage.removeItem(k); } catch { /* blocked */ } },
};

// useLibrarySort is the library's one order, shared by the grid's Sort menu and the
// table's column headers ("movies.sort" = "year:desc"). Before they shared it each had its
// own remembered sort; the first visit after the change carries the one on screen over,
// saves it and drops the old two.
export function useLibrarySort<T extends LibraryItem>(config: LibraryConfig<T>): LibraryOrder<T> {
  const choices = useMemo(() => sortChoices(config), [config]);
  // Runs before usePersisted reads the key below (hooks run in order), once per mount.
  useState(() => migrateSortPref(local, config.storageKey, choices));
  const first = choices[0];
  const [pref, setPref] = usePersisted<string>(`${config.storageKey}.sort`, `${first?.key ?? "title"}:${first?.dir ?? "asc"}`);
  const keys = choices.map((c) => c.key);
  const state = parseSortPref(pref, keys, first?.key ?? "title", first?.dir ?? "asc");
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
