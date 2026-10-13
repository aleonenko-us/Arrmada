import { useContext, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Link, Navigate, useSearchParams } from "react-router-dom";
import { PageHeader } from "../components/PageHeader";
import { LINKS } from "../lib/links";
import { useTabParam } from "../lib/useTabParam";
import { usePoll } from "../lib/usePoll";
import { hwBadge } from "../lib/plexHw";
import { TabPanel, Tabs } from "../ui/Tabs";
import { EmptyState } from "../ui/EmptyState";
import { IconButton, Modal, useToast } from "../ui";
import { type PlexConfig, type PlexStatus, type InsightsActivity, type InsightsStream, type HistoryEntry, type InsightsStats, type UserEntry, type LibraryStat, type RecentItem, type InsightsGraphs, type NameStat, type Reliability, type BufferGroup } from "../lib/api";
import { insightsApi } from "../lib/insightsApi";
import { useSearchParamState } from "../lib/useSearchState";
import { Page } from "../ui/Page";
import { CAUSE, Chip, DECISION, FilterGroup, InsightsCfg, StatTile, fmtClock, fmtDate, fmtDur, fmtHours, fmtHoursValue, fmtMbps, fmtStall, geoLabel, dayLabel, bandwidthLabels, type Metric } from "./insights/shared";
import { BarChart, ChartCard, HBarChart, Legend, LineChart } from "./insights/charts";

// Insights — Arrmada's Plex watch monitoring (a Tautulli replacement): live Activity, History,
// People, Graphs and Reliability (buffering). The Plex connection and the alerts used to be
// tabs here; they live in Settings → Connections (the Plex card) and Settings → Alerts now.
type Tab = "activity" | "history" | "people" | "graphs" | "reliability";
const TABS: { key: Tab; label: string }[] = [
  { key: "activity", label: "Activity" },
  { key: "history", label: "History" },
  { key: "people", label: "People" },
  { key: "graphs", label: "Graphs" },
  { key: "reliability", label: "Reliability" },
];
// The tabs that read recorded plays: they only grow while monitoring is on, so they carry
// the "monitoring is off" banner when it isn't.
const RECORDED_TABS: Tab[] = ["history", "people", "graphs", "reliability"];

// The header badge says what monitoring is doing, not just that a URL and token are saved:
// Activity asks Plex directly and works either way, so a green "connected" used to hide
// that nothing was being recorded.
const BADGE: Record<PlexStatus, { label: string; color: string; soft: string }> = {
  unconfigured: { label: "Not connected", color: "var(--avoid)", soft: "var(--avoid-soft)" },
  off: { label: "Connected · not recording", color: "var(--avoid)", soft: "var(--avoid-soft)" },
  recording: { label: "Recording", color: "var(--good)", soft: "var(--good-soft)" },
  unreachable: { label: "Plex unreachable", color: "var(--reject)", soft: "var(--reject-soft)" },
};

function MonitoringBadge({ c }: { c: PlexConfig }) {
  const b = BADGE[c.status] ?? BADGE.unconfigured;
  const title = c.status === "unreachable" && c.last_error ? c.last_error
    : c.status === "recording" && c.last_poll_at ? `Last answer from Plex ${fmtDate(c.last_poll_at)}` : undefined;
  return (
    <span title={title} className="inline-flex items-center gap-2 rounded-full px-3 py-1.5 text-sm font-semibold" style={{ border: `1px solid ${b.color}`, background: b.soft }}>
      <span className="h-2 w-2 rounded-full" style={{ background: b.color }} />
      {b.label}
    </span>
  );
}

// MonitoringOff sits above the recorded-plays tabs while Plex is connected but monitoring
// is switched off, so an empty or stale History isn't mistaken for nobody watching.
function MonitoringOff({ busy, onTurnOn }: { busy: boolean; onTurnOn: () => void }) {
  return (
    <div role="status" className="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-lg px-3.5 py-2 text-sm" style={{ border: "1px solid var(--avoid)", background: "var(--avoid-soft)", color: "var(--ink)" }}>
      <span>Monitoring is off — nothing new is recorded.</span>
      <button onClick={onTurnOn} disabled={busy} className="rounded-lg px-3 py-1 text-sm font-semibold disabled:opacity-60" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>
        {busy ? "Turning on…" : "Turn on"}
      </button>
    </div>
  );
}

export function Insights() {
  // The old Notifications tab's address (bookmarks, earlier copy) opens Alerts instead.
  const [params] = useSearchParams();
  if (params.get("tab") === "notifications") return <Navigate to={LINKS.alerts} replace />;
  // Users was renamed People; old links keep working.
  if (params.get("tab") === "users") return <Navigate to="/insights?tab=people" replace />;
  // The Plex connection moved from the old Settings tab to its card on Settings → Connections.
  if (params.get("tab") === "settings") return <Navigate to={LINKS.plexConnection} replace />;
  return <InsightsPage />;
}

function InsightsPage() {
  const [tab, setTab] = useTabParam(TABS.map((t) => t.key), "activity");
  const [cfg, setCfg] = useState<PlexConfig | null>(null);
  const toast = useToast();
  const say = (m: string, err = false) => toast(m, { tone: err ? "error" : "info", ms: 3500 });

  // cfgDone: the connection settings have answered (or failed). Until then the data tabs
  // wait, so a connected server doesn't flash the "imported history" notice or the
  // setup state while its settings load.
  const [cfgDone, setCfgDone] = useState(false);
  // Re-read every 15 s for the monitoring status, so the badge turns "unreachable" (and
  // back) on its own. The form that edits these settings lives in Settings now, so
  // nothing here is being typed into for a poll to overwrite.
  usePoll(() => insightsApi.insightsConfig()
    .then(setCfg)
    .catch(() => { if (!cfgDone) say("Could not load Plex settings", true); })
    .finally(() => setCfgDone(true)), 15000);
  const status = cfg?.status;
  const connected = !!status && status !== "unconfigured";

  // Turn on is the banner's one-click fix; it saves only the switch (a missing URL keeps
  // the saved one).
  const [turningOn, setTurningOn] = useState(false);
  const turnOn = async () => {
    if (!cfg) return;
    setTurningOn(true);
    try { setCfg(await insightsApi.updateInsightsConfig({ enabled: true })); say("Monitoring is on"); }
    catch (e) { say((e as Error).message, true); } finally { setTurningOn(false); }
  };

  return (
    <>
      <PageHeader title="Insights" />
      <Page>
        <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
          <p className="max-w-[64ch] text-base text-ink-dim">Watch monitoring for your Plex server — who's streaming what, right now and historically, with stream quality, transcode diagnostics and buffering reliability.
            {status === "unconfigured" && <> <Link to={LINKS.plexConnection} className="font-semibold" style={{ color: "var(--accent)" }}>Connect your Plex server in Settings → Connections</Link> to begin.</>}
          </p>
          <div className="flex flex-wrap items-center gap-2">
            {cfg && <MonitoringBadge c={cfg} />}
            {/* Monitoring, library updates, sign-in and linked accounts: one Plex page. */}
            <Link to={LINKS.plex} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" }}>Plex settings →</Link>
          </div>
        </div>

        <Tabs tabs={TABS} value={tab} onChange={setTab} idPrefix="insights" label="Insights sections" />

        {status === "off" && RECORDED_TABS.includes(tab) && <MonitoringOff busy={turningOn} onTurnOn={turnOn} />}

        <InsightsCfg.Provider value={cfg}>
        <TabPanel idPrefix="insights" value={tab}>
          {!cfgDone ? (
            <div className="rounded-xl p-10 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>Loading…</div>
          ) : tab === "activity" ? (
            <ActivityView connected={!!connected} />
          ) : tab === "history" ? (
            <HistoryView connected={!!connected} />
          ) : tab === "people" ? (
            <UsersView connected={!!connected} />
          ) : tab === "graphs" ? (
            <GraphsView connected={!!connected} />
          ) : (
            <ReliabilityView connected={!!connected} />
          )}
        </TabPanel>
        </InsightsCfg.Provider>
      </Page>
    </>
  );
}

/* ============================= ACTIVITY ============================= */
function ActivityView({ connected }: { connected: boolean }) {
  const [act, setAct] = useState<InsightsActivity | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [detail, setDetail] = useState<InsightsStream | null>(null);
  // Per-session smoothed playback clock. Plex only updates viewOffset every ~10s, so
  // re-baselining to it every poll makes the bar sawtooth. Instead we run our own 1×
  // clock and only re-sync to Plex when it diverges a lot (a real seek/resume) — small
  // drift from Plex's lazy updates is ignored, so the timer counts smoothly.
  const clock = useRef<Record<string, { base: number; at: number }>>({});
  const [, tickNow] = useState(0);

  usePoll(() => insightsApi.insightsActivity().then((a) => { setAct(a); setErr(null); }).catch((e) => { setErr((e as Error).message); }), connected ? 4000 : null);

  // Re-render once a second so playing streams' progress interpolates in real time.
  usePoll(() => tickNow((n) => n + 1), 1000, { immediate: false });

  // Reconcile the smoothed clocks whenever a fresh snapshot arrives.
  useEffect(() => {
    if (!act) return;
    const now = Date.now();
    const SEEK_MS = 15000; // treat a >15s gap vs our clock as a seek, not Plex lag
    const next: Record<string, { base: number; at: number }> = {};
    for (const s of act.streams) {
      const prev = clock.current[s.session_key];
      if (s.state !== "playing" || !prev) {
        next[s.session_key] = { base: s.offset_ms, at: now };
        continue;
      }
      const predicted = prev.base + (now - prev.at);
      next[s.session_key] = Math.abs(s.offset_ms - predicted) > SEEK_MS
        ? { base: s.offset_ms, at: now } // real seek/resume → snap to Plex
        : { base: predicted, at: now };  // keep our smooth clock, ignore Plex's jitter
    }
    clock.current = next; // ended sessions drop out
  }, [act]);

  // The watch statistics come from the database, so they stay on screen whatever the live
  // call does: without a server they sit under the setup state (when there are any), and
  // an unreachable server only replaces the live-streams part.
  if (!connected) {
    return (
      <div className="flex flex-col gap-3.5">
        <SetupState tab="activity" />
        <HomeExtras live={false} />
      </div>
    );
  }

  // liveOffset reads the smoothed clock: advance while playing, hold otherwise; capped.
  const liveOffset = (s: InsightsStream): number => {
    const c = clock.current[s.session_key];
    const off = c ? (s.state === "playing" ? c.base + (Date.now() - c.at) : c.base) : s.offset_ms;
    return s.duration_ms > 0 ? Math.min(s.duration_ms, off) : off;
  };
  let live: ReactNode;
  if (err) {
    live = <div className="rounded-xl p-6 text-base" style={{ border: "1px solid var(--reject)", color: "var(--reject)" }}>Couldn’t reach Plex: {err}</div>;
  } else if (!act) {
    live = <div className="rounded-xl p-10 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>Loading activity…</div>;
  } else {
    const streams = act.streams;
    live = (
      <>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="text-base text-ink-dim">{streams.length ? `${streams.length} stream${streams.length === 1 ? "" : "s"} active` : "Nothing is playing right now."}</div>
          <div className="flex items-center gap-3 font-mono text-xs">
            <BW label="TOTAL" v={act.bandwidth.total_kbps} accent />
            <BW label="LAN" v={act.bandwidth.lan_kbps} />
            <BW label="WAN" v={act.bandwidth.wan_kbps} />
          </div>
        </div>
        {streams.length === 0 ? (
          <div className="rounded-xl p-12 text-center text-base text-ink-faint" style={{ border: "1px dashed var(--line)" }}>No active streams.</div>
        ) : (
          <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(360px, 100%), 1fr))" }}>
            {streams.map((s) => <StreamCard key={s.session_key} s={s} offsetMs={liveOffset(s)} onOpen={() => setDetail(s)} />)}
          </div>
        )}
        {!act.geo_active && streams.some((s) => !s.geo.local) && (
          <div className="text-xs text-ink-faint">Tip: drop a MaxMind <code>GeoLite2-City.mmdb</code> into the data dir (or set <code>ARRMADA_GEOIP_DB</code>) to resolve remote IPs to a city.</div>
        )}
      </>
    );
  }
  return (
    <div className="flex flex-col gap-3.5">
      {live}
      <HomeExtras live />
      {detail && <DeepDive s={detail} onClose={() => setDetail(null)} />}
    </div>
  );
}

/* ============================= HOME DASHBOARD (stats / libraries / recently added) ============================= */
// HomeExtras: watch statistics (from the database) plus library counts and recently added,
// which Plex answers live. Without a server (live=false) the Plex calls are skipped, and the
// block shows only when there are imported statistics to show.
function HomeExtras({ live }: { live: boolean }) {
  const [stats, setStats] = useState<InsightsStats | null>(null);
  const [libs, setLibs] = useState<LibraryStat[] | null>(null);
  const [recent, setRecent] = useState<RecentItem[] | null>(null);
  const [metric, setMetric] = useState<"plays" | "duration">("plays");
  // plexErr: a live call failed. The statistics above come from the database and stay; only
  // these two parts need the server, so they say so in place instead of vanishing.
  const [plexErr, setPlexErr] = useState<string | null>(null);

  useEffect(() => { insightsApi.insightsStats(30, metric).then(setStats).catch(() => setStats(null)); }, [metric]);
  useEffect(() => {
    if (!live) return;
    const failed = (e: unknown) => setPlexErr((e as Error).message || "no answer");
    insightsApi.insightsLibraries().then(setLibs).catch((e) => { setLibs([]); failed(e); });
    insightsApi.insightsRecentlyAdded(20).then(setRecent).catch((e) => { setRecent([]); failed(e); });
  }, [live]);

  const hasStats = stats && (stats.most_watched_movies.length || stats.most_watched_shows.length || stats.most_active_users.length);
  if (!live && !hasStats) return null;

  return (
    <div className="mt-4 flex flex-col gap-5">
      {/* Watch statistics */}
      <section>
        <div className="mb-2.5 flex items-center justify-between">
          <h3 className="text-md font-bold">Watch statistics <span className="font-normal text-ink-faint">· last 30 days</span></h3>
          <div className="flex gap-1">
            <Chip active={metric === "plays"} onClick={() => setMetric("plays")}>Plays</Chip>
            <Chip active={metric === "duration"} onClick={() => setMetric("duration")}>Duration</Chip>
          </div>
        </div>
        {!hasStats ? (
          <div className="rounded-xl p-6 text-center text-sm text-ink-faint" style={{ border: "1px dashed var(--line)" }}>No watch data yet — statistics build up as people stream.</div>
        ) : (
          <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(300px, 100%), 1fr))" }}>
            <StatCard title="Most watched movies" rows={stats!.most_watched_movies.map((m) => ({ label: m.title, thumb: m.thumb_url, v: metric === "plays" ? m.plays : m.secs }))} metric={metric} />
            <StatCard title="Most watched TV" rows={stats!.most_watched_shows.map((m) => ({ label: m.title, thumb: m.thumb_url, v: metric === "plays" ? m.plays : m.secs }))} metric={metric} />
            <StatCard title="Most active users" rows={stats!.most_active_users.map((u) => ({ label: u.name, v: metric === "plays" ? u.plays : u.secs }))} metric={metric} />
            <StatCard title="Most active platforms" rows={stats!.most_active_platforms.map((p) => ({ label: p.name, v: metric === "plays" ? p.plays : p.secs }))} metric={metric} />
          </div>
        )}
      </section>

      {plexErr && (
        <div className="rounded-xl px-4 py-3 text-sm" style={{ border: "1px solid var(--reject)", color: "var(--reject)" }}>
          Plex unreachable — library counts and recently added come straight from the server: {plexErr}
        </div>
      )}

      {/* Library statistics */}
      {libs && libs.length > 0 && (
        <section>
          <h3 className="mb-2.5 text-md font-bold">Library statistics</h3>
          <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(220px, 100%), 1fr))" }}>
            {libs.map((l) => (
              <div key={l.title} className="rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
                <div className="font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">{libTypeLabel(l.type)}</div>
                <div className="mt-0.5 text-md font-semibold">{l.title}</div>
                <div className="mt-1 text-3xl font-extrabold tracking-tight">{l.count.toLocaleString()}</div>
              </div>
            ))}
          </div>
        </section>
      )}

      {/* Recently added */}
      {recent && recent.length > 0 && (
        <section>
          <h3 className="mb-2.5 text-md font-bold">Recently added</h3>
          <div className="flex gap-3 overflow-x-auto pb-2 thin-scroll">
            {recent.map((it, i) => (
              <div key={i} className="flex-none" style={{ width: 96 }}>
                <div className="relative h-[144px] w-[96px] overflow-hidden rounded-lg" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
                  {it.thumb_url ? <img src={it.thumb_url} alt="" loading="lazy" className="h-full w-full object-cover" /> : null}
                </div>
                <div className="mt-1 truncate text-xs font-semibold" title={it.title}>{it.title}</div>
                <div className="truncate text-xs text-ink-faint">{it.subtitle}</div>
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function libTypeLabel(t: string): string {
  return t === "movie" ? "Movies" : t === "show" ? "TV Shows" : t === "artist" ? "Music" : t;
}

function StatCard({ title, rows, metric }: { title: string; rows: { label: string; thumb?: string; v: number }[]; metric: "plays" | "duration" }) {
  return (
    <div className="rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <div className="mb-2 text-base font-bold">{title}</div>
      {rows.length === 0 ? <div className="text-xs text-ink-faint">—</div> : (
        <ol className="flex flex-col gap-1.5">
          {rows.map((r, i) => (
            <li key={i} className="flex items-center gap-2 text-sm">
              <span className="w-3 flex-none font-mono text-xs text-ink-faint">{i + 1}</span>
              {r.thumb !== undefined && <span className="h-7 w-5 flex-none overflow-hidden rounded" style={{ background: "var(--panel-2)" }}>{r.thumb ? <img src={r.thumb} alt="" loading="lazy" className="h-full w-full object-cover" /> : null}</span>}
              <span className="min-w-0 flex-1 truncate">{r.label}</span>
              <span className="flex-none font-mono text-xs font-semibold" style={{ color: "var(--accent)" }}>{metric === "plays" ? r.v : fmtHours(r.v)}</span>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}

/* ============================= USERS ============================= */
function UsersView({ connected }: { connected: boolean }) {
  const [users, setUsers] = useState<UserEntry[] | null>(null);
  // Users come from recorded plays, so an import shows here before Plex is connected.
  useEffect(() => { insightsApi.insightsUsers().then(setUsers).catch(() => setUsers([])); }, []);
  if (!users) return <div className="rounded-xl p-10 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>Loading people…</div>;
  if (users.length === 0 && !connected) return <SetupState tab="people" />;
  if (users.length === 0) return <div className="rounded-xl p-10 text-center text-base text-ink-faint" style={{ border: "1px solid var(--line)" }}>Nobody has watched anything yet.</div>;
  return (
    <div className="flex flex-col gap-3">
      {!connected && <ImportedNotice />}
      <div className="overflow-x-auto rounded-xl" style={{ border: "1px solid var(--line)" }}>
        <table className="w-full border-collapse text-base" style={{ minWidth: 820 }}>
          <thead><tr style={{ background: "var(--panel-2)" }}>{["User", "Last seen", "Location", "Platform", "Last played", "Plays", "Watch time"].map((h) => <th key={h} className="px-3 py-2 text-left font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">{h}</th>)}</tr></thead>
          <tbody>
            {users.map((u, i) => (
              <tr key={u.id} style={{ borderTop: i === 0 ? "none" : "1px solid var(--line-soft)" }}>
                <td className="px-3 py-2 font-semibold">{u.username}</td>
                <td className="whitespace-nowrap px-3 py-2 font-mono text-xs text-ink-dim">{u.last_seen ? fmtDate(u.last_seen) : "never"}</td>
                <td className="px-3 py-2 font-mono text-xs text-ink-dim">{u.last_ip ? geoLabel(u.geo) : "—"}</td>
                <td className="px-3 py-2 text-ink-dim">{u.last_platform || "—"}</td>
                <td className="px-3 py-2 text-ink-dim"><div className="max-w-[240px] truncate">{u.last_title || "—"}</div></td>
                <td className="px-3 py-2 font-mono tabular-nums">{u.total_plays.toLocaleString()}</td>
                <td className="whitespace-nowrap px-3 py-2 font-mono tabular-nums text-ink-dim">{fmtDur(u.total_secs)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function BW({ label, v, accent }: { label: string; v: number; accent?: boolean }) {
  return (
    <span className="inline-flex items-baseline gap-1">
      <span className="text-xs font-bold uppercase tracking-wide text-ink-faint">{label}</span>
      <span style={{ color: accent ? "var(--accent)" : "var(--ink-dim)" }}>{fmtMbps(v)}<span className="text-xs text-ink-faint"> Mb/s</span></span>
    </span>
  );
}

function StreamCard({ s, offsetMs, onOpen }: { s: InsightsStream; offsetMs: number; onOpen: () => void }) {
  const d = DECISION[s.decision] ?? DECISION.direct_play;
  const hw = hwBadge(s);
  const buffering = s.state === "buffering";
  const pct = s.duration_ms > 0 ? Math.min(100, (offsetMs * 100) / s.duration_ms) : s.progress_pct;
  return (
    <button onClick={onOpen} className="relative flex gap-3 overflow-hidden rounded-xl p-3 text-left transition-colors" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      {/* The title's background art, faint under a panel-coloured wash so the text keeps its
          contrast in both themes. */}
      {s.art && (
        <>
          <img src={s.art} alt="" aria-hidden loading="lazy" className="pointer-events-none absolute inset-0 h-full w-full object-cover opacity-30" />
          <span aria-hidden className="pointer-events-none absolute inset-0" style={{ background: "linear-gradient(90deg, var(--panel) 35%, color-mix(in srgb, var(--panel) 72%, transparent) 100%)" }} />
        </>
      )}
      <div className="relative h-[104px] w-[70px] flex-none overflow-hidden rounded-md" style={{ background: "var(--panel-2)" }}>
        {s.thumb ? <img src={s.thumb} alt="" className="h-full w-full object-cover" loading="lazy" /> : null}
      </div>
      <div className="relative flex min-w-0 flex-1 flex-col">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <div className="truncate text-md font-semibold">{s.title}</div>
            <div className="truncate text-xs text-ink-dim">{s.subtitle}</div>
          </div>
          <span className="flex-none rounded-full px-2 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: d.color, color: "var(--accent-ink)" }}>{d.label}</span>
        </div>
        <div className="mt-1 truncate text-xs text-ink-dim">{s.user} · {s.player || s.platform}</div>
        <div className="truncate font-mono text-xs text-ink-faint">
          {geoLabel(s.geo)} · {fmtMbps(s.bandwidth_kbps)} Mb/s
          {hw && <> · <span style={hw.fellBack ? { color: "var(--avoid)" } : undefined} title={hw.fellBack ? "Plex fell back to CPU — hardware transcoding was requested but not used" : s.hw_title || undefined}>{hw.label}{hw.fellBack ? " (fell back)" : ""}</span></>}
        </div>
        {/* progress */}
        <div className="mt-auto pt-2">
          <div className="h-1.5 w-full overflow-hidden rounded-full" style={{ background: "var(--panel-2)" }}>
            <div className="h-full rounded-full" style={{ width: `${pct}%`, background: buffering ? "var(--avoid)" : "var(--accent)", transition: "width 1s linear" }} />
          </div>
          <div className="mt-1 flex items-center justify-between font-mono text-xs text-ink-faint">
            <span style={{ color: buffering ? "var(--avoid)" : undefined }}>{buffering ? "● Buffering" : s.state === "paused" ? "⏸ Paused" : "▶ Playing"}</span>
            <span>{fmtClock(offsetMs)} / {fmtClock(s.duration_ms)}</span>
          </div>
        </div>
      </div>
    </button>
  );
}

function DeepDive({ s, onClose }: { s: InsightsStream; onClose: () => void }) {
  const d = DECISION[s.decision] ?? DECISION.direct_play;
  const hw = hwBadge(s);
  const titleId = useId();
  return (
    <Modal onClose={onClose} labelledBy={titleId} size="md">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 id={titleId} className="m-0 truncate text-lg font-bold">{s.title}</h2>
          <p className="m-0 mt-0.5 text-sm text-ink-dim">{s.subtitle}</p>
        </div>
        <IconButton label="Close" onClick={onClose} className="text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <span className="rounded-full px-2.5 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: d.color, color: "var(--accent-ink)" }}>{d.label}</span>
        {hw && <span title={s.hw_title || undefined} className="rounded-full px-2.5 py-0.5 text-xs font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>{hw.label}{s.hw_title && hw.label !== "CPU" ? ` · ${s.hw_title}` : ""}</span>}
        {hw?.fellBack && <span className="rounded-full px-2.5 py-0.5 text-xs font-semibold" style={{ border: "1px solid var(--avoid)", background: "var(--avoid-soft)", color: "var(--avoid)" }}>Fell back to CPU</span>}
        {s.throttled && <span className="rounded-full px-2.5 py-0.5 text-xs font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Throttled</span>}
      </div>

      {s.reasons && s.reasons.length > 0 && (
        <div className="mb-3 rounded-lg p-3 text-sm" style={{ background: "var(--avoid-soft)", color: "var(--avoid)" }}>
          <div className="mb-1 text-xs font-bold uppercase tracking-wide">Why it's transcoding</div>
          <ul className="list-none space-y-0.5 p-0">{s.reasons.map((r, i) => <li key={i}>• {r}</li>)}</ul>
        </div>
      )}

      <div className="grid gap-px overflow-hidden rounded-lg text-sm" style={{ background: "var(--line)" }}>
        <DDRow label="User" a={s.user} />
        <DDRow label="Player" a={`${s.player || "—"} · ${s.platform} · ${s.product}`} />
        <DDRow label="Location" a={`${geoLabel(s.geo)}${s.ip ? ` · ${s.ip}` : ""} · ${s.location?.toUpperCase() || "—"}`} />
        <DDRow label="Bandwidth" a={`${fmtMbps(s.bandwidth_kbps)} Mb/s`} />
        <DDRow label="Video" a={s.video.src} b={s.video.stream} />
        <DDRow label="Audio" a={s.audio.src} b={s.audio.stream} />
        <DDRow label="Container" a={s.container.src} b={s.container.stream} />
        <DDRow label="Progress" a={`${fmtClock(s.offset_ms)} / ${fmtClock(s.duration_ms)} (${s.progress_pct}%)`} />
      </div>
    </Modal>
  );
}

function DDRow({ label, a, b }: { label: string; a: string; b?: string }) {
  return (
    <div className="flex items-center justify-between gap-3 px-3 py-2" style={{ background: "var(--panel)" }}>
      <span className="font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">{label}</span>
      <span className="text-right text-ink-dim">
        {a}{b ? <span style={{ color: "var(--avoid)" }}> → {b}</span> : null}
      </span>
    </div>
  );
}

/* ============================= HISTORY ============================= */
const TYPE_FILTERS: { key: string; label: string }[] = [
  { key: "", label: "All" }, { key: "movie", label: "Movies" }, { key: "episode", label: "TV" }, { key: "track", label: "Music" },
];
const DEC_FILTERS: { key: string; label: string }[] = [
  { key: "", label: "All" }, { key: "direct_play", label: "Direct Play" }, { key: "direct_stream", label: "Direct Stream" }, { key: "transcode", label: "Transcode" },
];

function HistoryView({ connected }: { connected: boolean }) {
  const [rows, setRows] = useState<HistoryEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [type, setType] = useState("");
  const [decision, setDecision] = useState("");
  const [q, setQ] = useState("");
  const [loading, setLoading] = useState(true);
  const [detail, setDetail] = useState<HistoryEntry | null>(null);
  const pageSize = 50;

  useEffect(() => { setPage(1); }, [type, decision, q]);
  // History is read from the database, not from Plex, so a Tautulli import is listed even
  // before a server is connected.
  useEffect(() => {
    let alive = true;
    setLoading(true);
    const run = () => insightsApi.insightsHistory({ type, decision, q, page, page_size: pageSize })
      .then((r) => { if (alive) { setRows(r.rows ?? []); setTotal(r.total); } })
      .catch(() => { if (alive) setRows([]); })
      .finally(() => { if (alive) setLoading(false); });
    const t = setTimeout(run, q ? 300 : 0); // debounce search
    return () => { alive = false; clearTimeout(t); };
  }, [type, decision, q, page]);

  // Nothing recorded at all (not just nothing matching a filter) and no server to record
  // from: the setup state says more than an empty table.
  const unfiltered = !type && !decision && !q;
  if (!connected && !loading && total === 0 && unfiltered) return <SetupState tab="history" />;

  const from = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const to = Math.min(page * pageSize, total);
  const maxPage = Math.max(1, Math.ceil(total / pageSize));

  return (
    <div className="flex flex-col gap-3">
      {!connected && (total > 0 || !unfiltered) && <ImportedNotice />}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <FilterGroup label="Type">{TYPE_FILTERS.map((f) => <Chip key={f.key} active={type === f.key} onClick={() => setType(f.key)}>{f.label}</Chip>)}</FilterGroup>
        <FilterGroup label="Stream">{DEC_FILTERS.map((f) => <Chip key={f.key} active={decision === f.key} onClick={() => setDecision(f.key)}>{f.label}</Chip>)}</FilterGroup>
        <input aria-label="Search title or user" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search title or user…" className="ml-auto rounded-lg px-3 py-1.5 text-sm" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)", minWidth: 200 }} />
      </div>

      <div className="overflow-x-auto rounded-xl" style={{ border: "1px solid var(--line)" }}>
        <table className="w-full border-collapse text-base" style={{ minWidth: 820 }}>
          <thead><tr style={{ background: "var(--panel-2)" }}>{["When", "User", "Title", "Player", "Location", "Stream", "Watched", ""].map((h) => <th key={h} className="px-3 py-2 text-left font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">{h}</th>)}</tr></thead>
          <tbody>
            {loading && rows.length === 0 ? (
              <tr><td colSpan={8} className="px-3 py-10 text-center text-sm text-ink-dim">Loading history…</td></tr>
            ) : rows.length === 0 ? (
              <tr><td colSpan={8} className="px-3 py-10 text-center text-sm text-ink-faint">No plays recorded yet. History fills in as people watch.</td></tr>
            ) : rows.map((r, i) => {
              const d = DECISION[r.decision] ?? DECISION.direct_play;
              return (
                <tr key={r.id} onClick={() => setDetail(r)} className="cursor-pointer" style={{ borderTop: i === 0 ? "none" : "1px solid var(--line-soft)" }}>
                  <td className="whitespace-nowrap px-3 py-2 font-mono text-xs text-ink-dim">{fmtDate(r.started_at)}</td>
                  <td className="px-3 py-2">{r.user_name}</td>
                  <td className="px-3 py-2"><div className="font-semibold">{r.title}</div>{r.subtitle && <div className="text-xs text-ink-faint">{r.subtitle}</div>}</td>
                  <td className="px-3 py-2 text-ink-dim">{r.player || r.platform}</td>
                  <td className="px-3 py-2 font-mono text-xs text-ink-dim">{geoLabel(r.geo)}</td>
                  <td className="px-3 py-2"><span className="rounded-full px-2 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: d.color, color: "var(--accent-ink)" }}>{d.label}</span></td>
                  <td className="whitespace-nowrap px-3 py-2 font-mono tabular-nums text-ink-dim">{fmtDur(r.watched_secs)}{r.buffer_count > 0 && <span title={`${r.buffer_count} buffer event(s)`} style={{ color: "var(--avoid)" }}> · ⚠{r.buffer_count}</span>}</td>
                  <td className="px-3 py-2 text-right text-ink-faint">›</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <div className="flex items-center justify-between text-sm text-ink-faint">
        <span>{total > 0 ? `${from}–${to} of ${total.toLocaleString()}` : "0 plays"}</span>
        <span className="flex items-center gap-2">
          <button disabled={page <= 1} onClick={() => setPage((p) => p - 1)} className="rounded-lg px-2.5 py-1 font-semibold disabled:opacity-40" style={{ border: "1px solid var(--line)", color: "var(--ink)" }}>Prev</button>
          <span className="font-mono">{page}/{maxPage}</span>
          <button disabled={page >= maxPage} onClick={() => setPage((p) => p + 1)} className="rounded-lg px-2.5 py-1 font-semibold disabled:opacity-40" style={{ border: "1px solid var(--line)", color: "var(--ink)" }}>Next</button>
        </span>
      </div>

      {detail && <HistoryDetail r={detail} onClose={() => setDetail(null)} />}
    </div>
  );
}

// HistoryHW is a recorded play's hardware badge. Plays now record a hardware encode Plex
// really used; older ones recorded "hardware was requested", which a CPU fallback also set.
function HistoryHW({ startedAt }: { startedAt: number }) {
  const since = useContext(InsightsCfg)?.hw_since ?? 0;
  const old = !since || startedAt < since;
  return (
    <span title={old ? `Plays before ${since ? fmtDate(since) : "this update"} show HW as requested — Plex may have fallen back to CPU.` : undefined}
      className="rounded-full px-2.5 py-0.5 text-xs font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>
      {old ? "HW requested" : "HW enc"}
    </span>
  );
}

function HistoryDetail({ r, onClose }: { r: HistoryEntry; onClose: () => void }) {
  const d = DECISION[r.decision] ?? DECISION.direct_play;
  const titleId = useId();
  return (
    <Modal onClose={onClose} labelledBy={titleId} size="md">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 id={titleId} className="m-0 truncate text-lg font-bold">{r.title}</h2>
          <p className="m-0 mt-0.5 text-sm text-ink-dim">{r.subtitle}</p>
        </div>
        <IconButton label="Close" onClick={onClose} className="text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
      </div>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <span className="rounded-full px-2.5 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: d.color, color: "var(--accent-ink)" }}>{d.label}</span>
        {r.hw_transcode && <HistoryHW startedAt={r.started_at} />}
        {r.buffer_count > 0 && <span className="rounded-full px-2.5 py-0.5 text-xs font-semibold" style={{ background: "var(--avoid-soft)", color: "var(--avoid)" }}>{r.buffer_count} buffer event{r.buffer_count === 1 ? "" : "s"}</span>}
      </div>
      <div className="grid gap-px overflow-hidden rounded-lg text-sm" style={{ background: "var(--line)" }}>
        <DDRow label="User" a={r.user_name} />
        <DDRow label="Player" a={`${r.player || "—"} · ${r.platform} · ${r.product}`} />
        <DDRow label="Location" a={`${geoLabel(r.geo)}${r.ip_address ? ` · ${r.ip_address}` : ""} · ${r.location?.toUpperCase() || "—"}`} />
        <DDRow label="Started" a={fmtDate(r.started_at)} />
        <DDRow label="Watched" a={`${fmtDur(r.watched_secs)}${r.paused_ms > 0 ? ` (paused ${fmtDur(Math.round(r.paused_ms / 1000))})` : ""}`} />
        <DDRow label="Video" a={r.video_src} b={r.video_stream} />
        <DDRow label="Audio" a={r.audio_src} b={r.audio_stream} />
        <DDRow label="Container" a={r.container_src} b={r.container_stream} />
      </div>
    </Modal>
  );
}

/* ============================= GRAPHS ============================= */
const SERIES = {
  tv: { label: "TV", color: "var(--accent)" },
  movies: { label: "Movies", color: "var(--good)", dash: "6 3" },
  music: { label: "Music", color: "var(--avoid)", dash: "2 3" },
};
// Stream types keep the decision colours; the dashes tell them apart for readers who
// can't separate those hues.
const STREAM_SERIES = {
  direct_play: { label: "Direct Play", color: DECISION.direct_play.color, dash: "6 3" },
  direct_stream: { label: "Direct Stream", color: DECISION.direct_stream.color },
  transcode: { label: "Transcode", color: DECISION.transcode.color, dash: "2 3" },
};
const DOW = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const DOW_LONG = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const WINDOWS = [7, 30, 90] as const;
const METRICS = ["plays", "duration"] as const;

function GraphsView({ connected }: { connected: boolean }) {
  const [g, setG] = useState<InsightsGraphs | null>(null);
  const [failed, setFailed] = useState(false);
  // The window and the metric live in the address, so a refresh or a shared link keeps them.
  const [winParam, setWin] = useSearchParamState<string>("window", "30", { allowed: WINDOWS.map(String) });
  const [metric, setMetric] = useSearchParamState<Metric>("metric", "plays", { allowed: METRICS });
  const win = Number(winParam) || 30;
  // Graphs are drawn from recorded plays, so imported history charts before Plex is connected.
  useEffect(() => {
    let live = true;
    insightsApi.insightsGraphs(win, metric).then((r) => { if (live) { setG(r); setFailed(false); } }).catch(() => { if (live) setFailed(true); });
    return () => { live = false; };
  }, [win, metric]);
  if (failed && !g) return <div className="rounded-xl p-10 text-center text-base" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>Couldn't load the graphs.</div>;
  if (!g) return <div className="rounded-xl p-10 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>Loading graphs…</div>;

  const hours = g.metric === "duration";
  const fmt = hours ? fmtHoursValue : (v: number) => String(Math.round(v));
  const unit = hours ? "Hours watched" : "Plays";
  const anyPlays = g.daily_tv.some(Boolean) || g.daily_movies.some(Boolean) || g.daily_music.some(Boolean);
  const dayLabels = g.days.map((d) => dayLabel(d));
  const dayTips = g.days.map((d) => dayLabel(d, true));
  // Nothing to chart and no server to record from. The window chips stay above it, so an
  // older import is still one click away.
  const nothing = !connected && !anyPlays && g.bandwidth.length === 0;
  const rank = (rows: NameStat[]) => rows.map((r) => ({ label: r.name, value: hours ? r.secs / 3600 : r.plays }));

  return (
    <div className="flex flex-col gap-4">
      {!connected && !nothing && <ImportedNotice />}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <FilterGroup label="Window">{WINDOWS.map((w) => <Chip key={w} active={win === w} onClick={() => setWin(String(w))}>{w}d</Chip>)}</FilterGroup>
        <FilterGroup label="Show">
          <Chip active={!hours} onClick={() => setMetric("plays")}>Plays</Chip>
          <Chip active={hours} onClick={() => setMetric("duration")}>Hours</Chip>
        </FilterGroup>
      </div>

      {nothing ? (
        <SetupState tab="graphs" />
      ) : !anyPlays ? (
        <div className="rounded-xl p-10 text-center text-sm text-ink-faint" style={{ border: "1px dashed var(--line)" }}>No plays in this window yet — graphs fill in as history accrues.</div>
      ) : (
        <>
          <ChartCard title={`Daily ${hours ? "hours watched" : "plays"} by media type`} extra={<Legend items={[SERIES.tv, SERIES.movies, SERIES.music]} />}>
            <LineChart caption={`Daily ${unit.toLowerCase()} by media type`} xLabels={dayLabels} tipTitles={dayTips} fmt={fmt} series={[
              { ...SERIES.tv, values: g.daily_tv },
              { ...SERIES.movies, values: g.daily_movies },
              { ...SERIES.music, values: g.daily_music },
            ]} />
          </ChartCard>

          <ChartCard title="Stream type over time" extra={<Legend items={[STREAM_SERIES.direct_play, STREAM_SERIES.direct_stream, STREAM_SERIES.transcode]} />}>
            <LineChart caption={`Daily ${unit.toLowerCase()} by stream type`} xLabels={dayLabels} tipTitles={dayTips} fmt={fmt} series={[
              { ...STREAM_SERIES.direct_play, values: g.daily_direct_play ?? [] },
              { ...STREAM_SERIES.direct_stream, values: g.daily_direct_stream ?? [] },
              { ...STREAM_SERIES.transcode, values: g.daily_transcode ?? [] },
            ]} />
          </ChartCard>

          <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(min(320px, 100%), 1fr))" }}>
            <ChartCard title={`${unit} by day of week`}><BarChart caption={`${unit} by day of week`} values={g.by_day_of_week} labels={DOW} tipTitles={DOW_LONG} fmt={fmt} /></ChartCard>
            <ChartCard title={`${unit} by hour of day`}><BarChart caption={`${unit} by hour of day`} values={g.by_hour} fmt={fmt}
              labels={g.by_hour.map((_, i) => (i % 3 === 0 ? String(i).padStart(2, "0") : ""))} tipTitles={g.by_hour.map((_, i) => `${String(i).padStart(2, "0")}:00–${String(i).padStart(2, "0")}:59`)} /></ChartCard>
            <ChartCard title="Top platforms"><HBarChart rows={rank(g.top_platforms)} fmt={fmt} /></ChartCard>
            <ChartCard title="Top users"><HBarChart rows={rank(g.top_users)} fmt={fmt} /></ChartCard>
          </div>
        </>
      )}

      {!nothing && <ChartCard title="Bandwidth (peak per hour)" extra={g.bandwidth.length > 0 ? <Legend items={[{ label: "Total", color: "var(--accent)" }, { label: "LAN", color: "var(--good)", dash: "6 3" }, { label: "WAN", color: "var(--avoid)", dash: "2 3" }]} /> : undefined}>
        {g.bandwidth.length === 0 ? <div className="py-6 text-center text-xs text-ink-faint">No bandwidth recorded yet.</div> : (
          <LineChart caption="Peak bandwidth per hour, Mb/s" xLabels={bandwidthLabels(g.bandwidth.map((p) => p.t))} tipTitles={g.bandwidth.map((p) => `${dayLabel(p.t.slice(0, 10), true)} ${p.t.slice(11)}`)}
            fmt={(v) => `${v >= 10 ? v.toFixed(0) : v.toFixed(1)} Mb/s`} series={[
              { label: "Total", color: "var(--accent)", values: g.bandwidth.map((p) => p.total_kbps / 1000) },
              { label: "LAN", color: "var(--good)", dash: "6 3", values: g.bandwidth.map((p) => p.lan_kbps / 1000) },
              { label: "WAN", color: "var(--avoid)", dash: "2 3", values: g.bandwidth.map((p) => p.wan_kbps / 1000) },
            ]} />
        )}
      </ChartCard>}
    </div>
  );
}

/* ============================= RELIABILITY (buffering history) ============================= */
function ReliabilityView({ connected }: { connected: boolean }) {
  const [r, setR] = useState<Reliability | null>(null);
  const [win, setWin] = useState(30);
  // Stalls are read from recorded sessions, so imported history shows before Plex is connected.
  useEffect(() => { insightsApi.insightsReliability(win).then(setR).catch(() => setR(null)); }, [win]);
  if (!r) return <div className="rounded-xl p-10 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>Loading reliability…</div>;

  const s = r.summary;
  const clean = s.total_events === 0;
  // No sessions in the window and no server to record more: the setup state, with the
  // window chips kept above it so an older import is still reachable.
  const nothing = !connected && s.total_sessions === 0;
  const rateColor = s.buffer_rate_pct === 0 ? "var(--good)" : s.buffer_rate_pct < 10 ? "var(--avoid)" : "var(--reject)";

  return (
    <div className="flex flex-col gap-4">
      {!connected && !nothing && <ImportedNotice />}
      <div className="flex items-center justify-between">
        <p className="max-w-[54ch] text-sm text-ink-dim">Where and when streams stalled mid-playback — sampled every few seconds, so treat counts as observed lower bounds. Startup, seeking and resume refills aren't counted.</p>
        <div className="flex gap-1">{[7, 30, 90].map((w) => <Chip key={w} active={win === w} onClick={() => setWin(w)}>{w}d</Chip>)}</div>
      </div>

      {/* Summary tiles */}
      {!nothing && <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(min(180px, 100%), 1fr))" }}>
        <StatTile label="Observed stall time" value={fmtStall(s.total_stall_ms)} sub="across the window" color={s.total_stall_ms ? "var(--avoid)" : "var(--good)"} />
        <StatTile label="Stall rate" value={`${s.buffer_rate_pct}%`} sub={`${s.buffered_sessions} of ${s.total_sessions} streams`} color={rateColor} />
        <StatTile label="Observed stalls" value={s.total_events.toLocaleString()} sub="sampled events" color={s.total_events ? "var(--avoid)" : "var(--good)"} />
      </div>}

      {nothing ? (
        <SetupState tab="reliability" />
      ) : clean ? (
        <div className="rounded-xl p-12 text-center" style={{ border: "1px dashed var(--line)", background: "var(--panel)" }}>
          <div className="text-3xl">✓</div>
          <div className="mt-1 text-md font-bold" style={{ color: "var(--good)" }}>Smooth sailing</div>
          <p className="mx-auto mt-1 max-w-[46ch] text-sm text-ink-dim">No mid-playback stalls observed in this window. When a stream stalls while playing (startup, seeks and resume refills don't count), it's logged here with who, what, and how it was playing — so you can pinpoint heavy transcodes or shaky connections.</p>
        </div>
      ) : (
        <>
          <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(min(300px, 100%), 1fr))" }}>
            <OffenderCard title="Worst-hit users" rows={r.by_user} />
            <OffenderCard title="Worst-hit platforms" rows={r.by_platform} />
            <OffenderCard title="Worst-hit titles" rows={r.by_title} />
          </div>

          {/* Why streams buffered — diagnosed cause breakdown */}
          {r.causes.length > 0 && (
            <div className="rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
              <div className="mb-2 text-base font-bold">Why streams stalled</div>
              <div className="flex flex-wrap gap-2">
                {r.causes.map((c) => {
                  const cc = CAUSE[c.cause] ?? CAUSE.unknown;
                  return (
                    <span key={c.cause} className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold" style={{ background: "var(--panel-2)", border: `1px solid ${cc.color}` }}>
                      <span className="inline-block h-2 w-2 rounded-full" style={{ background: cc.color }} />
                      {cc.label}<span className="font-mono text-ink-faint">{c.stall_ms > 0 ? fmtStall(c.stall_ms) : c.count}</span>
                    </span>
                  );
                })}
              </div>
            </div>
          )}

          {/* Timeline */}
          <div className="rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
            <div className="mb-2 text-base font-bold">Recent observed stalls</div>
            <div className="flex flex-col">
              {r.events.map((e, i) => {
                const d = DECISION[e.decision] ?? DECISION.direct_play;
                const cc = CAUSE[e.cause] ?? CAUSE.unknown;
                return (
                  <div key={i} className="py-1.5 text-sm" style={{ borderTop: i === 0 ? "none" : "1px solid var(--line-soft)" }}>
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
                      <span className="w-2 flex-none"><span className="inline-block h-2 w-2 rounded-full" style={{ background: cc.color }} /></span>
                      <span className="w-[92px] flex-none font-mono text-xs text-ink-faint">{fmtDate(e.at)}</span>
                      <span className="min-w-[140px] flex-1 truncate"><b className="font-semibold">{e.user}</b> · {e.title}</span>
                      <span className="flex-none font-mono text-xs text-ink-faint">@ {fmtClock(e.offset_ms)}</span>
                      {e.duration_ms > 0 && <span className="flex-none font-mono text-xs font-semibold" style={{ color: "var(--avoid)" }}>{fmtStall(e.duration_ms)}</span>}
                      <span className="flex-none rounded-full px-2 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: d.color, color: "var(--accent-ink)" }}>{d.label}</span>
                    </div>
                    {e.detail && <div className="pl-5 text-xs sm:pl-[112px]" style={{ color: cc.color }}>{e.detail}</div>}
                  </div>
                );
              })}
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function OffenderCard({ title, rows }: { title: string; rows: BufferGroup[] }) {
  // Rank/scale by observed stall time; legacy rows (pre-duration data) fall back to counts.
  const anyStall = rows.some((r) => r.stall_ms > 0);
  const metric = (r: BufferGroup) => (anyStall ? r.stall_ms : r.events);
  const max = Math.max(1, ...rows.map(metric));
  return (
    <div className="rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <div className="mb-2 text-base font-bold">{title}</div>
      {rows.length === 0 ? <div className="text-xs text-ink-faint">—</div> : (
        <div className="flex flex-col gap-2">
          {rows.map((r, i) => (
            <div key={i} className="text-sm">
              <div className="flex items-center justify-between gap-2">
                <span className="min-w-0 flex-1 truncate" title={r.name}>{r.name || "—"}</span>
                <span className="flex-none font-mono text-ink-dim">
                  {r.stall_ms > 0 ? `${fmtStall(r.stall_ms)} · ` : ""}{r.events} stall{r.events === 1 ? "" : "s"} · {r.rate_pct}%
                </span>
              </div>
              <span className="mt-0.5 block h-1.5 w-full overflow-hidden rounded-full" style={{ background: "var(--panel-2)" }}>
                <span className="block h-full rounded-full" style={{ width: `${(metric(r) / max) * 100}%`, background: "var(--avoid)" }} />
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

const ABOUT = {
  activity: "Live now-playing — who's streaming what, on which device, with progress, transcode decision, bandwidth and geolocation.",
  history: "Every play recorded — a filterable table with stream-type, geolocated IP and a click-through deep-dive.",
  people: "Per-person activity — last seen, platform, total plays and watch time.",
  graphs: "Plays by day, hour, platform and user, plus bandwidth over time.",
  reliability: "The buffering view — see historically when and where streams choked, by user, platform and title.",
};

// SetupState stands in for a tab only while no Plex server is set up and the database has
// nothing for it yet (an imported Tautulli history shows without a server). Every tab is
// built, so it says what the tab will show and links to the Plex settings page.
function SetupState({ tab }: { tab: keyof typeof ABOUT }) {
  return (
    <EmptyState
      title={<span className="capitalize">{tab}</span>}
      body={<p className="mx-auto max-w-[52ch] text-sm">{ABOUT[tab]}</p>}
      action={<Link to={LINKS.plexConnection} className="rounded-lg px-3.5 py-2 text-base font-semibold" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>Connect your Plex server →</Link>}
    />
  );
}

// ImportedNotice sits above data a tab already has while no server is connected — plays
// imported from Tautulli, or recorded before the connection was removed. The data is real;
// it just won't grow until Plex is connected.
function ImportedNotice() {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg px-3.5 py-2 text-sm text-ink-dim" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <span>Showing imported history — connect Plex to record new plays.</span>
      <Link to={LINKS.plexConnection} className="font-semibold" style={{ color: "var(--accent)" }}>Connect your Plex server →</Link>
    </div>
  );
}
