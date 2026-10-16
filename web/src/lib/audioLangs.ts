import type { AudioLang, AudioLangFit, Episode } from "./api";

// The audio-language badges (issue #12): what a library file's audio tracks are, from the
// server's analysis of the file. The server sends them deduplicated and ordered — the
// title's original language first — so these only label them.

/** chipText is a badge's face: the code in capitals, "?" for a track nothing identified. */
export function chipText(l: AudioLang): string {
  return l.code ? l.code.toUpperCase() : "?";
}

/** chipTitle is a badge's tooltip and accessible name: the language in words. */
export function chipTitle(l: AudioLang): string {
  if (!l.code) return "An audio track with no language tag, not identified yet";
  const name = l.name || l.code.toUpperCase();
  return l.detected ? `${name} — detected by the AI (the track isn't tagged)` : name;
}

/** langsLabel names a file's audio languages in one line, for screen readers. */
export function langsLabel(list: AudioLang[], fit?: AudioLangFit): string {
  const names = list.map((l) => (!l.code ? "an unidentified track" : `${l.name || l.code.toUpperCase()}${l.detected ? " (detected)" : ""}`));
  const head = `Audio: ${names.join(", ")}`;
  return fit ? `${head}. ${FIT_WORD[fit.status]}: ${fit.note}` : head;
}

/** The fit's word, so colour is never the only signal. */
export const FIT_WORD: Record<AudioLangFit["status"], string> = {
  good: "Languages fit",
  partial: "Missing a preferred language",
  bad: "Doesn't fit the language rules",
};

/** The fit's colour and mark: green fits, orange lacks a preferred language, red fails. */
export const FIT_TONE: Record<AudioLangFit["status"], { color: string; mark: string }> = {
  good: { color: "var(--good-text)", mark: "✓" },
  partial: { color: "var(--under)", mark: "!" },
  bad: { color: "var(--reject-text)", mark: "✕" },
};

export interface SeasonLang {
  code: string; // "" = unidentified
  have: number; // episode files carrying it
}

/** seasonAudio counts, over a season's analysed episode files, how many carry each
 *  language: the per-season summary ("DE 8/10"). Languages in every file first, then by
 *  count; of is how many files were read. Empty when none has been analysed. */
export function seasonAudio(eps: Episode[]): { langs: SeasonLang[]; of: number } {
  const counts = new Map<string, number>();
  const firstSeen: string[] = [];
  let of = 0;
  for (const e of eps) {
    if (!e.has_file || !e.audio_langs?.length) continue;
    of++;
    for (const code of new Set(e.audio_langs.map((l) => l.code))) {
      if (!counts.has(code)) firstSeen.push(code);
      counts.set(code, (counts.get(code) ?? 0) + 1);
    }
  }
  const langs = firstSeen
    .map((code) => ({ code, have: counts.get(code) ?? 0 }))
    .sort((a, b) => b.have - a.have || firstSeen.indexOf(a.code) - firstSeen.indexOf(b.code));
  return { langs, of };
}

/** seasonFits counts the season's files by language fit, worst first. */
export function seasonFits(eps: Episode[]): { bad: number; partial: number; good: number } {
  const out = { bad: 0, partial: 0, good: 0 };
  for (const e of eps) {
    const s = e.has_file ? e.audio_lang_fit?.status : undefined;
    if (s) out[s]++;
  }
  return out;
}
