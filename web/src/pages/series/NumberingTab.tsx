import { useState } from "react";
import { Link } from "react-router-dom";
import type { ImportReview, NumberingPending, Series as SeriesT } from "../../lib/api";
import { LINKS } from "../../lib/links";
import { Button, StatusChip } from "../../ui";
import { switchNumbering, type Flash } from "./HeroActions";
import { NumberingReviewModal } from "./NumberingReviewModal";
import { AliasPanel, SceneMapPanel } from "./panels";
import { fmtSize } from "./util";

const SOURCE: Record<string, string> = { tvdb: "TVDB", tvmaze: "TVmaze", tmdb: "TMDB" };

// showNumberingTab: anime always has numbering to look after; a standard show only while
// something needs a decision — a pending renumber or files that couldn't be placed.
export function showNumberingTab(series: SeriesT, pending: NumberingPending | null | undefined, unplaced: number): boolean {
  return series.series_type === "anime" || !!pending || unplaced > 0;
}

// NumberingTab is where a show's episode numbering is looked after: whose listing it
// follows, a renumber waiting for review, the standard/anime switch, the downloads held
// because their files couldn't be placed, alternate titles and (anime) the scene map.
export function NumberingTab({ series, pending, unplaced, onChange, onSettled, flash }: {
  series: SeriesT;
  pending: NumberingPending | null | undefined;
  unplaced: ImportReview[];
  onChange: () => void;
  onSettled: () => void;
  flash: Flash;
}) {
  const [review, setReview] = useState(false);
  const [busy, setBusy] = useState(false);
  const anime = series.series_type === "anime";
  const source = series.numbering_source ? SOURCE[series.numbering_source] ?? series.numbering_source : "";
  const flip = async () => { setBusy(true); await switchNumbering(series, onChange, flash); setBusy(false); };

  return (
    <div className="flex flex-col gap-4">
      <section aria-label="Numbering" className="rounded-xl p-3.5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-[12.5px] font-semibold">Numbering</span>
          <StatusChip tone={anime ? "accent" : "faint"}>{anime ? "Anime" : "Standard"}</StatusChip>
          {source ? <StatusChip tone="faint" title="Whose episode listing the stored numbering follows">From {source}</StatusChip> : <span className="text-[11px] text-ink-faint">source not recorded yet</span>}
          <Button size="sm" className="ml-auto" onClick={flip} busy={busy} busyLabel="Saving…">{anime ? "Switch to standard" : "Switch to anime"}</Button>
        </div>
        <p className="mb-0 mt-1.5 text-[11px] text-ink-faint">
          {anime
            ? "Releases are matched by absolute episode number (“- 137”) and per-cour numbering as well as SxxEyy."
            : "Releases are matched by season and episode (SxxEyy). Switch to anime for shows released with absolute numbers."}
        </p>
      </section>

      {pending && (
        <section aria-label="Numbering change" className="rounded-xl p-3.5" style={{ border: "1px solid var(--accent-line)", background: "var(--accent-soft)" }}>
          <div className="flex flex-wrap items-center gap-3 text-[12.5px]">
            <span className="min-w-0 flex-1">
              Numbering from {SOURCE[pending.to] ?? pending.to} differs — {pending.files} file{pending.files === 1 ? "" : "s"} would move. New episodes aren&apos;t added until it&apos;s applied or dismissed.
            </span>
            <Button size="sm" variant="primary" onClick={() => setReview(true)}>Review</Button>
          </div>
          <ul className="m-0 mt-2 list-none p-0 font-mono text-[11px] text-ink-dim">
            {pending.remaps.slice(0, 5).map((r, i) => <li key={`${r.old}:${i}`}>{r.old} → {r.new || "no episode"}</li>)}
            {pending.remaps.length > 5 && <li className="text-ink-faint">…and {pending.remaps.length - 5} more</li>}
          </ul>
        </section>
      )}

      {unplaced.length > 0 && (
        <section aria-label="Files that couldn't be placed">
          <h2 className="m-0 mb-1 text-[14px] font-bold">Held downloads</h2>
          <p className="m-0 mb-2 text-[11.5px] text-ink-dim">Downloaded for this show but not imported — their files couldn&apos;t be matched to an episode. Map or reject them in <Link to={LINKS.review} className="underline" style={{ color: "var(--accent-text)" }}>Activity → Needs you</Link>.</p>
          <div className="flex flex-col gap-1.5">
            {unplaced.map((rv) => (
              <div key={rv.id} className="rounded-lg px-3 py-2 text-[12px]" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
                <div className="truncate font-mono text-[11.5px]" title={rv.name}>{rv.name}</div>
                <div className="mt-0.5 text-[11px] text-ink-faint">{rv.reason}{rv.size_bytes ? ` · ${fmtSize(rv.size_bytes)}` : ""}</div>
              </div>
            ))}
          </div>
        </section>
      )}

      <AliasPanel series={series} defaultOpen />
      {anime && <SceneMapPanel series={series} defaultOpen />}

      {review && pending && (
        <NumberingReviewModal seriesId={series.id} pending={pending} onClose={() => setReview(false)} onSettled={onSettled} />
      )}
    </div>
  );
}
