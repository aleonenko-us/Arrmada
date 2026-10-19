import { Link } from "react-router-dom";
import { shownTitle } from "../../lib/displayTitle";
import { useProgressive } from "./progressive";
import type { SortState } from "./sort";
import type { LibraryConfig, LibraryItem } from "./types";

const TH = "px-2.5 py-2 text-left font-mono text-2xs font-bold uppercase tracking-[0.06em] text-ink-faint";
const TD = "px-2.5 py-2 align-middle";

// LibraryTable is the library's table view: the config's columns, each sortable by a
// click on its header (again to reverse). The rows arrive already in the library's one
// order — LibraryGrid's, shared with the grid's Sort menu — and the headers show which.
export function LibraryTable<T extends LibraryItem>({ items, config, selectable, selected, onToggle, listKey, sort, onSort }: {
  items: T[];
  config: LibraryConfig<T>;
  selectable: boolean;
  selected: ReadonlySet<number>;
  onToggle: (id: number) => void;
  /** The filter, search and sort the rows are for: a new one starts the table from the top. */
  listKey: string;
  sort: SortState;
  onSort: (key: string) => void;
}) {
  const cols = config.columns;
  // With a Year column of its own, the title cell leaves the year out.
  const yearCol = cols.some((c) => c.key === "year");
  // Rows render a few screens at a time, like the grid's cards.
  const rows = useProgressive(items, "table", listKey);

  return (
    <>
      <div className="thin-scroll overflow-x-auto rounded-xl" style={{ border: "1px solid var(--line)" }}>
        <table className="w-full border-collapse text-sm" style={{ minWidth: `${config.tableMinWidth}px` }}>
          <thead>
            <tr style={{ background: "var(--panel)", borderBottom: "1px solid var(--line)" }}>
              {selectable && <th className={TH}><span className="sr-only">Select</span></th>}
              <SortTh label="Title" k="title" sort={sort} onSort={onSort} />
              {cols.map((c) => (c.sort
                ? <SortTh key={c.key} label={c.label} k={c.key} align={c.align} title={c.title} sort={sort} onSort={onSort} />
                : <th key={c.key} className={`${TH}${c.align === "right" ? " text-right" : ""}`} title={c.title}>{c.label}</th>))}
              {config.rowAction && <th className={TH}><span className="sr-only">Actions</span></th>}
            </tr>
          </thead>
          <tbody>
            {rows.shown.map((it) => {
              const on = selected.has(it.id);
              const name = yearCol ? shownTitle(it) : <>{shownTitle(it)} <span className="font-normal text-ink-faint">{it.year || ""}</span></>;
              return (
                <tr key={it.id} className="transition-colors hover:bg-[var(--panel-2)]" style={{ background: on ? "var(--accent-soft)" : "var(--panel)", borderBottom: "1px solid var(--line-soft)" }}>
                  {selectable && <td className={TD}><input type="checkbox" aria-label={`Select ${shownTitle(it)}`} checked={on} onChange={() => onToggle(it.id)} /></td>}
                  <td className={`${TD} min-w-[220px]`}>
                    {selectable
                      ? <button onClick={() => onToggle(it.id)} className="text-left font-semibold">{name}</button>
                      : <Link to={config.detailPath(it)} className="font-semibold hover:text-[var(--accent)]">{name}</Link>}
                  </td>
                  {cols.map((c) => (
                    <td key={c.key} className={`${TD}${c.align === "right" ? " text-right" : ""} ${c.className ?? ""}`}>{c.cell(it)}</td>
                  ))}
                  {config.rowAction && <td className={`${TD} text-right`}>{config.rowAction(it)}</td>}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {rows.sentinel}
    </>
  );
}

// SortTh is a sortable column header: a button with the arrow of the order in use.
export function SortTh({ label, k, align, title, sort, onSort }: { label: string; k: string; align?: "right"; title?: string; sort: SortState; onSort: (k: string) => void }) {
  const active = sort.key === k;
  return (
    <th className={`${TH}${align === "right" ? " text-right" : ""}`} title={title} aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : undefined}>
      <button onClick={() => onSort(k)} className={`inline-flex items-center gap-1 hover:text-[var(--ink)] ${align === "right" ? "flex-row-reverse" : ""}`} style={{ color: active ? "var(--accent-text)" : undefined }}>
        {label}
        <span className="text-xs" style={{ opacity: active ? 1 : 0.3 }}>{active ? (sort.dir === "asc" ? "▲" : "▼") : "▲"}</span>
      </button>
    </th>
  );
}
