import type { SubLangStatus } from "../../lib/api";
import type { SubSidecar } from "../../lib/subtitleFileApi";

// The words for a subtitle's state, shared by the drawer and the Movie/Series detail pages.
// (The Subtitles page's Library keeps its own CoverChip; same vocabulary.)

// Why a missing language is backing off, and what brings it back early.
export const REASON_LABEL: Record<string, string> = {
  auth_failed: "OpenSubtitles login rejected", model_missing: "no whisper model for it", quota: "OpenSubtitles quota spent",
  rate_limited: "OpenSubtitles rate limit", provider_error: "OpenSubtitles error", ai_failed: "AI failed",
  out_of_sync: "only out-of-sync matches", below_score: "only poor matches", no_match: "no OpenSubtitles match",
  impossible: "whisper can't make this language from the audio", no_audio: "no audio", ocr_needed: "needs OCR",
  empty: "embedded track was empty", extract_failed: "extract failed", cancelled: "stopped", ok: "made",
};
export const RETRY_ON_LABEL: Record<string, string> = { creds: "login", model: "a model", langs: "a language change" };
const PLAN_LABEL: Record<string, string> = { extract: "extract", ocr: "OCR", download: "download", ai: "AI" };

// Where a sidecar came from, in a word.
export const SOURCE_LABEL: Record<string, string> = {
  embedded: "Embedded", release: "Release", opensubtitles: "OpenSubtitles", "ai-transcribe": "AI",
  "ai-translate": "AI translation", adopted: "Adopted", manual: "Chosen", unknown: "Not from Arrmada",
};

// retryIn is how far off a backoff's next try is: "in 6 days", "in 3 h", "in 10 min", or
// "soon" once it's due.
export function retryIn(at: number, now = Date.now()): string {
  const sec = at - now / 1000;
  if (sec < 60) return "soon";
  if (sec < 90 * 60) return `in ${Math.round(sec / 60)} min`;
  if (sec < 36 * 3600) return `in ${Math.round(sec / 3600)} h`;
  const d = Math.round(sec / 86400);
  return `in ${d} day${d === 1 ? "" : "s"}`;
}

// wantLine is one kept language's state as a short line: "✓ en · OpenSubtitles · Health
// 92", "es missing · next try in 6 days", "fr missing · will try download → AI".
export function wantLine(l: SubLangStatus, sidecar?: SubSidecar): { text: string; tone: "good" | "bad" | "warn"; title: string } {
  // A profile's forced want: only a ".forced" subtitle (the foreign-dialogue lines) meets it.
  const lang = l.variant === "forced" ? `${l.lang.toLowerCase()} forced` : l.lang.toLowerCase();
  if (l.have) {
    const parts = [`✓ ${lang}`];
    if (sidecar) parts.push(SOURCE_LABEL[sidecar.source] ?? sidecar.source);
    const h = l.health ?? sidecar?.health;
    if (h && h.score != null && h.state !== "unchecked" && h.state !== "unknown") parts.push(`Health ${h.score}`);
    const name = sidecar?.name ?? l.file;
    const tip = [name ? `${name}${sidecar?.variant === "sdh" ? " (SDH)" : ""}` : "A subtitle is beside the file", h?.note].filter(Boolean).join(" — ");
    return { text: parts.join(" · "), tone: h?.state === "bad" ? "warn" : "good", title: tip };
  }
  if (l.orphan) {
    return { text: `${lang} · orphaned`, tone: "warn", title: "A subtitle in the folder is named for no video, so Plex won't show it with this file." };
  }
  if (l.reason) {
    const why = REASON_LABEL[l.reason] ?? l.reason.replace(/_/g, " ");
    const when = l.next_retry_at ? `next try ${retryIn(l.next_retry_at)}`
      : l.retry_on ? `waiting for ${l.retry_on.split(",").map((e) => RETRY_ON_LABEL[e] ?? e).join(" or ")}`
      : "retry by hand";
    return { text: `${lang} missing · ${when}`, tone: "bad", title: `Last try: ${why}. The sweep leaves it alone until then; a button press tries now.` };
  }
  if (l.variant === "forced" && !l.source) {
    return { text: `${lang} missing · no source`, tone: "bad", title: "No forced track in the file and no OpenSubtitles download set up — whisper can't make a forced subtitle" };
  }
  if (!l.source) {
    return { text: `${lang} missing · no source`, tone: "bad", title: "No text track in the file, no OpenSubtitles download set up, and no AI model that can make this language" };
  }
  const first = PLAN_LABEL[l.source ?? "ai"] ?? l.source ?? "AI";
  const plan = l.fallback ? `${first} → ${PLAN_LABEL[l.fallback] ?? l.fallback}` : first;
  return { text: `${lang} missing · will try ${plan}`, tone: "bad", title: `Missing — the next run tries ${plan}` };
}

// sidecarFor picks the paired sidecar that covers a kept language: a full one first, then SDH.
export function sidecarForLang(lang: string, sidecars: SubSidecar[], first: boolean): SubSidecar | undefined {
  const l = lang.toLowerCase();
  const match = (s: SubSidecar) => s.variant !== "forced" && (s.lang.toLowerCase() === l || s.lang.toLowerCase().startsWith(l + "-") || (first && s.lang === ""));
  return sidecars.find((s) => match(s) && !s.variant) ?? sidecars.find(match);
}

// sidecarForWant is the paired sidecar that meets a want: the forced one for a forced
// want, else as sidecarForLang (first: this is the title's first language).
export function sidecarForWant(l: SubLangStatus, sidecars: SubSidecar[], first: boolean): SubSidecar | undefined {
  if (l.variant !== "forced") return sidecarForLang(l.lang, sidecars, first);
  const lang = l.lang.toLowerCase();
  return sidecars.find((s) => s.variant === "forced" && (s.lang.toLowerCase() === lang || s.lang.toLowerCase().startsWith(lang + "-")));
}

const TONE: Record<"good" | "bad" | "warn", { background: string; color: string; border?: string }> = {
  good: { background: "var(--good-soft, rgba(127,176,105,.16))", color: "var(--good)" },
  bad: { background: "var(--avoid-soft)", color: "var(--avoid)" },
  warn: { background: "var(--panel-2)", color: "var(--avoid)", border: "1px dashed var(--avoid)" },
};

// WantChip is one kept language's chip.
export function WantChip({ l, sidecar, onClick }: { l: SubLangStatus; sidecar?: SubSidecar; onClick?: () => void }) {
  const w = wantLine(l, sidecar);
  const cls = "rounded px-1.5 py-0.5 font-mono text-xs font-bold";
  if (onClick) {
    return <button type="button" onClick={onClick} className={`${cls} hover:opacity-80`} style={TONE[w.tone]} title={`${w.title} — open the subtitles`}>{w.text}</button>;
  }
  return <span className={cls} style={TONE[w.tone]} title={w.title}>{w.text}</span>;
}
