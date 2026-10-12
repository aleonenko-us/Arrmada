// The value Convert's subtitle keep-list (keep_sub_langs) holds when it follows the
// Subtitles module's kept languages (convert.SameAsSubtitles on the server).
export const SAME_AS_SUBTITLES = "@subtitles";

/** keepSubsLine says in words what Convert keeps, for the Subtitles page. */
export function keepSubsLine(keep: string, names: (code: string) => string): string {
  const parts = keep.split(",").map((t) => t.trim()).filter(Boolean);
  if (parts.length === 0) return "every language";
  const follows = parts.some((t) => t.toLowerCase() === SAME_AS_SUBTITLES);
  const extra = parts.filter((t) => t.toLowerCase() !== SAME_AS_SUBTITLES).map(names);
  if (!follows) return extra.join(", ");
  return extra.length ? `the same languages as Subtitles, plus ${extra.join(", ")}` : "the same languages as Subtitles";
}
