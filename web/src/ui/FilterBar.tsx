export interface FilterPill {
  key: string;
  label: string;
  count: number;
  title?: string;
}

export interface FilterBarProps {
  filters: FilterPill[];
  active: string;
  onFilter: (key: string) => void;
  query: string;
  onQuery: (q: string) => void;
  /** The library's plural: the box reads "Search <noun>…". */
  noun: string;
}

// FilterBar is the row under a library's toolbar: the filter pills, each with its count,
// and the search box — full width on a phone, a fixed 240px on the right from sm up.
export function FilterBar({ filters, active, onFilter, query, onQuery, noun }: FilterBarProps) {
  return (
    <div className="mb-4 flex flex-wrap items-center gap-2">
      {filters.map((f) => {
        const on = active === f.key;
        return (
          <button
            key={f.key}
            onClick={() => onFilter(f.key)}
            aria-pressed={on}
            title={f.title}
            className="whitespace-nowrap rounded-full px-3 py-1 text-[12px] font-semibold"
            style={{ border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`, background: on ? "var(--accent-soft)" : "var(--panel)", color: on ? "var(--accent)" : "var(--ink-faint)" }}
          >
            {f.label} <span className="font-mono text-[10.5px] opacity-70">{f.count}</span>
          </button>
        );
      })}
      <input
        type="search"
        value={query}
        onChange={(e) => onQuery(e.target.value)}
        placeholder={`Search ${noun}…`}
        aria-label={`Search ${noun}`}
        className="w-full rounded-lg px-3 py-1.5 text-[12px] sm:ml-auto sm:w-[240px]"
        style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
      />
    </div>
  );
}
