import type { Series, SeriesEpisodeDownload } from "../../lib/api";
import type { QueueProgressItem } from "../../lib/queueLive";

// downloadingCount is how many episodes of the show have a download in flight.
export function downloadingCount(s: Series): number {
  let n = 0;
  for (const sn of s.seasons ?? []) for (const e of sn.episodes ?? []) if (e.download) n++;
  return n;
}

// downloadHashes is the torrents behind the show's in-flight episodes (lower-cased), and
// whether every downloading episode has one — without, only a poll can move its bar.
export function downloadHashes(s: Series): { hashes: string[]; complete: boolean } {
  const out = new Set<string>();
  let complete = true;
  for (const sn of s.seasons ?? []) {
    for (const e of sn.episodes ?? []) {
      if (!e.download) continue;
      if (e.download.hash) out.add(e.download.hash.toLowerCase());
      else complete = false;
    }
  }
  return { hashes: [...out], complete };
}

// mergeDownloads lays the light poll's answer over the loaded show: every episode takes its
// download from the poll (or loses it when the poll no longer lists it), and nothing else
// changes. The show object is returned untouched when no episode's download moved, so a
// quiet tick doesn't re-render the page.
export function mergeDownloads(s: Series, dls: SeriesEpisodeDownload[]): Series {
  const byEp = new Map(dls.map((d) => [`${d.season}:${d.episode}`, d]));
  return mapDownloads(s, (e) => {
    const d = byEp.get(`${e.season_number}:${e.episode_number}`);
    return d ? { state: d.state, progress: d.progress, hash: d.hash } : undefined;
  });
}

// patchProgress moves the bars a queue.progress event covers (by torrent hash); episodes
// whose torrent it doesn't name keep what they have.
export function patchSeriesProgress(s: Series, live: Map<string, QueueProgressItem>): Series {
  return mapDownloads(s, (e) => {
    const cur = e.download;
    const p = cur?.hash ? live.get(cur.hash.toLowerCase()) : undefined;
    return cur && p ? { ...cur, progress: p.progress } : cur;
  });
}

type EpisodeDownload = NonNullable<NonNullable<NonNullable<Series["seasons"]>[number]["episodes"]>[number]["download"]>;
type Episode = NonNullable<NonNullable<Series["seasons"]>[number]["episodes"]>[number];

function mapDownloads(s: Series, next: (e: Episode) => EpisodeDownload | undefined): Series {
  let changed = false;
  const seasons = (s.seasons ?? []).map((sn) => {
    let seasonChanged = false;
    const episodes = (sn.episodes ?? []).map((e) => {
      const n = next(e);
      const cur = e.download;
      if (cur?.state === n?.state && cur?.progress === n?.progress && cur?.hash === n?.hash) return e;
      seasonChanged = true;
      return { ...e, download: n };
    });
    if (!seasonChanged) return sn;
    changed = true;
    return { ...sn, episodes };
  });
  return changed ? { ...s, seasons } : s;
}
