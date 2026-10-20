import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { LINKS } from "../../lib/links";
import { musicApi, type MusicScanSummary } from "../../lib/musicApi";
import { formatAgo } from "../../lib/format";
import { Button, IconButton, Modal } from "../../ui";

// ScanResults is the library scan's results drawer (MUS-37): what the last scan found,
// what it left alone because nothing changed, the folders waiting in Reviews to be matched,
// and how long it took. The scan never moves, renames or retags anything, and says so.

function seconds(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  const s = ms / 1000;
  return s < 90 ? `${s.toFixed(1)} s` : `${Math.round(s / 60)} min`;
}

function finishedAt(s?: string): number {
  if (!s) return 0;
  const t = Date.parse(s);
  return Number.isNaN(t) ? 0 : t;
}

export function ScanResultsModal({ onClose, onRescan }: { onClose: () => void; onRescan: (full: boolean) => void }) {
  const [last, setLast] = useState<MusicScanSummary | null | undefined>(undefined);
  const [finished, setFinished] = useState(0);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    musicApi
      .lastScan()
      .then((r) => { if (live) { setLast(r.last); setFinished(finishedAt(r.finished_at)); } })
      .catch((e: Error) => live && setError(e.message));
    return () => { live = false; };
  }, []);

  const rows: [string, number, string][] = last
    ? [
        ["Album folders in the music folder", last.folders, ""],
        ["Unchanged since the last scan", last.skipped, "not opened"],
        ["Albums with files newly recorded", last.albums, ""],
        ["Track files recorded", last.tracks, ""],
        ["Files followed to a new folder", last.moved, "renamed or moved, not lost"],
        ["Artists added (not monitored)", last.artists, ""],
        ["Folders to match in Reviews", last.reviews, ""],
        ["Waiting for a MusicBrainz track listing", last.waiting, "looked at again next scan"],
        ["Couldn't be judged just now", last.failed, "MusicBrainz didn't answer; next scan"],
        ["Left for the next scan", last.remaining, "a scan reads a few thousand folders at most"],
        ["Track files gone from disk", last.verify?.missing ?? 0, "flagged, not downloaded again by themselves"],
        ["Files not found this time", last.verify?.not_found ?? 0, "missing only if the next check agrees"],
        ["Files back on disk", last.verify?.back ?? 0, ""],
        ["Files changed outside Arrmada", last.verify?.changed ?? 0, "measured again"],
      ]
    : [];

  return (
    <Modal onClose={onClose} labelledBy="scan-results-title" size="md">
      <div className="mb-1 flex items-center justify-between">
        <h2 id="scan-results-title" className="m-0 text-lg font-bold">Library scan</h2>
        <IconButton label="Close" onClick={onClose} className="h-7 w-7 rounded-md text-ink-faint hover:text-[var(--ink)]">
          ✕
        </IconButton>
      </div>
      <p className="mb-3 text-sm text-ink-dim">
        The scan records the music already in your music folder, where it is: nothing is moved, renamed or retagged. Folders that haven't
        changed since the last scan aren't opened again.
      </p>
      {error ? (
        <p className="text-sm" style={{ color: "var(--reject-text)" }}>{error}</p>
      ) : last === undefined ? (
        <p className="text-sm text-ink-dim">Loading…</p>
      ) : last === null ? (
        <div className="rounded-xl p-6 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>
          No scan has run yet.
        </div>
      ) : (
        <>
          {finished > 0 && (
            <p className="mb-2 text-xs text-ink-faint" title={new Date(finished).toLocaleString()}>
              Finished {formatAgo(finished)}{last.full ? " · looked at every folder" : ""}
            </p>
          )}
          <dl className="m-0 rounded-xl" style={{ border: "1px solid var(--line)" }} data-testid="scan-results">
            {rows.filter(([, n], i) => n > 0 || i < 2).map(([label, n, note]) => (
              <div key={label} className="flex items-baseline gap-3 px-3 py-1.5" style={{ borderTop: "1px solid var(--line-soft)" }}>
                <dt className="min-w-0 flex-1 text-base">
                  {label}
                  {note && <span className="ml-2 text-xs text-ink-faint">{note}</span>}
                </dt>
                <dd className="m-0 font-mono text-base">{n.toLocaleString()}</dd>
              </div>
            ))}
          </dl>
          {last.verify?.refused && (
            <p role="alert" className="mb-0 mt-2 text-sm" style={{ color: "var(--avoid-text)" }}>{last.verify.refused}</p>
          )}
          <p className="mb-0 mt-2 font-mono text-xs text-ink-faint">
            {seconds(last.total_ms)} in all · walking the folder {seconds(last.walk_ms)} · reading {last.opened.toLocaleString()} file
            {last.opened === 1 ? "" : "s"} {seconds(last.read_ms)} across {last.workers} at once
          </p>
          {last.reviews > 0 && (
            <p className="mb-0 mt-3 text-base">
              <Link to={LINKS.review} onClick={onClose} className="font-semibold" style={{ color: "var(--accent-text)" }}>
                Match {last.reviews === 1 ? "the folder" : `the ${last.reviews.toLocaleString()} folders`} in Reviews
              </Link>
              <span className="ml-2 text-sm text-ink-dim">pick the album each one is; the files stay where they are</span>
            </p>
          )}
        </>
      )}
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Button size="sm" onClick={() => onRescan(false)}>Scan again</Button>
        <Button size="sm" onClick={() => onRescan(true)} title="Look at every folder, even ones that haven't changed">Scan everything</Button>
      </div>
    </Modal>
  );
}
