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
  { v: "release_title", l: "Name matches (regex)" },
  { v: "lossless", l: "Lossless audio" },
];

export const TYPE_LABEL: Record<string, string> = Object.fromEntries(CONDITION_TYPES.map((t) => [t.v, t.l]));

// formatSummary reads a format back as one line: "Source BluRay AND NOT Release group YTS".
export function formatSummary(f: QualityCustomFormat): string {
  const join = f.match === "any" ? " OR " : " AND ";
  return f.conditions
    .map((c) => `${c.negate ? "NOT " : ""}${TYPE_LABEL[c.type] ?? c.type}${c.type === "lossless" ? "" : ` ${c.value}`}`)
    .join(join);
}
