import { Suspense, lazy, useState } from "react";
import type { MusicAlbum } from "../../lib/api";
import { useLiveEvent } from "../../lib/live";
import { type AlbumSearchState, type AlbumUpgrade, musicSearchApi } from "../../lib/musicSearchApi";
import { invalidate, useQuery } from "../../lib/query";
import { searchFinishedFor, searchJobLine } from "../../lib/searchOutcome";
import { until } from "../../lib/taskTime";
import { jobFailed, useJob } from "../../lib/useJob";
import { Button, useToast } from "../../ui";
import { LastSearchLine } from "../LastSearch";

// Searching from an album's page (MUS-09, MUS-16, MUS-17): Search now (a job, followed
// to its outcome), the interactive search with each release's reason, the latest search
// and the next one, and where the album stands against its profile's cutoff.

const ReleaseSearchModal = lazy(() => import("../ReleaseSearchModal").then((m) => ({ default: m.ReleaseSearchModal })));

const stateKey = (id: number) => `music:album-search:${id}`;

// upgradeLine words the album's upgrade state: "Upgrades: MP3-320 → FLAC-24, next look in
// 3 h". null when there's nothing worth saying (incomplete: missing tracks come first).
function upgradeLine(u: AlbumUpgrade): string | null {
  switch (u.state) {
    case "below": {
      const next = u.next_at ? until(u.next_at) : "";
      const when = next && next !== "now" && next !== "—" ? `next look ${next}` : "looked for on the next upgrade sweep";
      return `Upgrades: ${u.held} → ${u.cutoff} (${u.profile}), ${when}`;
    }
    case "at":
      return `At ${u.held}, the top of the ${u.profile} ladder: no upgrades wanted`;
    case "unknown":
      return "Upgrades wait until every file's quality is measured";
    case "off":
      return u.held ? `Held as ${u.held}; ${u.profile} doesn't upgrade` : null;
  }
  return null;
}

export function AlbumSearch({ album, onChanged }: { album: MusicAlbum; onChanged: () => void }) {
  const toast = useToast();
  const [jobId, setJobId] = useState<number | null>(null);
  const [starting, setStarting] = useState(false);
  const [interactive, setInteractive] = useState(false);
  const state = useQuery<AlbumSearchState>(stateKey(album.id), () => musicSearchApi.albumSearchState(album.id), { staleMs: 15_000 });
  const refresh = () => invalidate(stateKey(album.id));
  useLiveEvent(["search.finished", "music.imported"], (ev) => {
    const d = ev.data as { album_id?: number } | null;
    if (searchFinishedFor(ev, "music", album.id) || (ev.topic === "music.imported" && d?.album_id === album.id)) refresh();
  });
  const job = useJob(jobId, {
    onDone: (j) => {
      setJobId(null);
      toast(searchJobLine(j), { tone: jobFailed(j) ? "error" : "info" });
      refresh();
      onChanged();
    },
  });

  const searchNow = async () => {
    setStarting(true);
    try {
      const r = await musicSearchApi.searchAlbum(album.id);
      if (r.job_id) setJobId(r.job_id);
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setStarting(false);
    }
  };

  const s = state.data;
  const up = s ? upgradeLine(s.upgrade) : null;
  // The "why isn't it downloading" line is for an album still missing tracks.
  const missing = stillMissing(album);
  return (
    <div className="mt-3">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="secondary" onClick={searchNow} busy={starting || job.running} busyLabel="Searching…"
          title={missing ? "Search the indexers for this album now" : "Look for a better copy now (when the profile allows upgrades)"}>
          Search now
        </Button>
        <Button size="sm" variant="secondary" onClick={() => setInteractive(true)}>Interactive search…</Button>
      </div>
      {missing && <LastSearchLine summary={s?.last_search} nextSearchAt={s?.next_search_at} className="mt-2" />}
      {up && <div className="mt-1 text-sm text-ink-dim" data-testid="album-upgrade">{up}</div>}
      {interactive && (
        <Suspense fallback={null}>
          <ReleaseSearchModal
            audio
            title={`Search indexers — ${album.title}`}
            subtitle="Every release found, with why it would or wouldn't be grabbed. Grabbing one by hand imports it into this album."
            fetchReleases={() => musicSearchApi.albumReleases(album.id)}
            onGrab={async (rel) => {
              await musicSearchApi.grabAlbum(album.id, rel.token ?? "");
              toast(`Grabbed ${rel.title}`);
              refresh();
              onChanged();
            }}
            onClose={() => setInteractive(false)}
          />
        </Suspense>
      )}
    </div>
  );
}

// stillMissing reports whether an album is still missing tracks it wants (or has no listing yet).
function stillMissing(album: MusicAlbum): boolean {
  const known = (album.edition_tracks ?? album.track_count) > 0;
  return !known || album.have_tracks < album.track_count;
}

// AlbumSearchButton is interactive search for one album from a list (the artist page's
// album rows): a small button that opens the same modal.
export function AlbumSearchButton({ album, onGrabbed }: { album: Pick<MusicAlbum, "id" | "title">; onGrabbed?: () => void }) {
  const toast = useToast();
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label={`Interactive search for ${album.title}`}
        title="Interactive search"
        className="grid h-7 w-7 flex-none place-items-center rounded-lg text-ink-faint hover:text-[var(--ink)]"
        style={{ border: "1px solid var(--line)" }}
      >
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden>
          <circle cx="11" cy="11" r="7" />
          <path d="M20 20l-3.5-3.5" />
        </svg>
      </button>
      {open && (
        <Suspense fallback={null}>
          <ReleaseSearchModal
            audio
            title={`Search indexers — ${album.title}`}
            fetchReleases={() => musicSearchApi.albumReleases(album.id)}
            onGrab={async (rel) => {
              await musicSearchApi.grabAlbum(album.id, rel.token ?? "");
              toast(`Grabbed ${rel.title}`);
              onGrabbed?.();
            }}
            onClose={() => setOpen(false)}
          />
        </Suspense>
      )}
    </>
  );
}
