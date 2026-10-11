import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { DeleteSeriesDialog } from "../../components/DeleteSeriesDialog";
import { ReleaseSearchModal } from "../../components/ReleaseSearchModal";
import { UploadTorrentModal } from "../../components/UploadTorrentModal";
import { LastSearches, refreshSearches } from "../../components/LastSearch";
import { api, type Series as SeriesT } from "../../lib/api";
import { jobFailed, jobToast, useJob } from "../../lib/useJob";
import { searchJobLine } from "../../lib/searchOutcome";
import { Button, Menu, type MenuItem } from "../../ui";
import { RenameModal } from "./RenameModal";
import { MONITOR_PRESETS } from "./presets";
import { ManualImportModal } from "./ManualImportModal";
import { ProfileSelector } from "./ProfileSelector";

export type Flash = (msg: string, err?: boolean) => void;

// Phone-sized tap targets in the page's menus; a desktop keeps the compact rows.
export const MENU_ITEM = "py-2.5 sm:py-1.5";

// switchNumbering flips a show between standard and anime (absolute) numbering. The hero's
// overflow menu and the Numbering tab both offer it.
export async function switchNumbering(series: SeriesT, onChange: () => void, flash: Flash) {
  const next = series.series_type === "anime" ? "standard" : "anime";
  try {
    await api.setSeriesType(series.id, next);
    onChange();
    flash(next === "anime" ? "Anime numbering on — rescan to match absolute-numbered files." : "Standard numbering.");
  } catch (e) {
    flash((e as Error).message, true);
  }
}

export const numberingLabel = (series: SeriesT) => (series.series_type === "anime" ? "Switch to standard numbering" : "Switch to anime numbering");

// HeroActions is the show's action row: the Monitor menu and the quality profile, then
// three primary actions — Search missing (the one accent button), Choose release… and the
// overflow menu holding everything else. extraItems lets another feature add to the
// overflow (above Delete) without touching this file's layout.
export function HeroActions({ series, onChange, flash, extraItems = [] }: { series: SeriesT; onChange: () => void; flash: Flash; extraItems?: MenuItem[] }) {
  const navigate = useNavigate();
  // The search and a whole-folder import run as jobs; when one ends the toast says what
  // actually happened.
  const [searchJob, setSearchJob] = useState<number | null>(null);
  const [importJob, setImportJob] = useState<number | null>(null);
  const [starting, setStarting] = useState(false);
  const [searchResult, setSearchResult] = useState<{ text: string; failed: boolean } | null>(null);
  const search = useJob(searchJob, {
    onDone: (j) => {
      setSearchJob(null);
      const text = searchJobLine(j);
      setSearchResult({ text, failed: jobFailed(j) });
      flash(text, jobFailed(j));
      refreshSearches("series", series.id);
      onChange();
    },
  });
  useJob(importJob, {
    onDone: (j) => { setImportJob(null); flash(jobToast(j, "Import finished."), jobFailed(j)); onChange(); },
  });
  const [open, setOpen] = useState<null | "import" | "choose" | "upload" | "rename" | "delete">(null);
  const close = () => setOpen(null);

  const searchMissing = async () => {
    setStarting(true);
    setSearchResult(null);
    try {
      const r = await api.searchSeries(series.id);
      if (r.job_id) setSearchJob(r.job_id);
      flash("Searching — follow packs and episodes in Activity → Queue once grabbed.");
    } catch (e) {
      flash((e as Error).message, true);
    } finally {
      setStarting(false);
    }
  };
  const searching = starting || search.running;

  const overflow: MenuItem[] = [
    {
      label: "Refresh & rescan",
      busyLabel: "Refreshing…",
      onSelect: async () => {
        try { await api.refreshSeries(series.id); onChange(); flash("Refreshed metadata and rescanned disk."); }
        catch (e) { flash((e as Error).message, true); }
      },
    },
    { label: "Rename…", onSelect: () => setOpen("rename") },
    { label: "Manual import…", onSelect: () => setOpen("import") },
    { label: "Upload torrent…", onSelect: () => setOpen("upload") },
    { label: numberingLabel(series), busyLabel: "Saving…", onSelect: () => switchNumbering(series, onChange, flash) },
    ...extraItems,
    { label: "Delete…", tone: "danger", onSelect: () => setOpen("delete") },
  ];

  return (
    <>
      <div className="mt-4 flex flex-wrap items-center gap-2 sm:gap-3">
        <MonitorMenu series={series} onChange={onChange} flash={flash} />
        <ProfileSelector series={series} onChange={onChange} />
      </div>
      <div className="mt-2.5 flex flex-nowrap items-center gap-2">
        <Button variant="primary" onClick={searchMissing} busy={searching} busyLabel="Searching…" title="Search every monitored season for what's missing and grab the best fit">
          Search missing
        </Button>
        <Button onClick={() => setOpen("choose")} title="Search indexers for the whole show and pick a pack or episode yourself">Choose release…</Button>
        <Menu
          label="More actions"
          trigger={<span aria-hidden className="text-[16px] leading-none">⋯</span>}
          triggerClassName="grid h-[34px] w-[38px] place-items-center rounded-lg"
          triggerStyle={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
          itemClassName={MENU_ITEM}
          items={overflow}
        />
      </div>
      {(search.running || searchResult) && (
        <div className="mt-2 text-[12px]" role="status" style={{ color: search.running ? "var(--ink-dim)" : searchResult?.failed ? "var(--reject)" : "var(--ink)" }}>
          {search.running ? "Searching…" : searchResult?.text}
        </div>
      )}
      <LastSearches kind="series" id={series.id} />

      {open === "upload" && (
        <UploadTorrentModal
          what={series.title}
          onPreview={(torrent) => api.previewTorrent(torrent)}
          onGrab={async (torrent, filename, title) => { await api.grabSeriesTorrent(series.id, torrent, filename, title); onChange(); }}
          onClose={close}
        />
      )}
      {open === "rename" && <RenameModal seriesId={series.id} title={series.title} onClose={close} onRenamed={onChange} />}
      {open === "import" && (
        <ManualImportModal
          series={series}
          onClose={close}
          onImported={() => { onChange(); flash("Imported."); }}
          onBackground={(jobId) => { onChange(); if (jobId) setImportJob(jobId); }}
        />
      )}
      {open === "choose" && (
        <ReleaseSearchModal
          title={`Choose a release — ${series.title}`}
          subtitle="Whole-series search. Pick any pack or episode to grab."
          fetchReleases={() => api.seriesReleases(series.id)}
          onGrab={async (rel) => { await api.grabSeries(series.id, { token: rel.token ?? "" }); onChange(); }}
          onClose={close}
        />
      )}
      {open === "delete" && <DeleteSeriesDialog series={series} onClose={close} onDeleted={() => navigate("/series")} />}
    </>
  );
}

// MonitorMenu holds the show's monitoring choices in one place: the pause gate (season
// and episode choices are kept while paused), the presets that pick which episodes are
// wanted, and whether a new season found on refresh is monitored.
function MonitorMenu({ series, onChange, flash }: { series: SeriesT; onChange: () => void; flash: Flash }) {
  const act = (fn: () => Promise<unknown>, done: string) => async () => {
    try { await fn(); onChange(); flash(done); }
    catch (e) { flash((e as Error).message, true); }
  };
  const row = (title: string, help?: string, on?: boolean) => (
    <span className="flex w-[240px] max-w-[calc(100vw-72px)] items-start gap-2">
      <span className="min-w-0 flex-1">
        <span className="block">{title}</span>
        {help && <span className="block text-[11px] font-normal leading-snug text-ink-faint">{help}</span>}
      </span>
      {on !== undefined && <span aria-hidden className="flex-none font-mono text-[12px]" style={{ color: on ? "var(--accent)" : "transparent" }}>✓</span>}
    </span>
  );
  const items: MenuItem[] = [
    series.monitored
      ? { label: row("Pause", "Search nothing. Season and episode choices are kept."), busyLabel: "Pausing…", onSelect: act(() => api.setSeriesMonitored(series.id, false), "Paused — nothing is searched until you resume.") }
      : { label: row("Resume", "Search again for the episodes chosen below."), busyLabel: "Resuming…", onSelect: act(() => api.setSeriesMonitored(series.id, true), "Resumed.") },
    ...MONITOR_PRESETS.map((p): MenuItem => ({
      label: row(`Monitor: ${p.label}`, p.help),
      busyLabel: "Applying…",
      onSelect: act(() => api.applySeriesMonitorPreset(series.id, p.value), `Monitoring: ${p.label}.`),
    })),
    {
      label: row("Monitor new seasons", "When a refresh finds a season the show didn't have, monitor it.", !!series.monitor_new_seasons),
      busyLabel: "Saving…",
      onSelect: act(() => api.setSeriesMonitorNewSeasons(series.id, !series.monitor_new_seasons), series.monitor_new_seasons ? "New seasons won't be monitored." : "New seasons will be monitored."),
    },
  ];
  return (
    <Menu
      label="Monitoring"
      align="left"
      header={series.monitored ? "Monitored — wanted episodes are searched" : "Paused — nothing is searched"}
      trigger={
        <span className="inline-flex items-center gap-2">
          <span aria-hidden className="inline-block h-2.5 w-2.5 rounded-full" style={{ background: series.monitored ? "var(--accent)" : "var(--line)" }} />
          {series.monitored ? "Monitored" : "Monitor"}
          <span aria-hidden className="text-[10px] text-ink-faint">▾</span>
        </span>
      }
      triggerClassName="rounded-lg px-3 py-2 text-[12.5px] font-semibold"
      triggerStyle={{ background: "var(--panel-2)", border: `1px solid ${series.monitored ? "var(--accent-line)" : "var(--line)"}`, color: series.monitored ? "var(--ink)" : "var(--ink-dim)" }}
      itemClassName={MENU_ITEM}
      items={items}
    />
  );
}
