// Times, Prowlarr sync results and Prowlarr's own address, as the Connections cards word them.
import type { ProwlarrInfo, ProwlarrLastSync, ProwlarrSyncResult } from "./api";
import { toMs } from "./taskTime";

// hhmm is a time as the server's messages word it: "14:02" (local, 24-hour).
export function hhmm(v?: string | null): string {
  const ms = toMs(v);
  if (ms === null) return "—";
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

export interface StatusLine {
  text: string;
  color: string;
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// The step that makes a usenet indexer useful, wherever one is waiting for it.
export const ADD_SABNZBD = "add SABnzbd in Settings → Connections";

// usenetIndexerLine is a usenet (Newznab) indexer's state in its card: searched once a
// usenet download client exists, and until then what to do about it.
export function usenetIndexerLine(hasUsenetClient: boolean): string {
  return hasUsenetClient
    ? "Usenet indexer: searched, and what it finds goes to SABnzbd."
    : `Not searched until you ${ADD_SABNZBD}.`;
}

// prowlarrSyncMessage says what a Prowlarr sync changed: "Added 2, updated 1, turned off
// 1 (disabled or removed in Prowlarr). Skipped 1 usenet indexer: they come in once you
// add SABnzbd in Settings → Connections." It claims FlareSolverr only when Prowlarr itself
// lists the proxy.
export function prowlarrSyncMessage(r: ProwlarrSyncResult): string {
  const parts: string[] = [];
  if (r.added) parts.push(`added ${r.added}`);
  if (r.updated) parts.push(`updated ${r.updated}`);
  if (r.reenabled) parts.push(`turned ${r.reenabled} back on`);
  if (r.disabled) parts.push(`turned off ${r.disabled} (disabled or removed in Prowlarr)`);
  let msg = parts.length > 0 ? `${parts.join(", ")}.` : "Nothing changed.";
  msg = msg.charAt(0).toUpperCase() + msg.slice(1);
  if (r.unchanged && parts.length > 0) msg += ` ${plural(r.unchanged, "indexer")} unchanged.`;
  if (r.skipped_usenet) msg += ` Skipped ${plural(r.skipped_usenet, "usenet indexer")}: they come in once you ${ADD_SABNZBD}.`;
  if (r.flaresolverr_ready) msg += " Prowlarr has a FlareSolverr proxy for Cloudflare trackers.";
  for (const n of r.notes ?? []) msg += ` ${n}`;
  return msg;
}

// prowlarrLastSyncLine is the Prowlarr panel's "Last synced 14:00: 2 added, 1 turned off"
// or "Last sync failed at 14:00: invalid API key", for the hourly sync and Sync now alike.
export function prowlarrLastSyncLine(ls: ProwlarrLastSync | undefined): StatusLine | null {
  if (!ls) return null;
  if (ls.error) return { text: `Last sync failed at ${hhmm(ls.at)}: ${ls.error}`, color: "var(--reject-text)" };
  const parts: string[] = [];
  if (ls.added) parts.push(`${ls.added} added`);
  if (ls.updated) parts.push(`${ls.updated} updated`);
  if (ls.reenabled) parts.push(`${ls.reenabled} back on`);
  if (ls.disabled) parts.push(`${ls.disabled} turned off`);
  return { text: `Last synced ${hhmm(ls.at)}: ${parts.length > 0 ? parts.join(", ") : "nothing changed"}`, color: "var(--ink-dim)" };
}

// prowlarrUILink is where "Open Prowlarr" goes: a saved address for a Prowlarr on another
// machine, else this host on the port Prowlarr's UI is published on — which install.sh
// may have moved to 9697 or 9698 when 9696 was taken. Only http(s) addresses are linked.
export function prowlarrUILink(info: Pick<ProwlarrInfo, "ui_url" | "ui_port"> | undefined, loc: { protocol: string; hostname: string }): string {
  const saved = info?.ui_url?.trim();
  if (saved && /^https?:\/\//i.test(saved)) return saved;
  return `${loc.protocol}//${loc.hostname}:${info?.ui_port || "9696"}`;
}
