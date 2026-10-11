// Times and Prowlarr sync results as the Connections cards word them.
import type { ProwlarrSyncResult } from "./api";
import { toMs } from "./taskTime";

// hhmm is a time as the server's messages word it: "14:02" (local, 24-hour).
export function hhmm(v?: string | null): string {
  const ms = toMs(v);
  if (ms === null) return "—";
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// prowlarrSyncMessage says what a Prowlarr sync changed: "Added 2, updated 1, turned off
// 1 (disabled or removed in Prowlarr). Skipped 1 usenet indexer: Arrmada has no usenet
// download client." It claims FlareSolverr only when Prowlarr itself lists the proxy.
export function prowlarrSyncMessage(r: ProwlarrSyncResult): string {
  const parts: string[] = [];
  if (r.added) parts.push(`added ${r.added}`);
  if (r.updated) parts.push(`updated ${r.updated}`);
  if (r.reenabled) parts.push(`turned ${r.reenabled} back on`);
  if (r.disabled) parts.push(`turned off ${r.disabled} (disabled or removed in Prowlarr)`);
  let msg = parts.length > 0 ? `${parts.join(", ")}.` : "Nothing changed.";
  msg = msg.charAt(0).toUpperCase() + msg.slice(1);
  if (r.unchanged && parts.length > 0) msg += ` ${plural(r.unchanged, "indexer")} unchanged.`;
  if (r.skipped_usenet) msg += ` Skipped ${plural(r.skipped_usenet, "usenet indexer")}: Arrmada has no usenet download client.`;
  if (r.flaresolverr_ready) msg += " Prowlarr has a FlareSolverr proxy for Cloudflare trackers.";
  for (const n of r.notes ?? []) msg += ` ${n}`;
  return msg;
}
