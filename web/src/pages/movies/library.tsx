import type { ReactNode } from "react";
import type { FitItem, MovieDownload, MovieSummary, SummaryMedia } from "../../lib/api";
import { FitBadge, FIT_COLOR, bitrateColor, fitRank, hasIssue } from "../../components/FitBadge";
import { isMovieDownloaded, isMovieWanted, movieStatus } from "../../lib/movieStatus";
import type { LibraryConfig } from "../../features/library/types";
import type { MenuItem } from "../../ui";

// The Movies library as LibraryGrid config: its filters, card slots and table columns.

// downloadLabel names a download that isn't the film's own missing file: "Upgrade", or the
// extra version's label.
export function downloadLabel(d?: MovieDownload): string {
  if (d?.kind === "upgrade") return "Upgrade";
  if (d?.kind === "version") return d.version_label || "Version";
  return "";
}

function gb(bytes?: number): string {
  return bytes ? `${(bytes / 1024 ** 3).toFixed(1)} GB` : "—";
}
function resValue(f?: SummaryMedia): number | undefined {
  const r = f?.resolution || (f?.quality ? f.quality.split(" ")[0] : "");
  if (!r) return undefined;
  const s = r.toLowerCase();
  if (s.includes("4k") || s.includes("2160")) return 2160;
  if (s.includes("1440")) return 1440;
  if (s.includes("1080")) return 1080;
  if (s.includes("720")) return 720;
  if (s.includes("480") || s === "sd") return 480;
  const n = parseInt(s.replace(/\D/g, ""), 10);
  return isNaN(n) ? undefined : n;
}
export function hasAtmos(f?: SummaryMedia): boolean {
  return !!f?.atmos || !!f?.audio?.some((a) => /atmos/i.test(a));
}

// YesNo renders a clear yes/no so gaps (missing HDR/Atmos) are easy to scan for.
function YesNo({ on, label }: { on: boolean; label?: string }) {
  return on
    ? <span className="rounded px-1.5 py-0.5 text-[10px] font-bold" style={{ background: "var(--good-soft, rgba(90,140,90,.14))", color: "var(--good)" }}>{label ?? "Yes"}</span>
    : <span className="text-[11px] text-ink-faint">—</span>;
}

// DownloadBar is a download's progress along the poster's foot, labelled when it's an
// upgrade or an extra version rather than the film's own missing file.
function DownloadBar({ d }: { d: MovieDownload }) {
  const label = downloadLabel(d);
  const pct = Math.round(d.progress * 100);
  return (
    <div className="absolute inset-x-0 bottom-0 p-1.5">
      <div className="flex items-center gap-1.5 rounded-md px-1.5 py-1" style={{ background: "rgba(20,12,7,.82)" }} title={label ? `Downloading: ${label}` : "Downloading"}>
        {label && <span className="max-w-[60px] truncate font-mono text-[8.5px] font-bold uppercase" style={{ color: "var(--accent)" }}>{label}</span>}
        <div className="h-1 flex-1 overflow-hidden rounded-full" style={{ background: "rgba(255,255,255,.25)" }}>
          <div className="h-full rounded-full" style={{ width: `${pct}%`, background: "var(--accent)" }} />
        </div>
        <span className="font-mono text-[9px] font-bold" style={{ color: "#fff" }}>{pct}%</span>
      </div>
    </div>
  );
}

export interface MovieConfigDeps {
  fits: Map<number, FitItem>;
  /** false while the download client can't be read: a wanted film may already be downloading. */
  queueKnown: boolean;
  actions: (m: MovieSummary) => MenuItem[];
  rowAction: (m: MovieSummary) => ReactNode;
}

export function movieLibraryConfig({ fits, queueKnown, actions, rowAction }: MovieConfigDeps): LibraryConfig<MovieSummary> {
  const bad = (m: MovieSummary, kind: string) => (hasIssue(fits.get(m.id)?.fit, kind) ? { color: FIT_COLOR.over, fontWeight: 600 } : undefined);
  return {
    noun: { one: "movie", many: "movies" },
    storageKey: "movies",
    detailPath: (m) => `/movies/${m.id}`,
    // Wanted is exactly what's badged Wanted (monitored, no file, nothing downloading) — the
    // same films as Wanted → Missing. A film with no file that isn't monitored is never
    // searched for; it has its own filter rather than hiding inside Wanted.
    filters: [
      { key: "all", label: "All" },
      { key: "monitored", label: "Monitored", match: (m) => m.monitored },
      { key: "unmonitored", label: "Unmonitored", match: (m) => !m.monitored },
      { key: "wanted", label: "Wanted", match: (m) => isMovieWanted(m, queueKnown) },
      { key: "nofile", label: "No file (unmonitored)", match: (m) => !m.monitored && !m.has_file },
      { key: "downloaded", label: "Downloaded", match: (m) => isMovieDownloaded(m, queueKnown) },
    ],
    searchText: (m) => m.title,
    // Alphabetical, "The" ignored.
    compare: (a, b) => (a.sort_title || a.title).localeCompare(b.sort_title || b.title),
    tableMinWidth: 900,
    columns: [
      { key: "status", label: "Status", sort: (m) => (m.has_file ? 0 : m.monitored ? 1 : 2), cell: (m) => { const st = movieStatus(m, undefined, queueKnown); return <span className="font-mono text-[10px] uppercase" style={{ color: st.color }}>{st.label}</span>; } },
      { key: "resolution", label: "Resolution", sort: (m) => resValue(m.media), cell: (m) => m.media?.resolution || (m.media?.quality ? m.media.quality.split(" ")[0] : "—") },
      { key: "codec", label: "Codec", sort: (m) => m.media?.codec || undefined, cell: (m) => <span style={bad(m, "codec")}>{m.media?.codec || "—"}</span> },
      { key: "audio", label: "Audio", sort: (m) => (m.media?.audio?.length ? m.media.audio.join(", ").toLowerCase() : undefined), cell: (m) => <span style={bad(m, "lossless")}>{m.media?.audio?.length ? m.media.audio.join(", ") : "—"}</span> },
      { key: "atmos", label: "Atmos", sort: (m) => (hasAtmos(m.media) ? 1 : 0), cell: (m) => (hasIssue(fits.get(m.id)?.fit, "atmos") ? <span className="text-[11px] font-semibold" style={{ color: FIT_COLOR.over }}>No</span> : <YesNo on={hasAtmos(m.media)} />) },
      { key: "hdr", label: "HDR", sort: (m) => (m.media?.hdr?.length ? 1 : 0), cell: (m) => {
        const hdr = m.media?.hdr;
        if (hasIssue(fits.get(m.id)?.fit, "hdr")) return <span className="text-[11px] font-semibold" style={{ color: FIT_COLOR.over }}>{hdr?.length ? hdr.join("/") : "SDR"}</span>;
        return hdr?.length ? <YesNo on label={hdr.join("/")} /> : <YesNo on={false} />;
      } },
      { key: "type", label: "Type", sort: (m) => m.media?.container || undefined, cell: (m) => <span className="font-mono text-[11px] text-ink-dim">{m.media?.container || "—"}</span> },
      { key: "size", label: "Size", align: "right", className: "font-mono text-[11px] text-ink-dim", sort: (m) => m.size_bytes || undefined, cell: (m) => gb(m.size_bytes) },
      { key: "bitrate", label: "Bitrate", align: "right", className: "font-mono text-[11px] text-ink-dim", sort: (m) => m.media?.bitrate_mbps || undefined, cell: (m) => {
        const fi = fits.get(m.id);
        const c = bitrateColor(fi?.fit);
        const n = m.media?.bitrate_mbps;
        return <span style={c ? { color: c, fontWeight: 600 } : undefined} title={fi?.fit?.window ? `Window ${fi.fit.window.min || 0}–${fi.fit.window.max || "∞"} Mb/s` : undefined}>{n ? `${n.toFixed(1)} Mbps` : "—"}</span>;
      } },
      { key: "fit", label: "Fit", sort: (m) => fitRank(fits.get(m.id)), cell: (m) => <FitBadge item={fits.get(m.id)} /> },
    ],
    card: {
      meta: (m) => m.year || "—",
      status: (m) => movieStatus(m, undefined, queueKnown),
      overlay: (m) => m.download && <DownloadBar d={m.download} />,
    },
    sorts: [{ key: "title", label: "Title" }],
    actions,
    rowAction,
  };
}

// FitLegend sits above the table: the misfits switch and what the fit colours mean.
export function FitLegend({ judged, misfits, misfitsOnly, onMisfitsOnly }: { judged: number; misfits: number; misfitsOnly: boolean; onMisfitsOnly: (on: boolean) => void }) {
  if (judged === 0) return null;
  return (
    <div className="mb-2 flex flex-wrap items-center gap-3 text-[11.5px] text-ink-dim">
      <label className="inline-flex cursor-pointer items-center gap-1.5">
        <input type="checkbox" checked={misfitsOnly} onChange={(e) => onMisfitsOnly(e.target.checked)} />
        Only files that don't fit their profile ({misfits} of {judged})
      </label>
      <span className="text-[10.5px] text-ink-faint">
        <span style={{ color: FIT_COLOR.over }}>■</span> over the bitrate ceiling ·{" "}
        <span style={{ color: FIT_COLOR.under }}>■</span> under the floor ·{" "}
        <span style={{ color: FIT_COLOR.mismatch }}>■</span> bitrate fine, spec isn't ·{" "}
        <span style={{ color: FIT_COLOR.fits }}>■</span> fits — hover for why
      </span>
    </div>
  );
}
