import type { MediaServerKind } from "./servers";

// Small marks for the media servers: simple geometric versions, not the official logos.
// They draw in currentColor, so they take the ink of whatever they sit on, and they are
// decoration only (aria-hidden): the server's name is always written next to them.

interface MarkProps {
  /** Pixels; the marks are square. */
  size?: number;
  className?: string;
}

// Plex: the chevron.
export function PlexMark({ size = 16, className }: MarkProps) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false" className={className}>
      <path fill="currentColor" stroke="currentColor" strokeWidth={1.2} strokeLinejoin="round" d="M5.5 3h6.6l6.4 9-6.4 9H5.5l6.4-9z" />
    </svg>
  );
}

// Jellyfin: a triangle with a smaller one inside it.
export function JellyfinMark({ size = 16, className }: MarkProps) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false" className={className}>
      <path
        fill="currentColor"
        fillRule="evenodd"
        stroke="currentColor"
        strokeWidth={1.4}
        strokeLinejoin="round"
        d="M12 2.8 21.6 20H2.4zm0 5.6-5 8.9h10z"
      />
      <path fill="currentColor" stroke="currentColor" strokeWidth={1.2} strokeLinejoin="round" d="M12 11.3l2.3 4.1H9.7z" />
    </svg>
  );
}

export function ServerMark({ server, ...rest }: MarkProps & { server: MediaServerKind }) {
  return server === "plex" ? <PlexMark {...rest} /> : <JellyfinMark {...rest} />;
}
