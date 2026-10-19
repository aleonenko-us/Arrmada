import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../../lib/api";
import { musicApi, type NewRelease } from "../../lib/musicApi";
import { IconButton, Modal, useToast } from "../../ui";
import { foundWords } from "./releaseRules";

// NewReleases lists what the scheduled refresh found in the last 30 days (MUS-13): the
// new releases it wanted, and everything it only listed, each saying why. Want is one
// click here, so nothing a refresh lists is ever out of reach.
export function NewReleasesModal({ onClose }: { onClose: () => void }) {
  const toast = useToast();
  const [albums, setAlbums] = useState<NewRelease[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);

  useEffect(() => {
    musicApi
      .newReleases(30)
      .then((r) => setAlbums(r.albums))
      .catch((e: Error) => setError(e.message));
  }, []);

  const want = async (al: NewRelease) => {
    setBusy(al.id);
    try {
      await api.setAlbumMonitored(al.id, true);
      setAlbums((list) => (list ?? []).map((x) => (x.id === al.id ? { ...x, monitored: true } : x)));
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(null);
    }
  };

  return (
    <Modal onClose={onClose} labelledBy="new-releases-title" size="lg">
      <div className="mb-1 flex items-center justify-between">
        <h2 id="new-releases-title" className="m-0 text-lg font-bold">New releases</h2>
        <IconButton label="Close" onClick={onClose} className="h-7 w-7 rounded-md text-ink-faint hover:text-[var(--ink)]">
          ✕
        </IconButton>
      </div>
      <p className="mb-3 text-sm text-ink-dim">
        What the daily refresh found on MusicBrainz in the last 30 days. Only new releases are wanted on their own, following each artist's New
        releases option; everything else is listed for you to decide.
      </p>
      {error ? (
        <p className="text-sm" style={{ color: "var(--reject-text)" }}>{error}</p>
      ) : albums === null ? (
        <p className="text-sm text-ink-dim">Loading…</p>
      ) : albums.length === 0 ? (
        <div className="rounded-xl p-6 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>
          Nothing new in the last 30 days.
        </div>
      ) : (
        <div className="thin-scroll flex max-h-[60vh] flex-col overflow-y-auto">
          {albums.map((al) => (
            <div key={al.id} className="flex items-center gap-3 border-b py-2.5" style={{ borderColor: "var(--line)" }}>
              <Link to={`/music/album/${al.id}`} onClick={onClose} className="min-w-0 flex-1 hover:underline">
                <div className="truncate text-md font-medium">{al.title}</div>
                <div className="truncate font-mono text-xs text-ink-faint">
                  {[al.artist_name, al.album_type, al.release_date].filter(Boolean).join(" · ")}
                </div>
                <div className="truncate text-xs text-ink-faint">{foundWords(al.found_as, al.monitored)}</div>
              </Link>
              {al.monitored ? (
                <span className="flex-none text-xs font-semibold" style={{ color: "var(--accent-text)" }}>Wanted</span>
              ) : (
                <button
                  disabled={busy !== null}
                  onClick={() => void want(al)}
                  className="flex-none rounded-lg px-2.5 py-1 text-xs font-semibold disabled:opacity-50"
                  style={{ border: "1px solid var(--accent)", color: "var(--accent-text)" }}
                >
                  {busy === al.id ? "Saving…" : "Want"}
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </Modal>
  );
}
