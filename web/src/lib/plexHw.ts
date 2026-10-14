import type { InsightsStream } from "./api";

export interface HwBadge {
  /** "HW dec+enc", "HW enc", "HW dec" or "CPU" (Plex); "HW (vaapi)" or "CPU" (Jellyfin). */
  label: string;
  /** Hardware transcoding was asked for but Plex is encoding on the CPU. Never for Jellyfin. */
  fellBack: boolean;
}

type HwFields = Pick<InsightsStream, "decision" | "hw_decode" | "hw_encode" | "hw_requested"> & Partial<Pick<InsightsStream, "server" | "hw_transcode" | "hw_title">>;

// hwBadge says where a transcode really runs. For Plex it reads what Plex reports using
// (hwDecoding/hwEncoding), not "hardware requested" — with hardware transcoding switched
// on, Plex still falls back to the CPU when the GPU can't take a stream. Jellyfin only
// says which acceleration is configured ("vaapi", "qsv"…), so its badge names that and
// never claims a fallback it can't see. Direct play and direct stream (a remux: video and
// audio copied) don't transcode, so no badge.
export function hwBadge(s: HwFields): HwBadge | null {
  if (s.decision !== "transcode") return null;
  if (s.server === "jellyfin") {
    return { label: s.hw_transcode && s.hw_title ? `HW (${s.hw_title})` : "CPU", fellBack: false };
  }
  const label = s.hw_decode && s.hw_encode ? "HW dec+enc" : s.hw_encode ? "HW enc" : s.hw_decode ? "HW dec" : "CPU";
  return { label, fellBack: !!s.hw_requested && !s.hw_encode };
}
