import type { ActivityDownload, DownloadClientsState } from "./api";

// The Activity queue's two extra ways to narrow its list: Stalled (a pill beside
// Downloading, Finishing and Seeding) and, with several download clients, which client a
// download is in.

/**
 * Whether a download is stalled. The server decides (automation.QueueStall): trying to
 * download, nothing arriving, and no progress for half an hour by the clock the stall
 * fail-over judges by, or by qBittorrent's own "stalled" held that long. A moment with no
 * peers sending isn't a stall, so this never reads the client's phase itself.
 */
export function isStalled(d: Pick<ActivityDownload, "stalled">): boolean {
  return d.stalled === true;
}

/** Minutes a stalled download has gone without progress; null when it isn't stalled or the time is unknown. */
export function stalledMinutes(d: Pick<ActivityDownload, "stalled" | "stalled_since">, nowSec: number): number | null {
  if (!isStalled(d) || !d.stalled_since || d.stalled_since > nowSec) return null;
  return (nowSec - d.stalled_since) / 60;
}

/**
 * Whether rows name their download client: only with more than one client switched on.
 * With one, every row would say the same thing. A switched-off client gets no downloads,
 * so it isn't counted.
 */
export function showClientNames(clients: DownloadClientsState | null | undefined): boolean {
  return !!clients && clients.enabled > 1;
}

export interface QueueClient {
  id: number;
  name: string;
  count: number;
}

/** The clients the given downloads are in, by name, with how many each holds. */
export function queueClients(list: readonly Pick<ActivityDownload, "client_id" | "client_name">[]): QueueClient[] {
  const byId = new Map<number, QueueClient>();
  for (const d of list) {
    if (!d.client_id) continue;
    const c = byId.get(d.client_id);
    if (c) c.count += 1;
    else byId.set(d.client_id, { id: d.client_id, name: d.client_name || `Client ${d.client_id}`, count: 1 });
  }
  return [...byId.values()].sort((a, b) => a.name.localeCompare(b.name) || a.id - b.id);
}

/** The downloads in one client, by its id as the address holds it ("" or nonsense: every download). */
export function inClient<T extends Pick<ActivityDownload, "client_id">>(list: T[], client: string): T[] {
  const id = Number(client);
  if (!client || !Number.isFinite(id) || id <= 0) return list;
  return list.filter((d) => d.client_id === id);
}
