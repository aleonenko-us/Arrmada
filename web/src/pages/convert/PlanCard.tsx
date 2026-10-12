import type { ReactNode } from "react";
import { type ConvertPlan, type ConvertTrackDecision } from "../../lib/api";
import { convertApi } from "../../lib/convertApi";
import { posterThumb } from "../../lib/img";
import { useQuery } from "../../lib/query";
import { StatusChip } from "../../ui";
import { sizeLine, timeLine, trackLabel, videoLine } from "./plan";

// The Library's "Conversion plan" card: what converting this one file would do, before
// anything runs — the picture before → after, every audio and subtitle track kept or
// removed (with why), the container, and the warnings. It reads the same decision code the
// runner uses, so it is the plan, not a guess at it.

export const planQueryKey = (key: string) => `convert-plan:${key}`;

export function usePlan(key: string, enabled = true) {
  // Short-lived: a settings change elsewhere changes the plan.
  return useQuery<ConvertPlan>(planQueryKey(key), () => convertApi.convertPlan(key), { enabled, staleMs: 10_000 });
}

export function PlanCard({ itemKey }: { itemKey: string }) {
  const { data, error, loading, refetch } = usePlan(itemKey);
  return (
    // Sticky to the left edge of the table's scroller and no wider than the screen, so on a
    // phone the card reads without scrolling sideways through a 900px table.
    <div className="sticky left-0 rounded-lg p-3" style={{ maxWidth: "calc(100vw - 2rem)", background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      <div className="font-mono text-[9.5px] font-bold uppercase tracking-[0.11em] text-ink-faint">Conversion plan</div>
      {!data && loading && <p className="mt-2 text-[12px] text-ink-dim">Reading the plan…</p>}
      {!data && error && (
        <p className="mt-2 text-[12px]" style={{ color: "var(--reject)" }}>
          {error.message} <button type="button" onClick={() => void refetch()} className="ml-1 underline">Retry</button>
        </p>
      )}
      {data && <PlanDetails plan={data} />}
    </div>
  );
}

// PlanDetails is the plan's body, shared with the Convert-now confirmation.
export function PlanDetails({ plan }: { plan: ConvertPlan }) {
  const v = plan.video;
  const notes = [v.codec_note, v.hdr_note, v.crop_note].filter(Boolean) as string[];
  return (
    <div className="mt-2 flex min-w-0 flex-col gap-2.5 text-[12px]">
      <div className="min-w-0">
        <div className="font-semibold">{videoLine(plan)}</div>
        <div className="text-ink-dim">
          {sizeLine(plan)}
          {plan.needs.save > 0 && timeLine(plan) && <> · {timeLine(plan)}</>}
          {plan.container.from !== plan.container.to && <> · {plan.container.from} → {plan.container.to}</>}
        </div>
        {!v.to_codec && v.why && <div className="text-[11px] text-ink-faint">Picture left alone: {v.why}</div>}
        {notes.map((n) => <div key={n} className="text-[11px] text-ink-faint">{n}</div>)}
      </div>
      <TrackList title="Audio" tracks={plan.audio} />
      <TrackList title="Subtitles" tracks={plan.subs} />
      {plan.warnings.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-1 p-0" aria-label="Warnings">
          {plan.warnings.map((w) => (
            <li key={w} className="text-[11.5px]" style={{ color: "var(--avoid)" }}>⚠ {w}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

function TrackList({ title, tracks }: { title: string; tracks: ConvertTrackDecision[] }) {
  if (tracks.length === 0) return <div className="text-[11px] text-ink-faint">{title}: none</div>;
  const removed = tracks.filter((t) => !t.keep).length;
  return (
    <div className="min-w-0">
      <div className="text-[11px] font-semibold text-ink-dim">
        {title} · {tracks.length - removed} kept{removed > 0 ? `, ${removed} removed` : ""}
      </div>
      <ul className="m-0 mt-1 flex list-none flex-col gap-0.5 p-0">
        {tracks.map((t) => (
          <li key={`${t.type}-${t.index}`} className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5 text-[11.5px]">
            <StatusChip tone={t.keep ? "good" : "avoid"} size="xs" className="flex-none">{t.keep ? "kept" : "removed"}</StatusChip>
            <span className="min-w-0 break-words" style={{ color: t.keep ? "var(--ink)" : "var(--ink-dim)" }}>{trackLabel(t)}</span>
            {t.reason && <span className="text-[10.5px] text-ink-faint">{t.reason}</span>}
          </li>
        ))}
      </ul>
    </div>
  );
}

// PlanToggle is a Library row's title: a chevron, the poster and the name, which opens and
// closes the row's plan card.
export function PlanToggle({ open, title, onToggle, children }: { open: boolean; title: string; onToggle: () => void; children: ReactNode }) {
  return (
    <button type="button" onClick={onToggle} aria-expanded={open} aria-label={`${open ? "Hide" : "Show"} the conversion plan for ${title}`}
      className="flex items-center gap-2 text-left hover:underline">
      <span aria-hidden className="w-3 flex-none font-mono text-[10px] text-ink-faint">{open ? "▾" : "▸"}</span>
      {children}
    </button>
  );
}

// Poster is the Library row's 32px poster, or a blank tile of the same size.
export function Poster({ url, title }: { url?: string; title: string }) {
  return (
    <span className="inline-block h-12 w-8 flex-none overflow-hidden rounded" style={{ background: "var(--panel-2)", border: "1px solid var(--line-soft)" }} aria-hidden={!url}>
      {url && <img src={posterThumb(url)} alt="" title={title} className="h-full w-full object-cover" loading="lazy" decoding="async" />}
    </span>
  );
}
