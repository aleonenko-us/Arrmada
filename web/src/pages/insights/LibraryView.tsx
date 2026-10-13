import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import type { InsightsLibrary, LibraryItem } from "../../lib/api";
import { insightsApi } from "../../lib/insightsApi";
import { useSearchParamState } from "../../lib/useSearchState";
import { Chip, FilterGroup, ImportedNotice, SetupState, fmtDay, fmtHours, type Metric } from "./shared";

// Library: what's watched most, what has sat unwatched since it was added, and which
// titles keep making Plex transcode — linked to the title in Arrmada and to Convert's
// library, where it can be converted. The tab only links: it never starts a conversion.

const WINDOWS = ["30", "90", "365"] as const;
type Win = (typeof WINDOWS)[number];
const WINDOW_LABEL: Record<Win, string> = { "30": "30d", "90": "90d", "365": "1y" };
const METRICS = ["plays", "duration"] as const;
const card = { border: "1px solid var(--line)", background: "var(--panel)" };
const linkStyle = { color: "var(--accent-text)" };

// arrmadaPath is the title's page in Arrmada, when Arrmada has it.
export function arrmadaPath(it: LibraryItem): string | null {
  if (!it.arrmada_id) return null;
  return it.media === "series" ? `/series/${it.arrmada_id}` : `/movies/${it.arrmada_id}`;
}

// convertPath is the title in Convert's library list (search pre-filled). It only opens
// the list; converting is still a choice made there.
export function convertPath(it: LibraryItem): string {
  const qs = new URLSearchParams({ tab: "library", media: it.media === "series" || it.kind === "show" ? "tv" : "movies", q: it.title });
  return `/convert?${qs.toString()}`;
}

function Poster({ url, w = 32 }: { url: string; w?: number }) {
  return (
    <span className="block flex-none overflow-hidden rounded" style={{ width: w, height: Math.round(w * 1.5), background: "var(--panel-2)" }}>
      {url ? <img src={url} alt="" loading="lazy" className="h-full w-full object-cover" /> : null}
    </span>
  );
}

function TitleLink({ it, className = "" }: { it: LibraryItem; className?: string }) {
  const to = arrmadaPath(it);
  const label = it.year ? `${it.title} (${it.year})` : it.title;
  return to
    ? <Link to={to} className={`truncate font-semibold hover:underline ${className}`} title={label}>{label}</Link>
    : <span className={`truncate font-semibold ${className}`} title={label}>{label}</span>;
}

export function LibraryView({ connected }: { connected: boolean }) {
  const [winParam, setWin] = useSearchParamState<Win>("window", "90", { allowed: WINDOWS });
  const [metric, setMetric] = useSearchParamState<Metric>("metric", "plays", { allowed: METRICS });
  const [v, setV] = useState<InsightsLibrary | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    insightsApi.insightsLibrary(Number(winParam), metric).then((r) => { if (live) { setV(r); setFailed(false); } }).catch(() => { if (live) setFailed(true); });
    return () => { live = false; };
  }, [winParam, metric]);

  if (failed && !v) return <div role="alert" className="rounded-xl p-10 text-center text-base" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>Couldn't load the library view.</div>;
  if (!v) return <div className="rounded-xl p-10 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>Loading the library…</div>;

  const hours = metric === "duration";
  const nothing = !v.most_watched_movies.length && !v.most_watched_shows.length && !v.hot_spots.length;
  if (!connected && nothing) return <SetupState tab="library" />;

  return (
    <div className="flex flex-col gap-4">
      {!connected && <ImportedNotice />}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <FilterGroup label="Window">{WINDOWS.map((w) => <Chip key={w} active={winParam === w} onClick={() => setWin(w)}>{WINDOW_LABEL[w]}</Chip>)}</FilterGroup>
        <FilterGroup label="Rank by">
          <Chip active={!hours} onClick={() => setMetric("plays")}>Plays</Chip>
          <Chip active={hours} onClick={() => setMetric("duration")}>Hours</Chip>
        </FilterGroup>
      </div>

      <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(min(340px, 100%), 1fr))" }}>
        <Ranked title="Most watched movies" items={v.most_watched_movies} hours={hours} />
        <Ranked title="Most watched TV" items={v.most_watched_shows} hours={hours} />
      </div>

      <section className="rounded-xl p-4" style={card}>
        <h3 className="m-0 text-md font-bold">Transcode hot spots</h3>
        <p className="mb-2.5 mt-0.5 text-sm text-ink-dim">The titles Plex converted most often in this window — candidates for Convert, which makes a copy more players can play directly.</p>
        {v.hot_spots.length === 0 ? <div className="text-sm text-ink-faint">Nothing was transcoded in this window.</div> : (
          <ul className="m-0 flex list-none flex-col p-0">
            {v.hot_spots.map((h, i) => (
              <li key={`${h.kind}:${h.rating_key || h.title}`} className="flex gap-3 py-2" style={{ borderTop: i === 0 ? "none" : "1px solid var(--line-soft)" }}>
                <Poster url={h.thumb_url} />
                <div className="min-w-0 flex-1">
                  <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                    <TitleLink it={h} />
                    <span className="font-mono text-xs text-ink-faint">{h.transcodes} of {h.total_plays} play{h.total_plays === 1 ? "" : "s"} transcoded</span>
                  </div>
                  <div className="text-sm text-ink-dim">
                    {h.top_pair || "Stream details not recorded (imported plays)"}
                    {h.platforms.length > 0 && <span className="text-ink-faint"> · {h.platforms.join(", ")}</span>}
                  </div>
                  {h.cause_label && <div className="text-xs" style={{ color: "var(--avoid-text)" }}>Stalls while transcoding: {h.cause_label}</div>}
                </div>
                <div className="flex flex-none flex-col items-end gap-1 text-sm">
                  {arrmadaPath(h) ? (
                    <>
                      <Link to={arrmadaPath(h)!} className="font-semibold" style={linkStyle}>Open</Link>
                      <Link to={convertPath(h)} className="font-semibold" style={linkStyle} title="Opens this title in Convert's library list">Find in Convert</Link>
                    </>
                  ) : <span className="text-xs text-ink-faint">Not in Arrmada</span>}
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="rounded-xl p-4" style={card}>
        <h3 className="m-0 text-md font-bold">Never watched</h3>
        <p className="mb-2.5 mt-0.5 text-sm text-ink-dim">Titles Plex added over 30 days ago that nobody has played, oldest first.</p>
        {v.never_watched_unavailable ? (
          <div role="status" className="rounded-lg px-3 py-2 text-sm" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink-dim)" }}>
            Needs Plex's library list, which isn't available right now (Plex not connected, or not read yet). Most watched and hot spots above come from recorded plays.
          </div>
        ) : v.never_watched.length === 0 ? (
          <div className="text-sm text-ink-faint">Everything added over 30 days ago has been played at least once.</div>
        ) : (
          <>
            {v.never_watched_total > v.never_watched.length && (
              <div className="mb-1.5 text-xs text-ink-faint">The oldest {v.never_watched.length} of {v.never_watched_total.toLocaleString()}.</div>
            )}
            <ul className="m-0 grid list-none gap-x-4 p-0" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(300px, 100%), 1fr))" }}>
              {v.never_watched.map((it) => (
                <li key={`${it.kind}:${it.rating_key}`} className="flex min-w-0 items-center gap-2.5 py-1.5" style={{ borderTop: "1px solid var(--line-soft)" }}>
                  <Poster url={it.thumb_url} w={28} />
                  <div className="flex min-w-0 flex-1 flex-col">
                    <TitleLink it={it} className="text-sm" />
                    <span className="text-xs text-ink-faint">{it.kind === "show" ? "Show" : "Movie"} · added {fmtDay(it.added_at ?? 0)}</span>
                  </div>
                </li>
              ))}
            </ul>
          </>
        )}
      </section>
    </div>
  );
}

function Ranked({ title, items, hours }: { title: string; items: LibraryItem[]; hours: boolean }) {
  return (
    <section className="min-w-0 rounded-xl p-4" style={card}>
      <h3 className="mb-2 mt-0 text-base font-bold">{title}</h3>
      {items.length === 0 ? <div className="text-xs text-ink-faint">Nothing played in this window.</div> : (
        <ol className="m-0 flex list-none flex-col gap-1.5 p-0">
          {items.map((it, i) => (
            <li key={`${it.title}:${i}`} className="flex min-w-0 items-center gap-2 text-sm">
              <span className="w-5 flex-none text-right font-mono text-xs text-ink-faint">{i + 1}</span>
              <Poster url={it.thumb_url} w={20} />
              <TitleLink it={it} className="min-w-0 flex-1 font-normal" />
              <span className="flex-none font-mono text-xs font-semibold" style={{ color: "var(--accent-text)" }}>
                {hours ? fmtHours(it.secs ?? 0) : `${it.plays ?? 0}`}
              </span>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
