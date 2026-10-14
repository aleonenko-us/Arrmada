import { ServerMark } from "./marks";
import { SERVER_FILL, SERVER_INK, SERVER_LABEL, type MediaServerKind } from "./servers";

const BADGE_SIZE = {
  xs: { box: "gap-1 px-1.5 py-0.5 text-2xs", mark: 10 },
  sm: { box: "gap-1 px-2 py-0.5 text-xs", mark: 12 },
} as const;

// ServerBadge is the tiny pill naming a media server ("Plex", "Jellyfin") on its brand
// fill, for a stream, a history row or a Watch link's server.
export function ServerBadge({ server, size = "xs", className }: { server: MediaServerKind; size?: keyof typeof BADGE_SIZE; className?: string }) {
  const s = BADGE_SIZE[size];
  return (
    <span
      className={`inline-flex flex-none items-center rounded-full font-bold leading-none ${s.box} ${className ?? ""}`}
      style={{ background: SERVER_FILL[server], color: SERVER_INK[server] }}
    >
      <ServerMark server={server} size={s.mark} />
      {SERVER_LABEL[server]}
    </span>
  );
}

// ServerTile is a server's mark on a rounded square of its brand fill: the icon at the
// head of its card on Settings → Connections.
export function ServerTile({ server, size = 40, muted }: { server: MediaServerKind; size?: number; muted?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className="grid flex-none place-items-center rounded-xl"
      style={{
        width: size,
        height: size,
        background: muted ? "var(--panel-2)" : SERVER_FILL[server],
        color: muted ? "var(--ink-faint)" : SERVER_INK[server],
        border: muted ? "1px solid var(--line)" : undefined,
      }}
    >
      <ServerMark server={server} size={Math.round(size * 0.55)} />
    </span>
  );
}
