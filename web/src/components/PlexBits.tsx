import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { plexLibraryApi, type WatchStats } from "../lib/plexLibraryApi";
import { ago } from "../lib/taskTime";
import { WatchPills } from "./brand/WatchPills";
import type { WatchLink } from "./brand/servers";

// TitleWatchPills is the "Plex" / "Jellyfin" links beside IMDb and TMDB on a title's page:
// each opens the title in that server's own app (Jellyfin at its public address), the
// viewer's own server first. Asked for after the page has loaded, and not shown at all
// when no server has the title (or none is connected).
export function TitleWatchPills({ media, tmdbId }: { media: "movie" | "series"; tmdbId: number }) {
  const [links, setLinks] = useState<WatchLink[]>([]);
  useEffect(() => {
    let live = true;
    setLinks([]);
    if (tmdbId > 0) plexLibraryApi.watchLinks(media, tmdbId).then((ls) => { if (live) setLinks(ls); }).catch(() => {});
    return () => { live = false; };
  }, [media, tmdbId]);
  return <WatchPills links={links} compact />;
}

// WatchedBy is the "Watched by 3 · last played 2 days ago" line under a title's header,
// from the Plex play history; tap it for the names. Staff pages only. Nothing shows when
// Plex isn't connected or nobody has played it.
export function WatchedBy({ kind, id }: { kind: "movies" | "series"; id: number }) {
  const [ws, setWs] = useState<WatchStats | null>(null);
  useEffect(() => {
    let live = true;
    setWs(null);
    plexLibraryApi.watchStats(kind, id).then((s) => { if (live) setWs(s); }).catch(() => {});
    return () => { live = false; };
  }, [kind, id]);
  if (!ws || !ws.available || ws.plays === 0 || ws.users.length === 0) return null;
  const names = ws.users.map((u) => u.name || "Someone");
  return (
    <details className="mt-2 text-sm text-ink-dim">
      <summary className="cursor-pointer select-none" title={names.join(", ")}>
        Watched by {ws.users.length === 1 ? names[0] : ws.users.length} · last played {ago(ws.last_played)}
      </summary>
      <ul className="m-0 mt-1.5 flex list-none flex-col gap-1 p-0 pl-3">
        {ws.users.map((u, i) => (
          <li key={u.id}>
            {/* Their People page in Insights (staff, like this line). */}
            {u.id ? <Link to={`/insights/people/${encodeURIComponent(u.id)}`} className="text-ink hover:underline">{names[i]}</Link> : <span className="text-ink">{names[i]}</span>}
            <span className="text-ink-faint"> · {u.plays} play{u.plays === 1 ? "" : "s"} · {ago(u.last_played)}</span>
          </li>
        ))}
      </ul>
    </details>
  );
}
