import { ServerMark } from "./marks";
import { SERVER_FILL, SERVER_INK, isMediaServer, safeWatchURL, watchLabel, type WatchLink } from "./servers";

// WatchButton is one "Watch on Plex" link. The main one is the accent button; the others
// are pills in their server's brand colours, so two servers read as two choices, not two
// equal calls to action. compact is the small tag a title's staff page shows beside its
// IMDb and TMDB links ("▶ Plex"). Links open in a new tab and only ever point at http(s).
export function WatchButton({ link, primary = false, compact = false, className }: { link: WatchLink; primary?: boolean; compact?: boolean; className?: string }) {
  const href = safeWatchURL(link.url);
  if (!href) return null;
  const label = watchLabel(link);
  const known = isMediaServer(link.server) ? link.server : null;
  const base = "inline-flex items-center gap-1.5 font-semibold no-underline";
  const brand = known
    ? { background: SERVER_FILL[known], color: SERVER_INK[known] }
    : { background: "var(--panel-2)", color: "var(--ink)", border: "1px solid var(--line)" };
  if (compact) {
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" title={`Watch on ${label}`} className={`${base} gap-1 rounded px-1.5 py-0.5 font-mono text-xs font-bold ${className ?? ""}`} style={brand}>
        {known ? <ServerMark server={known} size={11} /> : <span aria-hidden="true">▶</span>}
        {label}
      </a>
    );
  }
  if (primary) {
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" className={`${base} rounded-lg bg-accent-grad px-4 py-2 text-base text-accent-ink ${className ?? ""}`}>
        <span aria-hidden="true">▶</span>
        Watch on {label}
      </a>
    );
  }
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className={`${base} rounded-full px-3 py-1.5 text-sm ${className ?? ""}`} style={brand}>
      {known ? <ServerMark server={known} size={13} /> : <span aria-hidden="true">▶</span>}
      Watch on {label}
    </a>
  );
}

// WatchPills is a title's Watch links, one per server that has it, in the order given
// (the API puts the viewer's own server first). Nothing renders when no link is usable.
export function WatchPills({ links, compact = false, className }: { links: readonly WatchLink[]; compact?: boolean; className?: string }) {
  const usable = links.filter((l) => safeWatchURL(l.url));
  if (usable.length === 0) return null;
  return (
    <div className={`flex flex-wrap items-center ${compact ? "gap-1.5" : "gap-2"} ${className ?? ""}`}>
      {usable.map((l, i) => <WatchButton key={`${l.server}:${l.url}`} link={l} primary={!compact && i === 0} compact={compact} />)}
    </div>
  );
}
