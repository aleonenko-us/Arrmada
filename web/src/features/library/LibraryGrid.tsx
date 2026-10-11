import { useMemo, useState, type ReactNode } from "react";
import { usePersisted } from "../../lib/persist";
import { ErrorState, FilterBar, LibraryToolbar, Skeleton, StaleBanner, type LibraryTool, type ToolbarAction } from "../../ui";
import { LibraryCard } from "./LibraryCard";
import { LibraryTable } from "./LibraryTable";
import type { LibraryConfig, LibraryItem, LibrarySelection } from "./types";

export type LibraryViewMode = "grid" | "table";
const VIEW_OPTIONS = [{ value: "grid", label: "grid" }, { value: "table", label: "table" }];
export interface LibraryView {
  mode: LibraryViewMode;
  set: (v: LibraryViewMode) => void;
}

// useLibraryView is the remembered grid/table choice ("movies.view"). The page holds it
// (rather than the grid) so it can load what only the table shows.
export function useLibraryView(storageKey: string): LibraryView {
  const [mode, set] = usePersisted(`${storageKey}.view`, "grid", ["grid", "table"] as const);
  return { mode, set };
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
  /** Narrows the table only (Movies' "only files that don't fit"). */
  tableFilter?: (item: T) => boolean;
  /** The bulk-action bar's own controls; the grid adds the count, Select all and Clear. */
  bulkActions?: (sel: LibrarySelection<T>) => ReactNode;
  /** "No movies yet…": the library is really empty. */
  emptyText: ReactNode;
}

// LibraryGrid is the one library page body for Movies and Series: the toolbar, the filter
// pills with counts, search, multi-select and its bulk bar, the honest load states, and
// the grid or table. Each page passes a LibraryConfig and its own slots.
export function LibraryGrid<T extends LibraryItem>(p: LibraryGridProps<T>) {
  const { config, items, error, view } = p;
  const [filter, setFilter] = useState(config.filters[0]?.key ?? "all");
  const [query, setQuery] = useState("");
  const [multi, setMulti] = useState(false);
  const [selected, setSelected] = useState<ReadonlySet<number>>(new Set());

  const list = useMemo(() => items ?? [], [items]);
  const active = config.filters.find((f) => f.key === filter) ?? config.filters[0];
  const q = query.trim().toLowerCase();
  const visible = useMemo(
    () => list
      .filter((it) => !active?.match || active.match(it))
      .filter((it) => !q || config.searchText(it).toLowerCase().includes(q))
      .sort(config.compare),
    [list, active, q, config],
  );

  const sel: LibrarySelection<T> = {
    active: multi,
    ids: selected,
    items: list.filter((it) => selected.has(it.id)),
    visible,
    toggle: (id) => setSelected((s) => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n; }),
    set: (ids) => setSelected(new Set(ids)),
    clear: () => setSelected(new Set()),
    exit: () => { setMulti(false); setSelected(new Set()); },
  };

  const noun = config.noun;
  return (
    <div className="mx-auto w-full max-w-[1440px] px-4 py-6 sm:px-6">
      <LibraryToolbar
        count={items ? list.length : null}
        lead={p.lead}
        segments={[{ label: "View", value: view.mode, options: VIEW_OPTIONS, onChange: (v) => view.set(v as LibraryViewMode) }]}
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
          <span className="font-mono text-[11.5px] font-semibold" style={{ color: "var(--accent)" }}>{selected.size} selected</span>
          <button onClick={() => sel.set(visible.map((it) => it.id))} className="rounded-lg px-2.5 py-1.5 text-[11.5px] font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink)" }}>Select all ({visible.length})</button>
          <button onClick={sel.clear} disabled={selected.size === 0} className="rounded-lg px-2.5 py-1.5 text-[11.5px]" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Clear</button>
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
        <div className="rounded-xl p-12 text-center text-[12.5px] text-ink-dim" style={{ border: "1px solid var(--line)" }}>{p.emptyText}</div>
      ) : visible.length === 0 ? (
        <div className="rounded-xl p-12 text-center text-[12.5px] text-ink-dim" style={{ border: "1px solid var(--line)" }}>
          {q ? <>No {noun.many} match “<b>{query.trim()}</b>”.</> : <>No {noun.many} match the <b>{active?.label}</b> filter.</>}
        </div>
      ) : view.mode === "table" ? (
        <>
          {p.tableNote}
          <LibraryTable items={p.tableFilter ? visible.filter(p.tableFilter) : visible} config={config} selectable={multi} selected={selected} onToggle={sel.toggle} />
        </>
      ) : (
        <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))" }}>
          {visible.map((it) => (
            <LibraryCard key={it.id} item={it} config={config} selectable={multi} selected={selected.has(it.id)} onToggle={() => sel.toggle(it.id)} />
          ))}
        </div>
      )}
    </div>
  );
}
