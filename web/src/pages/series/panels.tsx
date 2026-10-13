import { useCallback, useEffect, useState } from "react";
import { api, type BlockEntry, type DuplicateEpisodeFile, type SceneOverride, type Series as SeriesT, type SeriesAlias } from "../../lib/api";
import { seriesApi } from "../../lib/seriesApi";

// SeriesBlocklistPanel mirrors the movie blocklist: releases rejected via Replace/stall-failover,
// with a Remove action. Hidden when empty.
export function SeriesBlocklistPanel({ seriesId, refreshKey }: { seriesId: number; refreshKey: unknown }) {
  const [entries, setEntries] = useState<BlockEntry[] | null>(null);
  const load = () => seriesApi.seriesBlocklist(seriesId).then(setEntries).catch(() => setEntries([]));
  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seriesId, refreshKey]);
  const unblock = async (bid: number) => { await seriesApi.unblockSeries(seriesId, bid); load(); };
  if (!entries || entries.length === 0) return null;
  return (
    <div className="mt-8">
      <h2 className="m-0 mb-3 text-md font-bold">Blocklist <span className="font-normal text-ink-faint">· {entries.length}</span></h2>
      <div className="flex flex-col gap-2">
        {entries.map((e) => (
          <div key={e.id} className="flex items-center gap-3 rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
            <div className="min-w-0 flex-1">
              <div className="truncate font-mono text-sm" title={e.title}>{e.title}</div>
              <div className="mt-0.5 text-xs text-ink-faint">{e.reason}{e.indexer ? ` · ${e.indexer}` : ""}</div>
            </div>
            <button onClick={() => unblock(e.id)} className="flex-none rounded-lg px-2.5 py-1 text-xs font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Remove</button>
          </div>
        ))}
      </div>
    </div>
  );
}

// AliasPanel manages the other titles a show is released under. Every show has it,
// collapsed: refresh seeds it from TMDB's alternative titles (the romaji name fansub groups
// use, US/UK variant titles), so most shows never need it opened.
//
// An arc released as its own show ("BLEACH Thousand-Year Blood War" for Bleach) is a
// title no amount of normalising will ever match, so those releases are discarded
// before they're scored. Pinning the alias to a TMDB season also fixes the numbering:
// the arc's own S01/S02 are cours inside that season, not the series' own seasons.
export function AliasPanel({ series, defaultOpen = false }: { series: SeriesT; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  const [rows, setRows] = useState<SeriesAlias[] | null>(null);
  const [title, setTitle] = useState("");
  const [season, setSeason] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api.seriesAliases(series.id).then((r) => setRows(r ?? [])).catch(() => setRows([]));
  }, [series.id]);
  useEffect(load, [load]);

  const add = async () => {
    setErr(null);
    setBusy(true);
    try {
      await api.addSeriesAlias(series.id, { title: title.trim(), tmdb_season: Number(season || 0) });
      setTitle(""); setSeason("");
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const empty = rows !== null && rows.length === 0;
  const count = rows?.length ?? 0;

  return (
    <div className="mt-3 rounded-xl p-3.5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <button
        type="button"
        className="flex w-full items-center gap-2 text-left"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="text-base font-semibold">Alternate titles</span>
        {count > 0 && <span className="font-mono text-xs text-ink-faint">{count}</span>}
        <span className="ml-auto font-mono text-xs text-ink-faint">{open ? "Hide" : "Show"}</span>
      </button>
      {open && (<>
      <p className="mb-2.5 mt-0.5 text-xs text-ink-faint">
        Other names this show is released under. Titles from TMDB are added on refresh and only match
        exactly. Add your own for an arc that ships as its own show, and set &ldquo;maps to season&rdquo; so
        the alias&apos; <code className="mx-1 font-mono">S02E02</code> reads as &ldquo;second cour, episode 2&rdquo; of
        that season — without it the title matches but the numbering is still wrong.
      </p>

      {rows && rows.length > 0 && (
        <div className="mb-2.5 flex flex-col gap-1.5">
          {rows.map((a) => (
            <div key={a.id} className="flex items-center gap-2 rounded-lg px-2.5 py-1.5" style={{ background: "var(--panel-2)" }}>
              <span className="min-w-0 truncate text-sm">{a.title}</span>
              <span className="shrink-0 font-mono text-xs text-ink-faint">
                {a.tmdb_season > 0 ? `→ season ${a.tmdb_season}` : "title only"}
              </span>
              <span
                className="shrink-0 rounded px-1.5 py-0.5 font-mono text-2xs uppercase"
                style={{ background: "var(--panel)", color: "var(--ink-faint)", border: "1px solid var(--line)" }}
              >
                {a.source === "tmdb" ? "from TMDB" : "added by you"}
              </span>
              <button
                className="ml-auto shrink-0 text-xs font-semibold"
                style={{ color: "var(--reject)" }}
                title={a.source === "tmdb" ? "Stops matching this title; a refresh won't add it back" : undefined}
                onClick={async () => { await api.deleteSeriesAlias(series.id, a.id); load(); }}
              >
                Remove
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-end gap-2">
        <label className="min-w-[220px] flex-1 text-xs text-ink-faint">
          <div>Release title</div>
          <input
            className="w-full rounded-lg px-2.5 py-1.5 text-base"
            style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="BLEACH Thousand-Year Blood War"
          />
        </label>
        <label className="text-xs text-ink-faint">
          <div>Maps to season</div>
          <input
            className="w-[104px] rounded-lg px-2 py-1.5 text-center text-base"
            style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
            inputMode="numeric"
            value={season}
            onChange={(e) => setSeason(e.target.value.replace(/[^0-9]/g, ""))}
            placeholder="optional"
          />
        </label>
        <button
          onClick={add}
          disabled={busy || title.trim() === ""}
          className="rounded-lg px-3.5 py-2 text-sm font-semibold disabled:opacity-50"
          style={{ border: "1px solid var(--line)", color: "var(--ink)" }}
        >
          {busy ? "Adding…" : "Add title"}
        </button>
      </div>
      {err && <p className="mb-0 mt-2 text-sm" style={{ color: "var(--reject)" }}>{err}</p>}
      {empty && !err && (
        <p className="mb-0 mt-2 text-xs text-ink-faint">No alternate titles — most shows never need one.</p>
      )}
      </>)}
    </div>
  );
}

// SceneMapPanel lets you pin where a scene/broadcast season starts in TMDB numbering —
// the manual escape hatch for anime whose cours don't line up. Anime-only: standard
// series match SxxExx directly, so a mapping there could only misroute.
//
// Automatic resolution (per-cour positional → TheXEM → air-date gap) handles most shows;
// this exists for the ones it can't, where the only alternative was importing every
// episode by hand.
export function SceneMapPanel({ series, defaultOpen = false }: { series: SeriesT; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  const [rows, setRows] = useState<SceneOverride[] | null>(null);
  const [sceneSeason, setSceneSeason] = useState("");
  const [tmdbSeason, setTmdbSeason] = useState("");
  const [tmdbEpisode, setTmdbEpisode] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    seriesApi.sceneOverrides(series.id).then((r) => setRows(r.overrides ?? [])).catch(() => setRows([]));
  }, [series.id]);
  useEffect(load, [load]);

  const add = async () => {
    setErr(null);
    setBusy(true);
    try {
      await seriesApi.setSceneOverride(series.id, {
        scene_season: Number(sceneSeason),
        tmdb_season: Number(tmdbSeason),
        tmdb_episode: Number(tmdbEpisode),
      });
      setSceneSeason(""); setTmdbSeason(""); setTmdbEpisode("");
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const num = "w-[64px] rounded-lg px-2 py-1.5 text-center text-base";
  const numStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;
  const ready = sceneSeason !== "" && tmdbSeason !== "" && tmdbEpisode !== "";

  return (
    <div className="mt-3 rounded-xl p-3.5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <button type="button" className="flex w-full items-center gap-2 text-left" aria-expanded={open} onClick={() => setOpen((v) => !v)}>
        <span className="text-base font-semibold">Scene season mapping</span>
        {(rows?.length ?? 0) > 0 && <span className="font-mono text-xs text-ink-faint">{rows?.length}</span>}
        <span className="ml-auto font-mono text-xs text-ink-faint">{open ? "Hide" : "Show"}</span>
      </button>
      {open && (<>
      <p className="mb-2.5 mt-0.5 text-xs text-ink-faint">
        Only needed when releases number seasons differently to TMDB — e.g. this show ships as
        <code className="mx-1 font-mono">S02E01</code> but TMDB counts it as one long season. Pin where each
        scene season starts and the rest of the cour follows automatically.
      </p>

      {rows && rows.length > 0 && (
        <div className="mb-2.5 flex flex-col gap-1.5">
          {rows.map((o) => (
            <div key={o.scene_season} className="flex items-center gap-2 rounded-lg px-2.5 py-1.5" style={{ background: "var(--panel-2)" }}>
              <span className="font-mono text-sm">
                Scene S{String(o.scene_season).padStart(2, "0")} → S{String(o.tmdb_season).padStart(2, "0")}E{String(o.tmdb_episode).padStart(2, "0")}
              </span>
              <button
                className="ml-auto text-xs font-semibold"
                style={{ color: "var(--reject)" }}
                onClick={async () => { await seriesApi.deleteSceneOverride(series.id, o.scene_season); load(); }}
              >
                Remove
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-end gap-2">
        <label className="text-xs text-ink-faint">
          <div>Scene season</div>
          <input className={num} style={numStyle} inputMode="numeric" value={sceneSeason} onChange={(e) => setSceneSeason(e.target.value)} placeholder="2" />
        </label>
        <span className="pb-1.5 text-sm text-ink-faint">starts at</span>
        <label className="text-xs text-ink-faint">
          <div>TMDB season</div>
          <input className={num} style={numStyle} inputMode="numeric" value={tmdbSeason} onChange={(e) => setTmdbSeason(e.target.value)} placeholder="1" />
        </label>
        <label className="text-xs text-ink-faint">
          <div>Episode</div>
          <input className={num} style={numStyle} inputMode="numeric" value={tmdbEpisode} onChange={(e) => setTmdbEpisode(e.target.value)} placeholder="13" />
        </label>
        <button
          disabled={!ready || busy}
          onClick={add}
          className="rounded-lg px-3 py-1.5 text-sm font-semibold"
          style={{ background: ready ? "var(--accent)" : "var(--panel-2)", color: ready ? "var(--accent-ink)" : "var(--ink-faint)" }}
        >
          {busy ? "Saving…" : "Add mapping"}
        </button>
      </div>
      {err && <div className="mt-2 text-sm" style={{ color: "var(--reject)" }}>{err}</div>}
      </>)}
    </div>
  );
}

export function fmtBytes(n: number): string {
  if (!n || n <= 0) return "";
  const gb = n / 1024 ** 3;
  return gb >= 1 ? `${gb.toFixed(2)} GB` : `${(n / 1024 ** 2).toFixed(0)} MB`;
}

// DuplicatesPanel surfaces episodes with more than one file on disk. These pile up when a
// re-import derives a different filename (a re-classified source tag, or the episode title
// being added or dropped): the library tracks the newest copy and every earlier one is
// orphaned — invisible, still eating disk, and a duplicate episode in Plex.
export function DuplicatesPanel({ seriesId, refreshKey, flash }: { seriesId: number; refreshKey: unknown; flash: (m: string) => void }) {
  const [dupes, setDupes] = useState<DuplicateEpisodeFile[] | null>(null);
  const [reclaim, setReclaim] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api.seriesDuplicates(seriesId)
      .then((r) => { setDupes(r.duplicates ?? []); setReclaim(r.reclaimable_bytes ?? 0); })
      .catch(() => setDupes([]));
  }, [seriesId]);
  useEffect(() => { load(); }, [load, refreshKey]);

  const remove = async (path: string) => {
    setBusy(path); setError(null);
    try { await api.deleteSeriesDuplicate(seriesId, path); flash("Duplicate deleted."); load(); }
    catch (e) { setError((e as Error).message); }
    finally { setBusy(null); }
  };

  // Nothing to say when the library is clean — don't add a permanently empty panel.
  if (dupes === null || dupes.length === 0) return null;

  const extras = dupes.reduce((n, d) => n + d.extras.length, 0);
  return (
    <div className="mt-8">
      <h2 className="m-0 mb-1 text-md font-bold" style={{ color: "var(--avoid)" }}>Duplicate files</h2>
      <p className="mb-3 text-sm text-ink-dim">
        {extras} extra file{extras === 1 ? "" : "s"} on disk across {dupes.length} episode{dupes.length === 1 ? "" : "s"}
        {reclaim > 0 && <> · {fmtBytes(reclaim)} reclaimable</>}. These are older copies left behind when a re-import
        wrote a different filename. Arrmada tracks the one marked <em>keeping</em>; the rest are unused.
      </p>
      {error && <div className="mb-3 rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--reject)", color: "var(--reject)" }}>{error}</div>}
      <div className="overflow-hidden rounded-xl" style={{ border: "1px solid var(--line)" }}>
        {dupes.map((d) => (
          <div key={`${d.season}-${d.episode}`} className="border-b p-3 last:border-b-0" style={{ borderColor: "var(--line-soft)" }}>
            <div className="mb-1.5 font-mono text-xs font-bold">{`S${String(d.season).padStart(2, "0")}E${String(d.episode).padStart(2, "0")}`}</div>
            <div className="flex items-center gap-2 py-1 text-sm">
              <span className="flex-none rounded px-1.5 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: "var(--panel-2)", color: "var(--good)" }}>keeping</span>
              <span className="min-w-0 flex-1 truncate text-ink-dim" title={d.keeping.path}>{d.keeping.filename}</span>
              <span className="flex-none font-mono text-xs text-ink-faint">{fmtBytes(d.keeping.size_bytes)}</span>
            </div>
            {d.extras.map((x) => (
              <div key={x.path} className="flex items-center gap-2 py-1 text-sm">
                <span className="flex-none rounded px-1.5 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: "var(--panel-2)", color: "var(--avoid)" }}>extra</span>
                <span className="min-w-0 flex-1 truncate text-ink-dim" title={x.path}>{x.filename}</span>
                <span className="flex-none font-mono text-xs text-ink-faint">{fmtBytes(x.size_bytes)}</span>
                <button disabled={busy !== null} onClick={() => remove(x.path)} className="flex-none rounded-lg px-2.5 py-1 text-xs font-semibold disabled:opacity-50" style={{ border: "1px solid var(--reject)", color: "var(--reject)" }}>
                  {busy === x.path ? "Deleting…" : "Delete"}
                </button>
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}
