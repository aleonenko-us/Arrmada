import { req, type SubFileEntry, type SubLangStatus, type SubtitleJob } from "../../lib/api";
import { Menu, useConfirm, type MenuItem } from "../../ui";

// Per-language redo: a present language's chip opens a menu of what to replace it with.
// Only that language is touched, a downloaded file is never fetched again for this video,
// and the old file goes to the recycle bin once the new one exists. The call lives in the
// Subtitles chunk, not lib/api.ts.

export type RedoMode = "next" | "ai" | "extract";

const redoApi = {
  redo: (f: SubFileEntry, lang: string, mode: RedoMode, trackIndex?: number) =>
    req<SubtitleJob>("/api/v1/subtitles/redo", {
      method: "POST",
      body: JSON.stringify({ kind: f.kind, movie_id: f.movie_id, series_id: f.series_id, season: f.season, episode: f.episode, lang, mode, track_index: trackIndex }),
    }),
};

export const REDO_WORDS: Record<string, string> = { next: "next download", ai: "AI", extract: "embedded track" };

// redoLabel is how a redo job reads in the Queue: "redo en (next download)".
export function redoLabel(j: SubtitleJob): string | null {
  if (!j.mode || !REDO_WORDS[j.mode]) return null;
  return `redo ${(j.langs ?? []).join(", ")} (${REDO_WORDS[j.mode]})`;
}

// RedoChip is a present language's coverage chip, with the redo menu behind it.
export function RedoChip({ f, l, flash, onQueued }: { f: SubFileEntry; l: SubLangStatus; flash: (m: string) => void; onQueued: () => void }) {
  const confirm = useConfirm();
  const file = l.file || `the ${l.lang.toUpperCase()} subtitle`;
  const run = async (mode: RedoMode, what: string, extra: string, trackIndex?: number) => {
    const ok = await confirm({
      title: `Replace ${file}?`,
      body: `${f.title}: ${file} is replaced by ${what}. Once the new one is written, ${file} moves to the recycle bin; if nothing better turns up, it stays where it is. Your other languages aren't touched.${extra}`,
      confirmLabel: "Replace",
    });
    if (!ok) return;
    try {
      await redoApi.redo(f, l.lang, mode, trackIndex);
      flash(`Queued: redo ${l.lang} (${REDO_WORDS[mode]}) for ${f.title}`);
      onQueued();
    } catch (e) { flash((e as Error).message); }
  };
  const items: MenuItem[] = [
    { label: "Replace with next download", title: "Another OpenSubtitles upload (never this one again), else an embedded track or AI",
      onSelect: () => run("next", "the next-best subtitle — another OpenSubtitles upload (never the one there now), an embedded track or AI", " A download spends one of today's OpenSubtitles downloads.") },
    { label: "Re-generate with AI", title: "Local whisper, even when a download exists",
      onSelect: () => run("ai", "a new AI subtitle made from the audio (large-v3 when it's installed)", "") },
  ];
  for (const t of f.embedded ?? []) {
    if (!t.text) continue;
    items.push({
      label: `Re-extract (track ${t.index + 1}${t.lang ? ` · ${t.lang}` : ""}${t.sdh ? " · SDH" : ""}${t.forced ? " · forced" : ""})`,
      onSelect: () => run("extract", `embedded subtitle track ${t.index + 1}${t.title ? ` (“${t.title}”)` : ""}`, "", t.index),
    });
  }
  return (
    <Menu
      portal
      align="left"
      label={`Redo ${l.lang}`}
      items={items}
      triggerClassName="rounded px-1.5 py-0.5 font-mono text-2xs font-bold uppercase"
      triggerStyle={{ background: "var(--good-soft, rgba(127,176,105,.16))", color: "var(--good)" }}
      trigger={<span title={`${file} — click to replace it`}>✓ {l.lang} ▾</span>}
    />
  );
}
