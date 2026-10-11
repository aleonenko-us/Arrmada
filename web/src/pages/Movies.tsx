import { useEffect, useMemo, useState } from "react";
import { PageHeader } from "../components/PageHeader";
import { MetadataMissing } from "../components/MetadataMissing";
import { PAGE } from "../lib/links";
import { api, type FitItem, type MovieDownload, type MovieSummary } from "../lib/api";
import { ReleaseSearchModal } from "../components/ReleaseSearchModal";
import { DeleteMovieDialog } from "../components/DeleteMovieDialog";
import { usePoll } from "../lib/usePoll";
import { useLiveQuery } from "../lib/useLiveQuery";
import { useQuery } from "../lib/query";
import { useLiveConnected, useLiveEvent } from "../lib/live";
import { QUEUE_CHANGED, QUEUE_PROGRESS, patchProgress, progressByHash, queueChangedFor } from "../lib/queueLive";
import { MoviesSwitch } from "../components/MoviesSwitch";
import { queueLine, queuedNote, useMovieSearchQueue } from "../lib/movieQueue";
import { StatusChip, useToast } from "../ui";
import { LibraryGrid, useLibraryView } from "../features/library/LibraryGrid";
import { FitLegend, isMisfit, movieLibraryConfig } from "./movies/library";
import { MovieBulkActions } from "./movies/MovieBulkActions";
import { AddMovieModal } from "./movies/AddMovieModal";
import { ScanDialog } from "./movies/ScanDialog";
import { ScanStatus, scanProgressLabel, useMovieScan } from "../components/movies/LibraryScan";

const NO_MOVIES: MovieSummary[] = [];
const NO_FITS = new Map<number, FitItem>();

// Events after which the library list may have changed.
const LIST_TOPICS = ["release.grabbed", "download.imported", "file.removed", "movie.renamed", "search.finished", "library.scanned", "movie.refreshed"];

export function Movies() {
  // Cached across visits, so Back from a movie shows the grid at once; it always
  // revalidates on arrival (staleMs 0) because the detail page may have changed it.
  // A grab, an import, a search or a scan can change it: those refetch it, at most once a
  // second, so a grab started in the background (search on add, the queue) shows up
  // without a reload. No blind poll of the whole library.
  const list = useLiveQuery("movies", () => api.movies(), { staleMs: 0, topics: LIST_TOPICS, debounceMs: 1000, fallbackMs: null });
  const movies = list.data?.movies ?? NO_MOVIES;
  const metaOK = list.data?.metadata_available ?? true;
  // false while the download client can't be read: a wanted movie may already be downloading.
  const queueKnown = list.data?.client_health?.ok ?? true;
  const refresh = list.refetch;
  const toast = useToast();
  const view = useLibraryView("movies");
  const [showAdd, setShowAdd] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<MovieSummary | null>(null);
  const [searchFor, setSearchFor] = useState<MovieSummary | null>(null); // the table's per-row "Search indexers"
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  useEffect(() => {
    api.qualityProfiles("movie").then((r) => setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })))).catch(() => {});
  }, []);
  // Every movie search waits its turn in one throttled queue; the header says how many
  // are running and waiting, kept live by the queue's events.
  const searchQueue = useMovieSearchQueue();
  const queueText = queueLine(searchQueue.running, searchQueue.queued);

  // How each file fits its profile's ideal file — report only, from the analysed library
  // (a database read). The table's Fit column and the "Doesn't fit" filter read it.
  const fitQ = useQuery("movies.fit", () => api.libraryFitMovies(), { staleMs: 60_000 });
  const fits = useMemo(() => (fitQ.data ? new Map((fitQ.data.items ?? []).map((it) => [it.movie_id ?? 0, it])) : NO_FITS), [fitQ.data]);

  // The library scan is a job (MOV-20): its live progress (job.updated on the one socket)
  // is the Library tools chip while it runs, and a summary linking to the folders that
  // need review shows when it ends. A scan started elsewhere is picked up too.
  const [showScan, setShowScan] = useState(false);
  const scan = useMovieScan(() => void refresh(), (msg, err) => toast(msg, { tone: err ? "error" : "info" }));
  const scanLibrary = (opts: { monitor: boolean; quality_profile?: string }) => {
    setShowScan(false);
    void scan.start(opts);
  };

  // While anything downloads, the bars move from queue.progress (by torrent hash) without
  // asking the server; a queue.changed naming one of the grid's torrents (finished,
  // removed, errored) refetches the list, since the film's own state moves too. The small
  // downloads endpoint is the slow backstop, and stays quick only for an old grab with no
  // hash to follow.
  const [downloads, setDownloads] = useState<Map<number, MovieDownload> | null>(null);
  useEffect(() => { setDownloads(null); }, [list.data]); // a fresh list carries its own progress
  const shown = useMemo(() => (downloads ? movies.map((m) => ({ ...m, download: downloads.get(m.id) })) : movies), [movies, downloads]);
  const anyDownloading = shown.some((m) => m.download);
  const allHashed = shown.every((m) => !m.download || !!m.download.hash);
  const connected = useLiveConnected();
  useLiveEvent([QUEUE_PROGRESS, QUEUE_CHANGED], (ev) => {
    const hashes = shown.flatMap((m) => (m.download?.hash ? [m.download.hash] : []));
    if (queueChangedFor(ev, hashes)) { void refresh(); return; }
    const live = progressByHash(ev);
    if (!live) return;
    const next = patchProgress(shown, live, (m) => m.download, (m, p) => ({ ...m, download: m.download && { ...m.download, progress: p.progress } }));
    if (next !== shown) setDownloads(new Map(next.flatMap((m) => (m.download ? [[m.id, m.download] as const] : []))));
  }, anyDownloading);
  const pollDownloads = async () => {
    try {
      const r = await api.movieDownloads();
      const next = new Map(r.downloads.map((d) => [d.movie_id, d]));
      const before = new Set(shown.filter((m) => m.download).map((m) => m.id));
      const changed = before.size !== next.size || [...next.keys()].some((id) => !before.has(id));
      if (changed) void refresh(); // finished or new: the list's own state moves too
      else setDownloads(next);
    } catch {
      /* the next tick tries again */
    }
  };
  usePoll(pollDownloads, anyDownloading ? (connected && allHashed ? 30_000 : 4000) : null, { immediate: false });

  const search = async (m: MovieSummary) => {
    try {
      const note = queuedNote(await api.searchMovie(m.id));
      toast(note ? `“${m.title}”: ${note}.` : `Searching for “${m.title}” — follow it in ${PAGE.activity} → Wanted.`);
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    }
  };

  const config = useMemo(() => movieLibraryConfig({
    fits,
    queueKnown,
    actions: (m) => [
      ...(!m.has_file && !m.download ? [{ label: "Search now", onSelect: () => search(m) }] : []),
      { label: "Search indexers…", onSelect: () => setSearchFor(m) },
      { label: "Remove…", tone: "danger" as const, onSelect: () => setConfirmDelete(m) },
    ],
    rowAction: (m) => <button onClick={() => setSearchFor(m)} title="Search your indexers and pick a release" className="whitespace-nowrap rounded-md px-2 py-1 text-[10.5px] font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Search</button>,
  // search reads only api and toast, both stable.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [fits, queueKnown]);

  const judged = useMemo(() => movies.filter((m) => fits.get(m.id)?.fit).length, [movies, fits]);
  const misfits = useMemo(() => movies.filter((m) => isMisfit(fits.get(m.id))).length, [movies, fits]);

  return (
    <>
      <PageHeader title="Movies" />
      <LibraryGrid
        config={config}
        items={list.data ? shown : undefined}
        error={list.error?.message ?? null}
        loading={list.loading}
        onRetry={refresh}
        view={view}
        lead={<>
          <MoviesSwitch active="library" />
          {queueText && <StatusChip tone="accent" title="Movie searches run two at a time; the rest wait their turn.">{queueText}</StatusChip>}
        </>}
        tools={[{ label: "Scan library", busyLabel: "Scanning…", busy: scan.running, progress: scanProgressLabel(scan), onClick: () => setShowScan(true), title: "Find movies already in your library folder and catalog them" }]}
        primary={{ label: "+ Add movie", onClick: () => setShowAdd(true), disabled: !metaOK }}
        banners={<>
          {!metaOK && <MetadataMissing variant="banner" />}
          <ScanStatus scan={scan} />
        </>}
        tableNote={<FitLegend judged={judged} misfits={misfits} />}
        bulkActions={(sel) => <MovieBulkActions sel={sel} profiles={profiles} onDone={refresh} />}
        emptyText={<>No movies yet. Click <b>Add movie</b>, search for a film, and Arrmada will monitor and grab it.</>}
      />
      {searchFor && (
        <ReleaseSearchModal
          title={`Search indexers — ${searchFor.title}`}
          subtitle="Pick a release to grab, or blocklist one to search for an alternate."
          fetchReleases={() => api.movieReleases(searchFor.id)}
          onGrab={async (rel) => { await api.grab({ token: rel.token ?? "", movie_id: searchFor.id }); toast(`Grabbed "${rel.summary}" for ${searchFor.title}.`); refresh(); }}
          onBlock={async (rel) => { await api.blockRelease(searchFor.id, { token: rel.token ?? "", search_again: true }); toast(`Blocklisted "${rel.summary}" — searching for an alternate.`); refresh(); }}
          onClose={() => setSearchFor(null)}
        />
      )}
      {showScan && <ScanDialog profiles={profiles} onScan={scanLibrary} onClose={() => setShowScan(false)} />}
      {showAdd && <AddMovieModal onClose={() => setShowAdd(false)} onAdded={refresh} />}
      {confirmDelete && <DeleteMovieDialog movie={confirmDelete} onClose={() => setConfirmDelete(null)} onDeleted={() => { setConfirmDelete(null); refresh(); }} />}
    </>
  );
}
