import type { QualityCustomFormat } from "../../lib/api";

// The condition types a custom format can use, in the order the editor offers them.
export const CONDITION_TYPES: { v: string; l: string }[] = [
  { v: "source", l: "Source" },
  { v: "resolution", l: "Resolution" },
  { v: "codec", l: "Codec" },
  { v: "dynamic_range", l: "Dynamic range" },
  { v: "audio", l: "Audio" },
  { v: "edition", l: "Edition" },
  { v: "release_group", l: "Release group" },
  { v: "streaming", l: "Streaming service" },
  { v: "language", l: "Audio language" },
  { v: "release_title", l: "Name matches (regex)" },
  { v: "lossless", l: "Lossless audio" },
  { v: "multi", l: "Several audio languages" },
  { v: "full_disc", l: "Full disc (BR-DISK)" },
  { v: "3d", l: "3D" },
  { v: "upscaled", l: "Upscaled" },
  { v: "extras", l: "Extras" },
];

export const TYPE_LABEL: Record<string, string> = Object.fromEntries(CONDITION_TYPES.map((t) => [t.v, t.l]));

// VALUELESS types are a yes/no the parser reads; the row says what that means instead of
// asking for a value.
export const VALUELESS: Record<string, string> = {
  lossless: "has a lossless audio track",
  multi: "says MULTi, DUAL or German DL/ML",
  full_disc: "is an untouched disc image",
  "3d": "is a 3D release",
  upscaled: "is upscaled from a lower resolution",
  extras: "is bonus material",
};

// UNWANTED_TYPES overlap the Unwanted pack in Rules, which already refuses or avoids them per
// profile; a custom format on them only adds a score.
export const UNWANTED_TYPES = new Set(["full_disc", "3d", "upscaled", "extras", "language"]);

export const isValueless = (type: string) => type in VALUELESS;

// formatSummary reads a format back as one line: "Source BluRay AND NOT Release group YTS".
export function formatSummary(f: QualityCustomFormat): string {
  const join = f.match === "any" ? " OR " : " AND ";
  return f.conditions
    .map((c) => `${c.negate ? "NOT " : ""}${TYPE_LABEL[c.type] ?? c.type}${isValueless(c.type) ? "" : ` ${c.value}`}`)
    .join(join);
}
