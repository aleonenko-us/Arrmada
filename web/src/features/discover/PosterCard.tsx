import type { ReactNode } from "react";
import { StatusChip, type Tone } from "../../ui";

export interface PosterCardProps {
  /** The title: the caption's first line and the poster button's name. */
  title: string;
  /** Optional media kind, shown on mixed movie/TV discovery cards. */
  kind?: string;
  /** The poster image, or a placeholder when there is none. */
  art: ReactNode;
  badge?: { label: string; tone: Tone } | null;
  /** 0..1 while it downloads: a bar along the bottom of the poster. */
  progress?: number;
  onOpen: () => void;
  /** The quick request, only where a mouse can reveal it first; null hides it. */
  quick?: { label: string; busy: boolean; onClick: () => void } | null;
  /** A grid cell (fills its column) rather than a fixed-width rail card. */
  full?: boolean;
  /** Use a square crop for artist art; title posters stay 2:3. */
  aspectRatio?: "2/3" | "1/1";
  /** The caption's lines under the title (year, rating, genres…). */
  children?: ReactNode;
}

// PosterCard is the poster tile every Discover surface shows, films and series and books
// alike: the whole poster opens the item's sheet, a corner badge says where it stands, a
// hover lifts it and offers a quick request, and an always-visible caption names it.
<<<<<<< HEAD
export function PosterCard({ title, art, badge, progress, onOpen, quick, full, aspectRatio = "2/3", children }: PosterCardProps) {
=======
export function PosterCard({ title, kind, art, badge, progress, onOpen, quick, full, children }: PosterCardProps) {
>>>>>>> pr-61
  return (
    <div className={`group relative hover:z-10 ${full ? "w-full" : "w-[150px] flex-none"}`} style={{ scrollSnapAlign: "start" }}>
      <div
<<<<<<< HEAD
        className="relative overflow-hidden rounded-xl transition-[transform,box-shadow] duration-200 will-change-transform group-hover:-translate-y-1 group-hover:scale-[1.03] group-hover:shadow-[0_12px_30px_rgba(0,0,0,0.45)]"
        style={{ aspectRatio, border: "1px solid var(--line)", background: "var(--panel-2)" }}
=======
        className="relative overflow-hidden rounded-xl transition-[transform,box-shadow] duration-200 will-change-transform group-hover:-translate-y-1 group-hover:shadow-[0_12px_30px_rgba(0,0,0,0.45)]"
        style={{ aspectRatio: "2/3", border: "1px solid var(--line)", background: "var(--panel-2)" }}
>>>>>>> pr-61
      >
        <button onClick={onOpen} aria-label={`View details for ${title}`} className="absolute inset-0 block h-full w-full text-left">
          {art}
          {kind && <span className="absolute bottom-1.5 left-1.5 rounded px-1.5 py-0.5 text-2xs font-bold uppercase tracking-wide" style={{ background: "rgba(20,12,7,.78)", color: "#fff" }}>{kind}</span>}
          {badge && <StatusChip tone={badge.tone} surface="poster" className="absolute right-1.5 top-1.5 max-w-[calc(100%-12px)] truncate" title={badge.label}>{badge.label}</StatusChip>}
          {/* Terracotta download bar along the bottom of the poster while it's grabbing. */}
          {progress != null && progress > 0 && progress < 1 && (
            <div className="absolute inset-x-0 bottom-0 z-10 h-1.5" style={{ background: "rgba(20,12,7,.55)" }}>
              <div className="h-full" style={{ width: `${Math.round(progress * 100)}%`, background: "var(--accent)" }} />
            </div>
          )}
          {/* Hover scrim, purely decorative: pointer-events-none so a tap anywhere on the
              poster opens the sheet, never something the user couldn't see. */}
          <div className="pointer-events-none absolute inset-0 flex flex-col justify-end p-2 opacity-0 transition-opacity group-hover:opacity-100" style={{ background: "linear-gradient(to top, rgba(0,0,0,.82) 0%, rgba(0,0,0,.15) 42%, transparent 70%)" }}>
            {!quick && (
              <span className="self-start rounded-md px-2.5 py-1 text-xs font-semibold" style={{ background: "rgba(255,255,255,.16)", border: "1px solid rgba(255,255,255,.26)", color: "#fff" }}>Details</span>
            )}
          </div>
        </button>
        {/* A sibling of the details button, not nested in it: a control inside a button is
            invalid HTML, and its invisible hit area filed requests from stray taps. It only
            takes clicks once hover or keyboard focus has revealed it. */}
        {quick && (
          <button
            onClick={quick.onClick}
            disabled={quick.busy}
            className="pointer-events-none absolute bottom-2 left-2 z-20 rounded-md px-2.5 py-1 text-xs font-semibold opacity-0 transition-opacity group-hover:pointer-events-auto group-hover:opacity-100 focus-visible:pointer-events-auto focus-visible:opacity-100 disabled:cursor-default"
            // Dimmed with a filter, not opacity, so the busy look doesn't fight the hover reveal.
            style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)", filter: quick.busy ? "brightness(.8)" : undefined }}
          >
            {quick.busy ? "Requesting…" : quick.label}
          </button>
        )}
      </div>
      {/* Always-visible caption strip so cards read before hover (Plex style). */}
      <div className="px-0.5 pt-2">
        <div className="truncate text-sm font-semibold" style={{ color: "var(--ink)" }} title={title}>{title}</div>
        {children}
      </div>
    </div>
  );
}
