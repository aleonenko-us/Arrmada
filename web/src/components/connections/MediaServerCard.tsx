import type { ReactNode } from "react";
import { ServerTile } from "../brand/ServerBadge";
import { SERVER_LABEL, type MediaServerKind } from "../brand/servers";
import type { ConnectionItem, MediaServerUse } from "../../lib/connectionsApi";
import { connectionStatusLine, needsFix } from "../../lib/connectionStatus";
import { Button, StatusChip, type Tone } from "../../ui";
import type { CardTest } from "./ConnectionCard";

const USE_LABEL: Record<MediaServerUse, string> = {
  watch: "Watch links",
  library: "Library updates",
  signin: "Sign-in",
  insights: "Insights",
};
const ALL_USES: MediaServerUse[] = ["watch", "library", "signin", "insights"];

// What an unconnected server's card says about it.
const PITCH: Record<MediaServerKind, string> = {
  plex: "Watch links, library updates, Plex sign-in and Insights.",
  jellyfin: "Free and open source. Connect it on its own or next to Plex.",
  emby: "Watch links and library updates from your Emby server.",
};

// The chip beside the server's name: its health in one word.
export function serverChip(it: ConnectionItem): { tone: Tone; label: string } {
  if (!it.configured) return { tone: "faint", label: "Not connected" };
  switch (it.state) {
    case "ok": return { tone: "good", label: "Connected" };
    case "warn": return { tone: "avoid", label: "Needs a look" };
    case "failing": return { tone: "avoid", label: "Failing" };
    case "backing_off": return { tone: "reject", label: "Paused" };
    case "disabled": return { tone: "faint", label: "Off" };
    default: return { tone: "faint", label: "Not checked yet" };
  }
}

// MediaServerCard is one media server on Settings → Connections: its mark, name, which
// server it is, a status chip and line, what Arrmada uses it for, and Test / Edit (Fix
// while it's failing). A server that isn't connected is a quiet dashed card with one
// Connect button. The editor opens inside the card (children). Its id is the card's
// anchor, so a health warning's link lands on it.
export function MediaServerCard({ server, item, identity, test, onTest, onEdit, editing, targeted, children }: {
  server: MediaServerKind;
  item: ConnectionItem;
  /** Which server it is: "Basement · Jellyfin 10.10.3". The item's detail when absent. */
  identity?: string;
  test?: CardTest;
  onTest?: () => void;
  /** Opens (or closes) the editor inside the card. Absent: this viewer can't change the
   * connection (an admin's), so the card is read-only. */
  onEdit?: () => void;
  editing: boolean;
  targeted?: boolean;
  children?: ReactNode;
}) {
  const name = SERVER_LABEL[server];
  const frame = `flex min-w-0 scroll-mt-20 flex-col gap-3 rounded-xl p-4 ${editing ? "md:col-span-2" : ""}`;
  const border = (style: string) => `1px ${style} ${targeted ? "var(--accent)" : "var(--line)"}`;

  if (!item.configured) {
    return (
      <div id={item.card} className={frame} style={{ border: border(editing ? "solid" : "dashed"), background: editing ? "var(--panel)" : "transparent" }}>
        <div className="flex items-center gap-3">
          <ServerTile server={server} size={36} />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-md font-semibold">{name}</span>
              <StatusChip tone="faint" size="xs">Not connected</StatusChip>
            </div>
            <p className="m-0 mt-0.5 text-sm text-ink-dim">{PITCH[server]}</p>
          </div>
        </div>
        <div>
          {onEdit ? (
            <Button size="sm" variant={editing ? "ghost" : "secondary"} onClick={onEdit} aria-expanded={editing}>
              {editing ? "Close" : `Connect ${name}`}
            </Button>
          ) : (
            <span className="text-xs text-ink-faint">An admin connects {name}.</span>
          )}
        </div>
        {children}
      </div>
    );
  }

  const chip = serverChip(item);
  const line = connectionStatusLine(item);
  const failing = needsFix(item);
  const who = identity || item.detail;
  const uses = item.used_for ?? ALL_USES;

  return (
    <div id={item.card} className={frame} style={{ background: "var(--panel)", border: border("solid") }}>
      <div className="flex items-start gap-3">
        <ServerTile server={server} />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-lg font-semibold leading-tight">{name}</span>
            <StatusChip tone={chip.tone} size="xs">{chip.label}</StatusChip>
          </div>
          {who && <div className="mt-0.5 truncate text-sm text-ink-dim" title={who}>{who}</div>}
          {item.subtitle && <div className="truncate font-mono text-xs text-ink-faint" title={item.subtitle}>{item.subtitle}</div>}
        </div>
      </div>

      <div className="break-words text-sm" style={{ color: line.color }}>{line.text}</div>
      {identity && item.detail && <div className="break-words text-sm text-ink-dim">{item.detail}</div>}
      {test && !test.running && test.detail && (
        <div role="status" className="break-words font-mono text-xs" style={{ color: test.ok ? "var(--good-text)" : "var(--reject-text)" }}>
          {test.ok ? "✓ " : "✕ "}{test.detail}
        </div>
      )}

      {uses.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <span className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Used for</span>
          <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0" aria-label={`What Arrmada uses ${name} for`}>
            {uses.map((u) => (
              <li key={u} className="rounded-full px-2 py-0.5 text-xs" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink-dim)" }}>
                {USE_LABEL[u] ?? u}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2">
        {failing && !editing && onEdit && <Button size="sm" variant="primary" onClick={onEdit}>Fix</Button>}
        {item.testable && onTest && (
          <Button size="sm" variant="secondary" onClick={onTest} busy={test?.running} busyLabel="Testing…">Test</Button>
        )}
        {onEdit ? (
          <Button size="sm" variant="ghost" onClick={onEdit} aria-expanded={editing}>{editing ? "Close" : "Edit"}</Button>
        ) : (
          <span className="text-xs text-ink-faint">An admin changes this connection.</span>
        )}
      </div>
      {children}
    </div>
  );
}
