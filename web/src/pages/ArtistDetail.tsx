import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { PageHeader } from "../components/PageHeader";
import { api, type Artist, type MusicAlbum, type MovieEvent } from "../lib/api";
import { formatBytes } from "../lib/format";
import { libraryStatus } from "../lib/status";
import { Button, StatusChip, useConfirm, useToast } from "../ui";
import { Switch } from "../ui/Switch";
import { Page } from "../ui/Page";

export function ArtistDetail() {
  const { id } = useParams();
  const aid = Number(id);
  const [a, setA] = useState<Artist | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const toast = useToast();
  const load = useCallback(() => {
    api
      .artistDetail(aid)
      .then(setA)
      .catch((e: Error) => {
        if (e.message.toLowerCase().includes("not found")) setNotFound(true);
        else setError(e.message);
      });
  }, [aid]);
  useEffect(() => {
    load();
  }, [load]);

  const run = async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key);
    try {
      await fn();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  if (notFound)
    return (
      <Shell>
        <div className="py-10 text-center text-md text-ink-dim">
          That artist isn't in your library.{" "}
          <Link to="/music" className="underline" style={{ color: "var(--accent-text)" }}>
            Back to Music
          </Link>
        </div>
      </Shell>
    );
  if (!a)
    return (
      <Shell>
        <p className="text-base text-ink-dim">{error ?? "Loading…"}</p>
      </Shell>
    );

  const done = a.stats?.have_tracks ?? 0;
  const all = a.stats?.tracks ?? 0;
  // The chip is the artist's library status; the Monitored toggle below says whether it's searched.
  const st = libraryStatus({ multi: true, hasFile: done > 0, monitored: a.monitored, have: done, total: all });
  // A discography is one torrent spanning every album, which fights per-album upgrades and
  // seeding. Only offer it where it's genuinely the better tool: you own little or nothing
  // by this artist. Topping up is what the per-album sweep is for.
  const mostlyMissing = (a.albums?.length ?? 0) > 1 && (all === 0 || done / Math.max(all, 1) < 0.25);

  return (
    <>
      <PageHeader title={a.name} />
      <Page>
        <Link to="/music" className="mb-4 inline-flex items-center gap-1 text-sm text-ink-dim hover:text-[var(--ink)]">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none">
            <path d="M15 19l-7-7 7-7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
          All artists
        </Link>

        <div className="flex flex-wrap items-center gap-2.5">
          <StatusChip tone={st.tone} size="md">
            {st.label}
          </StatusChip>
          <span className="font-mono text-xs text-ink-faint">
            {a.stats?.albums ?? 0} albums · {done}/{all} tracks
            {a.stats?.size_bytes ? ` · ${formatBytes(a.stats.size_bytes)}` : ""}
          </span>
          <a
            href={`https://musicbrainz.org/artist/${a.mbid}`}
            target="_blank"
            rel="noreferrer"
            className="rounded px-1.5 py-0.5 font-mono text-xs font-bold"
            style={{ background: "#4a2a6a", color: "#fff" }}
          >
            MusicBrainz
          </a>
        </div>
        {a.genres && a.genres.length > 0 && (
          <div className="mt-3 flex flex-wrap gap-1.5">
            {a.genres.map((g) => (
              <span key={g} className="rounded-full px-2 py-0.5 text-xs" style={{ background: "var(--panel-2)", color: "var(--ink-dim)" }}>
                {g}
              </span>
            ))}
          </div>
        )}

        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Switch size="sm" checked={a.monitored} disabled={busy !== null}
            onChange={() => run("monitor", async () => { await api.setArtistMonitored(a.id, !a.monitored); load(); })}>
            {a.monitored ? "Monitored" : "Monitor"}
          </Switch>
          <Button
            size="sm"
            disabled={busy !== null}
            busy={busy === "refresh"}
            busyLabel="Refreshing…"
            onClick={() => run("refresh", async () => { await api.refreshArtist(a.id); load(); toast("Refreshed from MusicBrainz."); })}
          >
            Refresh
          </Button>
          {mostlyMissing && (
            <Button
              size="sm"
              style={{ border: "1px solid var(--accent)", background: "transparent", color: "var(--accent-text)" }}
              disabled={busy !== null}
              title="Grab one torrent covering this artist's whole catalogue. Best when you own little or nothing by them — for topping up, per-album grabs keep upgrades and seeding clean."
              onClick={() => run("disco", async () => { await api.grabDiscography(a.id); toast("Looking for a discography — check Downloads."); })}
            >
              {busy === "disco" ? "Searching…" : "Grab discography"}
            </Button>
          )}
          <DeleteArtistButton artist={a} />
        </div>

        {error && (
          <div className="mt-4 rounded-lg p-3 text-sm" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>
            {error}
          </div>
        )}

        <h2 className="m-0 mb-3 mt-8 text-md font-bold">Albums</h2>
        {!a.albums || a.albums.length === 0 ? (
          <div className="rounded-xl p-8 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>
            No albums listed. Try Refresh — MusicBrainz may not have returned them yet.
          </div>
        ) : (
          <div className="overflow-hidden rounded-xl" style={{ border: "1px solid var(--line)" }}>
            {a.albums.map((al) => (
              <AlbumRow key={al.id} al={al} onChange={load} />
            ))}
          </div>
        )}

        <HistoryPanel artistId={a.id} refreshKey={a.stats?.have_tracks} />
      </Page>

    </>
  );
}

function AlbumRow({ al, onChange }: { al: MusicAlbum; onChange: () => void }) {
  const [busy, setBusy] = useState(false);
  // track_count is 0 until the listing is fetched, which happens when the album is opened.
  const known = al.track_count > 0;
  const complete = known && al.have_tracks >= al.track_count;
  const tone = !known ? "var(--ink-faint)" : complete ? "var(--good)" : al.monitored ? "var(--avoid)" : "var(--ink-faint)";
  return (
    <div className="flex items-center gap-3 border-b px-4 py-2.5 last:border-b-0" style={{ background: "var(--panel)", borderColor: "var(--line-soft)" }}>
      <Link to={`/music/album/${al.id}`} className="min-w-0 flex-1 hover:underline">
        <div className="truncate text-base font-medium">{al.title}</div>
        <div className="truncate font-mono text-xs text-ink-faint">
          {[al.year || "", al.album_type || ""].filter(Boolean).join(" · ")}
        </div>
      </Link>
      <span className="flex-none font-mono text-xs" style={{ color: tone }}>
        {known ? `${al.have_tracks}/${al.track_count}` : "not listed"}
      </span>
      <button
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          try {
            await api.setAlbumMonitored(al.id, !al.monitored);
            onChange();
          } finally {
            setBusy(false);
          }
        }}
        className="flex-none rounded-lg px-2.5 py-1 text-xs font-semibold disabled:opacity-50"
        style={{ border: `1px solid ${al.monitored ? "var(--accent)" : "var(--line)"}`, color: al.monitored ? "var(--accent-text)" : "var(--ink-faint)" }}
      >
        {al.monitored ? "Monitored" : "Monitor"}
      </button>
    </div>
  );
}

// DeleteArtistButton asks the same question it always did, now in the shared confirm. A
// failed delete says so instead of leaving the page as if nothing happened.
function DeleteArtistButton({ artist }: { artist: Artist }) {
  const confirm = useConfirm();
  const toast = useToast();
  const navigate = useNavigate();
  const remove = async () => {
    const ok = await confirm({ title: `Remove ${artist.name} and its albums?`, confirmLabel: "Delete", tone: "danger" });
    if (!ok) return;
    try {
      await api.deleteArtist(artist.id);
      navigate("/music");
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    }
  };
  return (
    <Button size="sm" onClick={remove} style={{ border: "1px solid var(--reject)", background: "transparent", color: "var(--reject-text)" }}>
      Delete
    </Button>
  );
}

const EVENT_TONES: Record<string, string> = {
  added: "var(--ink-faint)",
  grabbed: "var(--accent-text)",
  imported: "var(--good-text)",
  refreshed: "var(--ink-dim)",
  failed: "var(--reject-text)",
};

function fmtEventTime(s: string): string {
  const d = new Date(s.includes("T") ? s : s.replace(" ", "T") + "Z");
  if (isNaN(d.getTime())) return s;
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function HistoryPanel({ artistId, refreshKey }: { artistId: number; refreshKey: unknown }) {
  const [events, setEvents] = useState<MovieEvent[] | null>(null);
  useEffect(() => {
    api.artistHistory(artistId).then(setEvents).catch(() => setEvents([]));
  }, [artistId, refreshKey]);

  return (
    <div className="mt-8">
      <h2 className="m-0 mb-3 text-md font-bold">History</h2>
      {events === null ? (
        <div className="text-base text-ink-dim">Loading…</div>
      ) : events.length === 0 ? (
        <div className="rounded-xl p-6 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>
          No activity yet.
        </div>
      ) : (
        <div className="flex flex-col">
          {events.map((e, i) => (
            <div key={i} className="flex items-center gap-3 border-b py-2 text-sm" style={{ borderColor: "var(--line)" }}>
              <span className="w-[74px] flex-none font-mono text-2xs font-bold uppercase" style={{ color: EVENT_TONES[e.event] ?? "var(--ink-dim)" }}>
                {e.event}
              </span>
              <span className="min-w-0 flex-1 truncate text-ink-dim" title={e.detail}>
                {e.detail || "—"}
              </span>
              <span className="flex-none font-mono text-xs text-ink-faint">{fmtEventTime(e.created_at)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <>
      <PageHeader title="Music" />
      <Page>{children}</Page>
    </>
  );
}
