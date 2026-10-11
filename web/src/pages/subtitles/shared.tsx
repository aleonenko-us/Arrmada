import { Link } from "react-router-dom";
import { LINKS } from "../../lib/links";

// What the Subtitles page and its settings (Settings → Subtitles & Convert) share.

// Common subtitle languages offered as toggle chips (ISO 639-1). The backend accepts any.
export const LANGS: { code: string; name: string }[] = [
  { code: "en", name: "English" }, { code: "es", name: "Spanish" }, { code: "fr", name: "French" },
  { code: "de", name: "German" }, { code: "it", name: "Italian" }, { code: "pt", name: "Portuguese" },
  { code: "nl", name: "Dutch" }, { code: "sv", name: "Swedish" }, { code: "pl", name: "Polish" },
  { code: "ru", name: "Russian" }, { code: "tr", name: "Turkish" }, { code: "ar", name: "Arabic" },
  { code: "hi", name: "Hindi" }, { code: "ja", name: "Japanese" }, { code: "ko", name: "Korean" },
  { code: "zh", name: "Chinese" },
];
export const langName = (code: string) => LANGS.find((l) => l.code === code)?.name ?? code.toUpperCase();

export const card = "rounded-xl p-4";
export const cardStyle = { border: "1px solid var(--line)", background: "var(--panel)" } as const;

// KeptTracksNote says where embedded tracks stand. Subtitles never edits the video; dropping
// the languages you don't keep happens in Convert, under its own setting, when it re-encodes.
export function KeptTracksNote() {
  return (
    <>Embedded subtitle tracks stay inside the video. Convert can remove the languages you don't keep when it re-encodes a file — set that in <Link to={LINKS.convertSettings} style={{ color: "var(--accent)" }}>Convert's settings</Link>.</>
  );
}
