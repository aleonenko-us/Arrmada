import { useId } from "react";
import type { MediaServerKind } from "./servers";

// Small marks for the media servers: simple geometric versions, not the official logos.
// They draw in currentColor, so they take the ink of whatever they sit on, and they are
// decoration only (aria-hidden): the server's name is always written next to them.

interface MarkProps {
  /** Pixels; the marks are square. */
  size?: number;
  className?: string;
}

// Plex: the chevron, with the notch at its point.
export function PlexMark({ size = 16, className }: MarkProps) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false" className={className}>
      <path fill="currentColor" stroke="currentColor" strokeWidth={0.8} strokeLinejoin="round" d="M4.6 2.2h7.3l7.5 9.8-7.5 9.8H4.6L12.1 12z" />
    </svg>
  );
}

// Jellyfin: a rounded triangle with a smaller one inside it. Draws in currentColor, or in
// the brand gradient (purple to blue) when `gradient` is set, as on a dark tile.
export function JellyfinMark({ size = 16, className, gradient }: MarkProps & { gradient?: boolean }) {
  const id = useId();
  const paint = gradient ? `url(#${id})` : "currentColor";
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false" className={className}>
      {gradient && (
        <defs>
          <linearGradient id={id} x1="4" y1="3" x2="20" y2="21" gradientUnits="userSpaceOnUse">
            <stop offset="0" stopColor="#aa5cc3" />
            <stop offset="1" stopColor="#00a4dc" />
          </linearGradient>
        </defs>
      )}
      <path fill="none" stroke={paint} strokeWidth={2.8} strokeLinejoin="round" d="M12 3.2 21.2 19.6H2.8z" />
      <path fill={paint} stroke={paint} strokeWidth={1.8} strokeLinejoin="round" d="M12 9.8l3.4 5.9H8.6z" />
    </svg>
  );
}

// Emby: a bold E whose bars end in a slant, like a play arrow.
export function EmbyMark({ size = 16, className }: MarkProps) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false" className={className}>
      <path fill="currentColor" d="M4 3h15.2l-2.3 3.7H8.4v3h7.3L13.5 13.3H8.4v3.3h10.6L19.4 21H4z" />
    </svg>
  );
}

export function ServerMark({ server, ...rest }: MarkProps & { server: MediaServerKind }) {
  if (server === "plex") return <PlexMark {...rest} />;
  if (server === "jellyfin") return <JellyfinMark {...rest} />;
  return <EmbyMark {...rest} />;
}
