import type { ReactNode } from "react";
import { Menu, type MenuItem } from "./Menu";
import { StatusChip } from "./StatusChip";

export interface ToolbarSegment {
  /** The group's accessible name ("View", "Group by"). */
  label: string;
  value: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
}

export interface LibraryTool {
  label: string;
  onClick: () => void;
  title?: string;
  disabled?: boolean;
  /** Running: the item is disabled and reads busyLabel. */
  busy?: boolean;
  busyLabel?: string;
  /** A sweep's progress ("Searching… 2/5"): shown on the item and as a chip beside the
   *  menu, so it stays in sight while the menu is closed. */
  progress?: string;
  hidden?: boolean;
}

export interface ToolbarAction {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  title?: string;
  /** Running: disabled, aria-busy, and busyLabel (if given) in place of the label. */
  busy?: boolean;
  busyLabel?: string;
}

export interface LibraryToolbarProps {
  /** null while the list loads: no count yet. */
  count: number | null;
  /** Beside the count: a page switch, a queue chip. */
  lead?: ReactNode;
  segments?: ToolbarSegment[];
  /** Dropdown pickers ("View: Books · grid"), for a choice too long for a segmented toggle. */
  pickers?: ToolbarSegment[];
  /** A sort picker ("Sort: Recently added"). */
  sort?: ToolbarSegment;
  /** A ↑/↓ button beside the sort picker that reverses the order. */
  sortDir?: { dir: "asc" | "desc"; onToggle: () => void };
  select?: { active: boolean; onToggle: () => void };
  tools?: LibraryTool[];
  /** A small count on the tools button: something in the menu wants a look (possible duplicates). */
  toolsBadge?: number;
  /** Everyday buttons between the tools menu and the add buttons ("Search missing (4)"). */
  actions?: ToolbarAction[];
  /** A second add button before the primary ("+ Add author"). */
  secondary?: ToolbarAction;
  primary?: ToolbarAction;
}

const PANEL = { background: "var(--panel-2)", border: "1px solid var(--line)" };

// LibraryToolbar is the one header row of every library page (Movies, Series, Books,
// Music): the count on the left; on the right the segmented toggles, Select, a "Library
// tools" menu for the occasional jobs (Scan library, sweeps) and the "+ Add …" buttons.
// It wraps rather than squeezing: below sm the count takes its own line, and no button
// ever shrinks into a narrow column.
export function LibraryToolbar({ count, lead, segments = [], pickers = [], sort, sortDir, select, tools = [], toolsBadge = 0, actions = [], secondary, primary }: LibraryToolbarProps) {
  const shown = tools.filter((t) => !t.hidden);
  const running = shown.filter((t) => t.progress);
  const items: MenuItem[] = shown.map((t) => ({
    label: t.progress ?? (t.busy ? t.busyLabel ?? t.label : t.label),
    onSelect: t.onClick,
    disabled: t.disabled || t.busy,
    title: t.title,
  }));
  return (
    <div className="mb-4 flex flex-wrap items-center gap-2">
      <span className="flex w-full min-w-0 flex-wrap items-center gap-2.5 sm:w-auto">
        {lead}
        <span className="font-mono text-xs text-ink-faint">{count === null ? "" : `${count} in library`}</span>
        {running.map((t) => <StatusChip key={t.label} tone="accent" title={t.title}>{t.progress}</StatusChip>)}
      </span>
      <div className="flex flex-wrap items-center gap-2 sm:ml-auto">
        {segments.map((g) => (
          <div key={g.label} role="group" aria-label={g.label} className="inline-flex shrink-0 rounded-lg p-0.5" style={PANEL}>
            {g.options.map((o) => {
              const on = g.value === o.value;
              return (
                <button key={o.value} onClick={() => g.onChange(o.value)} aria-pressed={on} className="whitespace-nowrap rounded-md px-3 py-1.5 text-sm font-semibold capitalize" style={{ background: on ? "var(--accent)" : "transparent", color: on ? "var(--accent-ink)" : "var(--ink-faint)" }}>
                  {o.label}
                </button>
              );
            })}
          </div>
        ))}
        {[...pickers, ...(sort ? [sort] : [])].map((p) => (
          <span key={p.label} className="inline-flex shrink-0 items-center rounded-lg py-0.5 pl-2.5 pr-1" style={PANEL}>
            <label className="inline-flex items-center gap-1.5">
              <span className="font-mono text-2xs uppercase text-ink-faint">{p.label}</span>
              <select value={p.value} onChange={(e) => p.onChange(e.target.value)} aria-label={p.label} className="rounded-md bg-transparent py-1 pr-1 text-sm font-semibold" style={{ color: "var(--ink)" }}>
                {p.options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </label>
            {p === sort && sortDir && (
              <button
                type="button"
                onClick={sortDir.onToggle}
                aria-label={sortDir.dir === "asc" ? "Ascending: reverse the order" : "Descending: reverse the order"}
                title="Reverse the order"
                className="rounded-md px-1.5 py-1 text-sm font-semibold hover:bg-[var(--panel)]"
                style={{ color: "var(--ink)" }}
              >
                {sortDir.dir === "asc" ? "↑" : "↓"}
              </button>
            )}
          </span>
        ))}
        {select && (
          <button
            onClick={select.onToggle}
            aria-pressed={select.active}
            className="shrink-0 whitespace-nowrap rounded-lg px-3 py-2 text-base font-semibold"
            style={{ border: `1px solid ${select.active ? "var(--accent)" : "var(--line)"}`, background: select.active ? "var(--accent-soft)" : "var(--panel-2)", color: select.active ? "var(--accent-text)" : "var(--ink)" }}
          >
            {select.active ? "Done" : "Select"}
          </button>
        )}
        {items.length > 0 && (
          <Menu
            label="Library tools"
            trigger={<>
              <span aria-hidden="true">⋯</span> Tools
              {toolsBadge > 0 && <span aria-hidden="true" className="ml-1.5 rounded-full px-1.5 py-px font-mono text-xs" style={{ background: "var(--accent)", color: "var(--accent-ink)" }}>{toolsBadge}</span>}
            </>}
            items={items}
            triggerClassName="shrink-0 whitespace-nowrap rounded-lg px-3 py-2 text-base font-semibold"
            triggerStyle={{ ...PANEL, color: "var(--ink)" }}
            itemClassName="min-h-[40px]"
            // Portalled so the list is kept on screen: on a phone the button wraps to the
            // left edge, where a right-aligned list would open off the side.
            portal
          />
        )}
        {actions.map((a) => (
          <button key={a.label} onClick={a.onClick} disabled={a.disabled || a.busy} aria-busy={a.busy || undefined} title={a.title} className="shrink-0 whitespace-nowrap rounded-lg px-3 py-2 text-base font-semibold disabled:opacity-60" style={{ ...PANEL, color: "var(--ink)" }}>
            {a.busy && a.busyLabel ? a.busyLabel : a.label}
          </button>
        ))}
        {secondary && (
          <button onClick={secondary.onClick} disabled={secondary.disabled} title={secondary.title} className="shrink-0 whitespace-nowrap rounded-lg px-3.5 py-2 text-base font-semibold disabled:opacity-50" style={{ border: "1px solid var(--accent-line)", background: "var(--panel-2)", color: "var(--accent-text)" }}>
            {secondary.label}
          </button>
        )}
        {primary && (
          <button onClick={primary.onClick} disabled={primary.disabled} title={primary.title} className="bg-accent-grad shrink-0 whitespace-nowrap rounded-lg px-3.5 py-2 text-base font-semibold text-accent-ink disabled:opacity-50">
            {primary.label}
          </button>
        )}
      </div>
    </div>
  );
}
