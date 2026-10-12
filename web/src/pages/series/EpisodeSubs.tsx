import { useEffect, useMemo, useState, type RefObject } from "react";
import { api, type SubFileEntry } from "../../lib/api";
import { useQuery } from "../../lib/query";
import { wantLine } from "../../components/subtitles/WantChip";

// Subtitle state on the Series page (SUB-29): one compact chip per episode with a file,
// opening the per-file drawer. The show's episodes are read from GET
// /subtitles/library?media=tv&series=ID, which probes each file — so it is only asked once
// the episode cards scroll into view, never on page load.

// useSeenOnce reports whether the element has been on screen (or near it) at least once.
export function useSeenOnce(ref: RefObject<HTMLElement>): boolean {
  const [seen, setSeen] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (seen || !el) return;
    if (typeof IntersectionObserver === "undefined") { setSeen(true); return; }
    const io = new IntersectionObserver((es) => { if (es.some((e) => e.isIntersecting)) setSeen(true); }, { rootMargin: "0px 0px 200px 0px" });
    io.observe(el);
    return () => io.disconnect();
  }, [ref, seen]);
  return seen;
}

// useEpisodeSubs is the show's per-episode subtitle state by "season:episode", once
// enabled; null until then (and while the first answer is on its way).
export function useEpisodeSubs(seriesId: number, enabled: boolean): { bySxE: Map<string, SubFileEntry> | null; refetch: () => void } {
  const q = useQuery(`subs-episodes:${seriesId}`, () => api.subtitleSeriesEpisodes(seriesId), { enabled, staleMs: 60_000 });
  const bySxE = useMemo(() => (q.data ? new Map(q.data.map((f) => [`${f.season}:${f.episode}`, f])) : null), [q.data]);
  return { bySxE, refetch: () => void q.refetch() };
}

// EpisodeSubChip is "subs ✓" or "subs · 1 missing", with each kept language's state on hover.
export function EpisodeSubChip({ entry, onOpen }: { entry: SubFileEntry; onOpen: () => void }) {
  const done = entry.missing === 0;
  const title = (entry.languages ?? []).map((l) => wantLine(l).text).join("\n");
  return (
    <button
      type="button"
      onClick={onOpen}
      title={`${title}\nOpen this episode's subtitles`}
      className="rounded px-1.5 py-0.5 font-mono text-[10px] font-bold hover:opacity-80"
      style={done ? { background: "var(--good-soft, rgba(127,176,105,.16))", color: "var(--good)" } : { background: "var(--avoid-soft)", color: "var(--avoid)" }}
    >
      {done ? "subs ✓" : `subs · ${entry.missing} missing`}
    </button>
  );
}
