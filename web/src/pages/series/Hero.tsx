import { useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { PlexPill, WatchedBy } from "../../components/PlexBits";
import type { Series as SeriesT } from "../../lib/api";
import { libraryStatus } from "../../lib/status";
import { StatusChip } from "../../ui";
import { fmtAirDay, fmtSize, nextEpisode, sxe } from "./util";
import { Page } from "../../ui/Page";

// Hero is the top of a show's page: the backdrop, poster and title, what state the show is
// in, when the next episode airs, how much of it is here and how big it is. The actions
// and anything else the page puts under the facts come in as children.
export function Hero({ series: s, children }: { series: SeriesT; children?: ReactNode }) {
  const ex = s.extra;
  const continuing = /return|continu/i.test(s.status ?? "");
  // Overall progress from the server's roll-up — the same numbers as the list's card, so
  // the two agree: only monitored episodes in monitored seasons count as missing.
  const have = s.stats?.have_files ?? 0;
  const counted = s.stats?.episodes ?? 0;
  const missing = s.stats?.missing ?? Math.max(0, counted - have);
  const notMonitored = s.stats?.unmonitored_missing ?? 0;
  const st = libraryStatus({ multi: true, hasFile: have > 0, monitored: s.monitored, have: counted - missing, total: counted });
  const next = nextEpisode(s);
  const nextDate = s.stats?.next_air_date;

  return (
    <div className="relative">
      {ex?.backdrop_url && (
        <>
          <div aria-hidden className="pointer-events-none absolute inset-0 bg-cover bg-center opacity-[0.45]" style={{ backgroundImage: `url(${ex.backdrop_url})` }} />
          <div aria-hidden className="pointer-events-none absolute inset-0" style={{ background: "linear-gradient(180deg, color-mix(in srgb, var(--bg) 35%, transparent) 0%, color-mix(in srgb, var(--bg) 70%, transparent) 55%, var(--bg) 100%)" }} />
        </>
      )}
      <Page className="relative py-5 sm:py-6">
        <Link to="/series" className="mb-4 inline-flex items-center gap-1 text-[12px] text-ink-dim hover:text-[var(--ink)]">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden><path d="M15 19l-7-7 7-7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>
          All series
        </Link>

        {/* A grid: on a phone the poster sits beside the title and facts while the overview
            and actions run full width under both; from sm up everything after the poster
            stays in the column beside it. */}
        <div className="grid grid-cols-[96px_minmax(0,1fr)] gap-x-4 sm:grid-cols-[180px_minmax(0,1fr)] sm:grid-rows-[auto_auto_1fr] sm:gap-x-6">
          <div className="row-span-1 w-[96px] self-start overflow-hidden rounded-xl sm:row-span-3 sm:w-[180px]" style={{ border: "1px solid var(--line)", aspectRatio: "2/3", boxShadow: "var(--shadow)" }}>
            {s.poster_url ? (
              <img src={s.poster_url} alt={s.title} className="h-full w-full object-cover" />
            ) : (
              <div className="flex h-full w-full items-end p-3" style={{ background: "linear-gradient(150deg, hsl(24 40% 30%), hsl(20 35% 16%))" }}>
                <span className="text-[13px] font-bold text-white sm:text-[15px]">{s.title}</span>
              </div>
            )}
          </div>

          <div className="min-w-0">
            <h2 className="m-0 text-[20px] font-bold leading-tight tracking-[-0.01em] sm:text-[26px]">{s.title}</h2>
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <StatusChip tone={st.tone} size="md">{st.label}</StatusChip>
              <StatusChip tone={continuing ? "good" : "faint"} size="md">{continuing ? "Continuing" : "Ended"}</StatusChip>
              {s.network && <span className="font-mono text-[11px] text-ink-dim">{s.network}</span>}
              {s.year > 0 && <span className="font-mono text-[11px] text-ink-faint">{s.year}</span>}
              <span className="flex items-center gap-2">
                {s.imdb_id && <a href={`https://www.imdb.com/title/${s.imdb_id}`} target="_blank" rel="noreferrer" className="rounded px-1.5 py-0.5 font-mono text-[10px] font-bold" style={{ background: "#f5c518", color: "#000" }}>IMDb</a>}
                <a href={`https://www.themoviedb.org/tv/${s.tmdb_id}`} target="_blank" rel="noreferrer" className="rounded px-1.5 py-0.5 font-mono text-[10px] font-bold" style={{ background: "#01b4e4", color: "#fff" }}>TMDB</a>
                <PlexPill media="series" tmdbId={s.tmdb_id} />
              </span>
            </div>

            <div className="mt-2.5 flex flex-col gap-0.5 text-[12.5px]" data-testid="series-facts">
              {nextDate && (
                <div style={{ color: "var(--ink)" }}>
                  <span className="text-ink-faint">Next: </span>
                  {next && <span className="font-mono">{sxe(next)} · </span>}
                  {fmtAirDay(nextDate)}
                </div>
              )}
              {counted > 0 && (
                <div className="text-ink-dim">
                  <span className="font-mono" style={{ color: "var(--ink)" }}>{have}/{counted}</span> monitored episodes
                  {(s.stats?.size_bytes ?? 0) > 0 && <> · <span className="font-mono">{fmtSize(s.stats?.size_bytes)}</span></>}
                  {notMonitored > 0 && <span className="text-ink-faint" title="Aired episodes without a file that aren't monitored — not counted as missing"> · {notMonitored} not monitored</span>}
                </div>
              )}
            </div>
            <WatchedBy kind="series" id={s.id} />
          </div>

          <div className="col-span-2 min-w-0 sm:col-span-1 sm:col-start-2">
            <Overview text={s.overview} />
            {ex?.genres && ex.genres.length > 0 && (
              <div className="mt-2.5 flex flex-wrap gap-1.5">{ex.genres.map((g) => <span key={g} className="rounded-full px-2 py-0.5 text-[11px]" style={{ background: "var(--panel-2)", color: "var(--ink-dim)" }}>{g}</span>)}</div>
            )}
          </div>

          <div className="col-span-2 min-w-0 sm:col-span-1 sm:col-start-2">{children}</div>
        </div>
      </Page>
    </div>
  );
}

// Overview clamps a long synopsis to a few lines with a More/Less toggle.
function Overview({ text }: { text?: string }) {
  const [open, setOpen] = useState(false);
  const long = (text?.length ?? 0) > 240;
  return (
    <div className="mt-3">
      <p className={`m-0 text-[13px] leading-relaxed text-ink-dim ${long && !open ? "line-clamp-3" : ""}`}>{text || "No overview available."}</p>
      {long && (
        <button type="button" onClick={() => setOpen((o) => !o)} className="mt-0.5 text-[11.5px] font-semibold" style={{ color: "var(--accent-text)" }} aria-expanded={open}>
          {open ? "Less" : "More"}
        </button>
      )}
    </div>
  );
}
