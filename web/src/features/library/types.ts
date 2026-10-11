import type { ReactNode } from "react";

// The shared library page (LibraryGrid) is configured per media type. Movies and Series
// used to be two hand-built copies of the same page; fixes kept landing on one and not the
// other. What differs between them lives here as data and slots, not boolean flags.

/** What every library row has: the grid and table key, link and caption on these. */
export interface LibraryItem {
  id: number;
  title: string;
  year?: number;
  poster_url?: string;
}

/** A comparable value for sorting; undefined is "nothing to sort by" and always sorts last. */
export type SortValue = number | string | undefined;

export interface LibraryFilter<T> {
  key: string;
  label: string;
  /** Every filter but "all" decides per item; "all" may leave this out. */
  match?: (item: T) => boolean;
  title?: string;
}

export interface LibraryColumn<T> {
  key: string;
  label: string;
  align?: "right";
  /** A header tooltip. */
  title?: string;
  /** Leave out for a column that can't be sorted. */
  sort?: (item: T) => SortValue;
  /** Counts read best biggest-first, so their first click sorts descending. */
  descFirst?: boolean;
  /** Extra classes on the cell. */
  className?: string;
  cell: (item: T) => ReactNode;
}

export interface LibraryStatusWord {
  label: string;
  color: string;
  title?: string;
}

export interface LibraryConfig<T extends LibraryItem> {
  /** "movie" / "movies": counts, placeholders and empty states. */
  noun: { one: string; many: string };
  /** The prefix of the remembered view and sort ("movies" → movies.view, movies.table.sort). */
  storageKey: string;
  detailPath: (item: T) => string;
  /** The filter pills, "all" first. */
  filters: LibraryFilter<T>[];
  /** What the search box matches against, lower-cased by the grid. */
  searchText: (item: T) => string;
  /** The default order of the grid (and the table before a column is clicked). */
  compare: (a: T, b: T) => number;
  /** The table's columns after Title (which is always first and sorts by compare). */
  columns: LibraryColumn<T>[];
  /** The table scrolls sideways below this width rather than squashing its columns. */
  tableMinWidth: number;
  card: {
    /** The caption's left side: "2021", "2019 · 3 sns". */
    meta: (item: T) => ReactNode;
    status: (item: T) => LibraryStatusWord;
    /** Over the poster's bottom edge: the episode bar, a download's progress. */
    overlay?: (item: T) => ReactNode;
    /** Hover-revealed controls on the poster (not in select mode). */
    hoverActions?: (item: T) => ReactNode;
  };
  /** The table row's own button (Search), right-aligned in the last column. */
  rowAction?: (item: T) => ReactNode;
}

/**
 * The multi-select state the grid owns, handed to the page's bulk bar. A bulk action reads
 * `items` (or `ids`), and afterwards either `clear()`s or `set()`s the ids that failed so
 * they stay selected for another try.
 */
export interface LibrarySelection<T> {
  /** Select mode is on. */
  active: boolean;
  ids: ReadonlySet<number>;
  /** The selected items that are still in the library list. */
  items: T[];
  /** What the filter and search show right now ("Select all" picks these). */
  visible: T[];
  toggle(id: number): void;
  set(ids: Iterable<number>): void;
  clear(): void;
  /** Leave select mode (and clear). */
  exit(): void;
}
