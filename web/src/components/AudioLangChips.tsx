import type { AudioLang, AudioLangFit, Episode } from "../lib/api";
import { chipText, chipTitle, FIT_TONE, FIT_WORD, langsLabel, seasonAudio, seasonFits } from "../lib/audioLangs";

// AudioLangChips shows a file's audio languages as small code chips (EN · DE · ?), with
// the full name on hover and for screen readers. When the title's profile has language
// rules, a mark in front says how they fit — ✓ green, ! orange, ✕ red — with the reason
// in its tooltip and label, so the colour is never the only signal. Renders nothing for a
// file not analysed yet.
export function AudioLangChips({ langs, fit, className = "" }: { langs?: AudioLang[]; fit?: AudioLangFit; className?: string }) {
  if (!langs?.length) return null;
  const tone = fit ? FIT_TONE[fit.status] : undefined;
  return (
    <span role="group" aria-label={langsLabel(langs, fit)} data-testid="audio-langs" data-fit={fit?.status}
      className={`inline-flex flex-wrap items-center gap-1 ${className}`}>
      {fit && tone && (
        <span aria-hidden title={`${FIT_WORD[fit.status]} — ${fit.note}`} className="cursor-help font-mono text-xs font-bold" style={{ color: tone.color }}>
          {tone.mark}
        </span>
      )}
      {langs.map((l, i) => (
        <span key={`${l.code}-${i}`} aria-hidden title={chipTitle(l)}
          className="cursor-help rounded px-1 py-px font-mono text-2xs font-bold"
          style={{
            color: tone?.color ?? "var(--ink-dim)",
            border: `1px ${l.detected || !l.code ? "dashed" : "solid"} ${tone?.color ?? "var(--line)"}`,
          }}>
          {chipText(l)}
        </span>
      ))}
    </span>
  );
}

// SeasonAudioSummary is a season's languages over its analysed episode files — "EN 10/10
// · DE 8/10" — and how many miss the profile's language rules. Nothing until a file of the
// season has been analysed.
export function SeasonAudioSummary({ episodes }: { episodes: Episode[] }) {
  const { langs, of } = seasonAudio(episodes);
  if (of === 0) return null;
  const fits = seasonFits(episodes);
  const shown = langs.slice(0, 4);
  const words = shown.map((l) => `${l.code ? l.code.toUpperCase() : "?"} ${l.have}/${of}`);
  const misses = fits.bad + fits.partial;
  const label = `Audio languages over ${of} file${of === 1 ? "" : "s"}: ${words.join(", ")}${misses ? `. ${fits.bad} don't fit the language rules, ${fits.partial} miss a preferred language` : ""}`;
  return (
    <span data-testid="season-audio" title={label} aria-label={label} role="note" className="inline-flex items-center gap-1.5 font-mono text-xs text-ink-faint">
      <span aria-hidden>{words.join(" · ")}{langs.length > shown.length ? " …" : ""}</span>
      {fits.bad > 0 && <span aria-hidden style={{ color: FIT_TONE.bad.color }}>✕ {fits.bad}</span>}
      {fits.partial > 0 && <span aria-hidden style={{ color: FIT_TONE.partial.color }}>! {fits.partial}</span>}
    </span>
  );
}
