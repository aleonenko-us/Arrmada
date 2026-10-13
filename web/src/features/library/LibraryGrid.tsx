import { useMemo, useState, type ReactNode } from "react";
import { usePersisted } from "../../lib/persist";
import { useSearchParamState } from "../../lib/useSearchState";
import { PHONE, useMediaQuery } from "../../lib/useMediaQuery";
import { ErrorState, FilterBar, LibraryToolbar, Skeleton, StaleBanner, type LibraryTool, type ToolbarAction } from "../../ui";
import { LibraryCard } from "./LibraryCard";
import { LibraryTable } from "./LibraryTable";
import { useProgressive } from "./progressive";
import { compareValues } from "./sort";
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
  const active = config.filters.find((f) => f.key === filter) ?? config.filters[0];
  const q = query.trim().toLowerCase();
  // The grid's order is a remembered preference ("series.grid.sort"), like the view.
  const sortKeys = useMemo(() => config.sorts.map((s) => s.key), [config.sorts]);
  const [sortKey, setSortKey] = usePersisted<string>(`${config.storageKey}.grid.sort`, sortKeys[0] ?? "title", sortKeys);
  const sort = config.sorts.find((s) => s.key === sortKey) ?? config.sorts[0];
  const visible = useMemo(() => {
    const by = sort?.value;
    const dir = sort?.dir ?? "asc";
    // Every word must appear somewhere ("dune 2021" finds Dune, 2021), in any order.
    const words = q.split(/\s+/).filter(Boolean);
    return list
      .filter((it) => !active?.match || active.match(it))
      .filter((it) => { if (!words.length) return true; const text = config.searchText(it).toLowerCase(); return words.every((w) => text.includes(w)); })
      .sort((a, b) => (by ? compareValues(by(a), by(b), dir) : 0) || config.compare(a, b));
  }, [list, active, q, config, sort]);

  // The grid renders a few screens and grows as it scrolls; a new filter, search or sort
  // starts it from the top again. The table does the same in its own order.
  const listKey = `${active?.key}|${q}`;
  const grid = useProgressive(visible, "grid", `${listKey}|${sort?.key}`);

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
        sort={view.mode === "grid" && config.sorts.length > 1 ? { label: "Sort", value: sort?.key ?? "", options: config.sorts.map((s) => ({ value: s.key, label: s.label })), onChange: setSortKey } : undefined}
        select={{ active: multi, onToggle: () => (multi ? sel.exit() : setMulti(true)) }}
        tools={p.tools}
        primary={p.primary}
      />
      <FilterBar
        filters={config.filters.map((f) => ({ key: f.key, label: f.label, title: f.title, count: f.match ? list.filter(f.match).length : list.length }))}
        active={filter}
        onFilter={setFilter}
        query={query}
        onQuery={setQuery}
        noun={noun.many}
      />

      {multi && (
        <div className="mb-4 flex flex-wrap items-center gap-2.5 rounded-xl p-3" style={{ background: "var(--panel)", border: "1px solid var(--accent-line)" }}>
          <span className="font-mono text-sm font-semibold" style={{ color: "var(--accent)" }}>{sel.ids.size} selected</span>
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
      ) : visible.length === 0 ? (
        <div className="rounded-xl p-12 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>
          {q ? <>No {noun.many} match “<b>{query.trim()}</b>”.</> : <>No {noun.many} match the <b>{active?.label}</b> filter.</>}
        </div>
      ) : view.mode === "table" ? (
        <>
          {p.tableNote}
          <LibraryTable items={visible} config={config} selectable={multi} selected={selected} onToggle={sel.toggle} listKey={listKey} />
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
