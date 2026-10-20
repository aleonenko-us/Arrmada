import type { MusicAlbum, MusicTrack } from "../../lib/api";

// The music file check's markers (MUS-15), in words: files gone from disk (flagged, never
// downloaded again by themselves), files not found once (not missing yet), and files
// changed outside Arrmada.

/** The one line every "Search for missing tracks" place says first (MUS-15). */
export const MISSING_MOVED_HINT = "Moved or retagged these files yourself? Run a library scan first; Arrmada links them again instead of downloading.";

/** An album row's line about its files, or "" when there's nothing to say. */
export function albumFileMarks(al: Pick<MusicAlbum, "missing_tracks" | "changed_tracks">): string {
  const parts: string[] = [];
  const gone = al.missing_tracks ?? 0;
  const changed = al.changed_tracks ?? 0;
  if (gone > 0) parts.push(`${gone} track file${gone === 1 ? "" : "s"} gone from disk`);
  if (changed > 0) parts.push(`${changed} changed outside Arrmada`);
  return parts.join(" · ");
}

/** A track's file marker, or null: [short words, the longer explanation]. */
export function trackFileMark(t: Pick<MusicTrack, "missing" | "not_found_since" | "changed_at" | "has_file">): [string, string] | null {
  if (t.missing) return ["gone from disk", "The file isn't on disk any more. Arrmada doesn't download it again by itself (it may only be renamed, or on a disk that's away): use Search for missing tracks. Its record is kept, so if the file comes back it's linked again."];
  if (t.not_found_since) return ["not found", "The last file check couldn't find this file. It's only marked gone if the next check, a day later, still can't."];
  if (t.changed_at && t.has_file) return ["changed", "The file was changed outside Arrmada (another tagger, say). It was measured again; its tags are as that tool left them."];
  return null;
}
