import { useEffect, useMemo, useState } from "react";
import { PageHeader } from "../components/PageHeader";
import { MetadataMissing } from "../components/MetadataMissing";
import { DeleteSeriesDialog } from "../components/DeleteSeriesDialog";
import { api, type Series as SeriesT, type SeriesFitSummary } from "../lib/api";
import { SeriesSearchModal } from "../components/SeriesSearchModal";
import { usePollBurst } from "../lib/usePoll";
import { jobFailed, jobToast, useJob } from "../lib/useJob";
import { useQuery } from "../lib/query";
import { useConfirm, useToast } from "../ui";
import { LibraryGrid, useLibraryView } from "../features/library/LibraryGrid";
import { seriesLibraryConfig, statsOf } from "./series/library";
import { SeriesBulkActions } from "./series/SeriesBulkActions";
import { AddSeriesModal } from "./series/AddSeriesModal";

const NO_FITS = new Map<number, SeriesFitSummary>();

export function Series() {
  // Cached across visits, so Back from a show renders at once, then revalidates.
  const seriesQ = useQuery("series", () => api.series(), { staleMs: 0 });
  const list = seriesQ.data?.series;
  const metaOK = seriesQ.data?.metadata_available ?? true;
  const refresh = seriesQ.refetch;
  const toast = useToast();
  const confirm = useConfirm();
  const view = useLibraryView("series");
  const [adding, setAdding] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<SeriesT | null>(null);
  const [searchFor, setSearchFor] = useState<SeriesT | null>(null); // the table's per-row "Search indexers"
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  useEffect(() => {
    api.qualityProfiles("series").then((r) => setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })))).catch(() => {});
  }, []);

  // How each show's episode files fit its profile's ideal file — report only; only the
  // table shows it.
  const fitQ = useQuery("series.fit", () => api.libraryFitSeries(), { staleMs: 60_000, enabled: view.mode === "table" });
  const fits = useMemo(() => (fitQ.data ? new Map((fitQ.data.items ?? []).map((it) => [it.series_id, it])) : NO_FITS), [fitQ.data]);

  // While a refresh-all works, poll so the user sees episode counts settle rather than
  // having to guess whether anything happened.
  const [refreshingAll, setRefreshingAll] = useState(false);
  const watchRefreshAll = usePollBurst(() => refresh(), 3000, 40, () => setRefreshingAll(false));
  // Refresh + rescan every series. This is the bulk form of the per-show "Refresh"
  // button: it reconciles episodes whose file is already on disk but whose row still
  // reads as missing, which is what makes the searcher re-grab things you already have.
  const refreshAll = async () => {
    const ok = await confirm({
      title: `Refresh all ${list?.length ?? 0} series?`,
      body: "Arrmada re-pulls each show's metadata and rescans its files. This runs in the background and can take a while.",
      confirmLabel: "Refresh all",
    });
    if (!ok) return;
    setRefreshingAll(true);
    try {
      const r = await api.refreshAllSeries();
      toast(`Refreshing ${r.queued} series in the background — counts will update as it goes.`);
      watchRefreshAll();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
      setRefreshingAll(false);
    }
  };

  // The scan runs as a job; poll the grid while entries land, and when it ends the toast
  // says what it added.
  const [scanning, setScanning] = useState(false);
  const watchScan = usePollBurst(() => refresh(), 2500, 12, () => setScanning(false));
  const [scanJob, setScanJob] = useState<number | null>(null);
  const scan = useJob(scanJob, {
    onDone: (j) => { setScanJob(null); refresh(); toast(jobToast(j, "Scan finished."), { tone: jobFailed(j) ? "error" : "info" }); },
  });
  const scanLibrary = async () => {
    setScanning(true);
    try {
      const r = await api.scanSeries();
      if (r.job_id) setScanJob(r.job_id);
      toast("Scanning your library — existing series will appear shortly.");
      watchScan();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
      setScanning(false);
    }
  };

  const search = async (s: SeriesT) => {
    try {
      await api.searchSeries(s.id);
      toast(`Searching for “${s.title}” — follow packs and episodes in Activity → Queue once grabbed.`);
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    }
  };

  const config = useMemo(() => seriesLibraryConfig({
    fits,
    actions: (s) => [
      ...(s.monitored && statsOf(s).missing > 0 ? [{ label: "Search missing", onSelect: () => search(s) }] : []),
      { label: "Search indexers…", onSelect: () => setSearchFor(s) },
      { label: "Remove…", tone: "danger" as const, onSelect: () => setConfirmDelete(s) },
    ],
    rowAction: (s) => <button onClick={() => setSearchFor(s)} title="Search your indexers — whole show or one season" className="whitespace-nowrap rounded-md px-2 py-1 text-[10.5px] font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Search</button>,
  // search reads only api and toast, both stable.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [fits]);

  return (
    <>
      <PageHeader title="Series" />
      <LibraryGrid
        config={config}
        items={list}
        error={seriesQ.error?.message ?? null}
        loading={seriesQ.loading}
        onRetry={refresh}
        view={view}
        tools={[
          { label: "Scan library", busyLabel: "Scanning…", busy: scanning || scan.running, onClick: scanLibrary, title: "Find series already in your library folder and catalog them" },
          { label: "Refresh all", busyLabel: "Refreshing…", busy: refreshingAll, disabled: !list?.length, onClick: refreshAll, title: "Re-pull metadata and rescan files for every series — fixes episodes that are on disk but still show as missing" },
        ]}
        primary={{ label: "+ Add series", onClick: () => setAdding(true), disabled: !metaOK }}
        banners={!metaOK && <MetadataMissing variant="banner" />}
        bulkActions={(sel) => <SeriesBulkActions sel={sel} profiles={profiles} onDone={refresh} />}
        emptyText={<>No series yet. Click <b>Add series</b>, search for a show, and Arrmada will monitor and grab it.</>}
      />
      {searchFor && (
        <SeriesSearchModal id={searchFor.id} title={searchFor.title} onClose={() => setSearchFor(null)} onGrabbed={() => { toast(`Grabbed for ${searchFor.title} — follow it in Activity → Queue.`); refresh(); }} />
      )}
      {adding && <AddSeriesModal onClose={() => setAdding(false)} onAdded={() => { setAdding(false); refresh(); }} />}
      {confirmDelete && <DeleteSeriesDialog series={confirmDelete} onClose={() => setConfirmDelete(null)} onDeleted={() => { setConfirmDelete(null); refresh(); }} />}
    </>
  );
}
