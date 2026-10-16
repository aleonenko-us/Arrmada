import type { PreviewRelease, PreviewResult, RequestPreview } from "./api";

// Words for a request's release preview (staff only; loaded with the preview panel). Pure,
// so the panel and the list row say the same thing and the wording is tested.

export type PreviewTone = "good" | "avoid" | "faint";

/** The headline: what approving would grab, or why it wouldn't grab anything. */
export function previewHeadline(p: RequestPreview | undefined): { tone: PreviewTone; title: string } {
  if (!p || (!p.result && p.state !== "checking")) return { tone: "faint", title: "Not checked yet" };
  if (p.state === "checking" && !p.result) return { tone: "faint", title: "Checking indexers…" };
  const r = p.result!;
  switch (r.outcome) {
    case "found":
      return { tone: "good", title: "Would grab" };
    case "unavailable":
      return { tone: "avoid", title: r.reason === "no-indexers" ? "No indexer set up for this" : "Indexers unavailable" };
    case "busy":
      return { tone: "faint", title: r.reason === "already-downloading" ? "Already downloading" : "Being searched right now" };
    case "not_wanted":
      return { tone: "faint", title: "Nothing to look for" };
  }
  return { tone: "avoid", title: "Nothing found yet" };
}

/** A release's facts, in reading order: "1080p", "WEB-DL", "x265", "HDR10", "22.4 GB", "40 seeders" or "Usenet". */
export function releaseFacts(rel: PreviewRelease): string[] {
  const out: string[] = [];
  if (rel.format) out.push(rel.format);
  if (rel.resolution) out.push(rel.resolution === "2160p" ? "4K" : rel.resolution);
  if (rel.source) out.push(rel.source);
  if (rel.codec) out.push(rel.codec);
  for (const h of rel.hdr ?? []) out.push(h);
  if (rel.size_gb > 0) out.push(formatGB(rel.size_gb));
  out.push(rel.transport === "usenet" ? "Usenet" : `${rel.seeders} ${rel.seeders === 1 ? "seeder" : "seeders"}`);
  return out;
}

export function formatGB(gb: number): string {
  return gb >= 100 ? `${Math.round(gb)} GB` : gb >= 1 ? `${gb.toFixed(1)} GB` : `${Math.round(gb * 1024)} MB`;
}

/**
 * A show's preview in one line: "Season 1: 1 pack · 1080p WEB-DL · 22.4 GB" or "Season 2:
 * 10 episodes · 1080p WEB-DL · 14.0 GB in all". "" for anything but a show's found preview.
 */
export function seasonLine(r: PreviewResult): string {
  if (!r.season || r.outcome !== "found" || r.releases.length === 0) return "";
  const packs = r.releases.filter((x) => x.kind && x.kind !== "episode").length;
  const eps = r.releases.length - packs;
  const parts: string[] = [];
  if (packs) parts.push(`${packs} ${packs === 1 ? "pack" : "packs"}`);
  if (eps) parts.push(`${eps} ${eps === 1 ? "episode" : "episodes"}`);
  const first = r.releases[0];
  const quality = [first.resolution === "2160p" ? "4K" : first.resolution, first.source].filter(Boolean).join(" ");
  const total = r.releases.reduce((n, x) => n + x.size_gb, 0);
  const size = total > 0 ? `${formatGB(total)}${r.releases.length > 1 ? " in all" : ""}` : "";
  return `Season ${r.season}: ${[parts.join(" + "), quality, size].filter(Boolean).join(" · ")}`;
}

/** Why nothing was found: "12 releases · 9 for other titles, 3 too few seeders". */
export function reasonsLine(r: PreviewResult): string {
  if (r.returned === 0) return r.outcome === "none" ? "The indexers returned no releases" : "";
  const head = `${r.returned} ${r.returned === 1 ? "release" : "releases"}`;
  const why = (r.top_reasons ?? []).map((x) => `${x.count} ${x.label}`);
  return why.length ? `${head} · ${why.join(", ")}` : head;
}

/** When it was checked: "just now", "12 min ago", "3 h ago", "2 days ago". */
export function checkedAgo(checkedAt: number, nowMs = Date.now()): string {
  if (!checkedAt) return "";
  const s = Math.max(0, Math.round(nowMs / 1000 - checkedAt));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  const d = Math.floor(s / 86400);
  return `${d} ${d === 1 ? "day" : "days"} ago`;
}

/**
 * Whether "Approve & grab this" applies to the approval the sheet would make: the preview
 * found something, its tokens are still good, it was ranked under the profile picked, and
 * (a show) the season it covers is still among those being approved.
 */
export function grabApplies(p: RequestPreview | undefined, profile: string, keep: number[] | null): boolean {
  if (!p?.result || p.result.outcome !== "found" || !p.grabbable || p.state === "checking") return false;
  if (profile && p.profile !== profile) return false;
  if (keep && p.result.season && !keep.includes(p.result.season)) return false;
  return true;
}

/** The list row's one line: "Would grab 1080p · BluRay · 12.0 GB", "Nothing found yet"… */
export function previewLine(p: RequestPreview | undefined): string {
  const h = previewHeadline(p);
  const r = p?.result;
  if (r?.outcome === "found" && r.releases.length > 0) {
    if (r.season) return `${h.title} — ${seasonLine(r)}`;
    return `${h.title} ${releaseFacts(r.releases[0]).slice(0, 4).join(" · ")}`;
  }
  return h.title;
}
