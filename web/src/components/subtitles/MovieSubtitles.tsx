import { useCallback, useEffect, useState, type ReactNode } from "react";
import { subtitleFileApi, type SubFileView } from "../../lib/subtitleFileApi";
import { useProfiles } from "../../pages/subtitles/profileApi";
import { LazySubtitleDrawer } from "./LazySubtitleDrawer";
import { WantChip, sidecarForWant } from "./WantChip";

// MovieSubtitles is the Movie page's subtitle line for the file Subtitles works on (SUB-29):
// one chip per kept language ("✓ en · OpenSubtitles · Health 92", "es missing · next try in
// 6 days"), any forced extras, and Find subtitles, which opens the per-file drawer. When the
// state can't be read it shows fallback (the file names), as the page did before.
export function MovieSubtitles({ movieId, fallback, onChanged }: { movieId: number; fallback: ReactNode; onChanged?: () => void }) {
  const [view, setView] = useState<SubFileView | null>(null);
  const [failed, setFailed] = useState(false);
  const [open, setOpen] = useState(false);
  const load = useCallback(() => subtitleFileApi.view({ kind: "movie", movie_id: movieId }).then((v) => { setView(v); setFailed(false); }).catch(() => setFailed(true)), [movieId]);
  // The chips are the movie's profile's wants: read again when it is pinned elsewhere.
  const pinned = useProfiles().data?.overrides.find((o) => o.kind === "movie" && o.id === movieId)?.profile_id ?? 0;
  useEffect(() => { void load(); }, [load, pinned]);

  if (failed && !view) return <>{fallback}</>;
  if (!view) return null;
  // Forced files the profile doesn't ask for are extras; a forced want has its own chip.
  const forcedWants = new Set(view.languages.filter((l) => l.variant === "forced").map((l) => l.lang.toLowerCase()));
  const forced = view.sidecars.filter((s) => s.variant === "forced" && !forcedWants.has(s.lang.toLowerCase()));
  return (
    <div className="mt-1.5 flex flex-wrap items-center gap-1.5 text-xs">
      <span className="font-mono uppercase text-ink-faint">Subs</span>
      {view.languages.map((l) => (
        <WantChip key={l.lang + (l.variant ?? "")} l={l} sidecar={sidecarForWant(l, view.sidecars, view.kept[0] === l.lang)} onClick={() => setOpen(true)} />
      ))}
      {forced.map((s) => (
        <span key={s.name} className="rounded px-1.5 py-0.5 font-mono text-xs text-ink-dim" style={{ background: "var(--panel-2)" }} title={`${s.name} — the foreign-language lines only`}>{s.lang} forced</span>
      ))}
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="rounded-md px-2 py-0.5 text-xs font-semibold"
        style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}
        title="See this file's subtitles: previews, where each came from, timing and delete"
      >
        Find subtitles
      </button>
      {open && (
        <LazySubtitleDrawer
          media={{ kind: "movie", movie_id: movieId }}
          title={view.title}
          onClose={() => setOpen(false)}
          onChanged={() => { void load(); onChanged?.(); }}
        />
      )}
    </div>
  );
}
