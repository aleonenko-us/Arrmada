import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import type { ConnectionItem } from "../../lib/connectionsApi";
import { connectionStatusLine, countsLine, kindLabel, needsFix } from "../../lib/connectionStatus";
import { Button, Menu, type MenuItem } from "../../ui";
import { StatusDot } from "./StatusDot";

/** The last Test's answer on a card. */
export interface CardTest {
  running?: boolean;
  ok?: boolean;
  detail?: string;
}

/** Where a card's Fix (or Set up) goes: a page, or an editor opened in place. */
export interface CardFix {
  to?: string;
  onClick?: () => void;
}

// ConnectionCard is one integration: its dot, name and kind, one status line ("Last OK 3
// min ago", or "Failing since 14:02: login failed · next try 15:00"), an indexer's last
// day, and one main action — Test, or Fix while it's failing (Set up when it isn't). Its
// id is the card's anchor, so a health warning's link lands on it; the targeted card is
// outlined.
export function ConnectionCard({ item, test, onTest, fix, menu, targeted, children }: {
  item: ConnectionItem;
  test?: CardTest;
  onTest?: () => void;
  fix?: CardFix;
  menu?: MenuItem[];
  targeted?: boolean;
  /** An editor opened inside the card. */
  children?: ReactNode;
}) {
  const line = connectionStatusLine(item);
  const counts = countsLine(item.counts);
  const failing = needsFix(item);
  const setUp = !item.configured && !!fix;
  const canTest = item.testable && !!onTest;

  const fixButton = (label: string) =>
    fix?.to ? (
      <Link to={fix.to} className="rounded-md bg-accent-grad px-2.5 py-1 text-[11.5px] font-semibold text-accent-ink no-underline">{label} →</Link>
    ) : (
      <Button size="sm" variant="primary" onClick={fix?.onClick}>{label}</Button>
    );

  return (
    <div
      id={item.card}
      className="flex min-w-0 scroll-mt-20 flex-col gap-1.5 rounded-xl p-4"
      style={{ background: "var(--panel)", border: `1px solid ${targeted ? "var(--accent)" : "var(--line)"}` }}
    >
      <div className="flex items-start gap-2.5">
        <span className="mt-[5px]"><StatusDot item={item} /></span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="text-[13.5px] font-semibold" style={{ opacity: item.state === "disabled" ? 0.6 : 1 }}>{item.name}</span>
            <span className="rounded px-1.5 py-0.5 font-mono text-[9.5px] uppercase" style={{ background: "var(--panel-2)", color: "var(--ink-faint)" }}>
              {kindLabel(item)}
            </span>
            {item.managed_by === "prowlarr" && (
              <span className="rounded px-1.5 py-0.5 text-[10px] font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent)" }} title="Its name, address and key come from Prowlarr; the rest is yours">
                Managed by Prowlarr
              </span>
            )}
            {item.source === "env" && (
              <span className="rounded px-1.5 py-0.5 text-[10px]" style={{ background: "var(--panel-2)", color: "var(--ink-faint)" }} title="Set when Arrmada was installed; a value saved here replaces it">
                from install
              </span>
            )}
          </div>
          {item.subtitle && <div className="mt-0.5 truncate text-[11px] text-ink-faint" title={item.subtitle}>{item.subtitle}</div>}
        </div>
        {menu && menu.length > 0 && (
          <Menu
            trigger={<span aria-hidden className="text-[15px] leading-none">⋯</span>}
            label={`More for ${item.name}`}
            items={menu}
            triggerClassName="flex-none rounded-md px-2 py-1"
            triggerStyle={{ color: "var(--ink-dim)", border: "1px solid var(--line)" }}
          />
        )}
      </div>

      <div className="break-words text-[11.5px]" style={{ color: line.color }}>{line.text}</div>
      {counts && <div className="text-[11px] text-ink-faint">{counts}</div>}
      {item.detail && <div className="break-words text-[11.5px] text-ink-dim">{item.detail}</div>}
      {test && !test.running && test.detail && (
        <div role="status" className="break-words font-mono text-[11px]" style={{ color: test.ok ? "var(--good)" : "var(--reject)" }}>
          {test.ok ? "✓ " : "✕ "}{test.detail}
        </div>
      )}

      {(canTest || failing || setUp || (fix && item.fix_path)) && (
        <div className="mt-1 flex flex-wrap items-center gap-2">
          {setUp ? fixButton("Set up") : failing && fix ? fixButton("Fix") : null}
          {canTest && (
            <Button size="sm" variant="secondary" onClick={onTest} busy={test?.running} busyLabel="Testing…">Test</Button>
          )}
          {!setUp && !failing && fix?.to && item.fix_path && (
            <Link to={fix.to} className="text-[11.5px] font-semibold no-underline" style={{ color: "var(--accent)" }}>Edit →</Link>
          )}
        </div>
      )}
      {children}
    </div>
  );
}
