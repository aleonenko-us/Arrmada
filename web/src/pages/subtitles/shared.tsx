import { Link } from "react-router-dom";
import { LINKS } from "../../lib/links";

// What the Subtitles page and its settings (Settings → Subtitles & Convert) share.

export const card = "rounded-xl p-4";
export const cardStyle = { border: "1px solid var(--line)", background: "var(--panel)" } as const;

// KeptTracksNote says where embedded tracks stand. Subtitles never edits the video; dropping
// the languages you don't keep happens in Convert, under its own setting, when it re-encodes.
export function KeptTracksNote() {
  return (
    <>Embedded subtitle tracks stay inside the video. Convert can remove the languages you don't keep when it re-encodes a file — set that in <Link to={LINKS.convertSettings} style={{ color: "var(--accent)" }}>Convert's settings</Link>.</>
  );
}
