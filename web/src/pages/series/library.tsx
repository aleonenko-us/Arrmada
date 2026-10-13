import type { ReactNode } from "react";
import type { Series, SeriesFitSummary } from "../../lib/api";
import { FIT_COLOR } from "../../components/FitBadge";
import { isWanted, libraryStatus } from "../../lib/status";
import type { LibraryConfig } from "../../features/library/types";
import type { MenuItem } from "../../ui";

// The Series library as LibraryGrid config: its filters, card slots and table columns.

export function statsOf(s: Series) {
  const st = s.stats ?? { episodes: 0, have_files: 0, size_bytes: 0, seasons: 0 };
  // An older server sends no missing count; the gap is the next best thing.
  return { ...st, missing: st.missing ?? Math.max(0, st.episodes - st.have_files), unmonitored_missing: st.unmonitored_missing ?? 0 };
}

// Counted episodes are the files plus the monitored aired ones still missing, so the
// downloaded share is the count less the missing.
function statusInput(s: Series) {
  const st = statsOf(s);
  return { multi: true, hasFile: st.have_files > 0, monitored: s.monitored, have: st.episodes - st.missing, total: st.episodes };
}
export const seriesStatus = (s: Series) => libraryStatus(statusInput(s));

function gb(bytes?: number): string {
  return bytes ? `${(bytes / 1024 ** 3).toFixed(1)} GB` : "—";
}

// The greyed remainder beside a have/total count: aired episodes nobody asked for.
function NotMonitored({ n }: { n: number }) {
  if (n <= 0) return null;
  return (
    <span className="text-ink-faint" title={`${n} aired episode${n === 1 ? "" : "s"} without a file that ${n === 1 ? "isn't" : "aren't"} monitored — not counted as missing`}>
      {" "}+{n} not monitored
    </span>
  );
}

// SeriesFit summarises a show's episode files against its profile's ideal file: "✓ all 24",
// or the counts that don't fit, worst first.
function SeriesFit({ f }: { f?: SeriesFitSummary }) {
  if (!f || f.checked === 0) return <span className="text-xs text-ink-faint" title="No episodes judged — the profile has no ideal file set up, or nothing's analysed yet">—</span>;
  if (f.fits === f.checked) return <span className="font-mono text-xs font-bold" style={{ color: FIT_COLOR.fits }}>✓ all {f.checked}</span>;
  const parts: [number, string, string][] = [[f.over, "over", FIT_COLOR.over], [f.under, "under", FIT_COLOR.under], [f.mismatch, "don't fit", FIT_COLOR.mismatch]];
  return (
    <span className="inline-flex flex-wrap gap-x-2 font-mono text-xs font-semibold" title={`${f.fits} of ${f.checked} episodes fit`}>
      {parts.filter(([n]) => n > 0).map(([n, label, c]) => <span key={label} style={{ color: c }}>{n} {label}</span>)}
    </span>
  );
}

// EpisodeBar is the series card's episode progress along the poster's foot.
function EpisodeBar({ s }: { s: Series }) {
  const st = statsOf(s);
  const pct = st.episodes > 0 ? Math.round((st.have_files / st.episodes) * 100) : 0;
  const complete = st.episodes > 0 && st.missing === 0;
  return (
    <div className="absolute inset-x-0 bottom-0 flex items-center gap-1.5 px-2 py-1.5" style={{ background: "linear-gradient(to top, rgba(0,0,0,.78), transparent)" }}>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full" style={{ background: "rgba(255,255,255,.25)" }}>
        <div className="h-full rounded-full" style={{ width: `${pct}%`, background: complete ? "var(--good)" : "var(--accent)" }} />
      </div>
      <span className="font-mono text-xs text-white" title={st.unmonitored_missing > 0 ? `+${st.unmonitored_missing} aired episodes not monitored` : undefined}>{st.have_files}/{st.episodes}</span>
    </div>
  );
}

const STATUS_RANK: Record<string, number> = { Complete: 0, Partial: 1, Wanted: 2, Unmonitored: 3 };
const ENDED = /end|cancel/i;

// nextAiring says when a show's next episode airs, from its YYYY-MM-DD date: "Airs today",
// "Airs tomorrow", "Next Thu" within the week, otherwise "Next Oct 20". "" for no date or
// one already past.
export function nextAiring(date: string | undefined, now = new Date()): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(date ?? "");
  if (!m) return "";
  const day = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const days = Math.round((day.getTime() - today.getTime()) / 86_400_000);
  if (days < 0) return "";
  if (days === 0) return "Airs today";
  if (days === 1) return "Airs tomorrow";
  if (days < 7) return `Next ${day.toLocaleDateString(undefined, { weekday: "short" })}`;
  return `Next ${day.toLocaleDateString(undefined, { month: "short", day: "numeric" })}`;
}

// cardMeta is the caption under a series poster: "Network · Next Thu" for a show still
// airing with an episode coming, otherwise "2019 · 3 sns".
export function cardMeta(s: Series, now = new Date()): string {
  const next = ENDED.test(s.status ?? "") ? "" : nextAiring(s.stats?.next_air_date, now);
  if (next) return s.network ? `${s.network} · ${next}` : next;
  const n = statsOf(s).seasons;
  return `${s.year || "—"} · ${n} ${n === 1 ? "sn" : "sns"}`;
}

export interface SeriesConfigDeps {
  fits: Map<number, SeriesFitSummary>;
  actions: (s: Series) => MenuItem[];
  rowAction: (s: Series) => ReactNode;
}

export function seriesLibraryConfig({ fits, actions, rowAction }: SeriesConfigDeps): LibraryConfig<Series> {
  return {
    noun: { one: "series", many: "series" },
    storageKey: "series",
    detailPath: (s) => `/series/${s.id}`,
    filters: [
      { key: "all", label: "All" },
      { key: "monitored", label: "Monitored", match: (s) => s.monitored },
      { key: "continuing", label: "Continuing", match: (s) => /return|continu/i.test(s.status ?? "") },
      { key: "ended", label: "Ended", match: (s) => ENDED.test(s.status ?? "") },
      { key: "wanted", label: "Wanted", match: (s) => isWanted(statusInput(s)) },
    ],
    searchText: (s) => s.title,
    compare: (a, b) => a.title.localeCompare(b.title),
    tableMinWidth: 820,
    columns: [
      { key: "status", label: "Status", sort: (s) => STATUS_RANK[seriesStatus(s).label] ?? 4, cell: (s) => { const st = seriesStatus(s); return <span className="font-mono text-2xs uppercase" style={{ color: st.color }}>{st.label}</span>; } },
      { key: "network", label: "Network", sort: (s) => (s.network ? s.network.toLowerCase() : undefined), cell: (s) => s.network || "—" },
      { key: "seasons", label: "Seasons", align: "right", descFirst: true, className: "font-mono text-xs text-ink-dim", sort: (s) => statsOf(s).seasons, cell: (s) => statsOf(s).seasons },
      // How complete the show is, then how big: 151/163 sorts below 8/8.
      { key: "episodes", label: "Episodes", align: "right", descFirst: true, className: "font-mono text-xs", sort: (s) => { const st = statsOf(s); return st.episodes > 0 ? st.have_files / st.episodes + st.episodes / 1e6 : undefined; }, cell: (s) => {
        const st = statsOf(s);
        return <><span style={{ color: st.missing === 0 && st.episodes > 0 ? "var(--good)" : "var(--ink-dim)" }}>{st.have_files}/{st.episodes}</span><NotMonitored n={st.unmonitored_missing} /></>;
      } },
      { key: "size", label: "Size", align: "right", descFirst: true, className: "font-mono text-xs text-ink-dim", sort: (s) => statsOf(s).size_bytes || undefined, cell: (s) => gb(statsOf(s).size_bytes) },
      { key: "monitored", label: "Monitored", sort: (s) => (s.monitored ? 0 : 1), cell: (s) => <span className="font-mono text-2xs uppercase" style={{ color: s.monitored ? "var(--accent)" : "var(--ink-faint)" }}>{s.monitored ? "Yes" : "No"}</span> },
      // Share of judged episodes that fit: ascending puts the worst shows first.
      { key: "fit", label: "Fit", title: "How the episode files fit this show's profile — sort ascending for the worst first; open a show for each episode's reasons", sort: (s) => { const f = fits.get(s.id); return f && f.checked > 0 ? f.fits / f.checked : undefined; }, cell: (s) => <SeriesFit f={fits.get(s.id)} /> },
    ],
    sorts: [
      { key: "title", label: "Title" },
      { key: "added", label: "Recently added", dir: "desc", value: (s) => s.added_at || undefined },
      { key: "next", label: "Next airing", value: (s) => (ENDED.test(s.status ?? "") ? undefined : s.stats?.next_air_date || undefined) },
      { key: "size", label: "Size", dir: "desc", value: (s) => statsOf(s).size_bytes || undefined },
      { key: "missing", label: "Missing episodes", dir: "desc", value: (s) => statsOf(s).missing || undefined },
    ],
    card: {
      meta: (s) => cardMeta(s),
      status: seriesStatus,
      overlay: (s) => <EpisodeBar s={s} />,
    },
    actions,
    rowAction,
  };
}
