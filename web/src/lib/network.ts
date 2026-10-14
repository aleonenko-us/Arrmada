import type { ActivityDownload, DownloadClient, NetworkPreference, RankedRelease } from "./api";

// Usenet beside torrents, in the few words every page needs: which rows are usenet ones,
// whether a release has a client to go to, and the preference's choices. Small and
// dependency-free, so the staff pages that use it don't pull anything else in.

/** A usenet job's download key: "sab-" and SABnzbd's own id (internal/download/key.go). */
export const USENET_KEY_PREFIX = "sab-";

/** The download client kinds that fetch from Usenet. */
const USENET_KINDS = new Set(["sabnzbd"]);

/** A client kind's own name, for copy that names it ("SABnzbd's path"). */
export function clientKindName(kind: string): string {
  return kind === "sabnzbd" ? "SABnzbd" : kind === "qbittorrent" ? "qBittorrent" : kind;
}

/** Whether a download client fetches from Usenet. */
export function isUsenetClient(c: Pick<DownloadClient, "kind">): boolean {
  return USENET_KINDS.has(c.kind);
}

/** Whether any of these clients is a usenet one (switched on or not). */
export function hasUsenetClient(clients: Pick<DownloadClient, "kind">[] | undefined): boolean {
  return !!clients?.some(isUsenetClient);
}

/**
 * Whether a queue row is a usenet job: by the transport the server names, else by its key,
 * which for a usenet job always starts "sab-" (a torrent's is a hex hash).
 */
export function isUsenetDownload(it: Pick<ActivityDownload, "hash" | "transport">): boolean {
  if (it.transport) return it.transport === "usenet";
  return it.hash.toLowerCase().startsWith(USENET_KEY_PREFIX);
}

// The server's reasons for a release no client of its network can take
// (automation/coordinator.go usenetReject / torrentReject).
export const NO_USENET_CLIENT = "Usenet release — no usenet download client is set up";
export const NO_TORRENT_CLIENT = "Torrent release — no torrent download client is set up";

/** Whether a release is listed but has no download client of its network to go to. */
export function noClientFor(rel: Pick<RankedRelease, "reject_reason">): boolean {
  return rel.reject_reason === NO_USENET_CLIENT || rel.reject_reason === NO_TORRENT_CLIENT;
}

export function isUsenetRelease(rel: Pick<RankedRelease, "transport">): boolean {
  return rel.transport === "usenet";
}

/** Settings → Downloads → #network: when a release is on both Usenet and torrents. */
export const NETWORK_PREFERENCES: readonly { value: NetworkPreference; label: string }[] = [
  { value: "best", label: "Best release wins" },
  { value: "usenet", label: "Prefer Usenet" },
  { value: "torrent", label: "Prefer torrents" },
];

/** Whether a usenet job has failed (SABnzbd gave up on it): it can be retried. */
export function usenetFailed(it: Pick<ActivityDownload, "phase" | "state" | "stage">): boolean {
  return it.stage === "failed" || it.phase === "error" || it.state === "error";
}

// The pipeline's generic words for any download stopped with an error (pipeline/stage.go):
// for a usenet job, SABnzbd's own reason says more.
const GENERIC_FAIL = "The download client stopped it with an error";

/**
 * usenetFailLine is why a usenet job failed, in plain words: the server's reading of it
 * when it has one of its own, else SABnzbd's reason ("Out of retention"), else that it
 * failed.
 */
export function usenetFailLine(it: Pick<ActivityDownload, "why" | "fail_message">): string {
  const why = it.why?.trim();
  if (why && why !== GENERIC_FAIL) return why;
  return it.fail_message?.trim() || "SABnzbd couldn't finish this download";
}

/**
 * leftLine is a usenet job's "1.2 GB left · ETA 4m": what SABnzbd still has to fetch (its
 * own figure when it gives one, else worked out from the progress) and when it'll be done.
 */
export function leftLine(it: Pick<ActivityDownload, "size_bytes" | "progress" | "eta_seconds" | "remaining_bytes">, bytes: (n: number) => string, eta: (s: number) => string): string {
  const left = it.remaining_bytes && it.remaining_bytes > 0 ? it.remaining_bytes : Math.max(0, Math.round(it.size_bytes * (1 - it.progress)));
  return `${bytes(left)} left · ETA ${eta(it.eta_seconds)}`;
}
