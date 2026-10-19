import { useMemo, useState, type ReactNode } from "react";
import { usePersisted } from "../../lib/persist";
import { useSearchParamState } from "../../lib/useSearchState";
import { PHONE, useMediaQuery } from "../../lib/useMediaQuery";
import { ErrorState, FilterBar, LibraryToolbar, Skeleton, StaleBanner, type LibraryTool, type ToolbarAction, type ToolbarSegment } from "../../ui";
import { LibraryCard } from "./LibraryCard";
import { LibraryTable } from "./LibraryTable";
import { useProgressive } from "./progressive";
import { sortItems } from "./sort";
import { useLibrarySort } from "./useLibrarySort";
import type { LibraryConfig, LibraryItem, LibrarySelection } from "./types";
import { Page } from "../../ui/Page";

export type LibraryViewMode = "grid" | "table";
const VIEW_OPTIONS = [{ value: "grid", label: "grid" }, { value: "table", label: "table" }];
export interface LibraryView {
  mode: LibraryViewMode;
  set: (v: LibraryViewMode) => void;
  /** false on a phone: the table doesn't fit, so the toggle hides and the grid shows. */
  canTable: boolean;
}

// useLibraryView is the remembered grid/table choice ("movies.view"). The page holds it
// (rather than the grid) so it can load what only the table shows. A phone always gets the
// grid, without touching the saved choice, so the desktop keeps its table.
export function useLibraryView(storageKey: string): LibraryView {
  const [saved, set] = usePersisted(`${storageKey}.view`, "grid", ["grid", "table"] as const);
  const phone = useMediaQuery(PHONE);
  return { mode: phone ? "grid" : saved, set, canTable: !phone };
}

export interface LibraryGridProps<T extends LibraryItem> {
  config: LibraryConfig<T>;
  /** undefined until the first answer: a skeleton, never "No movies yet". */
  items: T[] | undefined;
  error: string | null;
  loading: boolean;
  onRetry: () => void;
  view: LibraryView;
  /** Beside the count: the Movies Library | Wanted switch, a search-queue chip. */
  lead?: ReactNode;
  /** The toolbar's "Library tools" menu (Scan library…). */
  tools?: LibraryTool[];
  primary: ToolbarAction;
  /** Notices above the list (metadata missing). */
  banners?: ReactNode;
  /** Above the table only (the fit legend). */
  tableNote?: ReactNode;
  /** The bulk-action bar's own controls; the grid adds the count, Select all and Clear. */
  bulkActions?: (sel: LibrarySelection<T>) => ReactNode;
  /** The library is really empty: the page's <LibraryEmpty> (scan or add). */
  empty: ReactNode;
  /** Part of the library the page narrows to (one folder of several): its picker in the
   *  toolbar, which titles are in it, its name and the way back to all of them. */
  scope?: LibraryScope<T>;
}

export interface LibraryScope<T> {
  picker: ToolbarSegment;
  /** Left out while the whole library shows. */
  match?: (item: T) => boolean;
  /** "Kids", for "No movies in Kids yet". */
  name: string;
  onClear: () => void;
}

// LibraryGrid is the one library page body for Movies and Series: the toolbar, the filter
// pills with counts, search, multi-select and its bulk bar, the honest load states, and
// the grid or table. Each page passes a LibraryConfig and its own slots.
export function LibraryGrid<T extends LibraryItem>(p: LibraryGridProps<T>) {
  const { config, items, error, view } = p;
  // The filter and search live in the address (?filter=wanted&q=dune): Back from a title
  // brings them back, and the view can be bookmarked or shared.
  const filterKeys = useMemo(() => config.filters.map((f) => f.key), [config.filters]);
  const [filter, setFilter] = useSearchParamState("filter", filterKeys[0] ?? "all", { allowed: filterKeys });
  const [query, setQuery] = useSearchParamState<string>("q", "", { debounceMs: 300 });
  const [multi, setMulti] = useState(false);
  const [selected, setSelected] = useState<ReadonlySet<number>>(new Set());

  const list = useMemo(() => items ?? [], [items]);
  // The scope (one folder of several) narrows everything below it: the pills count within
  // it, and the filters and search look only inside it.
  const scopeMatch = p.scope?.match;
  const scoped = useMemo(() => (scopeMatch ? list.filter(scopeMatch) : list), [list, scopeMatch]);
  const active = config.filters.find((f) => f.key === filter) ?? config.filters[0];
  const q = query.trim().toLowerCase();
  // One order for the grid's Sort menu and the table's headers, remembered ("movies.sort").
  const order = useLibrarySort(config);
  const { choice, state: sortState } = order;
  const visible = useMemo(() => {
    // Every word must appear somewhere ("dune 2021" finds Dune, 2021), in any order.
    const words = q.split(/\s+/).filter(Boolean);
    const shown = scoped
      .filter((it) => !active?.match || active.match(it))
      .filter((it) => { if (!words.length) return true; const text = config.searchText(it).toLowerCase(); return words.every((w) => text.includes(w)); });
    return sortItems(shown, choice, sortState.dir, config.compare);
  }, [scoped, active, q, config, choice, sortState.dir]);

  // The grid renders a few screens and grows as it scrolls; a new scope, filter, search or
  // sort starts it from the top again. The table does the same.
  const listKey = `${p.scope?.picker.value ?? ""}|${active?.key}|${q}|${sortState.key}:${sortState.dir}`;
  const grid = useProgressive(visible, "grid", listKey);
  // The Sort menu lists the config's orders; a column sort picked in the table (Codec)
  // joins it while it's in use, so the menu always names the order on screen.
  const menuChoices = config.sorts.some((s) => s.key === sortState.key) || !choice ? config.sorts : [...config.sorts, { key: choice.key, label: choice.label }];

  // Only titles still in the library count as selected: one removed meanwhile (here or
  // elsewhere) drops out instead of failing the next bulk action.
  const chosen = list.filter((it) => selected.has(it.id));
  const sel: LibrarySelection<T> = {
    active: multi,
    ids: new Set(chosen.map((it) => it.id)),
    items: chosen,
    visible,
    toggle: (id) => setSelected((s) => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n; }),
    set: (ids) => setSelected(new Set(ids)),
    clear: () => setSelected(new Set()),
    exit: () => { setMulti(false); setSelected(new Set()); },
  };

  const noun = config.noun;
  return (
    <Page width="wide">
      <LibraryToolbar
        count={items ? list.length : null}
        lead={p.lead}
        segments={view.canTable ? [{ label: "View", value: view.mode, options: VIEW_OPTIONS, onChange: (v) => view.set(v as LibraryViewMode) }] : []}
        pickers={p.scope ? [p.scope.picker] : []}
        sort={config.sorts.length > 1 ? { label: "Sort", value: sortState.key, options: menuChoices.map((s) => ({ value: s.key, label: s.label })), onChange: order.onPick } : undefined}
        sortDir={config.sorts.length > 1 ? { dir: sortState.dir, onToggle: order.reverse } : undefined}
        select={{ active: multi, onToggle: () => (multi ? sel.exit() : setMulti(true)) }}
        tools={p.tools}
        primary={p.primary}
      />
      <FilterBar
        filters={config.filters.map((f) => ({ key: f.key, label: f.label, title: f.title, count: f.match ? scoped.filter(f.match).length : scoped.length }))}
        active={filter}
        onFilter={setFilter}
        query={query}
        onQuery={setQuery}
        noun={noun.many}
      />

      {multi && (
        <div className="mb-4 flex flex-wrap items-center gap-2.5 rounded-xl p-3" style={{ background: "var(--panel)", border: "1px solid var(--accent-line)" }}>
          <span className="font-mono text-sm font-semibold" style={{ color: "var(--accent-text)" }}>{sel.ids.size} selected</span>
          <button onClick={() => sel.set(visible.map((it) => it.id))} className="rounded-lg px-2.5 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink)" }}>Select all ({visible.length})</button>
          <button onClick={sel.clear} disabled={sel.ids.size === 0} className="rounded-lg px-2.5 py-1.5 text-sm" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Clear</button>
          {p.bulkActions && <span className="mx-1 h-5 w-px" style={{ background: "var(--line)" }} />}
          {p.bulkActions?.(sel)}
        </div>
      )}

      {p.banners}
      {/* Honest states: a skeleton until the first answer, an error (not "No movies yet")
          if it failed, and the last good list with a notice if a refresh failed. */}
      {items && error && <StaleBanner message={error} onRetry={p.onRetry} />}

      {!items ? (
        error ? <ErrorState what={`your ${noun.many}`} message={error} onRetry={p.onRetry} busy={p.loading} /> : <Skeleton variant={view.mode === "table" ? "table" : "grid"} />
      ) : list.length === 0 ? (
        p.empty
      ) : scoped.length === 0 && p.scope ? (
        <div className="rounded-xl p-12 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>
          No {noun.many} in <b>{p.scope.name}</b> yet.{" "}
          <button onClick={p.scope.onClear} className="font-semibold" style={{ color: "var(--accent-text)" }}>Show all folders</button>
        </div>
      ) : visible.length === 0 ? (
        <div className="rounded-xl p-12 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>
          {q ? <>No {noun.many} match “<b>{query.trim()}</b>”.</> : <>No {noun.many} match the <b>{active?.label}</b> filter.</>}
        </div>
      ) : view.mode === "table" ? (
        <>
          {p.tableNote}
          <LibraryTable items={visible} config={config} selectable={multi} selected={selected} onToggle={sel.toggle} listKey={listKey} sort={sortState} onSort={order.onColumn} />
        </>
      ) : (
        <>
          <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))" }}>
            {grid.shown.map((it) => (
              <LibraryCard key={it.id} item={it} config={config} selectable={multi} selected={selected.has(it.id)} onToggle={() => sel.toggle(it.id)} />
            ))}
          </div>
          {grid.sentinel}
        </>
      )}
    </Page>
  );
}
